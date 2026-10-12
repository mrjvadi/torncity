package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The charter of a union (docs/adr/0076 4.6): its offices are the same kind of object the settlements write, under the same rails: nobody
// grants what they do not hold (R1), there is always an office manager who is held (R2), every act is audited (R3), the caps hold (R4).
// A union adds the delegation ceiling (D1). A holder can be recalled by the residents of the members.

type unionCharter struct {
	u       *application.Union
	offices []charter.Office
	seats   map[string][]application.CharterSeat
}

func (h *VillageHandler) loadUnionCharter(ctx context.Context, tx application.Tx, u *application.Union) (unionCharter, error) {
	offices, err := tx.Unions().Offices(ctx, u.ID)
	if err != nil {
		return unionCharter{}, err
	}
	seats, err := tx.Unions().Seats(ctx, u.ID)
	if err != nil {
		return unionCharter{}, err
	}
	c := unionCharter{u: u, offices: offices, seats: map[string][]application.CharterSeat{}}
	for _, s := range seats {
		c.seats[s.OfficeID] = append(c.seats[s.OfficeID], s)
	}
	return c, nil
}

// guard is rail R2 over the offices as they would stand: at least one manager office, held.
func (c unionCharter) guard(offices []charter.Office, seats map[string][]application.CharterSeat) error {
	states := make([]charter.OfficeState, 0, len(offices))
	for _, o := range offices {
		states = append(states, charter.OfficeState{Office: o, Held: len(seats[o.ID]) > 0})
	}
	return charter.ManagerGuard(states)
}

func (h *VillageHandler) unionOf(ctx context.Context, tx application.Tx, id string) (*application.Union, error) {
	u, err := tx.Unions().ByID(ctx, strings.TrimSpace(id), true)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != "active" {
		return nil, refuseVillage(village.UnionNoUnion, village.AddrUnion)
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

func (h *VillageHandler) unionOfficeSave(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	creating := strings.TrimSpace(req.Office) == ""
	need := charter.OfficeEdit
	if creating {
		need = charter.OfficeCreate
	}
	if _, err := h.requireUnionPower(ctx, tx, u, p.ID, need); err != nil {
		return err
	}
	held, err := tx.Unions().HeldIn(ctx, u.ID, p.ID)
	if err != nil {
		return err
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return err
	}
	lim := h.limits()
	var prev *charter.Office
	var others []string
	for i := range c.offices {
		if c.offices[i].ID == strings.TrimSpace(req.Office) {
			prev = &c.offices[i]
		} else {
			others = append(others, c.offices[i].Title)
		}
	}
	if !creating && prev == nil {
		return refuseVillage(village.UnionNoOffice, village.AddrUnion)
	}
	title, err := charter.ValidateTitle(req.Title, others, lim)
	if err != nil {
		return refuseVillage(village.UnionTitle, village.AddrUnion)
	}
	grants, err := charter.NormaliseGrants(grantsOf(req.Grants))
	if err != nil {
		return refuseCharter(err)
	}
	if err := polity.Ceiling(u.Delegations, grants); err != nil {
		return refuseVillage(village.UnionCeiling, village.AddrUnion)
	}
	// R1: only what is added or raised must be held by the one who saves
	var wanted []charter.Grant
	old := map[charter.Permission]int64{}
	if prev != nil {
		for _, g := range prev.Grants {
			old[g.Permission] = g.Limit
		}
	}
	for _, g := range grants {
		if l, ok := old[g.Permission]; !ok || !charter.Covers(l, g.Limit) {
			wanted = append(wanted, g)
		}
	}
	if err := held.CanGrant(wanted); err != nil {
		return refuseCharter(err)
	}
	seats := int(req.Seats)
	if seats == 0 {
		seats = 1
	}
	acq := charter.Acquisition(strings.ToLower(strings.TrimSpace(req.Acquisition)))
	if acq == "" {
		acq = charter.AcquireAppointment
	}
	if acq != charter.AcquireAppointment {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	o := charter.Office{ID: h.ids.NewID(), Title: title, Seats: seats, Grants: grants, Acquisition: acq, TermDays: int(req.TermDays)}
	open := len(c.offices)
	if prev != nil {
		o.ID = prev.ID
	} else {
		open++
	}
	if err := charter.ValidateOffice(o, open, lim); err != nil {
		return refuseCharter(err)
	}
	after := make([]charter.Office, 0, len(c.offices)+1)
	for _, x := range c.offices {
		if x.ID != o.ID {
			after = append(after, x)
		}
	}
	after = append(after, o)
	if err := c.guard(after, c.seats); err != nil {
		return refuseCharter(err)
	}
	if err := tx.Unions().SaveOffice(ctx, u.ID, o, p.ID, now); err != nil {
		return err
	}
	action := "office_created"
	if !creating {
		action = "office_changed"
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, action, o.ID, now, map[string]any{"title": title, "seats": seats, "grants": len(grants)})
}

func (h *VillageHandler) unionOfficeClose(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.OfficeEdit); err != nil {
		return err
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return err
	}
	var target *charter.Office
	var rest []charter.Office
	for i := range c.offices {
		if c.offices[i].ID == strings.TrimSpace(req.Office) {
			target = &c.offices[i]
		} else {
			rest = append(rest, c.offices[i])
		}
	}
	if target == nil {
		return refuseVillage(village.UnionNoOffice, village.AddrUnion)
	}
	if err := c.guard(rest, c.seats); err != nil {
		return refuseCharter(err)
	}
	if err := tx.Unions().EndSeatsOf(ctx, target.ID, "office_closed", now); err != nil {
		return err
	}
	if err := tx.Unions().CloseOffice(ctx, target.ID, now); err != nil {
		return err
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, "office_closed", target.ID, now, map[string]any{"title": target.Title})
}

func (h *VillageHandler) unionAppoint(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.OfficeAppoint); err != nil {
		return err
	}
	held, err := tx.Unions().HeldIn(ctx, u.ID, p.ID)
	if err != nil {
		return err
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return err
	}
	var o *charter.Office
	for i := range c.offices {
		if c.offices[i].ID == strings.TrimSpace(req.Office) {
			o = &c.offices[i]
		}
	}
	if o == nil {
		return refuseVillage(village.UnionNoOffice, village.AddrUnion)
	}
	// nobody puts a person into powers they do not hold themselves (R1)
	if err := held.CanGrant(o.Grants); err != nil {
		return refuseCharter(err)
	}
	if len(c.seats[o.ID]) >= o.Seats {
		return refuseVillage(village.UnionSeatsFull, village.AddrUnion)
	}
	target, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(req.Player))
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.UnionNoPlayer, village.AddrUnion)
	}
	fresh, err := tx.Unions().Seat(ctx, application.CharterSeat{ID: h.ids.NewID(), OfficeID: o.ID, HolderID: target, AppointedBy: p.ID, Since: now})
	if err != nil {
		return err
	}
	if !fresh {
		return refuseVillage(village.UnionSeatsFull, village.AddrUnion)
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, "seat_filled", o.ID, now, map[string]any{"title": o.Title, "holder": target})
}

