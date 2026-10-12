package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// ---------------------------------------------------------------------------
// the legal model and the offences (law.set)
// ---------------------------------------------------------------------------

// lawSetModel chooses the legal model of the settlement: the adversarial one, the default, keeps the prosecutor and the judge apart; the
// inquisitorial one lets a magistrate hold both, with an appeal to another office.
func (h *VillageHandler) lawSetModel(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if _, err := lawPerm(ctx, tx, lc, charter.LawSet); err != nil {
		return "", err
	}
	model := strings.TrimSpace(lc.req.Kind)
	if model != "adversarial" && model != "inquisitorial" {
		return "", lawRefuse(village.LawBadArgs)
	}
	if err := tx.Law().SetModel(ctx, lc.s.CityID, model, lc.p.ID, lc.now); err != nil {
		return "", err
	}
	if model == "adversarial" {
		// the model that keeps the roles apart refuses an office that holds both
		st, err := loadCharterState(ctx, tx, lc.s, "head")
		if err != nil {
			return "", err
		}
		for _, o := range st.offices {
			if !o.Closed {
				if err := charter.ValidateSeparation(o, false); err != nil {
					return "", lawRefuse(village.LawSeparation)
				}
			}
		}
	}
	return village.LawNoticeModel, nil
}

// lawSetOffence lists an offence the settlement outlaws, with bounds no higher than the code's; it applies after the notice period.
func (h *VillageHandler) lawSetOffence(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if _, err := lawPerm(ctx, tx, lc, charter.LawSet); err != nil {
		return "", err
	}
	def, ok := lc.snap.Offence(strings.TrimSpace(lc.req.Code))
	if !ok || lawMatureHidden(lc.mature, def.Mature) {
		return "", lawRefuse(village.LawNoOffence)
	}
	if missing, err := knowledgeMissing(ctx, tx, lc.s.CityID, needsKnowledge(def.Requires)); err != nil {
		return "", err
	} else if len(missing) > 0 {
		return "", researchRefusal(village.LawResearch, village.AddrLaw, missing, lc.snap)
	}
	fine, jail, ban := def.MaxFine, def.MaxJailHours, def.MaxBanDays
	if v := lc.req.num(lc.req.Amount); v > 0 {
		fine = min(v, def.MaxFine)
	}
	if v := lc.req.num(lc.req.Hours); v > 0 {
		jail = min(v, def.MaxJailHours)
	}
	if v := lc.req.num(lc.req.Days); v > 0 {
		ban = min(v, def.MaxBanDays)
	}
	if err := tx.Law().UpsertOffence(ctx, application.LawOffence{SettlementID: lc.s.CityID, Offence: def.Code, ListedBy: lc.p.ID,
		MaxFine: fine, MaxJailHours: jail, MaxBanDays: ban, ListedAt: lc.now, EffectiveAt: lc.now.Add(h.law.Grace)}); err != nil {
		return "", err
	}
	return village.LawNoticeOffence, nil
}

func (h *VillageHandler) lawUnsetOffence(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if _, err := lawPerm(ctx, tx, lc, charter.LawSet); err != nil {
		return "", err
	}
	if ok, err := tx.Law().WithdrawOffence(ctx, lc.s.CityID, strings.TrimSpace(lc.req.Code), lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoOffence)
	}
	return village.LawNoticeOffenceGone, nil
}

// ---------------------------------------------------------------------------
// patrol
// ---------------------------------------------------------------------------

// lawPatrol is one patrol shift of the beat: it costs points of the day, lifts the chance that an act of the beat is noticed, and walks over the
// acts recorded since the fact window began, noticing each by a seeded roll. A noticed act opens a case (source patrol) with a witness, and a trade
// on the hidden board is a noticed act too. Nothing is noticed that did not happen.
func (h *VillageHandler) lawPatrol(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.policeOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PolicePatrol); err != nil {
		return "", err
	}
	if posts, err := h.lawOpenPosts(ctx, tx, lc, "constabulary"); err != nil {
		return "", err
	} else if len(posts) == 0 {
		return "", lawRefuse(village.LawPostClosed)
	}
	if err := h.lawSpend(ctx, tx, lc, lc.t.PatrolCost, true); err != nil {
		return "", err
	}
	patrol, err := h.lawPatrolToday(ctx, tx, lc)
	if err != nil {
		return "", err
	}
	since := lc.now.Add(-time.Duration(lc.t.FactWindowDays) * 24 * time.Hour)
	acts, err := tx.Law().Acts(ctx, lc.s.CityID, "", since)
	if err != nil {
		return "", err
	}
	seen := 0
	for _, a := range acts {
		offence := lawActOffence(lc.snap, a)
		if offence == "" {
			continue
		}
		if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, offence, lc.now); err != nil {
			return "", err
		} else if o == nil {
			continue // the act is legal here
		}
		chance := lc.t.DetectChance(patrol, 0)
		// a bribe on the detection rung lowers the chance for the briber, never to nothing
		if b, err := tx.Law().BribeFrom(ctx, lc.s.CityID, a.Player, "", "detection", lc.now); err != nil {
			return "", err
		} else if b != nil {
			chance = lc.t.AfterBribe(chance, int64(b.EffectBPS))
		}
		if !law.Hit(chance, law.Roll(lc.s.CityID+"|patrol|"+a.Kind+"|"+a.Ref+"|"+a.Player)) {
			continue
		}
		c, err := h.lawOpenOrFind(ctx, tx, lc, offence, a.Player, "patrol", lc.p.ID, 0)
		if err != nil {
			return "", err
		}
		if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvWitness, a.Kind+":"+a.Ref+":patrol", true, -1, "", lc.p.ID); err != nil {
			return "", err
		}
		seen++
	}
	if seen == 0 {
		return village.LawNoticePatrolQuiet, nil
	}
	return village.LawNoticePatrolSaw, nil
}

