package handlers

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Police, courts, informants, the hidden board, laundering and customs (docs/adr/0078). A model of institutions, not a manual: roles, chances, costs
// and consequences, none of it describing how an act is done.
//
//   - WHO WORKS THERE. The posts' staff (constables, officers, clerks, warders, cooks, inspectors) are hired through the labour market and paid by the
//     treasury every local day the post is open; the offices that decide (a constable, a prosecutor, a judge, an auditor) are the charter's, held by
//     players, and the NPC judge is the fallback so a settlement is never a legal vacuum.
//   - WHAT IT CONSUMES. Paper, firewood and meals of the staff (the service day); points of a day's patrol and case work; the fees of a complaint, an
//     appeal, an audit and an escrow; the food of every prisoner, each day, from the treasury.
//   - WHAT IT PROVIDES. Evidence made only of recorded acts, warrants a judge signs, trials in a staffed courthouse, sentences (a fine, a ban, a term
//     in a real jail), a hidden board with a premium and an escrow that may fail, a customs roll on every declared load.
//   - WHAT BREAKS. A post nobody staffs, feeds or pays is closed that day: no patrol, no custody, no hearing, no cell, no inspection.

// VillageLawRequest is the payload of settlement.law.
type VillageLawRequest struct {
	// Action is "" (the screen) or one of village.LawAction*.
	Action string `json:"action,omitempty"`
	// ID names a case, a warrant, an item of evidence, a sentence, an informant, a bribe, a listing, an escrow, a load or a batch.
	ID string `json:"id,omitempty"`
	// Code names an offence, a good or a business; Player is a public code of a player.
	Code   string `json:"code,omitempty"`
	Player string `json:"player,omitempty"`
	// Kind is a kind of sentence, a rung of a bribe, a model or a party; Scope what a ban keeps a person out of.
	Kind  string `json:"kind,omitempty"`
	Scope string `json:"scope,omitempty"`
	// The figures: an amount of money, hours, days, a quantity or a number of basis points, sent as text like every argument.
	Amount string `json:"amount,omitempty"`
	Hours  string `json:"hours,omitempty"`
	Days   string `json:"days,omitempty"`
	Qty    string `json:"qty,omitempty"`
	Bps    string `json:"bps,omitempty"`
	// Dirty says an amount is paid from the dirty balance; Items is a manifest "code:qty,code:qty".
	Dirty string `json:"dirty,omitempty"`
	Items string `json:"items,omitempty"`
	Carry string `json:"carry,omitempty"`
}

