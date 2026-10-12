package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// ---------------------------------------------------------------------------
// laundering (docs/adr/0078 3.10)
// ---------------------------------------------------------------------------

// lawFrontRegister registers one of the player's businesses in the settlement as a front: the place dirty cash is passed through. It does not make
// the business illegal; the business is audited like any other, and a front is a business whose clean inflow may be out of proportion.
func (h *VillageHandler) lawFrontRegister(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.launderOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	co, err := tx.Companies().ByCode(ctx, strings.TrimSpace(lc.req.Code))
	if err != nil || co == nil {
		return "", lawRefuse(village.LawNoCompany)
	}
	if co.OwnerID != lc.p.ID || co.CityID != lc.s.CityID || co.Status != "active" {
		return "", lawRefuse(village.LawNoCompany)
	}
	if ok, err := tx.Law().InsertFront(ctx, application.LawFront{CompanyID: co.ID, SettlementID: lc.s.CityID, OwnerID: lc.p.ID, Since: lc.now}); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawAlreadyFront)
	}
	return village.LawNoticeFront, nil
}

// lawLaunder places a batch of dirty cash with a front. The money leaves the dirty balance into a pot at once and passes three states (placed, layered,
// integrated), each taking game time and each a chance of being noticed; at the end the fee goes to the sink and the rest is the front's clean money.
// A batch noticed midway is frozen: the pot is forfeited to the settlement. A batch over what the front's own turnover would explain is likelier noticed.
func (h *VillageHandler) lawLaunder(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.launderOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	co, err := tx.Companies().ByCode(ctx, strings.TrimSpace(lc.req.Code))
	if err != nil || co == nil {
		return "", lawRefuse(village.LawNoCompany)
	}
	front, err := tx.Law().FrontByCompany(ctx, co.ID)
	if err != nil {
		return "", err
	}
	if front == nil || front.OwnerID != lc.p.ID || front.SettlementID != lc.s.CityID {
		return "", lawRefuse(village.LawNoFront)
	}
	amount := lc.req.num(lc.req.Amount)
	if amount < 1 {
		return "", lawRefuse(village.LawBadArgs)
	}
	d, bal, err := lawBalance(ctx, tx, application.AccountPlayerDirty, lc.p.ID)
	if err != nil {
		return "", err
	}
	if bal < amount {
		return "", lawRefuse(village.LawNoMoney)
	}
	b := application.LawBatch{ID: h.ids.NewID(), CompanyID: co.ID, OwnerID: lc.p.ID, Amount: amount, Fee: lc.t.LaunderFee(amount), Day: h.lawDay(lc), At: lc.now}
	due := lc.now.Add(time.Duration(lc.t.LaunderStageHours) * time.Hour)
	b.StageDue = &due
	if err := tx.Law().InsertBatch(ctx, b); err != nil {
		return "", err
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, b.ID)
	if err != nil {
		return "", err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonLaunder, application.LawBatchReference, b.ID, d.ID, pot.ID, amount); err != nil {
		return "", err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLawBatch, application.LawBatchReference, b.ID, "", lc.p.ID, lc.now, due); err != nil {
		return "", err
	}
	return village.LawNoticeLaundered, nil
}

