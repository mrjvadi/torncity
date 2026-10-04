package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/election"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Charter phase 2 (docs/adr/0044 6.5 and 6.6; research note
// charter-phase2-elections-recall-amendments-interim.md; owner decision 2026-10-05):
//
//   - ELECTED OFFICES. Any resident who has lived here for charter_min_residency_days
//     may stand; the residents vote; plurality wins (election.Count: ties go to the
//     earlier candidacy); the term is charter_election_term_days (14) real days. An
//     election opens by an office holding election.call, or by itself when an elected
//     seat falls vacant or its term ends, or when the head seat is vacant. A ballot is
//     secret and final. An election that draws no vote ends "no_result" and is called
//     again a day later.
//   - RECALL. A petition about one holder, started by a resident, needs the holder to
//     have served charter_recall_min_tenure_days (5) and signatures of
//     charter_recall_signature_bps (20 percent) of the eligible residents, with a floor
//     of charter_recall_min_signatures that is never more than everyone. Enough
//     signatures open a recall vote; the holder is removed by a majority of the votes
//     cast (with the same floor as quorum). A holder who faced a vote is left alone for
//     charter_recall_cooldown_days, and a removed holder cannot be appointed back to the
//     office in that time.
//   - AMENDMENTS. Closing an elected office, or changing the key permissions, how an
//     office is filled or the deputy, goes to a vote of the residents (a majority of the
//     votes cast, with a quorum) once the settlement has charter_amend_vote_min_residents
//     residents; below that the holder of charter.amend decides. Every other change
//     stays with the offices.
//   - THE ACTING HEAD. While the head seat is vacant the deputy office's longest-serving
//     holder (else the longest-serving holder of any office) acts for at most
//     charter_acting_days with the founder's powers less everything that changes the
//     charter or binds the settlement abroad, spends capped. After that nothing they do is
//     valid: the checks simply find no powers. An election for the head seat is called at
//     once.
//
// Everything runs under the settlement's charter lock; a ballot closes by a scheduled
// action and, when nobody's action has run yet, by the next charter act (both settle a
// ballot once: the settling update is conditional).

func (h *VillageHandler) cset() charter.Settings {
	if h.charterSet.ElectionTermDays == 0 {
		return charter.Defaults()
	}
	return h.charterSet
}

// voterCut is the day by which a resident must have been living here to vote or stand
// in a ballot opened at `at`.
func (h *VillageHandler) voterCut(at time.Time) time.Time {
	return at.Add(-time.Duration(h.cset().MinResidencyDays) * 24 * time.Hour)
}

// charterCtx is what a charter act works with, under the lock.
type charterCtx struct {
	ctx  context.Context
	tx   application.Tx
	p    *application.Player
	s    application.FoundedSettlement
	st   charterState
	lang string
	now  time.Time
	meta envelope.Metadata
}

// charterAct runs one act on the charter: the viewer, the settlement, the lock, the
// housekeeping of the day (ballots that are due, terms that ended, vacancies), the
// idempotency key, then fn. fn returns what to show, or nil for a replay.
func (h *VillageHandler) charterAct(ctx context.Context, meta envelope.Metadata, fn func(c *charterCtx) (*village.CharterChangedView, error)) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.CharterChangedView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		st, err := h.charterTick(ctx, tx, s, lang)
		if err != nil {
			return err
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if !fresh {
			return nil
		}
		done, err = fn(&charterCtx{ctx: ctx, tx: tx, p: p, s: s, st: st, lang: lang, now: h.now(), meta: meta})
		return err
	})
	return h.charterAnswer(ctx, meta, lang, err, done)
}

func (h *VillageHandler) auditCharter(ctx context.Context, tx application.Tx, s application.FoundedSettlement, actor, action, office string, at time.Time, detail map[string]any) error {
	return tx.Charters().Audit(ctx, application.CharterAuditRow{ID: h.ids.NewID(), SettlementID: s.CityID, ActorID: actor,
		Action: action, OfficeID: office, At: at, Detail: detail})
}

// officeByID finds an open office of the state by id ("founder" for the unwritten one).
func (st charterState) officeByID(id string) (charter.Office, bool) {
	id = strings.TrimSpace(id)
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		if (id != "" && o.ID == id) || (id == FounderOfficeID && o.Acquisition == charter.AcquireHead) {
			return o, true
		}
	}
	return charter.Office{}, false
}

func (st charterState) headOffice() (charter.Office, bool) {
	for _, o := range st.offices {
		if o.Acquisition == charter.AcquireHead && !o.Closed {
			return o, true
		}
	}
	return charter.Office{}, false
}

// seatsHeld is how many seats of an office are filled right now.
func (st charterState) seatsHeld(o charter.Office) int {
	if o.Acquisition == charter.AcquireHead {
		if st.headHeld != "" {
			return 1
		}
		return 0
	}
	return len(st.seats[o.ID])
}

// --- housekeeping -------------------------------------------------------------------

