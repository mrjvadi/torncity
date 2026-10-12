package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// lawBounds are the bounds of an offence a sentence works inside: the code's, lowered by what the charter listed.
func lawBounds(lc *lawCtx, o *application.LawOffence, offence string) (law.Bounds, bool) {
	def, ok := lc.snap.Offence(offence)
	if !ok {
		return law.Bounds{}, false
	}
	b := law.Bounds{Kinds: def.Sentences, MaxFine: def.MaxFine, MaxJailHours: def.MaxJailHours, MaxBanDays: def.MaxBanDays}
	if o != nil {
		b.MaxFine, b.MaxJailHours, b.MaxBanDays = min(b.MaxFine, o.MaxFine), min(b.MaxJailHours, o.MaxJailHours), min(b.MaxBanDays, o.MaxBanDays)
	}
	return b, true
}

// The scopes a ban keeps a person out of; each is read by a system that exists.
const (
	BanMarket = "market"
	BanOffice = "office"
)

// parseSentences reads "fine:500,jail:24,ban:market:7,restitution:300,forfeiture:1000,caution".
func parseSentences(s string) ([]law.Sentence, bool) {
	var out []law.Sentence
	for _, tok := range strings.Split(strings.TrimSpace(s), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		f := strings.Split(tok, ":")
		num := func(i int) int64 {
			if i >= len(f) {
				return 0
			}
			n, _ := strconv.ParseInt(strings.TrimSpace(f[i]), 10, 64)
			return n
		}
		switch f[0] {
		case "fine", "restitution", "forfeiture":
			out = append(out, law.Sentence{Kind: f[0], Fine: num(1)})
		case "jail":
			out = append(out, law.Sentence{Kind: "jail", JailHours: num(1)})
		case "ban":
			if len(f) < 3 {
				return nil, false
			}
			out = append(out, law.Sentence{Kind: "ban", Ban: strings.TrimSpace(f[1]), BanDays: num(2)})
		case "caution":
			out = append(out, law.Sentence{Kind: "caution"})
		default:
			return nil, false
		}
	}
	return out, len(out) > 0
}

// lawJail is a jail of the settlement that is open today with a free cell. cells is its capacity, building its id.
func (h *VillageHandler) lawJail(ctx context.Context, tx application.Tx, lc *lawCtx) (building string, cell int, ok bool, err error) {
	posts, err := h.lawOpenPosts(ctx, tx, lc, "custody")
	if err != nil {
		return "", 0, false, err
	}
	for _, ps := range posts {
		var warders int64
		for _, st := range ps.Def.Staff {
			if st.Role == "warder" {
				warders += int64(st.Slots)
			}
		}
		c, free, err := tx.Law().FreeCell(ctx, ps.Building.ID, lc.t.JailCells(warders))
		if err != nil {
			return "", 0, false, err
		}
		if free {
			return ps.Building.ID, c, true, nil
		}
	}
	return "", 0, false, nil
}

// ---------------------------------------------------------------------------
// the charge, the defence, the exclusion
// ---------------------------------------------------------------------------

// lawCharge is the prosecutor charging a case: the file must hold enough to be worth a trial. The hearing is set a few hours on; the custody, if
// any, ends (the person awaits the trial free, or on bail).
func (h *VillageHandler) lawCharge(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.ProsecuteCharge); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "open" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if c.AccusedID == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	items, err := tx.Law().Evidence(ctx, c.ID)
	if err != nil {
		return "", err
	}
	if lc.t.Total(lawEvidenceItems(items), false) < lc.t.ChargeThreshold {
		return "", lawRefuse(village.LawWeakEvidence)
	}
	if fresh, err := tx.Law().Stage(ctx, c.ID, "charged", lc.p.ID, lc.now); err != nil {
		return "", err
	} else if !fresh {
		return village.LawNoticeCharged, nil
	}
	at := lc.now.Add(time.Duration(lc.t.HearingDelayHours) * time.Hour)
	if ok, err := tx.Law().Charge(ctx, c.ID, lc.p.ID, at, lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if _, err := tx.Law().ReleaseCustody(ctx, c.ID, lc.now); err != nil {
		return "", err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLawHearing, application.LawCaseReference, c.ID, "", "", lc.now, at); err != nil {
		return "", err
	}
	return village.LawNoticeCharged, nil
}

