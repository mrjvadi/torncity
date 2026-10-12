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
// informants
// ---------------------------------------------------------------------------

// lawContact writes one entry of the record of contacts, which only the integrity auditor reads and nobody changes.
func (h *VillageHandler) lawContact(ctx context.Context, tx application.Tx, lc *lawCtx, informant, handler, kind string, pay int64, caseID string) error {
	return tx.Law().AddContact(ctx, application.LawContact{ID: h.ids.NewID(), InformantID: informant, HandlerID: handler, Kind: kind, Pay: pay, CaseID: caseID, At: lc.now})
}

// lawRecruit enrols a resident as an informant of the settlement's police. A resident of another settlement is never recruited here (a foreign
// bureau's business is the agent of the intelligence item, not this), and a handler runs only so many at once.
func (h *VillageHandler) lawRecruit(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceInformantRun); err != nil {
		return "", err
	}
	src, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	if src == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if home, err := tx.Employment().ResidenceCityID(ctx, src); err != nil {
		return "", err
	} else if home != lc.s.CityID {
		return "", lawRefuse(village.LawNotResident)
	}
	if n, err := tx.Law().LiveInformants(ctx, lc.p.ID); err != nil {
		return "", err
	} else if n >= lc.t.MaxInformants {
		return "", lawRefuse(village.LawTooManyInformants)
	}
	id := h.ids.NewID()
	if ok, err := tx.Law().InsertInformant(ctx, application.LawInformant{ID: id, SettlementID: lc.s.CityID, HandlerID: lc.p.ID, SourceID: src,
		Reliability: int(lc.t.ReliabilityStart), Pay: lc.t.InformantPay, RecruitedAt: lc.now}); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawAlreadyInformant)
	}
	return village.LawNoticeRecruited, h.lawContact(ctx, tx, lc, id, lc.p.ID, "recruit", 0, "")
}

// lawInformantFor reads a live informant of this settlement.
func (h *VillageHandler) lawInformantFor(ctx context.Context, tx application.Tx, lc *lawCtx, id string) (*application.LawInformant, error) {
	i, err := tx.Law().InformantByID(ctx, strings.TrimSpace(id), true)
	if err != nil {
		return nil, err
	}
	if i == nil || !lc.has || i.SettlementID != lc.s.CityID || (i.State != "active" && i.State != "recruited") {
		return nil, lawRefuse(village.LawNoInformant)
	}
	return i, nil
}