// tickLawBatch moves a batch to its next state, or freezes it if it is noticed. Every move is a conditional update of the state, so a second delivery
// finds the batch moved and changes nothing.
func (h *VillageHandler) tickLawBatch(ctx context.Context, tx application.Tx, meta lawMeta, batchID string, now time.Time) error {
	b, err := tx.Law().BatchByID(ctx, batchID, true)
	if err != nil || b == nil || (b.State != "placed" && b.State != "layered") || b.StageDue == nil || now.Before(*b.StageDue) {
		return err
	}
	front, err := tx.Law().FrontByCompany(ctx, b.CompanyID)
	if err != nil || front == nil {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, front.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	// is the batch over what the front's own turnover explains?
	window := lc.now.Add(-time.Duration(lc.t.LaunderWindowDays) * 24 * time.Hour)
	turnover, err := tx.Law().Turnover(ctx, b.CompanyID, window)
	if err != nil {
		return err
	}
	done, err := tx.Law().Laundered(ctx, b.CompanyID, h.lawDay(lc)-lc.t.LaunderWindowDays)
	if err != nil {
		return err
	}
	chance := lc.t.LaunderExposureBPS
	if done > lc.t.LaunderCap(turnover) {
		chance *= 3
	}
	patrol, err := h.lawPatrolToday(ctx, tx, lc)
	if err != nil {
		return err
	}
	chance += patrol * lc.t.BustPatrolBPS
	if b2, err := tx.Law().BribeFrom(ctx, lc.s.CityID, b.OwnerID, "", "detection", lc.now); err != nil {
		return err
	} else if b2 != nil {
		chance = lc.t.AfterBribe(chance, int64(b2.EffectBPS))
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, b.ID)
	if err != nil {
		return err
	}
	if law.Hit(chance, law.Roll(b.ID+"|"+b.State+"|expose")) {
		if ok, err := tx.Law().FreezeBatch(ctx, b.ID, now); err != nil || !ok {
			return err
		}
		tr, err := lawTreasury(ctx, tx, lc.s.CityID)
		if err != nil {
			return err
		}
		return h.lawForfeitPot(ctx, tx, lc, b.ID, pot.ID, tr.ID, b.Amount)
	}
	if b.State == "placed" {
		due := now.Add(time.Duration(lc.t.LaunderStageHours) * time.Hour)
		if ok, err := tx.Law().AdvanceBatch(ctx, b.ID, "placed", "layered", &due, now); err != nil || !ok {
			return err
		}
		return h.schedulePolity(ctx, tx, application.TickLawBatch, application.LawBatchReference, b.ID, "", b.OwnerID, now, due)
	}
	if ok, err := tx.Law().AdvanceBatch(ctx, b.ID, "layered", "integrated", nil, now); err != nil || !ok {
		return err
	}
	// integrated: the fee leaves, the rest is the front's clean money
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return err
	}
	co, err := lawAcct(ctx, tx, application.AccountCompanyTreasury, b.CompanyID)
	if err != nil {
		return err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonLaunderFee, application.LawBatchReference, b.ID, pot.ID, sink.ID, b.Fee); err != nil {
		return err
	}
	return lawPost(ctx, tx, lc, application.ReasonLaunder, application.LawBatchReference, b.ID, pot.ID, co.ID, b.Amount-b.Fee)
}

// lawForfeitPot forfeits a pot of a frozen batch: the police share to the purse, the rest to the treasury.
func (h *VillageHandler) lawForfeitPot(ctx context.Context, tx application.Tx, lc *lawCtx, ref, pot, treasury string, amount int64) error {
	share := lc.t.PoliceShare(amount)
	pu, err := lawAcct(ctx, tx, application.AccountPolicePurse, lc.s.CityID)
	if err != nil {
		return err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonForfeiturePolice, application.LawBatchReference, ref, pot, pu.ID, share); err != nil {
		return err
	}
	return lawPost(ctx, tx, lc, application.ReasonForfeiture, application.LawBatchReference, ref, pot, treasury, amount-share)
}