func (h *VillageHandler) unionDismiss(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.OfficeDismiss); err != nil {
		return err
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return err
	}
	target, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(req.Player))
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.UnionNoPlayer, village.AddrUnion)
	}
	var o *charter.Office
	for i := range c.offices {
		if c.offices[i].ID == strings.TrimSpace(req.Office) {
			o = &c.offices[i]
		}
	}
	if o == nil {
		return refuseVillage(village.UnionNoOffice, village.AddrUnion)
	}
	left := map[string][]application.CharterSeat{}
	for id, ss := range c.seats {
		for _, s := range ss {
			if id == o.ID && s.HolderID == target {
				continue
			}
			left[id] = append(left[id], s)
		}
	}
	if err := c.guard(c.offices, left); err != nil {
		return refuseCharter(err)
	}
	if ok, err := tx.Unions().EndSeat(ctx, o.ID, target, "dismissed", now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.UnionNotHeld, village.AddrUnion)
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, "seat_dismissed", o.ID, now, map[string]any{"title": o.Title, "holder": target})
}

func (h *VillageHandler) unionRules(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.CharterAmend); err != nil {
		return err
	}
	fixed, share := atoi64(req.DuesFixed), atoi64(req.DuesShare)
	if fixed < 0 || share < 0 || share > h.polity.MaxDuesBPS {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	// dues above the bound that the members ratified would bind them to more than they signed
	if share > u.DuesShareBPS && polity.NeedsAssembly(nil, share, h.polity.AssemblyDuesBoundBPS) {
		return refuseVillage(village.UnionCeiling, village.AddrUnion)
	}
	u.DuesFixed, u.DuesShareBPS = fixed, share
	if err := tx.Unions().SaveRules(ctx, *u); err != nil {
		return err
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, "dues_changed", "", now, map[string]any{"fixed": fixed, "share_bps": share})
}

// ---- recall -------------------------------------------------------------------------------------------------------------------------