// charterTick settles what is due and calls the elections that are owed. It is
// idempotent: every step is a conditional write.
func (h *VillageHandler) charterTick(ctx context.Context, tx application.Tx, s application.FoundedSettlement, lang string) (charterState, error) {
	now := h.now()
	st, err := h.loadCharter(ctx, tx, s, lang)
	if err != nil {
		return st, err
	}
	// 1. ballots whose time has come
	open, err := tx.Charters().OpenBallots(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	for i := range open {
		if !now.Before(open[i].ClosesAt) {
			if err := h.settleBallot(ctx, tx, s, &open[i], lang, now); err != nil {
				return st, err
			}
		}
	}
	// 2. terms that ended
	expired, err := tx.Charters().ExpiredSeats(ctx, s.CityID, now)
	if err != nil {
		return st, err
	}
	for _, seat := range expired {
		if ok, err := tx.Charters().EndSeat(ctx, seat.OfficeID, seat.HolderID, "term_ended", now); err != nil {
			return st, err
		} else if ok {
			if err := h.auditCharter(ctx, tx, s, "", "seat_term_ended", seat.OfficeID, now, map[string]any{"holder": seat.HolderID}); err != nil {
				return st, err
			}
		}
	}
	if st, err = h.loadCharter(ctx, tx, s, lang); err != nil {
		return st, err
	}
	// 3. elections that are owed: an elected office with a free seat, and the head seat
	recent, err := tx.Charters().RecentBallots(ctx, s.CityID, 60)
	if err != nil {
		return st, err
	}
	busy := map[string]bool{}
	retry := map[string]bool{}
	for _, b := range recent {
		if b.Kind != string(charter.BallotElection) {
			continue
		}
		if b.Status == string(charter.BallotOpen) {
			busy[b.OfficeID] = true
		}
		if b.Status == string(charter.BallotNoResult) && now.Before(b.SettledAt.Add(24*time.Hour)) {
			retry[b.OfficeID] = true
		}
	}
	changed := false
	for _, o := range st.offices {
		if o.Closed || (o.Acquisition != charter.AcquireElection && o.Acquisition != charter.AcquireHead) {
			continue
		}
		if st.seatsHeld(o) >= o.Seats {
			continue
		}
		if o.ID == "" { // the unwritten founder's office: write the charter down first
			if st, err = h.materialise(ctx, tx, s, st, "", lang); err != nil {
				return st, err
			}
			o, _ = st.headOffice()
		}
		if busy[o.ID] || retry[o.ID] {
			continue
		}
		if o.Acquisition == charter.AcquireElection && o.Seats > 0 && st.seatsHeld(o) == 0 && !st.stored {
			continue
		}
		if _, err := h.openElection(ctx, tx, s, o, "", now); err != nil {
			return st, err
		}
		changed = true
	}
	if changed {
		return h.loadCharter(ctx, tx, s, lang)
	}
	return st, nil
}

// openElection calls an election for an office; false when one is open already.
func (h *VillageHandler) openElection(ctx context.Context, tx application.Tx, s application.FoundedSettlement, o charter.Office, by string, now time.Time) (string, error) {
	open, err := tx.Charters().OpenBallots(ctx, s.CityID)
	if err != nil {
		return "", err
	}
	for _, b := range open {
		if b.Kind == string(charter.BallotElection) && b.OfficeID == o.ID {
			return "", nil
		}
	}
	set := h.cset()
	eligible, err := tx.Charters().Eligible(ctx, s.CityID, h.voterCut(now))
	if err != nil {
		return "", err
	}
	id := h.ids.NewID()
	closes := set.ElectionCloses(now)
	actionID, err := h.schedule(ctx, tx, application.CharterBallotActionType, application.CharterBallotReference, id, s.CityID, now, closes)
	if err != nil {
		return "", err
	}
	ok, err := tx.Charters().OpenBallot(ctx, application.CharterBallot{ID: id, SettlementID: s.CityID, Kind: string(charter.BallotElection),
		OfficeID: o.ID, OpenedBy: by, OpensAt: now, ClosesAt: closes, Eligible: eligible, ActionID: actionID})
	if err != nil || !ok {
		return "", err
	}
	if err := h.auditCharter(ctx, tx, s, by, "election_opened", o.ID, now, map[string]any{"title": o.Title, "ballot": id, "eligible": eligible}); err != nil {
		return "", err
	}
	return id, nil
}

// CharterBallotClose handles settlement.charter.ballot.close from the SCHEDULER: a
// ballot reaching its closing time. Exactly once: the settling update is conditional.
func (h *VillageHandler) CharterBallotClose(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		b, err := tx.Charters().Ballot(ctx, in.ID)
		if err != nil {
			return err
		}
		if b == nil || b.Status != string(charter.BallotOpen) {
			return nil
		}
		s, err := tx.Settlements().ByID(ctx, b.SettlementID)
		if err != nil {
			return err
		}
		if err := tx.Charters().Lock(ctx, s.CityID); err != nil {
			return err
		}
		if now := h.now(); now.Before(b.ClosesAt) {
			return nil
		}
		return h.settleBallot(ctx, tx, s, b, "", h.now())
	})
}

type proposalJSON struct {
	Op     string     `json:"op"`
	Office officeJSON `json:"office"`
	By     string     `json:"by"`
}

type officeJSON struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Seats       int          `json:"seats"`
	Grants      []grantJSON2 `json:"grants"`
	Acquisition string       `json:"acquisition"`
	TermDays    int          `json:"term_days"`
	Deputy      bool         `json:"deputy"`
}

type grantJSON2 struct {
	P string `json:"p"`
	L int64  `json:"l,omitempty"`
}