// ---------------------------------------------------------------------------
// cases and evidence
// ---------------------------------------------------------------------------

// lawOpenOrFind opens a case against a player for a listed offence, or returns the one still running.
func (h *VillageHandler) lawOpenOrFind(ctx context.Context, tx application.Tx, lc *lawCtx, offence, accused, source, openedBy string, fee int64) (*application.LawCase, error) {
	if c, err := tx.Law().OpenCaseFor(ctx, lc.s.CityID, accused, offence); err != nil || c != nil {
		return c, err
	}
	model, err := tx.Law().Model(ctx, lc.s.CityID)
	if err != nil {
		return nil, err
	}
	c := application.LawCase{ID: h.ids.NewID(), SettlementID: lc.s.CityID, Offence: offence, AccusedID: accused, OpenedBy: openedBy, Source: source, State: "open",
		Model: model, ComplaintFee: fee, CreatedAt: lc.now, UpdatedAt: lc.now}
	if err := tx.Law().InsertCase(ctx, c); err != nil {
		return nil, err
	}
	return &c, nil
}

// lawEvidence stores an item of evidence of a kind, with the strength the tuning gives it. ref names the act the item is about: one item per act
// in a case. A tip's strength is scaled by the informant's reliability.
func (h *VillageHandler) lawEvidence(ctx context.Context, tx application.Tx, lc *lawCtx, caseID, kind, ref string, proper bool, reliability int, informantID, by string) (bool, error) {
	strength := lc.t.KindStrength(kind, int64(reliability))
	if strength <= 0 {
		return false, nil
	}
	return tx.Law().AddEvidence(ctx, application.LawEvidence{ID: h.ids.NewID(), CaseID: caseID, Kind: kind, Strength: strength, Proper: proper,
		Reliability: reliability, InformantID: informantID, ObtainedBy: by, Ref: ref, At: lc.now})
}

// lawEvidenceItems turns the stored items into the pure rules' view.
func lawEvidenceItems(items []application.LawEvidence) []law.Evidence {
	out := make([]law.Evidence, 0, len(items))
	for _, e := range items {
		out = append(out, law.Evidence{Kind: e.Kind, Strength: e.Strength, Proper: e.Proper, Excluded: e.Excluded, Reliability: int64(e.Reliability)})
	}
	return out
}

// lawComplain files a complaint: any resident may name a person and an offence the settlement outlaws; the fee is kept by the treasury and returned
// if the case ends in a conviction. A case opens with no evidence at all: the police must find it among the acts that were recorded, so a complaint
// tells its author nothing about whether the other did anything.
func (h *VillageHandler) lawComplain(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.policeOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	offence := strings.TrimSpace(lc.req.Code)
	def, ok := lc.snap.Offence(offence)
	if !ok || lawMatureHidden(lc.mature, def.Mature) {
		return "", lawRefuse(village.LawNoOffence)
	}
	if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, offence, lc.now); err != nil {
		return "", err
	} else if o == nil {
		return "", lawRefuse(village.LawNotOutlawed)
	}
	accused, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	if accused == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if c, err := tx.Law().OpenCaseFor(ctx, lc.s.CityID, accused, offence); err != nil {
		return "", err
	} else if c != nil {
		return "", lawRefuse(village.LawCaseOpen)
	}
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return "", err
	}
	caseID := h.ids.NewID()
	if err := lawPay(ctx, tx, lc, lc.p.ID, lc.t.ComplaintFee, application.ReasonCourtFee, application.LawCaseReference, caseID, tr.ID); err != nil {
		return "", err
	}
	model, err := tx.Law().Model(ctx, lc.s.CityID)
	if err != nil {
		return "", err
	}
	if err := tx.Law().InsertCase(ctx, application.LawCase{ID: caseID, SettlementID: lc.s.CityID, Offence: offence, AccusedID: accused, OpenedBy: lc.p.ID,
		Source: "complaint", State: "open", Model: model, ComplaintFee: lc.t.ComplaintFee, CreatedAt: lc.now, UpdatedAt: lc.now}); err != nil {
		return "", err
	}
	return village.LawNoticeComplaint, nil
}

