package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// MilitaryRules is the tuning of the armed forces (config military).
type MilitaryRules struct {
	// Period is one defence period, GAME time.
	Period time.Duration
	// ReadinessLossBPS and ReadinessRecoveryBPS are what a short and a
	// fully paid period do to readiness.
	ReadinessLossBPS     int64
	ReadinessRecoveryBPS int64
	// ReferenceRadarKM is the radar the forces screen measures designs by.
	ReferenceRadarKM int64
	// LicenceRevokeNotice is how long a revoked defence licence stays in
	// force, REAL time (docs/adr/0022, section 2.14); EndedLicencesShown
	// how many ended licences the registry lists.
	LicenceRevokeNotice time.Duration
	EndedLicencesShown  int
}

// MilitaryHandler serves the armed forces
// (docs/adr/0022-military-and-diplomacy.md): a country's ministry of
// defence and its forces, one branch's equipment and stationing it, arms
// procurement, and — from the scheduler — a defence period ending and
// equipment reaching its garrison, each once.
//
// # Who may do what
//
// Reading the ministry and the public summary is anyone's; the forces in
// full are for the offices military.yml clears. Buying arms is the action
// country.procure; stationing a branch's equipment its branch's command
// action (military.yml); both are authorised by application.Authorize, the
// same acting chain a lever's resolver walks.
type MilitaryHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	rules   MilitaryRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewMilitaryHandler wires the handler.
func NewMilitaryHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, rules MilitaryRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *MilitaryHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewMilitaryHandler requires content, cities, a policy reader and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.Period <= 0 || rules.ReferenceRadarKM <= 0 ||
		rules.ReadinessLossBPS < 0 || rules.ReadinessLossBPS > military.BPSWhole ||
		rules.ReadinessRecoveryBPS < 0 || rules.ReadinessRecoveryBPS > military.BPSWhole {
		panic("handlers: NewMilitaryHandler requires a game clock, an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &MilitaryHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		scale: scale, rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// MilitaryRequest is the payload of the military commands; which fields a
// command reads is its own.
type MilitaryRequest struct {
	Country string `json:"country,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Target  string `json:"target,omitempty"`
	City    string `json:"city,omitempty"`
	No      string `json:"no,omitempty"`
	Qty     string `json:"qty,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Verdict string `json:"verdict,omitempty"`
}

func (r MilitaryRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.MilitaryConfirm
}

func (h *MilitaryHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// militaryRefusal carries a refused military command out of a unit of work.
type militaryRefusal struct{ view screens.MilitaryRefusalView }

func (r *militaryRefusal) Error() string { return "handlers: military refused: " + r.view.Kind }

func refuseMilitary(kind string, country *application.Jurisdiction) *militaryRefusal {
	r := &militaryRefusal{view: screens.MilitaryRefusalView{Kind: kind}}
	if country != nil {
		r.view.Country = countryPlace(*country)
	}
	return r
}

func (r *militaryRefusal) back(addr ...string) *militaryRefusal {
	r.view.Back = addr
	return r
}

// finish turns a refusal into its screen.
func (h *MilitaryHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *militaryRefusal
	if stderrors.As(err, &r) {
		return screens.MilitaryRefusal(c, r.view), nil
	}
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(c, v), nil
	}
	if isSentinel(err, application.ErrJurisdictionNotFound) {
		return screens.MilitaryRefusal(c, screens.MilitaryRefusalView{Kind: screens.MilitaryRefusedNoCountry}), nil
	}
	return nil, err
}

// player reads the player behind a command and the language to answer in.
func (h *MilitaryHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

// reserve takes the idempotency key of a command that writes.
func (h *MilitaryHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// country resolves the country of a request, refusing one the player
// belongs to none of when it names none.
func (h *MilitaryHandler) country(ctx context.Context, tx application.Tx, p *application.Player, code string) (*application.Jurisdiction, error) {
	j, err := countryFor(ctx, tx, p, code)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, refuseMilitary(screens.MilitaryRefusedNoCountry, nil)
	}
	return j, nil
}

// ---------------------------------------------------------------------------
// The defence clock.

// MilitaryPeriodPayload is the jsonb a defence period's scheduled action
// carries.
type MilitaryPeriodPayload struct {
	CountryID string `json:"country_id"`
	PeriodNo  int64  `json:"period_no"`
}

// ensureClock makes sure a country's defence clock is running and returns
// it, locked.
func (h *MilitaryHandler) ensureClock(ctx context.Context, tx application.Tx, countryID string, now time.Time) (*application.MilitaryClock, error) {
	clock, err := tx.Military().Clock(ctx, countryID, now)
	if err != nil {
		return nil, err
	}
	if clock.ActionID != "" {
		return clock, nil
	}
	clock.PeriodStartedAt = now
	return clock, h.schedule(ctx, tx, clock, now)
}

// schedule puts the end of the clock's period on the schedule and saves it.
func (h *MilitaryHandler) schedule(ctx context.Context, tx application.Tx, clock *application.MilitaryClock, now time.Time) error {
	wait := h.scale.RealWait(h.rules.Period)
	next := clock.PeriodStartedAt.Add(wait)
	if !next.After(now) {
		next = now.Add(wait)
	}
	payload, err := json.Marshal(MilitaryPeriodPayload{CountryID: clock.CountryID, PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.MilitaryPeriodActionType, ActorType: "system",
		ReferenceType: application.MilitaryClockReference, ReferenceID: clock.CountryID, Payload: payload,
		StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.Military().SaveClock(ctx, *clock)
}

// StartClocks runs at start-up and after a content load: every country's
// defence clock is running. It is idempotent.
func (h *MilitaryHandler) StartClocks(ctx context.Context) error {
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		countries, err := tx.Diplomacy().Countries(ctx)
		if err != nil {
			return err
		}
		now := h.now()
		for _, c := range countries {
			if _, err := h.ensureClock(ctx, tx, c.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Settle handles military.settle from the SCHEDULER: one defence period of
// one country (ADR 0022 §2.4), exactly once — the clock row is locked first
// and must name this action and period, and the period's record has the
// country and the period as its primary key.
func (h *MilitaryHandler) Settle(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in MilitaryPeriodPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("defence period payload is unreadable").WithCause(err)
		}
	}
	if in.CountryID == "" {
		in.CountryID = req.ReferenceID
	}
	if in.CountryID == "" || in.PeriodNo < 1 {
		return nil, errors.InvalidInput("defence period names no country or no period")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Military().Clock(ctx, in.CountryID, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || clock.ActionID == "" || (req.ActionID != "" && clock.ActionID != req.ActionID) {
			// Settled already, or an action this clock no longer runs.
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			return errors.Internal(stderrors.New("handlers: a defence period ran before it ended"))
		}
		return h.settle(ctx, tx, snap, clock, now)
	})
}

// settle runs one period of one country, its clock locked.
func (h *MilitaryHandler) settle(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	clock *application.MilitaryClock, now time.Time,
) error {
	country := clock.CountryID
	cities, err := tx.Diplomacy().CitiesOf(ctx, country)
	if err != nil {
		return err
	}
	share, err := h.policy.Get(ctx, country, LeverRevenueShare)
	if err != nil {
		return err
	}
	defence, err := h.policy.Get(ctx, country, LeverDefenceBudget)
	if err != nil {
		return err
	}
	period := military.Period{RevenueShareBPS: share.Value, DefenceBudgetBPS: defence.Value, Readiness: clock.ReadinessBPS,
		LossBPS: h.rules.ReadinessLossBPS, RecoveryBPS: h.rules.ReadinessRecoveryBPS}
	// The war economy: a country at war levies its cities for the war too.
	wars, err := tx.War().Wars(ctx, country, now)
	if err != nil {
		return err
	}
	rules := make([]war.War, len(wars))
	for i, w := range wars {
		rules[i] = w.Rule()
	}
	if war.AtWar(rules, country, now) {
		levy, err := h.policy.Get(ctx, country, LeverWarLevy)
		if err != nil {
			return err
		}
		period.WarLevyBPS = levy.Value
	}
	accounts := map[string]string{}
	var revenue int64
	for _, city := range cities {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		took, err := tx.Military().Revenue(ctx, acct.ID, clock.PeriodStartedAt, now)
		if err != nil {
			return err
		}
		accounts[city.ID] = acct.ID
		revenue += took
		period.Cities = append(period.Cities, military.CityRevenue{CityID: city.ID, Revenue: took, Balance: acct.Balance.Minor()})
	}
	counts, err := tx.Military().CountByClass(ctx, country)
	if err != nil {
		return err
	}
	var pieces int64
	for _, n := range counts {
		pieces += n
	}
	if period.UpkeepDue, err = military.Upkeep(counts, snap.MilitaryClasses()); err != nil {
		return errors.Internal(err)
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountStateTreasury, country)
	if err != nil {
		return err
	}
	fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country)
	if err != nil {
		return err
	}
	period.Fund = fund.Balance.Minor()
	out, err := military.Settle(period)
	if err != nil {
		return errors.Internal(err)
	}

	ref := clock.ActionID
	if len(out.Levies) > 0 {
		entries := []application.LedgerEntry{{AccountID: treasury.ID, Amount: money.FromMinor(out.Levy)}}
		for _, l := range out.Levies {
			entries = append(entries, application.LedgerEntry{AccountID: accounts[l.CityID], Amount: money.FromMinor(-l.Amount)})
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonNationalLevy,
			ReferenceType: "game_actions", ReferenceID: ref, Entries: entries, CreatedAt: now}); err != nil {
			return err
		}
	}
	if out.Appropriation > 0 {
		if _, err := postRef(ctx, tx.Ledger(), application.ReasonDefenceAppropriation, "game_actions", ref,
			treasury.ID, fund.ID, money.FromMinor(out.Appropriation), now); err != nil {
			return err
		}
	}
	for _, l := range out.WarLevies {
		if _, err := postRef(ctx, tx.Ledger(), application.ReasonWarLevy, "game_actions", ref,
			accounts[l.CityID], fund.ID, money.FromMinor(l.Amount), now); err != nil {
			return err
		}
	}
	if out.UpkeepPaid > 0 {
		if _, err := postRef(ctx, tx.Ledger(), application.ReasonMilitaryUpkeep, "game_actions", ref,
			fund.ID, application.SystemSinkAccountID, money.FromMinor(out.UpkeepPaid), now); err != nil {
			return err
		}
	}
	// Repairs: equipment damaged in battle, the longest damaged first, as
	// far as the fund goes; the rest waits for the next period.
	damaged, err := tx.War().Damaged(ctx, country)
	if err != nil {
		return err
	}
	costs := make([]int64, len(damaged))
	for i, a := range damaged {
		if cl, ok := snap.ForceClass(a.ClassCode); ok {
			costs[i] = cl.Repair
		}
	}
	repaired, repairs, err := military.Repairs(costs, out.Fund)
	if err != nil {
		return errors.Internal(err)
	}
	if repairs > 0 {
		if _, err := postRef(ctx, tx.Ledger(), application.ReasonMilitaryRepair, "game_actions", ref,
			fund.ID, application.SystemSinkAccountID, money.FromMinor(repairs), now); err != nil {
			return err
		}
	}
	if repaired > 0 {
		ids := make([]string, repaired)
		for i := range ids {
			ids[i] = damaged[i].PieceID
		}
		if err := tx.War().Repair(ctx, ids, now); err != nil {
			return err
		}
	}
	if err := tx.Military().RecordPeriod(ctx, application.MilitaryPeriod{CountryID: country, PeriodNo: clock.PeriodNo,
		StartedAt: clock.PeriodStartedAt, EndedAt: now, Revenue: revenue, Levy: out.Levy, Appropriation: out.Appropriation,
		UpkeepDue: out.UpkeepDue, UpkeepPaid: out.UpkeepPaid, Pieces: pieces, ReadinessBPS: out.Readiness,
		WarLevy: out.WarLevy, Repairs: repairs, Repaired: int64(repaired)}); err != nil {
		return err
	}
	clock.PeriodNo++
	clock.PeriodStartedAt = now
	clock.ReadinessBPS = out.Readiness
	clock.NextAt, clock.ActionID = nil, ""
	return h.schedule(ctx, tx, clock, now)
}

// postRef moves amount from one account to another under reason, with a
// reference.
func postRef(ctx context.Context, ledger application.LedgerRepository, reason application.Reason, refType, refID, from, to string,
	amount money.Amount, now time.Time,
) (string, error) {
	neg, err := amount.Neg()
	if err != nil {
		return "", errors.Internal(err)
	}
	return ledger.Post(ctx, application.LedgerTransaction{
		Reason: reason, ReferenceType: refType, ReferenceID: refID,
		Entries:   []application.LedgerEntry{{AccountID: from, Amount: neg}, {AccountID: to, Amount: amount}},
		CreatedAt: now,
	})
}

// ---------------------------------------------------------------------------
// The ministry and the forces.

// forceSummary is a country's classes by branch, in the content's order,
// with bands and counts.
func forceSummary(snap *content.Snapshot, counts map[string]int64) []screens.BranchForces {
	var out []screens.BranchForces
	bands := snap.StrengthBands()
	for _, b := range snap.Branches() {
		bf := screens.BranchForces{Branch: named(b.Code, b.Name)}
		for _, cl := range snap.ForceClasses() {
			if cl.Branch != b.Code || counts[cl.Code] == 0 {
				continue
			}
			bf.Classes = append(bf.Classes, screens.ForceClassLine{Class: named(cl.Code, cl.Name),
				Band: military.BandOf(bands, counts[cl.Code]), Count: counts[cl.Code]})
		}
		out = append(out, bf)
	}
	return out
}

// ministryOffices are the offices the ministry lists: those cleared to see
// the forces, then those that buy, sanction and conclude treaties.
func ministryOffices(snap *content.Snapshot) []string {
	out := snap.MilitaryClearance()
	for _, a := range []string{content.ActionProcure, content.ActionSanction, content.ActionTreaty} {
		if o := actionOffice(snap, a); o != "" && !hasCode(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// Ministry handles military.ministry: a country's ministry of defence.
func (h *MilitaryHandler) Ministry(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MinistryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		now := h.now()
		view = screens.MinistryView{Country: countryPlace(*country)}
		for _, code := range ministryOffices(snap) {
			line, err := officeLine(ctx, tx, code, country.ID)
			if err != nil {
				return err
			}
			view.Offices = append(view.Offices, line)
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountStateTreasury, country.ID)
		if err != nil {
			return err
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country.ID)
		if err != nil {
			return err
		}
		view.Treasury, view.Fund = treasury.Balance.Minor(), fund.Balance.Minor()
		for lever, into := range map[string]*int64{LeverRevenueShare: &view.RevenueShareBPS,
			LeverDefenceBudget: &view.DefenceBudgetBPS, LeverArmsExports: &view.ArmsExports} {
			v, err := h.policy.Get(ctx, country.ID, lever)
			if err != nil {
				return err
			}
			*into = v.Value
		}
		periods, err := tx.Military().Periods(ctx, country.ID, 1)
		if err != nil {
			return err
		}
		if len(periods) > 0 {
			last := periods[0]
			view.Last = &screens.PeriodLine{Levy: last.Levy, Appropriation: last.Appropriation, UpkeepDue: last.UpkeepDue,
				UpkeepPaid: last.UpkeepPaid}
		}
		clock, err := h.ensureClock(ctx, tx, country.ID, now)
		if err != nil {
			return err
		}
		if clock.NextAt != nil && clock.NextAt.After(now) {
			view.NextIn, view.NextAt = clock.NextAt.Sub(now), *clock.NextAt
		}
		counts, err := tx.Military().CountByClass(ctx, country.ID)
		if err != nil {
			return err
		}
		view.Forces = forceSummary(snap, counts)
		if view.PendingLicences, err = pendingLicences(ctx, tx, country.ID, now); err != nil {
			return err
		}
		if !meta.InGroup() {
			if view.Cleared, err = cleared(ctx, tx, snap, country.ID, p); err != nil {
				return err
			}
			if _, view.CanProcure, err = mayAct(ctx, tx, snap, country.ID, content.ActionProcure, p); err != nil {
				return err
			}
		}
		if !view.Cleared {
			for i := range view.Forces {
				for j := range view.Forces[i].Classes {
					view.Forces[i].Classes[j].Count = 0
				}
			}
		} else {
			view.Readiness = clock.ReadinessBPS
			if view.Upkeep, err = military.Upkeep(counts, snap.MilitaryClasses()); err != nil {
				return errors.Internal(err)
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Ministry(h.screen(meta, lang), view), nil
}

// Forces handles military.forces: a country's forces by branch — the public
// summary, or the full count for a cleared viewer in private.
func (h *MilitaryHandler) Forces(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ForcesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		counts, err := tx.Military().CountByClass(ctx, country.ID)
		if err != nil {
			return err
		}
		view = screens.ForcesView{Country: countryPlace(*country), Branches: forceSummary(snap, counts)}
		if !meta.InGroup() {
			if view.Cleared, err = cleared(ctx, tx, snap, country.ID, p); err != nil {
				return err
			}
		}
		if !view.Cleared {
			for i := range view.Branches {
				for j := range view.Branches[i].Classes {
					view.Branches[i].Classes[j].Count = 0
				}
			}
			return nil
		}
		clock, err := h.ensureClock(ctx, tx, country.ID, h.now())
		if err != nil {
			return err
		}
		view.Readiness = clock.ReadinessBPS
		if view.Upkeep, err = military.Upkeep(counts, snap.MilitaryClasses()); err != nil {
			return errors.Internal(err)
		}
		moves, err := tx.Military().Moves(ctx, country.ID)
		if err != nil {
			return err
		}
		for _, m := range moves {
			view.Moving += m.Qty
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Forces(h.screen(meta, lang), view), nil
}

// assetGroup keys a piece's group: its good and design.
func assetGroup(a application.MilitaryAsset) string { return a.Item + "|" + a.DesignID }

// groupGood names a group's good, with its design when it has one.
func groupGood(ctx context.Context, tx application.Tx, snap *content.Snapshot, itemCode, designID string,
	designs map[string]*application.Design,
) (screens.Good, *application.Design, error) {
	if designID == "" {
		return screens.Good{Item: itemNamed(snap, itemCode)}, nil, nil
	}
	d, ok := designs[designID]
	if !ok {
		var err error
		if d, err = tx.Production().DesignByID(ctx, designID); err != nil {
			return screens.Good{}, nil, err
		}
		designs[designID] = d
	}
	return designGood(snap, *d), d, nil
}

// designAttributes are a design's attributes in the archetype's order, all
// of them: the cleared see the signature.
func designAttributes(snap *content.Snapshot, d *application.Design) []screens.AttributeLine {
	if d == nil {
		return nil
	}
	a, ok := snap.Archetype(d.Archetype)
	if !ok {
		return nil
	}
	attrs, err := item.ComputeAttributes(a, domainDesign(*d), snap.Components())
	if err != nil {
		return nil
	}
	var out []screens.AttributeLine
	for _, at := range a.Attributes {
		// Quality is the piece's own; a band that enlarges nothing is not
		// worth a line.
		if at.Name == "quality" || (at.Name == "rcs_gain" && attrs[at.Name] == military.BPSWhole) {
			continue
		}
		out = append(out, screens.AttributeLine{Name: at.Name, Value: attrs[at.Name], Observable: at.Observable})
	}
	return out
}

// Branch handles military.branch: one branch's equipment, in full, for a
// cleared viewer.
func (h *MilitaryHandler) Branch(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	return h.branchWith(ctx, meta, req, "")
}

func (h *MilitaryHandler) branchWith(ctx context.Context, meta envelope.Metadata, req MilitaryRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.BranchView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		branch, ok := snap.Branch(strings.TrimSpace(req.Branch))
		if !ok {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country)
		}
		ok, err = cleared(ctx, tx, snap, country.ID, p)
		if err != nil {
			return err
		}
		if !ok {
			r := refuseMilitary(screens.MilitaryRefusedNotHolder, country)
			r.view.Office = actionOffice(snap, branch.Command)
			return r
		}
		view = screens.BranchView{Country: countryPlace(*country), Branch: named(branch.Code, branch.Name),
			ReferenceRadarKM: h.rules.ReferenceRadarKM, Notice: notice}
		if _, view.CanStation, err = mayAct(ctx, tx, snap, country.ID, branch.Command, p); err != nil {
			return err
		}
		return h.fillBranch(ctx, tx, snap, country.ID, branch, &view)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Branch(h.screen(meta, lang), view), nil
}

// fillBranch groups a branch's pieces by good and design, with where they
// stand and what is on the move.
func (h *MilitaryHandler) fillBranch(ctx context.Context, tx application.Tx, snap *content.Snapshot, countryID string,
	branch content.BranchDef, view *screens.BranchView,
) error {
	assets, err := tx.Military().Assets(ctx, countryID)
	if err != nil {
		return err
	}
	cityNames := map[string]*application.City{}
	cityOf := func(id string) (*application.City, error) {
		if c, ok := cityNames[id]; ok {
			return c, nil
		}
		c, err := h.cities.ByID(ctx, id)
		if err != nil {
			return nil, err
		}
		cityNames[id] = c
		return c, nil
	}
	designs := map[string]*application.Design{}
	type group struct {
		view      screens.AssetGroup
		quality   int
		garrisons map[string]int64
	}
	groups := map[string]*group{}
	var order []string
	for _, a := range assets {
		if a.Branch != branch.Code {
			continue
		}
		k := assetGroup(a)
		g, ok := groups[k]
		if !ok {
			good, d, err := groupGood(ctx, tx, snap, a.Item, a.DesignID, designs)
			if err != nil {
				return err
			}
			cl, _ := snap.ForceClass(a.ClassCode)
			g = &group{view: screens.AssetGroup{Good: good, Class: named(a.ClassCode, cl.Name),
				Attributes: designAttributes(snap, d)}, garrisons: map[string]int64{}}
			for _, at := range g.view.Attributes {
				if at.Name == "rcs" && at.Value > 0 {
					g.view.SeenAt = military.DetectionRangeKM(h.rules.ReferenceRadarKM, at.Value, military.BPSWhole)
				}
			}
			groups[k] = g
			order = append(order, k)
		}
		g.view.Count++
		g.quality += a.Quality
		if a.Condition == application.AssetDamaged {
			g.view.Damaged++
		}
		switch {
		case a.Status == application.AssetMoving:
			g.view.Moving++
		case a.Status == application.AssetCommitted:
			g.view.Committed++
		case a.GarrisonCityID == "":
			g.view.Depot++
		default:
			g.garrisons[a.GarrisonCityID]++
		}
	}
	for _, k := range order {
		g := groups[k]
		g.view.Quality = max(g.quality/int(max(g.view.Count, 1)), 1)
		ids := make([]string, 0, len(g.garrisons))
		for id := range g.garrisons {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			city, err := cityOf(id)
			if err != nil {
				return err
			}
			g.view.Garrisons = append(g.view.Garrisons, screens.GarrisonLine{CityCode: city.Code, City: city.Name, Count: g.garrisons[id]})
		}
		view.Groups = append(view.Groups, g.view)
	}
	moves, err := tx.Military().Moves(ctx, countryID)
	if err != nil {
		return err
	}
	now := h.now()
	for _, m := range moves {
		if m.Branch != branch.Code {
			continue
		}
		good, _, err := groupGood(ctx, tx, snap, m.Item, m.DesignID, designs)
		if err != nil {
			return err
		}
		city, err := cityOf(m.ToCityID)
		if err != nil {
			return err
		}
		view.Moves = append(view.Moves, screens.MoveLine{Good: good, Qty: m.Qty, CityCode: city.Code, City: city.Name,
			Left: max(m.ArrivesAt.Sub(now), time.Second), At: m.ArrivesAt})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Stationing.

// groupTarget resolves a stationing target — «d12» for a design, else a
// good's code — to the good's code and design id.
func groupTarget(ctx context.Context, tx application.Tx, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if no, ok := strings.CutPrefix(raw, screens.DesignTargetPrefix); ok {
		n, ok := number(no)
		if !ok {
			return "", "", application.ErrDesignNotFound
		}
		d, err := tx.Production().Design(ctx, n, false)
		if err != nil {
			return "", "", err
		}
		return d.Item, d.ID, nil
	}
	return raw, "", nil
}

// Station handles military.station: ordering pieces of one good (and
// design) to a garrison in a city of the country — the city, then how many,
// then confirm. The move runs on the game clock; one scheduled action lands
// it (Arrive).
func (h *MilitaryHandler) Station(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view     screens.StationView
		done     bool
		country  *application.Jurisdiction
		branchCo string
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		if country, err = h.country(ctx, tx, p, req.Country); err != nil {
			return err
		}
		itemCode, designID, err := groupTarget(ctx, tx, req.Target)
		if isSentinel(err, application.ErrDesignNotFound) {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country)
		}
		if err != nil {
			return err
		}
		class, ok := snap.ClassOfItem(itemCode)
		if !ok {
			return refuseMilitary(screens.MilitaryRefusedNotArms, country)
		}
		branch, _ := snap.Branch(class.Branch)
		branchCo = branch.Code
		seat, ok, err := mayAct(ctx, tx, snap, country.ID, branch.Command, p)
		if err != nil {
			return err
		}
		if !ok {
			r := refuseMilitary(screens.MilitaryRefusedNotHolder, country).back(screens.AddrForces, country.Code)
			r.view.Office = actionOffice(snap, branch.Command)
			return r
		}
		assets, err := tx.Military().Assets(ctx, country.ID)
		if err != nil {
			return err
		}
		designs := map[string]*application.Design{}
		good, _, err := groupGood(ctx, tx, snap, itemCode, designID, designs)
		if err != nil {
			return err
		}
		view = screens.StationView{Country: countryPlace(*country), Branch: named(branch.Code, branch.Name), Good: good,
			Time: h.scale.RealWait(branch.RedeployTime())}
		cities, err := tx.Diplomacy().CitiesOf(ctx, country.ID)
		if err != nil {
			return err
		}
		for _, c := range cities {
			view.Cities = append(view.Cities, cityPlace(c))
		}
		code := strings.ToLower(strings.TrimSpace(req.City))
		if code == "" {
			return nil
		}
		var city *application.City
		for i := range cities {
			if cities[i].Code == code {
				city = &cities[i]
			}
		}
		if city == nil {
			return refuseMilitary(screens.MilitaryRefusedCity, country).back(screens.AddrStation, country.Code, good.TargetArg())
		}
		view.CityCode, view.City = city.Code, city.Name
		var movable []string
		for _, a := range assets {
			if a.Item == itemCode && a.DesignID == designID && a.Status == application.AssetStationed && a.GarrisonCityID != city.ID {
				movable = append(movable, a.PieceID)
			}
		}
		view.Available = int64(len(movable))
		if view.Available == 0 {
			r := refuseMilitary(screens.MilitaryRefusedStock, country).back(screens.AddrBranch, country.Code, branch.Code)
			return r
		}
		qty, ok := quantityArg(req.Qty)
		if !ok {
			return nil
		}
		if qty > view.Available {
			r := refuseMilitary(screens.MilitaryRefusedStock, country).back(screens.AddrStation, country.Code,
				good.TargetArg(), city.Code)
			r.view.Max = view.Available
			return r
		}
		view.Qty, view.Confirm = qty, true
		if !confirm {
			return nil
		}
		now := h.now()
		arrive := now.Add(view.Time)
		move := application.MilitaryMove{ID: h.ids.NewID(), CountryID: country.ID, Branch: branch.Code, ToCityID: city.ID,
			Item: itemCode, DesignID: designID, Qty: qty, GameActionID: h.ids.NewID(), OrderedBy: p.ID,
			OfficeCode: seat.OfficeCode, StartedAt: now, ArrivesAt: arrive}
		payload, err := json.Marshal(MilitaryMovePayload{MoveID: move.ID, PlayerID: p.ID})
		if err != nil {
			return err
		}
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: move.GameActionID,
			ActionType: application.MilitaryMoveActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: application.MilitaryMoveReference, ReferenceID: move.ID, Payload: payload,
			StartedAt: now, FinishAt: arrive}); err != nil {
			return err
		}
		if _, err := tx.Military().StartMove(ctx, move); err != nil {
			return err
		}
		if err := tx.Military().MarkMoving(ctx, movable[:qty], move.ID, now); err != nil {
			return err
		}
		done = true
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		notice := c.T("military.station.started", map[string]any{"count": screens.FormatNumber(c, view.Qty),
			"good": c.GoodName(view.Good), "city": c.CityName(view.CityCode, view.City),
			"time": screens.FormatDuration(c, view.Time)})
		return h.branchWith(ctx, meta, MilitaryRequest{Country: country.Code, Branch: branchCo}, notice)
	}
	return screens.Station(h.screen(meta, lang), view), nil
}

// MilitaryMovePayload is the jsonb a move's scheduled action carries.
type MilitaryMovePayload struct {
	MoveID   string `json:"move_id"`
	PlayerID string `json:"player_id"`
}

// Arrive handles military.arrive from the SCHEDULER: a move reaching its
// garrison, once — the move row is locked and must still be moving under
// this action.
func (h *MilitaryHandler) Arrive(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in MilitaryMovePayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("move payload is unreadable").WithCause(err)
		}
	}
	if in.MoveID == "" {
		in.MoveID = req.ReferenceID
	}
	if in.MoveID == "" {
		return nil, errors.InvalidInput("move names no move")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		m, err := tx.Military().Move(ctx, in.MoveID)
		if isSentinel(err, application.ErrMoveNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if m.Status != application.MoveMoving || (req.ActionID != "" && m.GameActionID != req.ActionID) {
			return nil
		}
		if now.Before(m.ArrivesAt) {
			return errors.Internal(stderrors.New("handlers: a move of forces landed before its time"))
		}
		landed, err := tx.Military().FinishMove(ctx, m.ID, now)
		if err != nil || landed == 0 {
			return err
		}
		country, err := tx.Governance().Jurisdiction(ctx, m.CountryID)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, m.ToCityID)
		if err != nil {
			return err
		}
		payload := map[string]any{"player_id": m.OrderedBy, "country_code": country.Code, "country_name": country.Name,
			"item": m.Item, "qty": landed, "city_code": city.Code, "city_name": city.Name, "branch": m.Branch}
		if b, ok := snap.Branch(m.Branch); ok {
			payload["branch_name"] = b.Name
		}
		if m.DesignID != "" {
			d, err := tx.Production().DesignByID(ctx, m.DesignID)
			if err != nil {
				return err
			}
			payload["design"], payload["design_no"] = d.Name, d.No
		}
		return appendDomainEvent(ctx, tx, meta, "military", "arrived", m.ID, payload)
	})
}

// qtyText is a count as a button argument.
func qtyText(n int64) string { return strconv.FormatInt(n, 10) }