func officeToJSON(o charter.Office) officeJSON {
	out := officeJSON{ID: o.ID, Title: o.Title, Seats: o.Seats, Acquisition: string(o.Acquisition), TermDays: o.TermDays, Deputy: o.Deputy}
	for _, g := range o.Grants {
		out.Grants = append(out.Grants, grantJSON2{P: string(g.Permission), L: g.Limit})
	}
	return out
}

func (j officeJSON) office() charter.Office {
	o := charter.Office{ID: j.ID, Title: j.Title, Seats: j.Seats, Acquisition: charter.Acquisition(j.Acquisition), TermDays: j.TermDays, Deputy: j.Deputy}
	for _, g := range j.Grants {
		o.Grants = append(o.Grants, charter.Grant{Permission: charter.Permission(g.P), Limit: g.L})
	}
	return o
}

// settleBallot closes a ballot whose time has come and acts on its result. Idempotent.
func (h *VillageHandler) settleBallot(ctx context.Context, tx application.Tx, s application.FoundedSettlement, b *application.CharterBallot, lang string, now time.Time) error {
	set := h.cset()
	st, err := h.loadCharter(ctx, tx, s, lang)
	if err != nil {
		return err
	}
	finish := func(status charter.BallotStatus, result map[string]any, action string, detail map[string]any) error {
		raw, _ := json.Marshal(result)
		ok, err := tx.Charters().SettleBallot(ctx, b.ID, string(status), raw, now)
		if err != nil || !ok {
			return err
		}
		d := map[string]any{"ballot": b.ID, "status": string(status)}
		for k, v := range detail {
			d[k] = v
		}
		return h.auditCharter(ctx, tx, s, "", action, b.OfficeID, now, d)
	}
	switch charter.BallotKind(b.Kind) {
	case charter.BallotElection:
		o, ok := st.officeByIDExact(b.OfficeID)
		if !ok {
			return finish(charter.BallotCancelled, nil, "election_cancelled", nil)
		}
		free := o.Seats - st.seatsHeld(o)
		if free <= 0 {
			return finish(charter.BallotCancelled, nil, "election_cancelled", nil)
		}
		cands, err := tx.Charters().Candidates(ctx, b.ID)
		if err != nil {
			return err
		}
		tally, err := tx.Charters().Tally(ctx, b.ID)
		if err != nil {
			return err
		}
		var cs []election.Candidate
		names := map[string]application.CharterCandidate{}
		for _, c := range cands {
			names[c.PlayerID] = c
			cs = append(cs, election.Candidate{PlayerID: c.PlayerID, StoodAt: c.StoodAt, Votes: tally[c.PlayerID]})
		}
		res := election.Count(cs, free)
		if len(res.Elected) == 0 {
			return finish(charter.BallotNoResult, map[string]any{"cast": res.Cast}, "election_no_result", nil)
		}
		// settle first: the conditional update is the fence; seats follow in this transaction
		var winners []string
		for _, w := range res.Elected {
			winners = append(winners, w.PlayerID)
		}
		raw, _ := json.Marshal(map[string]any{"cast": res.Cast, "winners": winners})
		ok2, err := tx.Charters().SettleBallot(ctx, b.ID, string(charter.BallotPassed), raw, now)
		if err != nil || !ok2 {
			return err
		}
		term := time.Duration(set.ElectionTermDays) * 24 * time.Hour
		if o.TermDays > 0 {
			term = time.Duration(o.TermDays) * 24 * time.Hour
		}
		for _, w := range res.Elected {
			if o.Acquisition == charter.AcquireHead {
				if _, _, err := application.ElectToOffice(ctx, tx, officeFor(s.Tier), s.JurisdictionID, 1, w.PlayerID, now); err != nil {
					return h.auditCharter(ctx, tx, s, "", "election_blocked", o.ID, now, map[string]any{"ballot": b.ID, "winner": w.PlayerID, "why": err.Error()})
				}
			} else if _, err := tx.Charters().Seat(ctx, application.CharterSeat{ID: h.ids.NewID(), OfficeID: o.ID, HolderID: w.PlayerID, Since: now, TermEnds: now.Add(term)}); err != nil {
				return err
			}
			if err := h.auditCharter(ctx, tx, s, "", "seat_elected", o.ID, now, map[string]any{"ballot": b.ID, "holder": w.PlayerID, "votes": w.Votes, "title": o.Title}); err != nil {
				return err
			}
		}
		return h.auditCharter(ctx, tx, s, "", "election_settled", o.ID, now, map[string]any{"ballot": b.ID, "cast": res.Cast, "title": o.Title})

	case charter.BallotRecall:
		tally, err := tx.Charters().Tally(ctx, b.ID)
		if err != nil {
			return err
		}
		yes, no := tally["yes"], tally["no"]
		need := charter.Floor(b.Eligible, set.RecallMinSignatures)
		carried := charter.Carries(yes, no, need)
		status := charter.BallotFailed
		if carried {
			status = charter.BallotPassed
		}
		raw, _ := json.Marshal(map[string]any{"yes": yes, "no": no, "needed": need})
		ok, err := tx.Charters().SettleBallot(ctx, b.ID, string(status), raw, now)
		if err != nil || !ok {
			return err
		}
		if carried {
			o, found := st.officeByIDExact(b.OfficeID)
			if found && o.Acquisition == charter.AcquireHead {
				if st.headHeld == b.TargetPlayerID {
					if _, _, err := application.VacateOffice(ctx, tx, officeFor(s.Tier), s.JurisdictionID, 1, now); err != nil {
						return err
					}
				}
			} else if _, err := tx.Charters().EndSeat(ctx, b.OfficeID, b.TargetPlayerID, "recalled", now); err != nil {
				return err
			}
		}
		return h.auditCharter(ctx, tx, s, "", "recall_settled", b.OfficeID, now, map[string]any{
			"ballot": b.ID, "target": b.TargetPlayerID, "yes": yes, "no": no, "removed": carried})

	case charter.BallotAmendment:
		tally, err := tx.Charters().Tally(ctx, b.ID)
		if err != nil {
			return err
		}
		yes, no := tally["yes"], tally["no"]
		need := set.QuorumVotes(b.Eligible)
		raw, _ := json.Marshal(map[string]any{"yes": yes, "no": no, "needed": need})
		if !charter.Carries(yes, no, need) {
			ok, err := tx.Charters().SettleBallot(ctx, b.ID, string(charter.BallotFailed), raw, now)
			if err != nil || !ok {
				return err
			}
			return h.auditCharter(ctx, tx, s, "", "amendment_failed", b.OfficeID, now, map[string]any{"ballot": b.ID, "yes": yes, "no": no, "needed": need})
		}
		var prop proposalJSON
		if err := json.Unmarshal(b.Proposal, &prop); err != nil {
			return err
		}
		ok, err := tx.Charters().SettleBallot(ctx, b.ID, string(charter.BallotPassed), raw, now)
		if err != nil || !ok {
			return err
		}
		if err := h.applyAmendment(ctx, tx, s, st, prop, now); err != nil {
			// carried, but no longer allowed (the offices moved meanwhile): void, audited
			return h.auditCharter(ctx, tx, s, prop.By, "amendment_void", b.OfficeID, now, map[string]any{"ballot": b.ID, "why": err.Error()})
		}
		return h.auditCharter(ctx, tx, s, prop.By, "amendment_passed", b.OfficeID, now, map[string]any{
			"ballot": b.ID, "yes": yes, "no": no, "title": prop.Office.Title, "op": prop.Op})
	}
	return nil
}