// lawInvestigate is one step of work on an open case: it costs points and adds what the recorded acts of the accused give for the case's offence
// (a witness by a seeded roll, and the written record of an act that has one). An act is in the file once.
func (h *VillageHandler) lawInvestigate(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.policeOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceInvestigate); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "open" && c.State != "charged" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if posts, err := h.lawOpenPosts(ctx, tx, lc, "constabulary"); err != nil {
		return "", err
	} else if len(posts) == 0 {
		return "", lawRefuse(village.LawPostClosed)
	}
	if err := h.lawSpend(ctx, tx, lc, lc.t.InvestigateCost, false); err != nil {
		return "", err
	}
	since := lc.now.Add(-time.Duration(lc.t.FactWindowDays) * 24 * time.Hour)
	acts, err := tx.Law().Acts(ctx, lc.s.CityID, c.AccusedID, since)
	if err != nil {
		return "", err
	}
	added := 0
	for _, a := range acts {
		if lawActOffence(lc.snap, a) != c.Offence {
			continue
		}
		ref := a.Kind + ":" + a.Ref
		if law.Hit(lc.t.WitnessBPS, law.Roll(c.ID+"|"+ref+"|witness")) {
			if ok, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvWitness, "w:"+ref, true, -1, "", lc.p.ID); err != nil {
				return "", err
			} else if ok {
				added++
			}
		}
		// the written record of an act that leaves one (a bribe's offer, a trade's escrow, a scam's pot, a declared load)
		switch a.Kind {
		case "trade", "scam", "bribe", "load", "batch":
			if ok, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvRecord, "r:"+ref, true, -1, "", lc.p.ID); err != nil {
				return "", err
			} else if ok {
				added++
			}
		}
	}
	if added == 0 {
		return village.LawNoticeNothingNew, nil
	}
	return village.LawNoticeInvestigated, nil
}

// lawCaseFor reads a case of this settlement by id (locked when it will change).
func (h *VillageHandler) lawCaseFor(ctx context.Context, tx application.Tx, lc *lawCtx, id string, lock bool) (*application.LawCase, error) {
	c, err := tx.Law().CaseByID(ctx, strings.TrimSpace(id), lock)
	if err != nil {
		return nil, err
	}
	if c == nil || !lc.has || c.SettlementID != lc.s.CityID {
		return nil, lawRefuse(village.LawNoCase)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// warrants and arrest
// ---------------------------------------------------------------------------

// lawWarrantAsk asks a judge for a warrant on a case.
func (h *VillageHandler) lawWarrantAsk(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.policeOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceWarrantAsk); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "open" && c.State != "charged" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if err := tx.Law().InsertWarrant(ctx, application.LawWarrant{ID: h.ids.NewID(), CaseID: c.ID, AskedBy: lc.p.ID, At: lc.now}); err != nil {
		return "", err
	}
	return village.LawNoticeWarrantAsked, nil
}

// lawWarrantSign is a judge signing, or refusing, a warrant. The warrant needs the proper evidence to reach the threshold and never rests on
// informants' tips alone. A bribe the accused made to this judge on the warrant rung lowers the strength the judge counts, never to nothing.
func (h *VillageHandler) lawWarrantSign(ctx context.Context, tx application.Tx, lc *lawCtx, sign bool) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.CourtWarrant); err != nil {
		return "", err
	}
	w, err := tx.Law().WarrantByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if w == nil || w.Status != "asked" {
		return "", lawRefuse(village.LawNoWarrant)
	}
	c, err := h.lawCaseFor(ctx, tx, lc, w.CaseID, true)
	if err != nil {
		return "", err
	}
	if !sign {
		if _, err := tx.Law().RefuseWarrant(ctx, w.ID); err != nil {
			return "", err
		}
		return village.LawNoticeWarrantRefused, nil
	}
	if law.Conflict(c.ChargedBy, lc.p.ID) {
		return "", lawRefuse(village.LawConflict)
	}
	items, err := tx.Law().Evidence(ctx, c.ID)
	if err != nil {
		return "", err
	}
	t := lc.t
	// a bribe on the warrant rung makes the judge count less of the file: the threshold stands, the strength seen falls
	if b, err := tx.Law().BribeFrom(ctx, lc.s.CityID, c.AccusedID, lc.p.ID, "warrant", lc.now); err != nil {
		return "", err
	} else if b != nil {
		t.WarrantThreshold = max(t.WarrantThreshold*law.BPS/max(law.BPS-t.BribeEffect(int64(b.EffectBPS)), 1), 1)
	}
	if err := t.WarrantAllowed(lawEvidenceItems(items)); err != nil {
		if err == law.ErrTipsAlone {
			return "", lawRefuse(village.LawTipsAlone)
		}
		return "", lawRefuse(village.LawWeakEvidence)
	}
	if ok, err := tx.Law().Sign(ctx, w.ID, lc.p.ID, lc.now.Add(time.Duration(lc.t.WarrantDays)*24*time.Hour), lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoWarrant)
	}
	return village.LawNoticeWarrantSigned, nil
}