// lawTip is the handler taking a report from an informant about a person. A report is paid for from the police purse. If the person did something
// the settlement outlaws that the game recorded, the tip is corroborated: it goes into the file of a case against him with a strength scaled by the
// source's reliability, and the source's reliability rises. If there is nothing behind it, the tip is contradicted and the reliability falls. A handler
// may press the informant for more than he knows (kind press): the item then goes into the file as improper evidence and leaves a record that an
// integrity audit finds.
func (h *VillageHandler) lawTip(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceInformantRun); err != nil {
		return "", err
	}
	inf, err := h.lawInformantFor(ctx, tx, lc, lc.req.ID)
	if err != nil {
		return "", err
	}
	if inf.HandlerID != lc.p.ID {
		return "", lawRefuse(village.LawNotYourInformant)
	}
	if lc.t.NeedsRotation(int64(lc.now.Sub(inf.HandlerSince) / time.Hour)) {
		return "", lawRefuse(village.LawRotateDue)
	}
	target, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	if target == lc.p.ID || target == inf.SourceID {
		return "", lawRefuse(village.LawSelf)
	}
	pay := inf.Pay
	srcCash, err := lawAcct(ctx, tx, application.AccountPlayerCash, inf.SourceID)
	if err != nil {
		return "", err
	}
	if err := lawFromPurse(ctx, tx, lc, application.ReasonInformantPay, application.LawInformantRef, inf.ID, srcCash.ID, pay); err != nil {
		return "", err
	}
	since := lc.now.Add(-time.Duration(lc.t.FactWindowDays) * 24 * time.Hour)
	acts, err := tx.Law().Acts(ctx, lc.s.CityID, target, since)
	if err != nil {
		return "", err
	}
	press := strings.TrimSpace(lc.req.Kind) == "press"
	var matched *application.LawAct
	var offence string
	for i := range acts {
		off := lawActOffence(lc.snap, acts[i])
		if off == "" || off == "false_evidence" {
			continue
		}
		if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, off, lc.now); err != nil {
			return "", err
		} else if o == nil {
			continue
		}
		matched, offence = &acts[i], off
		break
	}
	switch {
	case matched != nil:
		c, err := h.lawOpenOrFind(ctx, tx, lc, offence, target, "tip", lc.p.ID, 0)
		if err != nil {
			return "", err
		}
		if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvInformant, "t:"+inf.ID+":"+matched.Kind+":"+matched.Ref, true, int(inf.Reliability), inf.ID, lc.p.ID); err != nil {
			return "", err
		}
		if err := tx.Law().SetReliability(ctx, inf.ID, int(lc.t.NextReliability(int64(inf.Reliability), true))); err != nil {
			return "", err
		}
		return village.LawNoticeTip, h.lawContact(ctx, tx, lc, inf.ID, lc.p.ID, "tip", pay, c.ID)
	case press:
		// there is nothing behind the tip and the handler files it anyway: improper evidence, and a record
		offences, err := tx.Law().Offences(ctx, lc.s.CityID)
		if err != nil || len(offences) == 0 {
			return "", lawRefuse(village.LawNoOffence)
		}
		c, err := h.lawOpenOrFind(ctx, tx, lc, offences[0].Offence, target, "tip", lc.p.ID, 0)
		if err != nil {
			return "", err
		}
		strength := lc.t.KindStrength(law.EvInformant, 100)
		if _, err := tx.Law().AddEvidence(ctx, application.LawEvidence{ID: h.ids.NewID(), CaseID: c.ID, Kind: law.EvInformant, Strength: strength, Proper: false,
			Reliability: 100, InformantID: inf.ID, ObtainedBy: lc.p.ID, Ref: "press:" + inf.ID + ":" + lc.now.Format(time.RFC3339Nano), At: lc.now}); err != nil {
			return "", err
		}
		return village.LawNoticeTip, h.lawContact(ctx, tx, lc, inf.ID, lc.p.ID, "press", pay, c.ID)
	default:
		if err := tx.Law().SetReliability(ctx, inf.ID, int(lc.t.NextReliability(int64(inf.Reliability), false))); err != nil {
			return "", err
		}
		return village.LawNoticeTipEmpty, h.lawContact(ctx, tx, lc, inf.ID, lc.p.ID, "tip", pay, "")
	}
}

// lawTake over is another holder of the informant-running power taking an informant from his handler: the rotation the research asks of any police
// that runs sources.
func (h *VillageHandler) lawInformantTake(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceInformantRun); err != nil {
		return "", err
	}
	inf, err := h.lawInformantFor(ctx, tx, lc, lc.req.ID)
	if err != nil {
		return "", err
	}
	if inf.HandlerID == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if n, err := tx.Law().LiveInformants(ctx, lc.p.ID); err != nil {
		return "", err
	} else if n >= lc.t.MaxInformants {
		return "", lawRefuse(village.LawTooManyInformants)
	}
	// the handler has to have kept the informant a while before he passes, or the informant is taken over only when the rotation is due
	if !lc.t.NeedsRotation(int64(lc.now.Sub(inf.HandlerSince) / time.Hour)) {
		return "", lawRefuse(village.LawRotateNotDue)
	}
	if ok, err := tx.Law().PassInformant(ctx, inf.ID, lc.p.ID, lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoInformant)
	}
	return village.LawNoticeRotated, h.lawContact(ctx, tx, lc, inf.ID, lc.p.ID, "rotate", 0, "")
}