func (r VillageLawRequest) num(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// lawCtx is what one act of the law screen works with.
type lawCtx struct {
	meta   envelope.Metadata
	snap   *content.Snapshot
	s      application.FoundedSettlement
	has    bool
	p      *application.Player
	now    time.Time
	t      law.Tuning
	req    VillageLawRequest
	mature bool
}

func lawRefuse(kind string) error { return refuseVillage(kind, village.AddrLaw) }

// WithLaw sets the rules of the item (config law.*). Until they are set, and before their dates, every act of the item is refused.
func (h *VillageHandler) WithLaw(r application.LawRules) *VillageHandler {
	h.law = r
	return h
}

func (h *VillageHandler) policeOpen() bool  { return h.law.PoliceOpen(h.now()) }
func (h *VillageHandler) courtsOpen() bool  { return h.law.CourtsOpen(h.now()) }
func (h *VillageHandler) blackOpen() bool   { return h.law.BlackOpen(h.now()) }
func (h *VillageHandler) customsOpen() bool { return h.law.CustomsOpen(h.now()) }
func (h *VillageHandler) launderOpen() bool { return h.law.LaunderOpen(h.now()) }

// matureOn reads the global operator switch of the mature content. It reads through the pool, so it is called before a unit of work opens and never inside one.
func (h *VillageHandler) matureOn(ctx context.Context) bool {
	return h.law.Mature != nil && h.law.Mature(ctx)
}

// lawNeed refuses an act of a part of the item that has not begun.
func (h *VillageHandler) lawNeed(open bool, kind string) error {
	if !open {
		return lawRefuse(kind)
	}
	return nil
}

// lawPerm requires a permission of the charter and returns its ceiling.
func lawPerm(ctx context.Context, tx application.Tx, lc *lawCtx, perm charter.Permission) (int64, error) {
	if !lc.has {
		return 0, lawRefuse(village.LawNoSettlement)
	}
	return requirePermission(ctx, tx, lc.s, lc.p.ID, perm)
}

// lawMay is lawPerm as a flag for views and for acts that may be done by an office or by anyone.
func lawMay(ctx context.Context, tx application.Tx, lc *lawCtx, perm charter.Permission) bool {
	if !lc.has {
		return false
	}
	return hasPermission(ctx, tx, lc.s, lc.p.ID, perm)
}

// lawHolders lists the players who hold a permission in the settlement now, sorted.
func lawHolders(ctx context.Context, tx application.Tx, s application.FoundedSettlement, perm charter.Permission) ([]string, error) {
	st, err := loadCharterState(ctx, tx, s, "head")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		has := false
		for _, g := range o.Grants {
			has = has || g.Permission == perm
		}
		if !has {
			continue
		}
		if o.Acquisition == charter.AcquireHead {
			if st.headHeld != "" {
				seen[st.headHeld] = true
			}
			continue
		}
		for _, seat := range st.seats[o.ID] {
			seen[seat.HolderID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// lawPlayerByCode resolves a public code of a player.
func lawPlayerByCode(ctx context.Context, tx application.Tx, code string) (string, error) {
	id, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(code))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", lawRefuse(village.LawNoPlayer)
	}
	return id, nil
}

// lawAcct opens the account of a kind for an owner.
func lawAcct(ctx context.Context, tx application.Tx, kind application.AccountKind, owner string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, kind, owner)
}

// lawPost moves amount between two accounts under a reason, with a reference. A non-positive amount moves nothing.
func lawPost(ctx context.Context, tx application.Tx, lc *lawCtx, reason application.Reason, refType, refID, from, to string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	_, err := postRef(ctx, tx.Ledger(), reason, refType, refID, from, to, money.FromMinor(amount), lc.now)
	return err
}

// lawBalance reads the balance of an account of a kind for an owner.
func lawBalance(ctx context.Context, tx application.Tx, kind application.AccountKind, owner string) (application.Account, int64, error) {
	a, err := tx.Ledger().AccountFor(ctx, kind, owner)
	if err != nil {
		return a, 0, err
	}
	bal, err := tx.Ledger().Balance(ctx, a.ID)
	if err != nil {
		return a, 0, err
	}
	return a, bal.Minor(), nil
}

// lawTake takes up to amount from a player's cash and then his bank into the account `to` under the reason, and returns what was taken.
func lawTake(ctx context.Context, tx application.Tx, lc *lawCtx, playerID string, amount int64, reason application.Reason, refType, refID, to string) (int64, error) {
	var taken int64
	for _, kind := range []application.AccountKind{application.AccountPlayerCash, application.AccountPlayerBank} {
		if amount-taken <= 0 {
			break
		}
		a, bal, err := lawBalance(ctx, tx, kind, playerID)
		if err != nil {
			return taken, err
		}
		n := min(bal, amount-taken)
		if n <= 0 {
			continue
		}
		if err := lawPost(ctx, tx, lc, reason, refType, refID, a.ID, to, n); err != nil {
			return taken, err
		}
		taken += n
	}
	return taken, nil
}

// lawPay takes exactly amount from a player's cash and bank, or refuses with the player's lack of money.
func lawPay(ctx context.Context, tx application.Tx, lc *lawCtx, playerID string, amount int64, reason application.Reason, refType, refID, to string) error {
	if amount <= 0 {
		return nil
	}
	_, cash, err := lawBalance(ctx, tx, application.AccountPlayerCash, playerID)
	if err != nil {
		return err
	}
	_, bank, err := lawBalance(ctx, tx, application.AccountPlayerBank, playerID)
	if err != nil {
		return err
	}
	if cash+bank < amount {
		return lawRefuse(village.LawNoMoney)
	}
	_, err = lawTake(ctx, tx, lc, playerID, amount, reason, refType, refID, to)
	return err
}

// lawTreasury is the settlement's treasury account.
func lawTreasury(ctx context.Context, tx application.Tx, settlementID string) (application.Account, error) {
	return lawAcct(ctx, tx, application.AccountCityTreasury, settlementID)
}

// lawSink is the system sink.
func lawSink(ctx context.Context, tx application.Tx) (application.Account, error) {
	return lawAcct(ctx, tx, application.AccountSystemSink, "")
}

// lawFromTreasury pays amount out of the settlement's treasury, or refuses when it holds less.
func lawFromTreasury(ctx context.Context, tx application.Tx, lc *lawCtx, reason application.Reason, refType, refID, to string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	tr, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
	if err != nil {
		return err
	}
	if bal < amount {
		return lawRefuse(village.LawTreasuryShort)
	}
	return lawPost(ctx, tx, lc, reason, refType, refID, tr.ID, to, amount)
}

// lawFromPurse pays amount for the police: the purse first, the treasury for what the purse lacks.
func lawFromPurse(ctx context.Context, tx application.Tx, lc *lawCtx, reason application.Reason, refType, refID, to string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	pu, bal, err := lawBalance(ctx, tx, application.AccountPolicePurse, lc.s.CityID)
	if err != nil {
		return err
	}
	from := min(bal, amount)
	if err := lawPost(ctx, tx, lc, reason, refType, refID, pu.ID, to, from); err != nil {
		return err
	}
	return lawFromTreasury(ctx, tx, lc, reason, refType, refID, to, amount-from)
}

// lawDay is the settlement's local day number, the one the service day uses.
func (h *VillageHandler) lawDay(lc *lawCtx) int64 {
	return h.service.Clock.DayAtIn(lc.now, lc.s.Zone())
}

// lawPost is a post of the settlement that was open today.
type lawOpenPost struct {
	Building application.SettlementBuildingInstance
	Def      content.BuildingFunctionDef
	Staff    int64
}

// lawOpenPosts are the posts of a service (constabulary, court, custody, customs) that were open today. The day is judged now if nobody has yet.
func (h *VillageHandler) lawOpenPosts(ctx context.Context, tx application.Tx, lc *lawCtx, service string) ([]lawOpenPost, error) {
	if !lc.has {
		return nil, nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, lc.s.CityID)
	if err != nil {
		return nil, err
	}
	day, err := h.SettleServiceDay(ctx, tx, lc.snap, lc.s, buildings)
	if err != nil || day == nil {
		return nil, err
	}
	var out []lawOpenPost
	for _, sp := range servicePosts(lc.snap, buildings) {
		if sp.service() == service && day.HeldPost(sp.b.ID) {
			slots, _ := staffOf(lc.snap, sp.def)
			out = append(out, lawOpenPost{Building: sp.b, Def: sp.def, Staff: slots})
		}
	}
	return out, nil
}

// lawPoints grants the day's points once (the first act of the day writes them) and returns the day and what is left.
func (h *VillageHandler) lawPoints(ctx context.Context, tx application.Tx, lc *lawCtx) (day, left, patrol int64, err error) {
	day = h.lawDay(lc)
	posts, err := h.lawOpenPosts(ctx, tx, lc, "constabulary")
	if err != nil {
		return day, 0, 0, err
	}
	var points int64
	for _, ps := range posts {
		points += ps.Staff * lc.t.PointsPerStaff
		if ps.Building.TypeCode == "police_post" {
			points += lc.t.StationBasePoints
		} else {
			points += lc.t.PostBasePoints
		}
	}
	if _, err := tx.Law().GrantPoints(ctx, lc.s.CityID, day, points); err != nil {
		return day, 0, 0, err
	}
	p, spent, pat, _, err := tx.Law().Points(ctx, lc.s.CityID, day)
	return day, p - spent, pat, err
}

// lawSpend takes cost points of today, or refuses.
func (h *VillageHandler) lawSpend(ctx context.Context, tx application.Tx, lc *lawCtx, cost int64, patrol bool) error {
	day, left, _, err := h.lawPoints(ctx, tx, lc)
	if err != nil {
		return err
	}
	if left < cost {
		return lawRefuse(village.LawNoPoints)
	}
	if ok, err := tx.Law().Spend(ctx, lc.s.CityID, day, cost, patrol); err != nil {
		return err
	} else if !ok {
		return lawRefuse(village.LawNoPoints)
	}
	return nil
}

// lawPatrolToday is how many patrol shifts the beat has had today, for the chances that a patrol lifts.
func (h *VillageHandler) lawPatrolToday(ctx context.Context, tx application.Tx, lc *lawCtx) (int64, error) {
	_, _, pat, ok, err := tx.Law().Points(ctx, lc.s.CityID, h.lawDay(lc))
	if err != nil || !ok {
		return 0, err
	}
	return pat, nil
}

// lawActOffence is the offence a recorded act is, "" for an act that is no offence (a trade in a good nobody outlaws, a load declared whole).
func lawActOffence(snap *content.Snapshot, a application.LawAct) string {
	switch a.Kind {
	case "trade":
		if d, ok := snap.ShadowSupplyOf(a.Item); ok {
			return d.Offence
		}
	case "scam":
		return "fraud"
	case "batch":
		return "laundering"
	case "bribe":
		return "bribery"
	case "espionage":
		return "espionage"
	case "press":
		return "false_evidence"
	case "load":
		l := law.Load{Declared: a.Declared, Carried: a.Carried}
		if len(l.Undeclared()) > 0 {
			return "smuggling"
		}
	}
	return ""
}

// lawBribeScale lowers a number by an accepted bribe of the accused to the acting holder on a rung (a rung is a decision of a person: the
// holder's own).
func (h *VillageHandler) lawBribeScale(ctx context.Context, tx application.Tx, lc *lawCtx, accused, holder, rung string, v int64) (int64, error) {
	b, err := tx.Law().BribeFrom(ctx, lc.s.CityID, accused, holder, rung, lc.now)
	if err != nil || b == nil {
		return v, err
	}
	return lc.t.AfterBribe(v, int64(b.EffectBPS)), nil
}

// lawMaturePolicy refuses a mature offence or good while the switch is off: it is neither listed nor chargeable nor sent.
func lawMatureHidden(mature bool, isMature bool) bool { return isMature && !mature }