func (st charterState) officeByIDExact(id string) (charter.Office, bool) {
	for _, o := range st.offices {
		if !o.Closed && o.ID == id && id != "" {
			return o, true
		}
	}
	return charter.Office{}, false
}

// applyAmendment writes a change the residents approved, after checking it against
// the rails as they stand now: the proposer must still hold charter.amend and every
// permission the change adds (rail R1), and the charter must keep an office manager (R2).
func (h *VillageHandler) applyAmendment(ctx context.Context, tx application.Tx, s application.FoundedSettlement, st charterState, prop proposalJSON, now time.Time) error {
	next := prop.Office.office()
	held := st.heldBy(prop.By)
	if _, ok := held.Has(charter.CharterAmend); !ok {
		return charter.ErrNotHeld
	}
	var prev *charter.Office
	if o, ok := st.officeByIDExact(next.ID); ok {
		prev = &o
	}
	if prev == nil {
		return charter.ErrNotHeld
	}
	after := make([]charter.OfficeState, 0, len(st.offices))
	switch prop.Op {
	case "close":
		if prev.Acquisition == charter.AcquireHead {
			return charter.ErrHeadOffice
		}
		for _, o := range st.offices {
			if o.ID == prev.ID {
				o.Closed = true
			}
			after = append(after, charter.OfficeState{Office: o, Held: st.held(o)})
		}
		if err := charter.ManagerGuard(after); err != nil {
			return err
		}
		if err := tx.Charters().EndSeatsOf(ctx, prev.ID, "office_closed", now); err != nil {
			return err
		}
		return tx.Charters().CloseOffice(ctx, prev.ID, now)
	default:
		if err := held.CanGrant(widened(prev.Grants, next.Grants)); err != nil {
			return err
		}
		for _, o := range st.offices {
			if o.ID == prev.ID {
				o = next
			}
			after = append(after, charter.OfficeState{Office: o, Held: st.held(o)})
		}
		if err := charter.ManagerGuard(after); err != nil {
			return err
		}
		return tx.Charters().SaveOffice(ctx, s.CityID, next, prop.By, now)
	}
}

// --- the acts ------------------------------------------------------------------------

// VillageBallotRequest is the payload of the voting commands.
type VillageBallotRequest struct {
	Ballot   string `json:"ballot,omitempty"`
	Choice   string `json:"choice,omitempty"`
	Office   string `json:"office,omitempty"`
	Player   string `json:"player,omitempty"`
	Petition string `json:"petition,omitempty"`
}

// CharterElectionOpen handles settlement.charter.election.open (election.call): call an
// election for an elected office that has a free seat (or for the head seat when it is vacant).
func (h *VillageHandler) CharterElectionOpen(ctx context.Context, meta envelope.Metadata, req VillageBallotRequest) (*presentation.Response, error) {
	return h.charterAct(ctx, meta, func(c *charterCtx) (*village.CharterChangedView, error) {
		if _, ok := c.st.heldBy(c.p.ID).Has(charter.ElectionCall); !ok {
			return nil, application.ErrNotOfficeHolder.WithDetail("office", officeFor(c.s.Tier)).WithDetail("permission", string(charter.ElectionCall))
		}
		o, ok := c.st.officeByID(req.Office)
		if !ok || (o.Acquisition != charter.AcquireElection && o.Acquisition != charter.AcquireHead) {
			return nil, refuseVillage(village.CharterNotElected, village.AddrVillageCharter)
		}
		if c.st.seatsHeld(o) >= o.Seats {
			return nil, refuseVillage(village.CharterNoVacancy, village.AddrVillageCharter)
		}
		var err error
		if o.ID == "" || o.ID == FounderOfficeID {
			if c.st, err = h.materialise(c.ctx, c.tx, c.s, c.st, c.p.ID, c.lang); err != nil {
				return nil, err
			}
			o, _ = c.st.headOffice()
		}
		id, err := h.openElection(c.ctx, c.tx, c.s, o, c.p.ID, c.now)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, refuseVillage(village.CharterElectionOpen, village.AddrVillageCharter)
		}
		return &village.CharterChangedView{Action: "election_opened", Title: o.Title, BallotID: id}, nil
	})
}