// lawRetire lets an informant go.
func (h *VillageHandler) lawRetire(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.PoliceInformantRun); err != nil {
		return "", err
	}
	inf, err := h.lawInformantFor(ctx, tx, lc, lc.req.ID)
	if err != nil {
		return "", err
	}
	if inf.HandlerID != lc.p.ID {
		return "", lawRefuse(village.LawNotYourInformant)
	}
	if _, err := tx.Law().SetInformantState(ctx, inf.ID, "retired", lc.now); err != nil {
		return "", err
	}
	return village.LawNoticeRetired, h.lawContact(ctx, tx, lc, inf.ID, lc.p.ID, "retire", 0, "")
}

// lawAfterConviction is what follows a conviction: the complaint's fee comes back, an informant whose tip stood in the file may be burned (a cash
// penalty and a ban from the market for some days, never harm to the person), and a gang's convictions are counted.
func (h *VillageHandler) lawAfterConviction(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) error {
	if c.ComplaintFee > 0 && c.OpenedBy != "" {
		tr, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
		if err != nil {
			return err
		}
		to, err := lawAcct(ctx, tx, application.AccountPlayerBank, c.OpenedBy)
		if err != nil {
			return err
		}
		if err := lawPost(ctx, tx, lc, application.ReasonCourtFeeRefund, application.LawCaseReference, c.ID, tr.ID, to.ID, min(bal, c.ComplaintFee)); err != nil {
			return err
		}
	}
	items, err := tx.Law().Evidence(ctx, c.ID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range items {
		if e.Kind != law.EvInformant || e.InformantID == "" || seen[e.InformantID] {
			continue
		}
		seen[e.InformantID] = true
		if !law.Hit(lc.t.BurnBPS, law.Roll(c.ID+"|burn|"+e.InformantID)) {
			continue
		}
		if err := h.lawBurn(ctx, tx, lc, c, e.InformantID); err != nil {
			return err
		}
	}
	return h.lawGangAfter(ctx, tx, lc, c)
}

// lawBurn exposes an informant after a conviction his tip helped: the penalty is cash and a ban from the market, nothing else.
func (h *VillageHandler) lawBurn(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase, informantID string) error {
	inf, err := tx.Law().InformantByID(ctx, informantID, true)
	if err != nil || inf == nil {
		return err
	}
	if ok, err := tx.Law().SetInformantState(ctx, inf.ID, "burned", lc.now); err != nil || !ok {
		return err
	}
	if inf.SourceID != "" {
		fine, banDays := lc.t.Retaliation()
		sink, err := lawSink(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := lawTake(ctx, tx, lc, inf.SourceID, fine, application.ReasonInformantRetaliate, application.LawInformantRef, inf.ID, sink.ID); err != nil {
			return err
		}
		v, err := tx.Law().VerdictOf(ctx, c.ID, 1)
		if err != nil || v == nil {
			return err
		}
		end := lc.now.Add(time.Duration(banDays) * 24 * time.Hour)
		row := application.LawSentence{ID: h.ids.NewID(), CaseID: c.ID, VerdictID: v.ID, PlayerID: inf.SourceID, Kind: "ban", Ban: BanMarket, BanDays: banDays,
			StartsAt: lc.now, EndsAt: &end}
		if err := tx.Law().InsertSentence(ctx, row); err != nil {
			return err
		}
	}
	return h.lawContact(ctx, tx, lc, inf.ID, inf.HandlerID, "burn", 0, c.ID)
}

// ---------------------------------------------------------------------------
// bribery and audits
// ---------------------------------------------------------------------------

// The rungs a bribe may lower, and the power that decides each.
var bribeRungs = map[string]charter.Permission{
	"warrant":   charter.CourtWarrant,
	"detection": charter.PolicePatrol,
	"verdict":   charter.CourtJudge,
}

// lawBribeOffer offers money to an office-holder to make one rung of the law weigh less on the briber for a term. Nothing moves until the holder
// accepts; the offer itself is an act that is on the record.
func (h *VillageHandler) lawBribeOffer(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	rung := strings.TrimSpace(lc.req.Kind)
	perm, ok := bribeRungs[rung]
	if !ok {
		return "", lawRefuse(village.LawBadArgs)
	}
	holder, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	if holder == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if has, err := h.lawHolds(ctx, tx, lc, holder, perm); err != nil {
		return "", err
	} else if !has {
		return "", lawRefuse(village.LawNotHolder)
	}
	amount := lc.req.num(lc.req.Amount)
	if amount < lc.t.BribeMin {
		return "", lawRefuse(village.LawBribeSmall)
	}
	effect := lc.t.BribeEffect(lc.req.num(lc.req.Bps))
	if effect <= 0 {
		effect = lc.t.BribeEffect(lc.t.BribeMaxEffectBPS)
	}
	if err := tx.Law().InsertBribe(ctx, application.LawBribe{ID: h.ids.NewID(), SettlementID: lc.s.CityID, BriberID: lc.p.ID, HolderID: holder, Rung: rung,
		EffectBPS: int(effect), Amount: amount, Dirty: strings.TrimSpace(lc.req.Dirty) == "1", OfferedAt: lc.now}); err != nil {
		return "", err
	}
	return village.LawNoticeBribeOffered, nil
}

// lawHolds says a player holds a permission in the settlement.
func (h *VillageHandler) lawHolds(ctx context.Context, tx application.Tx, lc *lawCtx, player string, perm charter.Permission) (bool, error) {
	hs, err := lawHolders(ctx, tx, lc.s, perm)
	if err != nil {
		return false, err
	}
	for _, id := range hs {
		if id == player {
			return true, nil
		}
	}
	return false, nil
}

// lawBribeAnswer is the holder accepting or refusing an offer. Accepting takes the money from the briber (his dirty balance, or his clean money)
// into the holder's dirty balance, and starts the term; the holder is then exposed to an audit.
func (h *VillageHandler) lawBribeAnswer(ctx context.Context, tx application.Tx, lc *lawCtx, accept bool) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	b, err := tx.Law().BribeByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if b == nil || !lc.has || b.SettlementID != lc.s.CityID || b.HolderID != lc.p.ID || b.Status != "offered" {
		return "", lawRefuse(village.LawNoBribe)
	}
	if !accept {
		if _, err := tx.Law().AnswerBribe(ctx, b.ID, "refused", nil, lc.now); err != nil {
			return "", err
		}
		return village.LawNoticeBribeRefused, nil
	}
	hd, err := lawAcct(ctx, tx, application.AccountPlayerDirty, b.HolderID)
	if err != nil {
		return "", err
	}
	if b.Dirty {
		bd, bal, err := lawBalance(ctx, tx, application.AccountPlayerDirty, b.BriberID)
		if err != nil {
			return "", err
		}
		if bal < b.Amount {
			return "", lawRefuse(village.LawBriberShort)
		}
		if err := lawPost(ctx, tx, lc, application.ReasonBribe, application.LawBribeReference, b.ID, bd.ID, hd.ID, b.Amount); err != nil {
			return "", err
		}
	} else if err := lawPay(ctx, tx, lc, b.BriberID, b.Amount, application.ReasonBribe, application.LawBribeReference, b.ID, hd.ID); err != nil {
		return "", lawRefuse(village.LawBriberShort)
	}
	end := lc.now.Add(time.Duration(lc.t.BribeTermDays) * 24 * time.Hour)
	if ok, err := tx.Law().AnswerBribe(ctx, b.ID, "accepted", &end, lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoBribe)
	}
	return village.LawNoticeBribeTaken, nil
}