// lawDrop is the prosecutor dropping a case, or the magistrate closing one.
func (h *VillageHandler) lawDrop(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.ProsecuteDrop); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if fresh, err := tx.Law().Stage(ctx, c.ID, "dropped", lc.p.ID, lc.now); err != nil {
		return "", err
	} else if !fresh {
		return village.LawNoticeDropped, nil
	}
	if ok, err := tx.Law().Move(ctx, c.ID, []string{"open", "charged"}, "dropped", lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawCaseClosed)
	}
	_, err = tx.Law().ReleaseCustody(ctx, c.ID, lc.now)
	return village.LawNoticeDropped, err
}

// lawDefend gives the accused a defence: a defender of his choice (a holder of defence.appear), or the settlement's own, paid by the treasury.
func (h *VillageHandler) lawDefend(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "open" && c.State != "charged" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if strings.TrimSpace(lc.req.Kind) == "npc" {
		if c.AccusedID != lc.p.ID {
			return "", lawRefuse(village.LawNotAccused)
		}
		if ok, err := tx.Law().SetDefence(ctx, c.ID, "", true, lc.now); err != nil {
			return "", err
		} else if !ok {
			return "", lawRefuse(village.LawHasDefence)
		}
		return village.LawNoticeDefended, nil
	}
	if _, err := lawPerm(ctx, tx, lc, charter.DefenceAppear); err != nil {
		return "", err
	}
	if lc.p.ID == c.ChargedBy {
		return "", lawRefuse(village.LawConflict)
	}
	if ok, err := tx.Law().SetDefence(ctx, c.ID, lc.p.ID, false, lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawHasDefence)
	}
	return village.LawNoticeDefended, nil
}

// lawExclude is the defence asking for an improper item of evidence to be left out of the trial. The chance is the tuning's and the roll is seeded
// by the item: asking again gives the same answer.
func (h *VillageHandler) lawExclude(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	e, err := tx.Law().EvidenceByID(ctx, strings.TrimSpace(lc.req.ID))
	if err != nil {
		return "", err
	}
	if e == nil {
		return "", lawRefuse(village.LawNoEvidence)
	}
	c, err := h.lawCaseFor(ctx, tx, lc, e.CaseID, true)
	if err != nil {
		return "", err
	}
	if c.AccusedID != lc.p.ID && c.DefenceID != lc.p.ID {
		return "", lawRefuse(village.LawNotAccused)
	}
	if c.State != "charged" && c.State != "open" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if e.Proper || e.Excluded {
		return "", lawRefuse(village.LawNotImproper)
	}
	if !law.Hit(lc.t.ExclusionBPS, law.Roll(e.ID+"|exclude")) {
		return village.LawNoticeNotExcluded, nil
	}
	if _, err := tx.Law().Exclude(ctx, e.ID); err != nil {
		return "", err
	}
	return village.LawNoticeExcluded, nil
}

// ---------------------------------------------------------------------------
// the hearing and the verdict
// ---------------------------------------------------------------------------