// lawAuditBooks is the finance office looking at a front's books: the clean money that came to it over the window from laundering, set against what
// its own sales explain. Where it is out of proportion the anomaly is a ledger trace, evidence in a case against the owner. One look at one business a day.
func (h *VillageHandler) lawAuditBooks(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.launderOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.AuditBooks); err != nil {
		return "", err
	}
	co, err := tx.Companies().ByCode(ctx, strings.TrimSpace(lc.req.Code))
	if err != nil || co == nil {
		return "", lawRefuse(village.LawNoCompany)
	}
	front, err := tx.Law().FrontByCompany(ctx, co.ID)
	if err != nil {
		return "", err
	}
	// any business in the settlement may be audited; only one registered as a front has laundered money to find
	owner := co.OwnerID
	if co.CityID != lc.s.CityID {
		return "", lawRefuse(village.LawNoCompany)
	}
	audit := application.LawAudit{ID: h.ids.NewID(), SettlementID: lc.s.CityID, Kind: "books", AuditorID: lc.p.ID, TargetID: owner, Day: h.lawDay(lc), Outcome: "cleared",
		Fee: lc.t.AuditCost, At: lc.now}
	if fresh, err := tx.Law().InsertAudit(ctx, audit); err != nil {
		return "", err
	} else if !fresh {
		return "", lawRefuse(village.LawAuditedToday)
	}
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := lawFromTreasury(ctx, tx, lc, application.ReasonAuditFee, application.LawAuditReference, audit.ID, sink.ID, lc.t.AuditCost); err != nil {
		return "", err
	}
	if front == nil {
		return village.LawNoticeAuditClean, nil
	}
	window := lc.now.Add(-time.Duration(lc.t.LaunderWindowDays) * 24 * time.Hour)
	turnover, err := tx.Law().Turnover(ctx, co.ID, window)
	if err != nil {
		return "", err
	}
	laundered, err := tx.Law().Laundered(ctx, co.ID, h.lawDay(lc)-lc.t.LaunderWindowDays)
	if err != nil {
		return "", err
	}
	strength := lc.t.AnomalyStrength(turnover+laundered, turnover)
	if strength <= 0 {
		return village.LawNoticeAuditClean, nil
	}
	if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, "laundering", lc.now); err != nil {
		return "", err
	} else if o == nil {
		return village.LawNoticeAuditClean, nil
	}
	c, err := h.lawOpenOrFind(ctx, tx, lc, "laundering", owner, "audit", lc.p.ID, 0)
	if err != nil {
		return "", err
	}
	if ok, err := tx.Law().AddEvidence(ctx, application.LawEvidence{ID: h.ids.NewID(), CaseID: c.ID, Kind: law.EvLedger, Strength: strength, Proper: true,
		Reliability: -1, ObtainedBy: lc.p.ID, Ref: "ledger:" + co.ID + ":" + itoa(h.lawDay(lc)), At: lc.now}); err != nil {
		return "", err
	} else if !ok {
		return village.LawNoticeAuditClean, nil
	}
	return village.LawNoticeAuditAnomaly, nil
}

// ---------------------------------------------------------------------------
// customs (docs/adr/0078 3.8)
// ---------------------------------------------------------------------------

// parseManifest reads "item:qty,item:qty".
func parseManifest(s string) (map[string]int64, bool) {
	out := map[string]int64{}
	for _, tok := range strings.Split(strings.TrimSpace(s), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		f := strings.Split(tok, ":")
		if len(f) != 2 {
			return nil, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64)
		if err != nil || n < 1 || strings.TrimSpace(f[0]) == "" {
			return nil, false
		}
		out[strings.TrimSpace(f[0])] += n
	}
	return out, len(out) > 0
}

// lawDeclare declares a load for a settlement: what the player says he brings and what he really carries. The carried goods must be in his bags.
func (h *VillageHandler) lawDeclare(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.customsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	to, err := h.settlementByCode(ctx, tx, strings.TrimSpace(lc.req.Code))
	if err != nil {
		return "", err
	}
	if to == nil || to.CityID == lc.s.CityID {
		return "", lawRefuse(village.LawNoTarget)
	}
	carried, ok := parseManifest(lc.req.Carry)
	if !ok {
		return "", lawRefuse(village.LawBadArgs)
	}
	declared, _ := parseManifest(lc.req.Items)
	if declared == nil {
		declared = map[string]int64{}
	}
	for it, q := range carried {
		if _, ok := lc.snap.ItemDef(it); !ok {
			return "", lawRefuse(village.LawNoGoods)
		}
		if held, err := lawHeld(ctx, tx, lc.p.ID, application.HoldCarried, it); err != nil {
			return "", err
		} else if held < q {
			return "", lawRefuse(village.LawNoGoods)
		}
	}
	for it, q := range declared {
		if q > carried[it] {
			return "", lawRefuse(village.LawBadArgs) // one cannot declare what one does not carry
		}
	}
	l := application.LawLoad{ID: h.ids.NewID(), OwnerID: lc.p.ID, FromID: lc.s.CityID, ToID: to.CityID, Declared: declared, Carried: carried, At: lc.now}
	return village.LawNoticeDeclared, tx.Law().InsertLoad(ctx, l)
}