// lawAuditIntegrity is internal affairs looking at an office-holder: it costs the settlement a fee, and for each bribe the holder took that is still
// open it rolls a seeded chance, higher for each day the bribe has stood. A bribe found is forfeited out of what the holder holds dirty, and a case is
// opened against both the holder and the briber with the record of the bribe and the trace of the money in the ledger. Informant handlers' records of
// pressing a source are looked at too. One audit of one person a day.
func (h *VillageHandler) lawAuditIntegrity(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.AuditIntegrity); err != nil {
		return "", err
	}
	target, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	// the auditor is never the audited, and an office that can arrest does not audit (the charter keeps them apart)
	if target == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	day := h.lawDay(lc)
	audit := application.LawAudit{ID: h.ids.NewID(), SettlementID: lc.s.CityID, Kind: "integrity", AuditorID: lc.p.ID, TargetID: target, Day: day,
		Outcome: "cleared", Fee: lc.t.AuditCost, At: lc.now}
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
	bribes, err := tx.Law().OpenBribes(ctx, lc.s.CityID, target, lc.now)
	if err != nil {
		return "", err
	}
	found := false
	for _, b := range bribes {
		answered := b.OfferedAt
		if b.AnsweredAt != nil {
			answered = *b.AnsweredAt
		}
		age := int64(lc.now.Sub(answered) / (24 * time.Hour))
		if !law.Hit(lc.t.AuditChance(age), law.Roll(audit.ID+"|"+b.ID+"|audit")) {
			continue
		}
		if ok, err := tx.Law().FindBribe(ctx, b.ID, lc.now); err != nil || !ok {
			if err != nil {
				return "", err
			}
			continue
		}
		found = true
		if err := h.lawBribeFound(ctx, tx, lc, b); err != nil {
			return "", err
		}
	}
	// pressing an informant is on the record too
	acts, err := tx.Law().Acts(ctx, lc.s.CityID, target, lc.now.Add(-time.Duration(lc.t.FactWindowDays)*24*time.Hour))
	if err != nil {
		return "", err
	}
	for _, a := range acts {
		if a.Kind != "press" {
			continue
		}
		if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, "false_evidence", lc.now); err != nil {
			return "", err
		} else if o == nil {
			continue
		}
		found = true
		c, err := h.lawOpenOrFind(ctx, tx, lc, "false_evidence", target, "audit", lc.p.ID, 0)
		if err != nil {
			return "", err
		}
		if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvRecord, "press:"+a.Ref, true, -1, "", lc.p.ID); err != nil {
			return "", err
		}
	}
	if found {
		return village.LawNoticeAuditFound, nil
	}
	return village.LawNoticeAuditClean, nil
}