// CharterStand handles settlement.charter.stand: a resident puts their name on an
// election's ballot, while candidacy is open.
func (h *VillageHandler) CharterStand(ctx context.Context, meta envelope.Metadata, req VillageBallotRequest) (*presentation.Response, error) {
	return h.charterAct(ctx, meta, func(c *charterCtx) (*village.CharterChangedView, error) {
		b, err := h.openBallotOf(c.ctx, c, req.Ballot, charter.BallotElection)
		if err != nil {
			return nil, err
		}
		if h.cset().PhaseAt(b.OpensAt, c.now) != charter.PhaseCandidacy {
			return nil, refuseVillage(village.CharterNotCandidacy, village.AddrVillageCharter)
		}
		if ok, err := c.tx.Charters().IsEligible(c.ctx, c.s.CityID, c.p.ID, h.voterCut(b.OpensAt)); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterNotEligible, village.AddrVillageCharter)
		}
		if ok, err := c.tx.Charters().AddCandidate(c.ctx, b.ID, c.p.ID, c.now); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterAlreadyStanding, village.AddrVillageCharter)
		}
		o, _ := c.st.officeByIDExact(b.OfficeID)
		if err := h.auditCharter(c.ctx, c.tx, c.s, c.p.ID, "candidate_stood", b.OfficeID, c.now, map[string]any{"ballot": b.ID, "title": o.Title}); err != nil {
			return nil, err
		}
		return &village.CharterChangedView{Action: "candidate_stood", Title: o.Title, BallotID: b.ID}, nil
	})
}

// openBallotOf resolves an open ballot of the right kind in this settlement.
func (h *VillageHandler) openBallotOf(ctx context.Context, c *charterCtx, id string, kind charter.BallotKind) (*application.CharterBallot, error) {
	b, err := c.tx.Charters().Ballot(ctx, strings.TrimSpace(id))
	if err != nil {
		if stderrors.Is(err, application.ErrCityNotFound) {
			return nil, refuseVillage(village.CharterNoBallot, village.AddrVillageCharter)
		}
		return nil, err
	}
	if b == nil || b.SettlementID != c.s.CityID || b.Kind != string(kind) || b.Status != string(charter.BallotOpen) || !c.now.Before(b.ClosesAt) {
		return nil, refuseVillage(village.CharterNoBallot, village.AddrVillageCharter)
	}
	return b, nil
}

// CharterVote handles settlement.charter.vote: one secret, final vote on an election
// (choice: a candidate's public code) or a recall or amendment (choice: yes or no).
func (h *VillageHandler) CharterVote(ctx context.Context, meta envelope.Metadata, req VillageBallotRequest) (*presentation.Response, error) {
	return h.charterAct(ctx, meta, func(c *charterCtx) (*village.CharterChangedView, error) {
		b, err := c.tx.Charters().Ballot(c.ctx, strings.TrimSpace(req.Ballot))
		if err != nil {
			return nil, err
		}
		if b == nil || b.SettlementID != c.s.CityID || b.Status != string(charter.BallotOpen) || !c.now.Before(b.ClosesAt) {
			return nil, refuseVillage(village.CharterNoBallot, village.AddrVillageCharter)
		}
		set := h.cset()
		choice := strings.ToLower(strings.TrimSpace(req.Choice))
		switch charter.BallotKind(b.Kind) {
		case charter.BallotElection:
			if set.PhaseAt(b.OpensAt, c.now) != charter.PhaseVoting {
				return nil, refuseVillage(village.CharterNotVoting, village.AddrVillageCharter)
			}
			who, err := c.tx.Charters().ResidentByCode(c.ctx, c.s.CityID, strings.TrimSpace(req.Choice))
			if err != nil {
				return nil, err
			}
			cands, err := c.tx.Charters().Candidates(c.ctx, b.ID)
			if err != nil {
				return nil, err
			}
			choice = ""
			for _, cd := range cands {
				if who != nil && cd.PlayerID == who.ID {
					choice = cd.PlayerID
				}
			}
			if choice == "" {
				return nil, refuseVillage(village.CharterBadChoice, village.AddrVillageCharter)
			}
		default:
			if choice != "yes" && choice != "no" {
				return nil, refuseVillage(village.CharterBadChoice, village.AddrVillageCharter)
			}
			if b.TargetPlayerID == c.p.ID {
				return nil, refuseVillage(village.CharterTargetVoting, village.AddrVillageCharter)
			}
		}
		if ok, err := c.tx.Charters().IsEligible(c.ctx, c.s.CityID, c.p.ID, h.voterCut(b.OpensAt)); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterNotEligible, village.AddrVillageCharter)
		}
		if ok, err := c.tx.Charters().Cast(c.ctx, b.ID, c.p.ID, choice, c.now); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterAlreadyVoted, village.AddrVillageCharter)
		}
		// a vote is secret: the log says that someone voted, never how
		if err := h.auditCharter(c.ctx, c.tx, c.s, "", "vote_cast", b.OfficeID, c.now, map[string]any{"ballot": b.ID, "kind": b.Kind}); err != nil {
			return nil, err
		}
		return &village.CharterChangedView{Action: "vote_cast", BallotID: b.ID}, nil
	})
}