// lawCross is the load crossing into the destination. At a customs house that is open the duty on the declared value is due and an inspection is rolled:
// the chance grows with the inspectors and the size of the load. An inspection that finds goods not declared seizes them into the destination's stock, charges
// the penalty, and opens a case of smuggling with the inspection as evidence. A load that is not inspected crosses, and what was not declared is a recorded act
// that the police may find later. A destination with no open customs house collects nothing and looks at nothing.
func (h *VillageHandler) lawCross(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.customsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	l, err := tx.Law().LoadByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if l == nil || l.OwnerID != lc.p.ID || l.Status != "declared" {
		return "", lawRefuse(village.LawNoLoad)
	}
	dest, err := tx.Settlements().ByID(ctx, l.ToID)
	if err != nil {
		return "", err
	}
	dc := *lc
	dc.s, dc.has = dest, true
	posts, err := h.lawOpenPosts(ctx, tx, &dc, "customs")
	if err != nil {
		return "", err
	}
	ld := law.Load{Declared: l.Declared, Carried: l.Carried, Price: map[string]int64{}}
	for it := range l.Carried {
		if d, ok := lc.snap.ItemDef(it); ok {
			ld.Price[it] = d.BasePrice
		}
	}
	if len(posts) == 0 {
		_, err := tx.Law().CloseLoad(ctx, l.ID, "crossed", false, 0, 0, "", lc.now)
		return village.LawNoticeCrossed, err
	}
	var inspectors int64
	for _, ps := range posts {
		for _, st := range ps.Def.Staff {
			if st.Role == "customs_inspector" {
				inspectors += int64(st.Slots)
			}
		}
	}
	trd, err := lawTreasury(ctx, tx, dest.CityID)
	if err != nil {
		return "", err
	}
	duty := lc.t.Duty(ld.Value(l.Declared))
	if err := lawPay(ctx, tx, lc, lc.p.ID, duty, application.ReasonCustomsDuty, application.LawLoadReference, l.ID, trd.ID); err != nil {
		return "", err
	}
	chance := lc.t.InspectChance(inspectors, ld.Units())
	// a bribe of the carrier on the detection rung to a holder of the customs power lowers the chance, never to nothing
	if b, err := tx.Law().BribeFrom(ctx, dest.CityID, lc.p.ID, "", "detection", lc.now); err != nil {
		return "", err
	} else if b != nil {
		chance = lc.t.AfterBribe(chance, int64(b.EffectBPS))
	}
	inspected := law.Hit(chance, law.Roll(l.ID+"|inspect"))
	un := ld.Undeclared()
	if !inspected || len(un) == 0 {
		_, err := tx.Law().CloseLoad(ctx, l.ID, "crossed", inspected, duty, 0, "", lc.now)
		if err == nil && inspected {
			return village.LawNoticeInspectedClean, nil
		}
		return village.LawNoticeCrossed, err
	}
	// found: seize what was not declared, charge the penalty, open the case
	org := application.SettlementOrg(dest.CityID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return "", err
	}
	items := make([]string, 0, len(un))
	for it := range un {
		items = append(items, it)
	}
	sort.Strings(items)
	for _, it := range items {
		stack, pieces, err := lawGoods(ctx, tx, lc.p.ID, application.HoldCarried, it)
		if err != nil {
			return "", err
		}
		if err := tx.Items().LockOwner(ctx, lc.p.ID); err != nil {
			return "", err
		}
		if stack >= un[it] {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: un[it], From: lc.p.ID, FromHolding: application.HoldCarried, ToOrg: org,
				ToHolding: application.HoldWarehouse, Reason: application.ItemConfiscated, ReferenceType: application.LawLoadReference, ReferenceID: l.ID, At: lc.now}); err != nil {
				return "", err
			}
			continue
		}
		for i, p := range pieces {
			if int64(i) >= un[it] {
				break
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, PieceID: p.ID, Qty: 1, From: lc.p.ID, FromHolding: application.HoldCarried,
				ToOrg: org, ToHolding: application.HoldWarehouse, Reason: application.ItemConfiscated, ReferenceType: application.LawLoadReference, ReferenceID: l.ID, At: lc.now}); err != nil {
				return "", err
			}
		}
	}
	penalty := lc.t.Penalty(ld.Value(un))
	pu, err := lawAcct(ctx, tx, application.AccountPolicePurse, dest.CityID)
	if err != nil {
		return "", err
	}
	share := lc.t.PoliceShare(penalty)
	paid, err := lawTake(ctx, tx, lc, lc.p.ID, share, application.ReasonForfeiturePolice, application.LawLoadReference, l.ID, pu.ID)
	if err != nil {
		return "", err
	}
	more, err := lawTake(ctx, tx, lc, lc.p.ID, penalty-share, application.ReasonCustomsPenalty, application.LawLoadReference, l.ID, trd.ID)
	if err != nil {
		return "", err
	}
	caseID := ""
	if o, err := tx.Law().OffenceIn(ctx, dest.CityID, "smuggling", lc.now); err != nil {
		return "", err
	} else if o != nil {
		dc.has = true
		c, err := h.lawOpenOrFind(ctx, tx, &dc, "smuggling", lc.p.ID, "customs", "", 0)
		if err != nil {
			return "", err
		}
		if _, err := h.lawEvidence(ctx, tx, &dc, c.ID, law.EvInspection, "load:"+l.ID, true, -1, "", ""); err != nil {
			return "", err
		}
		caseID = c.ID
	}
	_, err = tx.Law().CloseLoad(ctx, l.ID, "seized", true, duty, paid+more, caseID, lc.now)
	return village.LawNoticeSeized, err
}