// lawJudges are the people who may judge the case: holders of court.judge, none of them the person who charged it in the adversarial model.
func lawJudges(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) ([]string, error) {
	all, err := lawHolders(ctx, tx, lc.s, charter.CourtJudge)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range all {
		if id == c.AccusedID {
			continue
		}
		if c.Model == "adversarial" && law.Conflict(c.ChargedBy, id) {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// tickLawHearing is the hearing of a charged case on its day. It needs a courthouse open today; without one it is put off a day. With a player judge
// the case waits for the judge's verdict for judge_wait_hours; with none, or when the wait is over, the NPC judge decides. Every step is fenced by a stage
// row, so a second replica finds the step done and changes nothing.
func (h *VillageHandler) tickLawHearing(ctx context.Context, tx application.Tx, meta lawMeta, caseID, aux string, now time.Time) error {
	c, err := tx.Law().CaseByID(ctx, caseID, true)
	if err != nil || c == nil || (c.State != "charged" && c.State != "hearing") {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, c.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	if c.State == "charged" {
		posts, err := h.lawOpenPosts(ctx, tx, lc, "court")
		if err != nil {
			return err
		}
		if len(posts) == 0 {
			day := strconv.FormatInt(h.lawDay(lc), 10)
			if fresh, err := tx.Law().Stage(ctx, c.ID, "postponed:"+day, "", now); err != nil {
				return err
			} else if fresh {
				return h.schedulePolity(ctx, tx, application.TickLawHearing, application.LawCaseReference, c.ID, "", "", now, now.Add(24*time.Hour))
			}
			return nil
		}
		if fresh, err := tx.Law().Stage(ctx, c.ID, "hearing", "", now); err != nil {
			return err
		} else if fresh {
			if _, err := tx.Law().Move(ctx, c.ID, []string{"charged"}, "hearing", now); err != nil {
				return err
			}
			judges, err := lawJudges(ctx, tx, lc, c)
			if err != nil {
				return err
			}
			if len(judges) > 0 {
				// a player judge decides; the NPC judge is the fallback when the wait is over
				wait := now.Add(time.Duration(lc.t.JudgeWaitHours) * time.Hour)
				return h.schedulePolity(ctx, tx, application.TickLawHearing, application.LawCaseReference, c.ID, "wait", "", now, wait)
			}
		}
	}
	c, err = tx.Law().CaseByID(ctx, caseID, true)
	if err != nil || c == nil || c.State != "hearing" {
		return err
	}
	judges, err := lawJudges(ctx, tx, lc, c)
	if err != nil {
		return err
	}
	if len(judges) > 0 && aux != "wait" {
		return nil // the judge's verdict is awaited
	}
	return h.lawNPCVerdict(ctx, tx, lc, c)
}

// lawVerdictInputs reads what a verdict is made from: the strength that stands, the procedure quality and whether a defence appeared.
func lawVerdictInputs(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) (law.VerdictIn, error) {
	items, err := tx.Law().Evidence(ctx, c.ID)
	if err != nil {
		return law.VerdictIn{}, err
	}
	ev := lawEvidenceItems(items)
	return law.VerdictIn{Total: lc.t.Total(ev, false), Quality: lc.t.Quality(ev), Defence: c.DefenceID != "" || c.DefenceNPC}, nil
}

// lawNPCVerdict is the NPC judge's decision: a threshold on the strength that stands, after the procedure and the defence. A guilty verdict is
// sentenced from the offence's bounds, a part of them (npc_sentence_bps), in a jail only when one is open with a cell.
func (h *VillageHandler) lawNPCVerdict(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) error {
	in, err := lawVerdictInputs(ctx, tx, lc, c)
	if err != nil {
		return err
	}
	if c.DefenceNPC {
		if err := h.lawDefenderFee(ctx, tx, lc, c); err != nil {
			return err
		}
	}
	guilty := lc.t.NPCGuilty(in)
	v := application.LawVerdict{ID: h.ids.NewID(), CaseID: c.ID, Instance: 1, NPC: true, Guilty: guilty, Total: in.Total, Effective: lc.t.Effective(in),
		Quality: int(in.Quality), Defence: in.Defence, At: lc.now}
	return h.lawRecordVerdict(ctx, tx, lc, c, v, nil)
}

// lawDefenderFee pays the settlement's own defender of a poor accused out of the treasury, when it can.
func (h *VillageHandler) lawDefenderFee(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) error {
	tr, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
	if err != nil {
		return err
	}
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return err
	}
	return lawPost(ctx, tx, lc, application.ReasonDefenderFee, application.LawCaseReference, c.ID, tr.ID, sink.ID, min(bal, lc.t.DefenceFee))
}

// lawRecordVerdict writes a verdict once, sentences a guilty one (the sentences a player judge named, or the NPC judge's own), and closes the stage.
func (h *VillageHandler) lawRecordVerdict(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase, v application.LawVerdict, named []law.Sentence) error {
	if fresh, err := tx.Law().Stage(ctx, c.ID, "decided", v.JudgeID, lc.now); err != nil || !fresh {
		return err
	}
	if ok, err := tx.Law().InsertVerdict(ctx, v); err != nil || !ok {
		return err
	}
	if v.Guilty {
		sentences := named
		if len(sentences) == 0 {
			var err error
			if sentences, err = h.lawNPCSentences(ctx, tx, lc, c); err != nil {
				return err
			}
		}
		if err := h.lawApplySentences(ctx, tx, lc, c, v.ID, sentences, v.NPC); err != nil {
			return err
		}
		if err := h.lawAfterConviction(ctx, tx, lc, c); err != nil {
			return err
		}
	}
	if _, err := tx.Law().Move(ctx, c.ID, []string{"hearing"}, "decided", lc.now); err != nil {
		return err
	}
	if err := tx.Law().Decided(ctx, c.ID, lc.now); err != nil {
		return err
	}
	return h.schedulePolity(ctx, tx, application.TickLawFinal, application.LawCaseReference, c.ID, "", "", lc.now, lc.now.Add(time.Duration(lc.t.AppealDays)*24*time.Hour))
}

// lawNPCSentences are what the NPC judge hands down: a part of the offence's bounds. A grave or serious offence is a jail term where a jail is open with
// a cell, a fine where it is not; a petty one is a fine.
func (h *VillageHandler) lawNPCSentences(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase) ([]law.Sentence, error) {
	o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, c.Offence, lc.now)
	if err != nil {
		return nil, err
	}
	b, ok := lawBounds(lc, o, c.Offence)
	if !ok {
		return nil, nil
	}
	share := func(bound int64) int64 { return max(bound*lc.t.NPCSentenceBPS/law.BPS, 1) }
	def, _ := lc.snap.Offence(c.Offence)
	allows := func(k string) bool { return def.Allows(k) }
	var out []law.Sentence
	if def.Class != "petty" && allows("jail") && b.MaxJailHours > 0 {
		if _, _, free, err := h.lawJail(ctx, tx, lc); err != nil {
			return nil, err
		} else if free {
			out = append(out, law.Sentence{Kind: "jail", JailHours: share(b.MaxJailHours)})
		}
	}
	if len(out) == 0 && allows("fine") && b.MaxFine > 0 {
		out = append(out, law.Sentence{Kind: "fine", Fine: share(b.MaxFine)})
	}
	if len(out) == 0 && allows("caution") {
		out = append(out, law.Sentence{Kind: "caution"})
	}
	return out, nil
}

// lawVerdict is a player judge deciding a case in hearing: guilty with sentences, or acquittal. Every sentence is checked against the bounds of the
// offence, the judge's own ceiling on a fine, and the cells that stand. A bribe the accused made to this judge on the verdict rung lowers the size of
// each sentence, never to nothing.
func (h *VillageHandler) lawVerdict(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	limit, err := lawPerm(ctx, tx, lc, charter.CourtJudge)
	if err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "hearing" {
		return "", lawRefuse(village.LawNotInHearing)
	}
	if c.Model == "adversarial" && law.Conflict(c.ChargedBy, lc.p.ID) || c.AccusedID == lc.p.ID {
		return "", lawRefuse(village.LawConflict)
	}
	in, err := lawVerdictInputs(ctx, tx, lc, c)
	if err != nil {
		return "", err
	}
	if c.DefenceNPC {
		if err := h.lawDefenderFee(ctx, tx, lc, c); err != nil {
			return "", err
		}
	}
	guilty := strings.TrimSpace(lc.req.Code) == "guilty"
	v := application.LawVerdict{ID: h.ids.NewID(), CaseID: c.ID, Instance: 1, JudgeID: lc.p.ID, Guilty: guilty, Total: in.Total, Effective: lc.t.Effective(in),
		Quality: int(in.Quality), Defence: in.Defence, At: lc.now}
	var named []law.Sentence
	if guilty {
		var ok bool
		if named, ok = parseSentences(lc.req.Items); !ok {
			return "", lawRefuse(village.LawBadArgs)
		}
		br, err := tx.Law().BribeFrom(ctx, lc.s.CityID, c.AccusedID, lc.p.ID, "verdict", lc.now)
		if err != nil {
			return "", err
		}
		o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, c.Offence, lc.now)
		if err != nil {
			return "", err
		}
		b, ok := lawBounds(lc, o, c.Offence)
		if !ok {
			return "", lawRefuse(village.LawNoOffence)
		}
		_, _, jailFree, err := h.lawJail(ctx, tx, lc)
		if err != nil {
			return "", err
		}
		for i := range named {
			if br != nil {
				named[i].Fine = lc.t.AfterBribe(named[i].Fine, int64(br.EffectBPS))
				named[i].JailHours = lc.t.AfterBribe(named[i].JailHours, int64(br.EffectBPS))
				named[i].BanDays = lc.t.AfterBribe(named[i].BanDays, int64(br.EffectBPS))
			}
			if err := h.lawCheckSentence(b, named[i], limit, jailFree); err != nil {
				return "", err
			}
		}
	}
	if err := h.lawRecordVerdict(ctx, tx, lc, c, v, named); err != nil {
		return "", err
	}
	return village.LawNoticeVerdict, nil
}

// lawCheckSentence maps the pure rule's refusals to the refusals a player reads.
func (h *VillageHandler) lawCheckSentence(b law.Bounds, s law.Sentence, judgeFineLimit int64, jailFree bool) error {
	err := law.ValidateSentence(b, s, judgeFineLimit, jailFree)
	switch {
	case err == nil:
		if s.Kind == "ban" && s.Ban != BanMarket && s.Ban != BanOffice {
			return lawRefuse(village.LawBadArgs)
		}
		return nil
	case stderrors.Is(err, law.ErrNoJail):
		return lawRefuse(village.LawNoJail)
	case stderrors.Is(err, law.ErrJudgeLimit):
		return lawRefuse(village.LawJudgeLimit)
	case stderrors.Is(err, law.ErrKindNotAllowed):
		return lawRefuse(village.LawKindNotAllowed)
	case stderrors.Is(err, law.ErrOverBound):
		return lawRefuse(village.LawOverBound)
	}
	return lawRefuse(village.LawBadArgs)
}

// lawApplySentences carries out each sentence of a verdict: a fine is taken now (what the accused can pay; the rest is written on the sentence), a
// restitution goes to the complainant, a forfeiture takes from the dirty balance with the police share, a jail term is served in a cell of a jail that
// stands, a ban is recorded and read by the system it names.
func (h *VillageHandler) lawApplySentences(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase, verdictID string, ss []law.Sentence, npc bool) error {
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return err
	}
	for _, s := range ss {
		row := application.LawSentence{ID: h.ids.NewID(), CaseID: c.ID, VerdictID: verdictID, PlayerID: c.AccusedID, Kind: s.Kind, Fine: s.Fine, JailHours: s.JailHours,
			BanDays: s.BanDays, Ban: s.Ban, StartsAt: lc.now}
		status, paid := "", int64(0)
		switch s.Kind {
		case "fine":
			paid, err = lawTake(ctx, tx, lc, c.AccusedID, s.Fine, application.ReasonFineVerdict, application.LawSentenceReference, row.ID, tr.ID)
			status = "paid"
		case "restitution":
			to := tr.ID
			if c.OpenedBy != "" {
				if a, e := lawAcct(ctx, tx, application.AccountPlayerCash, c.OpenedBy); e == nil {
					to = a.ID
				}
			}
			paid, err = lawTake(ctx, tx, lc, c.AccusedID, s.Fine, application.ReasonRestitutionVerdict, application.LawSentenceReference, row.ID, to)
			status = "paid"
		case "forfeiture":
			paid, err = h.lawForfeit(ctx, tx, lc, c.AccusedID, s.Fine, row.ID, tr.ID)
			status = "paid"
		case "jail":
			b, cell, ok, e := h.lawJail(ctx, tx, lc)
			if e != nil {
				return e
			}
			if !ok {
				if npc {
					continue // the jail closed between the choice and the sentence: no cell, no jail term
				}
				return lawRefuse(village.LawNoJail)
			}
			end := lc.now.Add(time.Duration(s.JailHours) * time.Hour)
			row.JailBuilding, row.Cell, row.EndsAt = b, cell, &end
		case "ban":
			end := lc.now.Add(time.Duration(s.BanDays) * 24 * time.Hour)
			row.EndsAt = &end
		case "caution":
			status = "served"
		}
		if err != nil {
			return err
		}
		if err := tx.Law().InsertSentence(ctx, row); err != nil {
			return err
		}
		if status != "" {
			if _, err := tx.Law().SetSentence(ctx, row.ID, status, paid, 0); err != nil {
				return err
			}
		}
		if s.Kind == "jail" {
			if err := h.schedulePolity(ctx, tx, application.TickLawJail, application.LawSentenceReference, row.ID, "", c.AccusedID, lc.now, *row.EndsAt); err != nil {
				return err
			}
		}
	}
	return nil
}