// holderSince finds when a player's seat in an office began, and whether they hold it.
func (c *charterCtx) holderSince(o charter.Office, player string) (time.Time, bool) {
	if o.Acquisition == charter.AcquireHead {
		if c.st.headHeld == player {
			return c.st.headSince, true
		}
		return time.Time{}, false
	}
	for _, seat := range c.st.seats[o.ID] {
		if seat.HolderID == player {
			return seat.Since, true
		}
	}
	return time.Time{}, false
}

// CharterRecallStart handles settlement.charter.recall.start: a resident opens a
// petition to recall one holder, after the holder's first days. The starter's own
// signature counts.
func (h *VillageHandler) CharterRecallStart(ctx context.Context, meta envelope.Metadata, req VillageBallotRequest) (*presentation.Response, error) {
	return h.charterAct(ctx, meta, func(c *charterCtx) (*village.CharterChangedView, error) {
		set := h.cset()
		o, ok := c.st.officeByID(req.Office)
		if !ok {
			return nil, refuseVillage(village.VillageNotFound, village.AddrVillageCharter)
		}
		if o.ID == FounderOfficeID || o.ID == "" {
			var err error
			if c.st, err = h.materialise(c.ctx, c.tx, c.s, c.st, c.p.ID, c.lang); err != nil {
				return nil, err
			}
			o, _ = c.st.headOffice()
		}
		who, err := c.tx.Charters().ResidentByCode(c.ctx, c.s.CityID, strings.TrimSpace(req.Player))
		if err != nil {
			return nil, err
		}
		if who == nil {
			// the holder may not live here (an appointed officer): find them by the seat
			who = h.holderByCode(c.ctx, c, o, req.Player)
			if who == nil {
				return nil, refuseVillage(village.CharterNotHolder, village.AddrVillageCharter)
			}
		}
		since, holds := c.holderSince(o, who.ID)
		if !holds {
			return nil, refuseVillage(village.CharterNotHolder, village.AddrVillageCharter)
		}
		if who.ID == c.p.ID {
			return nil, refuseVillage(village.CharterRecallSelf, village.AddrVillageCharter)
		}
		if ok, err := c.tx.Charters().IsEligible(c.ctx, c.s.CityID, c.p.ID, h.voterCut(c.now)); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterNotEligible, village.AddrVillageCharter)
		}
		if !set.RecallTenureOK(since, c.now) {
			return nil, refuseVillage(village.CharterRecallTooEarly, village.AddrVillageCharter)
		}
		if last, err := c.tx.Charters().LastRecall(c.ctx, o.ID, who.ID); err != nil {
			return nil, err
		} else if !last.IsZero() && c.now.Before(set.RecallCoolingUntil(last)) {
			return nil, refuseVillage(village.CharterRecallCooling, village.AddrVillageCharter)
		}
		pid := h.ids.NewID()
		if ok, err := c.tx.Charters().OpenPetition(c.ctx, application.CharterPetition{ID: pid, SettlementID: c.s.CityID, OfficeID: o.ID,
			TargetPlayerID: who.ID, StartedBy: c.p.ID, CreatedAt: c.now}); err != nil {
			return nil, err
		} else if !ok {
			return nil, refuseVillage(village.CharterRecallOpen, village.AddrVillageCharter)
		}
		if err := h.auditCharter(c.ctx, c.tx, c.s, c.p.ID, "recall_petition_started", o.ID, c.now, map[string]any{"petition": pid, "target": who.ID, "title": o.Title}); err != nil {
			return nil, err
		}
		pet, err := c.tx.Charters().Petition(c.ctx, pid)
		if err != nil {
			return nil, err
		}
		id, err := h.signPetition(c.ctx, c, pet)
		if err != nil {
			return nil, err
		}
		return &village.CharterChangedView{Action: "recall_petition_started", Title: o.Title, BallotID: id}, nil
	})
}

// holderByCode finds a seat holder of an office by public code (an officer who does not
// live in the settlement).
func (h *VillageHandler) holderByCode(ctx context.Context, c *charterCtx, o charter.Office, code string) *application.CharterPerson {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, seat := range c.st.seats[o.ID] {
		if p, err := c.tx.Players().GetByID(ctx, seat.HolderID); err == nil && strings.ToUpper(p.PublicCode) == code {
			return &application.CharterPerson{ID: p.ID, Name: p.DisplayName, Code: p.PublicCode}
		}
	}
	return nil
}