// ---------------------------------------------------------------------------
// gangs (docs/adr/0078 3.12)
// ---------------------------------------------------------------------------

// lawGangAfter counts a conviction against the gang of the convicted, if he belongs to one: the gang's heat rises, it may be revealed (a seeded roll), and when
// the linked convictions in the fact window reach the racketeering count a case of racketeering opens against its leader.
func (h *VillageHandler) lawGangAfter(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) error {
	m, err := tx.Factions().Membership(ctx, c.AccusedID)
	if err != nil {
		if stderrors.Is(err, application.ErrNotInFaction) {
			return nil
		}
		return err
	}
	g, err := tx.Law().Gang(ctx, m.FactionID)
	if err != nil || g == nil {
		return err
	}
	g.Heat += lc.t.GangHeatPerConviction
	if g.Hidden && law.Hit(lc.t.GangDiscoverBPS, law.Roll(c.ID+"|gang|discover")) {
		g.Hidden = false
		now := lc.now
		g.RevealedAt = &now
	}
	if err := tx.Law().SetGang(ctx, *g); err != nil {
		return err
	}
	members, err := tx.Factions().Members(ctx, m.FactionID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(members))
	for _, mm := range members {
		ids = append(ids, mm.PlayerID)
	}
	n, err := tx.Law().Convictions(ctx, lc.s.CityID, ids, lc.now.Add(-time.Duration(lc.t.FactWindowDays)*24*time.Hour))
	if err != nil || !lc.t.Racketeering(n) {
		return err
	}
	f, err := tx.Factions().ByID(ctx, m.FactionID)
	if err != nil {
		return err
	}
	if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, "racketeering", lc.now); err != nil || o == nil {
		return err
	}
	rc, err := h.lawOpenOrFind(ctx, tx, lc, "racketeering", f.LeaderID, "crew", "", 0)
	if err != nil {
		return err
	}
	_, err = h.lawEvidence(ctx, tx, lc, rc.ID, law.EvRecord, "gang:"+m.FactionID+":"+itoa(n), true, -1, "", "")
	return err
}

// lawGangDeclare is the leader of a faction declaring it a gang, openly: it stops being hidden. A faction becomes a gang by its leader's word; no cap on
// houses or lot share limits it, only the police, the rivals and the world.
func (h *VillageHandler) lawGangMark(ctx context.Context, tx application.Tx, lc *lawCtx, reveal bool) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	m, err := tx.Factions().Membership(ctx, lc.p.ID)
	if err != nil {
		return "", lawRefuse(village.LawNoFaction)
	}
	f, err := tx.Factions().ByID(ctx, m.FactionID)
	if err != nil || f.LeaderID != lc.p.ID {
		return "", lawRefuse(village.LawNotLeader)
	}
	g, err := tx.Law().Gang(ctx, f.ID)
	if err != nil {
		return "", err
	}
	if g == nil {
		g = &application.LawGang{FactionID: f.ID, Hidden: true}
	}
	if reveal {
		now := lc.now
		g.Hidden, g.RevealedAt = false, &now
	}
	if err := tx.Law().SetGang(ctx, *g); err != nil {
		return "", err
	}
	if reveal {
		return village.LawNoticeGangDeclared, nil
	}
	return village.LawNoticeGangMarked, nil
}