// lawForfeit takes what the accused holds in the dirty balance, up to the amount, for the treasury, less the police share (capped by the tuning).
func (h *VillageHandler) lawForfeit(ctx context.Context, tx application.Tx, lc *lawCtx, playerID string, amount int64, ref, treasury string) (int64, error) {
	d, bal, err := lawBalance(ctx, tx, application.AccountPlayerDirty, playerID)
	if err != nil {
		return 0, err
	}
	take := min(bal, amount)
	if take <= 0 {
		return 0, nil
	}
	share := lc.t.PoliceShare(take)
	pu, err := lawAcct(ctx, tx, application.AccountPolicePurse, lc.s.CityID)
	if err != nil {
		return 0, err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonForfeiturePolice, application.LawSentenceReference, ref, d.ID, pu.ID, share); err != nil {
		return 0, err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonForfeiture, application.LawSentenceReference, ref, d.ID, treasury, take-share); err != nil {
		return 0, err
	}
	return take, nil
}

// ---------------------------------------------------------------------------
// the appeal, the end of the case, pardon and bail
// ---------------------------------------------------------------------------

// lawAppeal is a party appealing a decided case once, within the window, for a fee. It moves the case to appealed and puts the appeal before a judge of
// the appeal bench; the NPC panel hears it when nobody does within judge_wait_hours.
func (h *VillageHandler) lawAppeal(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	party := strings.TrimSpace(lc.req.Kind)
	switch party {
	case "accused":
		if c.AccusedID != lc.p.ID {
			return "", lawRefuse(village.LawNotAccused)
		}
	case "prosecution":
		if _, err := lawPerm(ctx, tx, lc, charter.ProsecuteCharge); err != nil {
			return "", err
		}
	default:
		return "", lawRefuse(village.LawBadArgs)
	}
	if c.State != "decided" && c.State != "appealed" {
		return "", lawRefuse(village.LawCaseClosed)
	}
	v, err := tx.Law().VerdictOf(ctx, c.ID, 1)
	if err != nil || v == nil {
		return "", lawRefuse(village.LawCaseClosed)
	}
	apps, err := tx.Law().AppealsOf(ctx, c.ID)
	if err != nil {
		return "", err
	}
	used := false
	for _, a := range apps {
		used = used || a.Party == party
	}
	if err := lc.t.CanAppeal(used, int64(lc.now.Sub(v.At)/time.Hour)); err != nil {
		if err == law.ErrAppealUsed {
			return "", lawRefuse(village.LawAppealUsed)
		}
		return "", lawRefuse(village.LawAppealLate)
	}
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return "", err
	}
	if party == "accused" {
		if err := lawPay(ctx, tx, lc, lc.p.ID, lc.t.AppealFee, application.ReasonAppealFee, application.LawCaseReference, c.ID, tr.ID); err != nil {
			return "", err
		}
	}
	if ok, err := tx.Law().InsertAppeal(ctx, application.LawAppeal{CaseID: c.ID, Party: party, ByID: lc.p.ID, Fee: lc.t.AppealFee, At: lc.now}); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawAppealUsed)
	}
	if _, err := tx.Law().Move(ctx, c.ID, []string{"decided"}, "appealed", lc.now); err != nil {
		return "", err
	}
	if fresh, err := tx.Law().Stage(ctx, c.ID, "appeal:"+party, lc.p.ID, lc.now); err != nil {
		return "", err
	} else if fresh {
		wait := lc.now.Add(time.Duration(lc.t.JudgeWaitHours) * time.Hour)
		if err := h.schedulePolity(ctx, tx, application.TickLawAppeal, application.LawCaseReference, c.ID, party, "", lc.now, wait); err != nil {
			return "", err
		}
	}
	return village.LawNoticeAppealed, nil
}