// signPetition adds the player's signature and opens the recall vote when the petition
// has enough. It returns the ballot's id when it opened one.
func (h *VillageHandler) signPetition(ctx context.Context, c *charterCtx, pet *application.CharterPetition) (string, error) {
	if pet.TargetPlayerID == c.p.ID {
		return "", refuseVillage(village.CharterRecallSelf, village.AddrVillageCharter)
	}
	if ok, err := c.tx.Charters().IsEligible(ctx, c.s.CityID, c.p.ID, h.voterCut(pet.CreatedAt)); err != nil {
		return "", err
	} else if !ok {
		return "", refuseVillage(village.CharterNotEligible, village.AddrVillageCharter)
	}
	if _, err := c.tx.Charters().Sign(ctx, pet.ID, c.p.ID, c.now); err != nil {
		return "", err
	}
	fresh, err := c.tx.Charters().Petition(ctx, pet.ID)
	if err != nil {
		return "", err
	}
	eligible, err := c.tx.Charters().Eligible(ctx, c.s.CityID, h.voterCut(fresh.CreatedAt))
	if err != nil {
		return "", err
	}
	if fresh.Signatures < h.cset().RecallSignatures(eligible) {
		return "", nil
	}
	set := h.cset()
	id := h.ids.NewID()
	closes := c.now.Add(time.Duration(set.RecallVoteHours) * time.Hour)
	actionID, err := h.schedule(ctx, c.tx, application.CharterBallotActionType, application.CharterBallotReference, id, c.s.CityID, c.now, closes)
	if err != nil {
		return "", err
	}
	ok, err := c.tx.Charters().OpenBallot(ctx, application.CharterBallot{ID: id, SettlementID: c.s.CityID, Kind: string(charter.BallotRecall),
		OfficeID: fresh.OfficeID, TargetPlayerID: fresh.TargetPlayerID, OpenedBy: fresh.StartedBy, OpensAt: c.now, ClosesAt: closes, Eligible: eligible, ActionID: actionID})
	if err != nil || !ok {
		return "", err
	}
	if err := c.tx.Charters().SetPetition(ctx, fresh.ID, "voted", id); err != nil {
		return "", err
	}
	if err := h.auditCharter(ctx, c.tx, c.s, "", "recall_vote_opened", fresh.OfficeID, c.now, map[string]any{
		"ballot": id, "petition": fresh.ID, "target": fresh.TargetPlayerID, "signatures": fresh.Signatures, "eligible": eligible}); err != nil {
		return "", err
	}
	return id, nil
}

// CharterRecallSign handles settlement.charter.recall.sign: a resident signs a recall
// petition; the signature that reaches the threshold opens the vote.
func (h *VillageHandler) CharterRecallSign(ctx context.Context, meta envelope.Metadata, req VillageBallotRequest) (*presentation.Response, error) {
	return h.charterAct(ctx, meta, func(c *charterCtx) (*village.CharterChangedView, error) {
		pet, err := c.tx.Charters().Petition(c.ctx, strings.TrimSpace(req.Petition))
		if err != nil {
			return nil, err
		}
		if pet == nil || pet.SettlementID != c.s.CityID || pet.Status != "open" {
			return nil, refuseVillage(village.CharterNoBallot, village.AddrVillageCharter)
		}
		id, err := h.signPetition(c.ctx, c, pet)
		if err != nil {
			return nil, err
		}
		o, _ := c.st.officeByIDExact(pet.OfficeID)
		return &village.CharterChangedView{Action: "recall_signed", Title: o.Title, BallotID: id}, nil
	})
}