// lawBribeFound is what follows an audit finding a bribe: the money is forfeited out of the holder's dirty balance, and cases open against both.
func (h *VillageHandler) lawBribeFound(ctx context.Context, tx application.Tx, lc *lawCtx, b application.LawBribe) error {
	hd, bal, err := lawBalance(ctx, tx, application.AccountPlayerDirty, b.HolderID)
	if err != nil {
		return err
	}
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonBribeForfeit, application.LawBribeReference, b.ID, hd.ID, tr.ID, min(bal, b.Amount)); err != nil {
		return err
	}
	if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, "bribery", lc.now); err != nil || o == nil {
		return err
	}
	for _, who := range []string{b.HolderID, b.BriberID} {
		c, err := h.lawOpenOrFind(ctx, tx, lc, "bribery", who, "audit", lc.p.ID, 0)
		if err != nil {
			return err
		}
		if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvRecord, "b:"+b.ID, true, -1, "", lc.p.ID); err != nil {
			return err
		}
		if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvLedger, "bl:"+b.ID, true, -1, "", lc.p.ID); err != nil {
			return err
		}
	}
	return nil
}

// lawCaptured says several office-holders of the settlement hold a bribe that stands: the settlement is shown as captured.
func (h *VillageHandler) lawCaptured(ctx context.Context, tx application.Tx, lc *lawCtx) (bool, error) {
	n, err := tx.Law().BribedHolders(ctx, lc.s.CityID, lc.now)
	return n >= lc.t.CapturedFrom, err
}