// lawAppealDecide is a judge of the appeal bench confirming or quashing the verdict. The judge is not the one who tried the case and not the one who
// charged it.
func (h *VillageHandler) lawAppealDecide(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.CourtAppealHear); err != nil {
		return "", err
	}
	c, err := h.lawCaseFor(ctx, tx, lc, lc.req.ID, true)
	if err != nil {
		return "", err
	}
	if c.State != "appealed" {
		return "", lawRefuse(village.LawNotAppealed)
	}
	first, err := tx.Law().VerdictOf(ctx, c.ID, 1)
	if err != nil || first == nil {
		return "", lawRefuse(village.LawCaseClosed)
	}
	if lc.p.ID == first.JudgeID || lc.p.ID == c.ChargedBy || lc.p.ID == c.AccusedID {
		return "", lawRefuse(village.LawConflict)
	}
	guilty := strings.TrimSpace(lc.req.Code) != "quash"
	var named []law.Sentence
	if guilty && !first.Guilty {
		var ok bool
		if named, ok = parseSentences(lc.req.Items); !ok {
			return "", lawRefuse(village.LawBadArgs)
		}
	}
	return village.LawNoticeAppealDecided, h.lawFinishAppeal(ctx, tx, lc, c, first, lc.p.ID, guilty, named)
}