// unionRecall starts a recall petition against a holder, or signs the one that stands. Enough signatures put the holder to a vote of the
// residents of all the members.
func (h *VillageHandler) unionRecall(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) (string, error) {
	u, err := h.unionOf(ctx, tx, req.ID)
	if err != nil {
		return "", err
	}
	home, err := tx.Unions().ResidenceOf(ctx, p.ID)
	if err != nil {
		return "", err
	}
	m, err := tx.Unions().MemberOf(ctx, u.ID, home)
	if err != nil || home == "" || m == nil || !m.Active() {
		if err != nil {
			return "", err
		}
		return "", refuseVillage(village.UnionNotEligible, village.AddrUnion)
	}
	if ok, err := tx.Charters().IsEligible(ctx, home, p.ID, h.voterCut(now)); err != nil {
		return "", err
	} else if !ok {
		return "", refuseVillage(village.UnionNotEligible, village.AddrUnion)
	}
	target, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(req.Player))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuseVillage(village.UnionNoPlayer, village.AddrUnion)
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return "", err
	}
	var office *charter.Office
	var seat *application.CharterSeat
	for i := range c.offices {
		if c.offices[i].ID != strings.TrimSpace(req.Office) {
			continue
		}
		office = &c.offices[i]
		for j := range c.seats[office.ID] {
			if c.seats[office.ID][j].HolderID == target {
				seat = &c.seats[office.ID][j]
			}
		}
	}
	if office == nil || seat == nil {
		return "", refuseVillage(village.UnionNotHeld, village.AddrUnion)
	}
	set := h.cset()
	if !set.RecallTenureOK(seat.Since, now) {
		return "", refuseVillage(village.UnionTenure, village.AddrUnion)
	}
	b, err := tx.Unions().RecallBallotOf(ctx, office.ID, target)
	if err != nil {
		return "", err
	}
	if b == nil {
		eligible, err := h.unionElectorate(ctx, tx, u.ID, now)
		if err != nil {
			return "", err
		}
		nb := application.UnionBallot{ID: h.ids.NewID(), Kind: "recall", UnionID: u.ID, OfficeID: office.ID, TargetID: target, OpenedBy: p.ID, OpensAt: now,
			ClosesAt: now.Add(time.Duration(set.RecallVoteHours) * time.Hour), Eligible: eligible, Status: "petition"}
		if ok, err := tx.Unions().InsertBallot(ctx, nb); err != nil {
			return "", err
		} else if !ok {
			return "", refuseVillage(village.UnionDecided, village.AddrUnion)
		}
		b = &nb
		if err := h.auditUnion(ctx, tx, u.ID, p.ID, "recall_petition", office.ID, now, map[string]any{"target": target, "eligible": eligible}); err != nil {
			return "", err
		}
	}
	if b.Status == "open" {
		return "", refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	if _, err := tx.Unions().Sign(ctx, b.ID, p.ID, now); err != nil {
		return "", err
	}
	n, err := tx.Unions().Signatures(ctx, b.ID)
	if err != nil {
		return "", err
	}
	if n >= int64(set.RecallSignatures(b.Eligible)) {
		closes := now.Add(time.Duration(set.RecallVoteHours) * time.Hour)
		if ok, err := tx.Unions().OpenRecall(ctx, b.ID, now, closes); err != nil {
			return "", err
		} else if ok {
			if err := h.schedulePolity(ctx, tx, application.TickBallot, application.UnionReference, b.ID, "", p.ID, now, closes); err != nil {
				return "", err
			}
			if err := h.auditUnion(ctx, tx, u.ID, p.ID, "recall_vote_opened", office.ID, now, map[string]any{"target": target, "signatures": n}); err != nil {
				return "", err
			}
		}
		return village.UnionNoticeRecall, nil
	}
	return village.UnionNoticeSigned, nil
}

// applyRecall acts on a counted recall ballot. A recall that carried takes the holder out of the office, unless that would leave the union
// with no held manager: then it is void, and the holder stays until a successor is named (rail R2 wins over the vote).
func (h *VillageHandler) applyRecall(ctx context.Context, tx application.Tx, meta envelope.Metadata, b *application.UnionBallot, passed bool, yes, no int64, now time.Time) error {
	if !passed {
		return h.auditUnion(ctx, tx, b.UnionID, "", "recall_failed", b.OfficeID, now, map[string]any{"target": b.TargetID, "yes": yes, "no": no})
	}
	u, err := tx.Unions().ByID(ctx, b.UnionID, true)
	if err != nil || u == nil {
		return err
	}
	c, err := h.loadUnionCharter(ctx, tx, u)
	if err != nil {
		return err
	}
	left := map[string][]application.CharterSeat{}
	for id, ss := range c.seats {
		for _, s := range ss {
			if id == b.OfficeID && s.HolderID == b.TargetID {
				continue
			}
			left[id] = append(left[id], s)
		}
	}
	if err := c.guard(c.offices, left); err != nil {
		res, _ := json.Marshal(map[string]any{"yes": yes, "no": no, "void": "last_manager"})
		_, _ = tx.Unions().SetBallot(ctx, b.ID, "passed", "void", res, now)
		return h.auditUnion(ctx, tx, b.UnionID, "", "recall_void", b.OfficeID, now, map[string]any{"target": b.TargetID, "why": "last_manager"})
	}
	if _, err := tx.Unions().EndSeat(ctx, b.OfficeID, b.TargetID, "recalled", now); err != nil {
		return err
	}
	return h.auditUnion(ctx, tx, b.UnionID, "", "recalled", b.OfficeID, now, map[string]any{"target": b.TargetID, "yes": yes, "no": no})
}