// charterPhase2View adds the elections, recall petitions, amendment votes and the
// acting head to the charter view, from the viewer's side.
func (h *VillageHandler) charterPhase2View(ctx context.Context, tx application.Tx, s application.FoundedSettlement, st charterState,
	viewerID string, held charter.Held, v *village.CharterView,
) error {
	set := h.cset()
	now := h.now()
	v.Rules = village.CharterRulesView{ElectionTermDays: set.ElectionTermDays, CandidacyHours: set.CandidacyHours, VotingHours: set.VotingHours,
		RecallMinTenureDays: set.RecallMinTenureDays, RecallSignatureBPS: set.RecallSignatureBPS, RecallMinSignatures: set.RecallMinSignatures,
		RecallVoteHours: set.RecallVoteHours, AmendVoteHours: set.AmendVoteHours, ActingDays: set.ActingDays}
	_, v.CanCallElection = held.Has(charter.ElectionCall)
	v.HeadVacant = st.headHeld == ""
	ids := map[string]bool{}
	if st.acting != nil {
		ids[st.acting.Player] = true
	}
	ballots, err := tx.Charters().RecentBallots(ctx, s.CityID, 12)
	if err != nil {
		return err
	}
	petitions, err := tx.Charters().OpenPetitions(ctx, s.CityID)
	if err != nil {
		return err
	}
	for _, b := range ballots {
		if b.TargetPlayerID != "" {
			ids[b.TargetPlayerID] = true
		}
	}
	for _, p := range petitions {
		ids[p.TargetPlayerID] = true
	}
	results := map[string]map[string]any{}
	for _, b := range ballots {
		if len(b.Result) > 0 {
			var r map[string]any
			if json.Unmarshal(b.Result, &r) == nil {
				results[b.ID] = r
				if ws, ok := r["winners"].([]any); ok {
					for _, w := range ws {
						if id, ok := w.(string); ok {
							ids[id] = true
						}
					}
				}
			}
		}
	}
	people, err := h.peopleOf(ctx, tx, ids)
	if err != nil {
		return err
	}
	cut := h.voterCut(now)
	voter, err := tx.Charters().IsEligible(ctx, s.CityID, viewerID, cut)
	if err != nil {
		return err
	}
	titles := map[string]string{}
	for _, o := range st.offices {
		titles[o.ID] = o.Title
	}
	if st.acting != nil {
		a := &village.CharterActingView{Player: people[st.acting.Player], Ends: st.acting.Ends}
		a.Office = titles[st.acting.OfficeID]
		for _, g := range st.acting.Grants {
			if g.Permission == charter.TreasurySpend {
				a.SpendCap = g.Limit
			}
		}
		v.Acting = a
	}
	for _, b := range ballots {
		bv := village.CharterBallotView{ID: b.ID, Kind: b.Kind, OfficeID: b.OfficeID, Office: titles[b.OfficeID], Status: b.Status,
			OpensAt: b.OpensAt, ClosesAt: b.ClosesAt, Eligible: b.Eligible}
		settled := b.Status != string(charter.BallotOpen)
		open := !settled && now.Before(b.ClosesAt)
		bv.Phase = string(charter.PhaseClosed)
		if b.TargetPlayerID != "" {
			t := people[b.TargetPlayerID]
			bv.Target = &t
		}
		voted, err := tx.Charters().HasVoted(ctx, b.ID, viewerID)
		if err != nil {
			return err
		}
		bv.Voted = voted
		elig := voter
		if vc := h.voterCut(b.OpensAt); vc.Before(cut) {
			if elig, err = tx.Charters().IsEligible(ctx, s.CityID, viewerID, vc); err != nil {
				return err
			}
		}
		switch charter.BallotKind(b.Kind) {
		case charter.BallotElection:
			if open {
				bv.Phase = string(set.PhaseAt(b.OpensAt, now))
			}
			ce := b.OpensAt.Add(time.Duration(set.CandidacyHours) * time.Hour)
			bv.CandidacyEnds = &ce
			cands, err := tx.Charters().Candidates(ctx, b.ID)
			if err != nil {
				return err
			}
			var tally map[string]int64
			if settled {
				if tally, err = tx.Charters().Tally(ctx, b.ID); err != nil {
					return err
				}
			}
			for _, c := range cands {
				cv := village.CharterCandidateView{Name: c.Name, Code: c.Code}
				if settled {
					n := tally[c.PlayerID]
					cv.Votes = &n
				}
				if c.PlayerID == viewerID {
					bv.Standing = true
				}
				bv.Candidates = append(bv.Candidates, cv)
			}
			bv.CanStand = open && bv.Phase == string(charter.PhaseCandidacy) && elig && !bv.Standing
			bv.CanVote = open && bv.Phase == string(charter.PhaseVoting) && elig && !voted && len(cands) > 0
			if ws, ok := results[b.ID]["winners"].([]any); ok {
				for _, w := range ws {
					if id, ok := w.(string); ok {
						bv.Winners = append(bv.Winners, people[id])
					}
				}
			}
		case charter.BallotRecall, charter.BallotAmendment:
			if open {
				bv.Phase = string(charter.PhaseVoting)
			}
			bv.Needed = charter.Floor(b.Eligible, set.RecallMinSignatures)
			if b.Kind == string(charter.BallotAmendment) {
				bv.Needed = set.QuorumVotes(b.Eligible)
				var prop proposalJSON
				if json.Unmarshal(b.Proposal, &prop) == nil {
					pv := &village.CharterProposalView{Op: prop.Op, Title: prop.Office.Title, Seats: prop.Office.Seats,
						Acquisition: prop.Office.Acquisition, Deputy: prop.Office.Deputy}
					for _, g := range prop.Office.Grants {
						pv.Grants = append(pv.Grants, village.CharterGrantView{Permission: g.P, Limit: g.L})
					}
					bv.Proposal = pv
				}
			}
			bv.CanVote = open && elig && !voted && b.TargetPlayerID != viewerID
			if settled {
				if r := results[b.ID]; r != nil {
					y, _ := r["yes"].(float64)
					n, _ := r["no"].(float64)
					yy, nn := int64(y), int64(n)
					bv.Yes, bv.No = &yy, &nn
				}
			}
		}
		v.Ballots = append(v.Ballots, bv)
	}
	for _, p := range petitions {
		eligible, err := tx.Charters().Eligible(ctx, s.CityID, h.voterCut(p.CreatedAt))
		if err != nil {
			return err
		}
		pv := village.CharterPetitionView{ID: p.ID, OfficeID: p.OfficeID, Office: titles[p.OfficeID], Target: people[p.TargetPlayerID],
			Signatures: p.Signatures, Needed: set.RecallSignatures(eligible)}
		pv.CanSign = voter && p.TargetPlayerID != viewerID
		v.Petitions = append(v.Petitions, pv)
	}
	// per office: the earliest end of a held term, and whether the viewer may start a recall
	for i := range v.Offices {
		ov := &v.Offices[i]
		for _, o := range st.offices {
			oid := o.ID
			if oid == "" {
				oid = FounderOfficeID
			}
			if oid != ov.ID {
				continue
			}
			for _, seat := range st.seats[o.ID] {
				if !seat.TermEnds.IsZero() && (ov.TermEnds == nil || seat.TermEnds.Before(*ov.TermEnds)) {
					t := seat.TermEnds
					ov.TermEnds = &t
				}
			}
			ov.CanRecall = voter && len(ov.Holders) > 0
		}
	}
	return nil
}