// tickLawAppeal is the NPC panel hearing an appeal nobody heard in time: it convicts only if the strength that stands clears the NPC threshold
// raised by the appeal margin.
func (h *VillageHandler) tickLawAppeal(ctx context.Context, tx application.Tx, meta lawMeta, caseID string, now time.Time) error {
	c, err := tx.Law().CaseByID(ctx, caseID, true)
	if err != nil || c == nil || c.State != "appealed" {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, c.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	first, err := tx.Law().VerdictOf(ctx, c.ID, 1)
	if err != nil || first == nil {
		return err
	}
	in, err := lawVerdictInputs(ctx, tx, lc, c)
	if err != nil {
		return err
	}
	guilty := lc.t.Effective(in) >= lc.t.NPCThreshold+lc.t.AppealMargin
	var named []law.Sentence
	if guilty && !first.Guilty {
		if named, err = h.lawNPCSentences(ctx, tx, lc, c); err != nil {
			return err
		}
	}
	return h.lawFinishAppeal(ctx, tx, lc, c, first, "", guilty, named)
}

// lawFinishAppeal writes the appeal verdict once. A quashed conviction frees the person, gives back what was paid and the appeal fee; a conviction
// on appeal is sentenced like any other.
func (h *VillageHandler) lawFinishAppeal(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase, first *application.LawVerdict, judge string, guilty bool, named []law.Sentence) error {
	if fresh, err := tx.Law().Stage(ctx, c.ID, "appeal_decided", judge, lc.now); err != nil || !fresh {
		return err
	}
	in, err := lawVerdictInputs(ctx, tx, lc, c)
	if err != nil {
		return err
	}
	v := application.LawVerdict{ID: h.ids.NewID(), CaseID: c.ID, Instance: 2, JudgeID: judge, NPC: judge == "", Guilty: guilty, Total: in.Total,
		Effective: lc.t.Effective(in), Quality: int(in.Quality), Defence: in.Defence, At: lc.now}
	if ok, err := tx.Law().InsertVerdict(ctx, v); err != nil || !ok {
		return err
	}
	apps, err := tx.Law().AppealsOf(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, a := range apps {
		if _, err := tx.Law().SetAppeal(ctx, c.ID, a.Party, "heard"); err != nil {
			return err
		}
	}
	switch {
	case first.Guilty && !guilty:
		if err := h.lawQuash(ctx, tx, lc, c, apps); err != nil {
			return err
		}
	case !first.Guilty && guilty:
		if len(named) == 0 {
			if named, err = h.lawNPCSentences(ctx, tx, lc, c); err != nil {
				return err
			}
		}
		if err := h.lawApplySentences(ctx, tx, lc, c, v.ID, named, judge == ""); err != nil {
			return err
		}
		if err := h.lawAfterConviction(ctx, tx, lc, c); err != nil {
			return err
		}
	}
	if _, err := tx.Law().Move(ctx, c.ID, []string{"appealed"}, "final", lc.now); err != nil {
		return err
	}
	_, err = tx.Law().Stage(ctx, c.ID, "final", judge, lc.now)
	return err
}

// lawQuash undoes a conviction: the sentences are quashed, the cell is freed, what was paid in fines comes back from the treasury, the fee of the
// accused's appeal and the complaint's fee are returned.
func (h *VillageHandler) lawQuash(ctx context.Context, tx application.Tx, lc *lawCtx, c *application.LawCase, apps []application.LawAppeal) error {
	ss, err := tx.Law().SentencesOf(ctx, c.ID)
	if err != nil {
		return err
	}
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return err
	}
	acct, err := lawAcct(ctx, tx, application.AccountPlayerCash, c.AccusedID)
	if err != nil {
		return err
	}
	for _, s := range ss {
		if s.Status == "quashed" {
			continue
		}
		if s.Kind == "fine" && s.FinePaid > 0 {
			_, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
			if err != nil {
				return err
			}
			if err := lawPost(ctx, tx, lc, application.ReasonFineRefund, application.LawSentenceReference, s.ID, tr.ID, acct.ID, min(bal, s.FinePaid)); err != nil {
				return err
			}
		}
		if _, err := tx.Law().ForceSentence(ctx, s.ID, "quashed"); err != nil {
			return err
		}
	}
	for _, a := range apps {
		if a.Party == "accused" && a.Fee > 0 {
			_, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
			if err != nil {
				return err
			}
			if err := lawPost(ctx, tx, lc, application.ReasonAppealRefund, application.LawCaseReference, c.ID, tr.ID, acct.ID, min(bal, a.Fee)); err != nil {
				return err
			}
		}
	}
	return nil
}

// tickLawFinal closes a decided case when the appeal window is over and nobody appealed.
func (h *VillageHandler) tickLawFinal(ctx context.Context, tx application.Tx, meta lawMeta, caseID string, now time.Time) error {
	c, err := tx.Law().CaseByID(ctx, caseID, true)
	if err != nil || c == nil || c.State != "decided" {
		return err
	}
	if fresh, err := tx.Law().Stage(ctx, c.ID, "final", "", now); err != nil || !fresh {
		return err
	}
	_, err = tx.Law().Move(ctx, c.ID, []string{"decided"}, "final", now)
	return err
}

// lawPardon ends a sentence by mercy: the cell is freed, nothing is given back.
func (h *VillageHandler) lawPardon(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, err := lawPerm(ctx, tx, lc, charter.Pardon); err != nil {
		return "", err
	}
	s, err := tx.Law().SentenceByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", lawRefuse(village.LawNoSentence)
	}
	if c, err := tx.Law().CaseByID(ctx, s.CaseID, false); err != nil || c == nil || c.SettlementID != lc.s.CityID {
		return "", lawRefuse(village.LawNoSentence)
	}
	if ok, err := tx.Law().SetSentence(ctx, s.ID, "pardoned", s.FinePaid, 0); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoSentence)
	}
	return village.LawNoticePardoned, nil
}