// lawArrest holds the accused in a post's lock-up until the custody clock runs out. A flagrant case (one a patrol or the customs opened) may be
// arrested on sight; any other needs a signed warrant, and an arrest without one is made at the cost of the procedure: it adds a custody record that
// is improper evidence, and the case's quality falls.
func (h *VillageHandler) lawArrest(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.policeOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceArrest); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "open" && c.State != "charged" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if c.CustodyUntil != nil {
		return "", lawRefuse(village.LawInCustody)
	}
	posts, err := h.lawOpenPosts(ctx, tx, lc, "constabulary")
	if err != nil {
		return "", err
	}
	if len(posts) == 0 {
		return "", lawRefuse(village.LawPostClosed)
	}
	if err := h.lawSpend(ctx, tx, lc, lc.t.ArrestCost, false); err != nil {
		return "", err
	}
	flagrant := c.Source == "patrol" || c.Source == "customs"
	w, err := tx.Law().LiveWarrant(ctx, c.ID, lc.now)
	if err != nil {
		return "", err
	}
	if w != nil {
		if _, err := tx.Law().UseWarrant(ctx, w.ID); err != nil {
			return "", err
		}
	} else if !flagrant {
		// an arrest without a warrant: the custody record is improper evidence
		if _, err := tx.Law().AddEvidence(ctx, application.LawEvidence{ID: h.ids.NewID(), CaseID: c.ID, Kind: law.EvRecord, Strength: 1, Proper: false,
			Reliability: -1, ObtainedBy: lc.p.ID, Ref: "arrest", At: lc.now}); err != nil {
			return "", err
		}
	}
	until := lc.now.Add(time.Duration(lc.t.CustodyHours) * time.Hour)
	if ok, err := tx.Law().Arrest(ctx, c.ID, posts[0].Building.ID, until, lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawInCustody)
	}
	return village.LawNoticeArrested, h.schedulePolity(ctx, tx, application.TickLawCustody, application.LawCaseReference, c.ID, "", "", lc.now, until)
}

// lawCustodyLapse releases the accused of a case that was not charged before the custody clock ran out, and lets the case lapse.
func (h *VillageHandler) lawCustodyLapse(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) error {
	if c.CustodyUntil == nil || lc.now.Before(*c.CustodyUntil) || c.State != "open" {
		return nil
	}
	if _, err := tx.Law().ReleaseCustody(ctx, c.ID, lc.now); err != nil {
		return err
	}
	if fresh, err := tx.Law().Stage(ctx, c.ID, "lapsed", "", lc.now); err != nil || !fresh {
		return err
	}
	_, err := tx.Law().Move(ctx, c.ID, []string{"open"}, "lapsed", lc.now)
	return err
}

// lawEspionageCase is the charge against an agent a watch exposed (docs/adr/0077 6.6, docs/adr/0078 3.11). It exists only where the settlement lists
// the offence of espionage and a courthouse stands, and only once the courts have begun: nothing exists by default, and until then the effect is the
// expulsion alone. The case is against the agent, never against the sponsoring settlement; the trace is the evidence, and it names no one but him.
func (h *VillageHandler) lawEspionageCase(ctx context.Context, tx application.Tx, target application.FoundedSettlement, agentPlayerID, expulsionID string, now time.Time) error {
	if !h.courtsOpen() {
		return nil
	}
	lc := &lawCtx{snap: h.content.Current(), s: target, has: true, now: now, t: h.law.T}
	if o, err := tx.Law().OffenceIn(ctx, target.CityID, "espionage", now); err != nil || o == nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, target.CityID)
	if err != nil {
		return err
	}
	stands := false
	for _, b := range buildings {
		stands = stands || (b.TypeCode == "courthouse" && b.Status == "complete")
	}
	if !stands {
		return nil
	}
	c, err := h.lawOpenOrFind(ctx, tx, lc, "espionage", agentPlayerID, "trace", "", 0)
	if err != nil {
		return err
	}
	_, err = h.lawEvidence(ctx, tx, lc, c.ID, law.EvIntelTrace, "x:"+expulsionID, true, -1, "", "")
	return err
}