// lawBail lets a person serving a jail term out by paying the bail for the hours left.
func (h *VillageHandler) lawBail(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.courtsOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	s, err := tx.Law().SentenceByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if s == nil || s.Kind != "jail" || s.Status != "active" || s.PlayerID != lc.p.ID || s.EndsAt == nil {
		return "", lawRefuse(village.LawNoSentence)
	}
	left := int64(s.EndsAt.Sub(lc.now)/time.Hour) + 1
	bail := lc.t.Bail(max(left, 1))
	tr, err := lawTreasury(ctx, tx, lc.s.CityID)
	if err != nil {
		return "", err
	}
	if err := lawPay(ctx, tx, lc, lc.p.ID, bail, application.ReasonBail, application.LawSentenceReference, s.ID, tr.ID); err != nil {
		return "", err
	}
	if _, err := tx.Law().SetSentence(ctx, s.ID, "bailed", s.FinePaid, bail); err != nil {
		return "", err
	}
	return village.LawNoticeBailed, nil
}

// tickLawJail ends a jail term whose time is up, and feeds the prisoner for the days served.
func (h *VillageHandler) tickLawJail(ctx context.Context, tx application.Tx, meta lawMeta, sentenceID string, now time.Time) error {
	s, err := tx.Law().SentenceByID(ctx, sentenceID, true)
	if err != nil || s == nil || s.Status != "active" || s.EndsAt == nil || now.Before(*s.EndsAt) {
		return err
	}
	c, err := tx.Law().CaseByID(ctx, s.CaseID, false)
	if err != nil || c == nil {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, c.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	return h.lawEndJail(ctx, tx, lc, s, "served")
}

// lawEndJail closes a jail sentence and charges the treasury for the food of the days served, as far as it holds.
func (h *VillageHandler) lawEndJail(ctx context.Context, tx application.Tx, lc *lawCtx, s *application.LawSentence, status string) error {
	if ok, err := tx.Law().SetSentence(ctx, s.ID, status, s.FinePaid, s.BailPaid); err != nil || !ok {
		return err
	}
	end := lc.now
	if s.EndsAt != nil && s.EndsAt.Before(end) {
		end = *s.EndsAt
	}
	days := int64(end.Sub(s.StartsAt)/(24*time.Hour)) + 1
	tr, bal, err := lawBalance(ctx, tx, application.AccountCityTreasury, lc.s.CityID)
	if err != nil {
		return err
	}
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return err
	}
	return lawPost(ctx, tx, lc, application.ReasonJailFood, application.LawSentenceReference, s.ID, tr.ID, sink.ID, min(bal, days*lc.t.JailFoodPerDay))
}

// lawJailCheck lets out the prisoners of a settlement whose term is over, and those whose jail was not open today (no warder, no food, no wage): a closed
// jail holds nobody, and nobody is starved.
func (h *VillageHandler) lawJailCheck(ctx context.Context, tx application.Tx, lc *lawCtx) error {
	if !lc.has || !h.courtsOpen() {
		return nil
	}
	js, err := tx.Law().JailsIn(ctx, lc.s.CityID)
	if err != nil || len(js) == 0 {
		return err
	}
	open := map[string]bool{}
	posts, err := h.lawOpenPosts(ctx, tx, lc, "custody")
	if err != nil {
		return err
	}
	for _, ps := range posts {
		open[ps.Building.ID] = true
	}
	for i := range js {
		s := js[i]
		switch {
		case s.EndsAt != nil && !lc.now.Before(*s.EndsAt):
			if err := h.lawEndJail(ctx, tx, lc, &s, "served"); err != nil {
				return err
			}
		case !open[s.JailBuilding]:
			if err := h.lawEndJail(ctx, tx, lc, &s, "released"); err != nil {
				return err
			}
		}
	}
	return nil
}
