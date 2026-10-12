package handlers

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Members of a union: leaving after a notice, the motions the members decide (admit, expel, dissolve, consent to an exit), joining
// later, the dues each period, and the purse paid out when a member goes or the union ends (docs/adr/0076 4.4 and 4.5). Lots, buildings,
// items and personal accounts never move: only the member's share of the purse and, for a country, its place in the tree.

func (h *VillageHandler) memberRules(u *application.Union, members []application.UnionMember) (polity.Rules, []polity.Member, error) {
	return polity.Rules{Mode: polity.Mode(u.VoteMode), WeightCapBPS: h.polity.WeightCapBPS, MembersBPS: h.polity.MembersMajorityBPS, PopulationBPS: h.polity.PopulationMajorityBPS}, nil, nil
}

// pollMembers lists the members that vote (the active ones, minus any excluded) with their residents.
func (h *VillageHandler) pollMembers(ctx context.Context, tx application.Tx, unionID string, exclude string) ([]polity.Member, error) {
	ms, err := tx.Unions().Members(ctx, unionID)
	if err != nil {
		return nil, err
	}
	var out []polity.Member
	for _, m := range ms {
		if !m.Active() || m.SettlementID == exclude {
			continue
		}
		n, err := tx.Settlements().ResidentCount(ctx, m.SettlementID)
		if err != nil {
			return nil, err
		}
		out = append(out, polity.Member{ID: m.SettlementID, Residents: n})
	}
	return out, nil
}

// ---- leaving ----------------------------------------------------------------------------------------------------------------------

func (h *VillageHandler) unionLeave(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, req VillageUnionRequest, now time.Time) error {
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.UnionRatify); err != nil {
		return err
	}
	u, err := tx.Unions().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if u == nil || u.Status != "active" {
		return refuseVillage(village.UnionNoUnion, village.AddrUnion)
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return err
	}
	m, err := tx.Unions().MemberOf(ctx, u.ID, s.CityID)
	if err != nil {
		return err
	}
	if m == nil || !m.Active() {
		return refuseVillage(village.UnionNotMember, village.AddrUnion)
	}
	if m.Status == "notice" {
		return refuseVillage(village.UnionNoticeRunning, village.AddrUnion)
	}
	if u.ExitRule == "consent" {
		// the other members must agree; the notice runs after they do
		return h.openMotion(ctx, tx, meta, u, "exit", s.CityID, p.ID, nil, now)
	}
	return h.beginNotice(ctx, tx, meta, u, s.CityID, p.ID, now)
}

func (h *VillageHandler) beginNotice(ctx context.Context, tx application.Tx, meta envelope.Metadata, u *application.Union, settlementID, actor string, now time.Time) error {
	if ok, err := tx.Unions().GiveNotice(ctx, u.ID, settlementID, now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.UnionNoticeRunning, village.AddrUnion)
	}
	at := polity.ExitAt(now, h.polity.ExitNoticeDays)
	if err := h.schedulePolity(ctx, tx, application.TickExit, application.UnionReference, u.ID, settlementID, actor, now, at); err != nil {
		return err
	}
	return h.auditUnion(ctx, tx, u.ID, actor, "notice_given", "", now, map[string]any{"settlement": settlementID, "ends": at})
}

func (h *VillageHandler) unionStay(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, req VillageUnionRequest, now time.Time) error {
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.UnionRatify); err != nil {
		return err
	}
	u, err := tx.Unions().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || u == nil {
		return refuseVillage(village.UnionNoUnion, village.AddrUnion)
	}
	if ok, err := tx.Unions().SetMember(ctx, u.ID, s.CityID, "notice", "active", now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.UnionNotMember, village.AddrUnion)
	}
	return h.auditUnion(ctx, tx, u.ID, p.ID, "notice_withdrawn", "", now, map[string]any{"settlement": s.CityID})
}

// tickExit lets a member out when its notice has run: its share of the purse, its place in the tree. A notice withdrawn meanwhile leaves
// the member where it is.
func (h *VillageHandler) tickExit(ctx context.Context, tx application.Tx, meta envelope.Metadata, unionID, settlementID string, now time.Time) error {
	u, err := tx.Unions().ByID(ctx, unionID, true)
	if err != nil || u == nil || u.Status != "active" {
		return err
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return err
	}
	m, err := tx.Unions().MemberOf(ctx, u.ID, settlementID)
	if err != nil || m == nil || m.Status != "notice" || m.NoticeAt == nil {
		return err
	}
	if !polity.ExitReady(*m.NoticeAt, h.polity.ExitNoticeDays, now) {
		return nil
	}
	return h.removeMember(ctx, tx, meta, u, m, "left", "exit", now)
}

// removeMember takes a member out of the union: its share of the purse, its parent back, the union's end if it is left too small.
func (h *VillageHandler) removeMember(ctx context.Context, tx application.Tx, meta envelope.Metadata, u *application.Union, m *application.UnionMember, status, reason string, now time.Time) error {
	paid, err := tx.Unions().DuesPaidBy(ctx, u.ID)
	if err != nil {
		return err
	}
	purse, err := unionPurse(ctx, tx, u)
	if err != nil {
		return err
	}
	bal, err := accountBalance(ctx, tx, purse)
	if err != nil {
		return err
	}
	// the leaver's part is its dues over the dues of the members still in (itself included): those who left before took theirs already
	current, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return err
	}
	live := map[string]int64{}
	for _, x := range current {
		if x.Active() {
			live[x.SettlementID] = paid[x.SettlementID]
		}
	}
	share := polity.Share(bal, live, m.SettlementID)
	if ok, err := tx.Unions().SetMember(ctx, u.ID, m.SettlementID, m.Status, status, now); err != nil || !ok {
		return err
	}
	if share > 0 {
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, m.SettlementID)
		if err != nil {
			return err
		}
		txID := h.ids.NewID()
		if _, err := move(ctx, tx, purse.ID, treasury.ID, share, application.ReasonUnionExitSettlement, application.UnionReference, u.ID, txID, now); err != nil {
			return err
		}
		if _, err := tx.Unions().InsertSettlement(ctx, u.ID, m.SettlementID, reason, share, txID, now); err != nil {
			return err
		}
	}
	if u.Kind == polity.KindCountry {
		st, err := tx.Settlements().ByID(ctx, m.SettlementID)
		if err != nil {
			return err
		}
		parent := m.PrevParentID
		if parent == "" {
			def, err := tx.Diplomacy().CountryByCode(ctx, application.DefaultFoundingCountryCode)
			if err != nil {
				return err
			}
			parent = def.ID
		}
		if err := tx.Unions().SetParent(ctx, st.JurisdictionID, parent); err != nil {
			return err
		}
	}
	if err := h.auditUnion(ctx, tx, u.ID, "", "member_"+status, "", now, map[string]any{"settlement": m.SettlementID, "share": share, "reason": reason}); err != nil {
		return err
	}
	if err := appendVillageEvent(ctx, tx, meta, "union_member_"+status, m.SettlementID, map[string]any{"union": u.ID, "settlement": m.SettlementID, "share": share}); err != nil {
		return err
	}
	ms, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return err
	}
	active := 0
	for _, x := range ms {
		if x.Active() {
			active++
		}
	}
	min := 2
	if u.Kind == polity.KindCountry {
		min = 1
	}
	if active < min {
		return h.dissolve(ctx, tx, meta, u, now)
	}
	return nil
}

// dissolve ends a union: the purse is paid out by the same formula, every member goes back to where it was, the row is kept as history.
func (h *VillageHandler) dissolve(ctx context.Context, tx application.Tx, meta envelope.Metadata, u *application.Union, now time.Time) error {
	if ok, err := tx.Unions().SetStatus(ctx, u.ID, "dissolved", now); err != nil || !ok {
		return err
	}
	paid, err := tx.Unions().DuesPaidBy(ctx, u.ID)
	if err != nil {
		return err
	}
	purse, err := unionPurse(ctx, tx, u)
	if err != nil {
		return err
	}
	bal, err := accountBalance(ctx, tx, purse)
	if err != nil {
		return err
	}
	ms, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return err
	}
	// those who left or were expelled took their shares already: the rest is shared among the members that remain, by what they paid
	remaining := map[string]int64{}
	for _, m := range ms {
		if m.Active() {
			remaining[m.SettlementID] = paid[m.SettlementID]
		}
	}
	shares := polity.SharesAll(bal, remaining)
	if len(shares) == 0 && bal > 0 {
		// nobody paid dues (the purse is all gifts): it is shared equally among the members that remain, the remainder to the first by id
		var ids []string
		for _, m := range ms {
			if m.Active() {
				ids = append(ids, m.SettlementID)
			}
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			var given int64
			for _, id := range ids {
				shares[id] = bal / int64(len(ids))
				given += shares[id]
			}
			shares[ids[0]] += bal - given
		}
	}
	for _, m := range ms {
		if !m.Active() {
			continue
		}
		if amount := shares[m.SettlementID]; amount > 0 {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, m.SettlementID)
			if err != nil {
				return err
			}
			txID := h.ids.NewID()
			if _, err := move(ctx, tx, purse.ID, treasury.ID, amount, application.ReasonUnionDissolutionPayout, application.UnionReference, u.ID, txID, now); err != nil {
				return err
			}
			if _, err := tx.Unions().InsertSettlement(ctx, u.ID, m.SettlementID, "dissolution", amount, txID, now); err != nil {
				return err
			}
		}
		if _, err := tx.Unions().SetMember(ctx, u.ID, m.SettlementID, m.Status, "left", now); err != nil {
			return err
		}
		if u.Kind == polity.KindCountry {
			st, err := tx.Settlements().ByID(ctx, m.SettlementID)
			if err != nil {
				return err
			}
			parent := m.PrevParentID
			if parent == "" {
				def, err := tx.Diplomacy().CountryByCode(ctx, application.DefaultFoundingCountryCode)
				if err != nil {
					return err
				}
				parent = def.ID
			}
			if err := tx.Unions().SetParent(ctx, st.JurisdictionID, parent); err != nil {
				return err
			}
		}
	}
	if err := h.auditUnion(ctx, tx, u.ID, "", "dissolved", "", now, nil); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "union_dissolved", u.ID, map[string]any{"union": u.ID})
}

// ---- motions ----------------------------------------------------------------------------------------------------------------------

func (h *VillageHandler) unionMotionOpen(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageUnionRequest, now time.Time) error {
	u, err := tx.Unions().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if u == nil || u.Status != "active" {
		return refuseVillage(village.UnionNoUnion, village.AddrUnion)
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return err
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	var subject string
	switch kind {
	case "admit":
		if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.UnionAdmit); err != nil {
			return err
		}
		o, err := h.settlementByCode(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		if o == nil {
			return refuseVillage(village.UnionNoTarget, village.AddrUnion)
		}
		if m, err := tx.Unions().MemberOf(ctx, u.ID, o.CityID); err != nil {
			return err
		} else if m != nil && m.Active() {
			return refuseVillage(village.UnionAlreadyMember, village.AddrUnion)
		}
		subject = o.CityID
	case "expel":
		if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.UnionExpel); err != nil {
			return err
		}
		o, err := h.settlementByCode(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		if o == nil {
			return refuseVillage(village.UnionNoTarget, village.AddrUnion)
		}
		if m, err := tx.Unions().MemberOf(ctx, u.ID, o.CityID); err != nil {
			return err
		} else if m == nil || !m.Active() {
			return refuseVillage(village.UnionNotMember, village.AddrUnion)
		}
		subject = o.CityID
	case "dissolve":
		if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.CharterAmend); err != nil {
			return err
		}
	default:
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	return h.openMotion(ctx, tx, meta, u, kind, subject, p.ID, nil, now)
}

func (h *VillageHandler) openMotion(ctx context.Context, tx application.Tx, meta envelope.Metadata, u *application.Union, kind, subject, by string, detail []byte, now time.Time) error {
	closes := now.Add(h.polity.VoteWindow)
	m := application.UnionMotion{ID: h.ids.NewID(), UnionID: u.ID, Kind: kind, SubjectID: subject, OpenedBy: by, OpenedAt: now, ClosesAt: closes, Detail: detail}
	ok, err := tx.Unions().InsertMotion(ctx, m)
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.UnionMotionOpen, village.AddrUnion)
	}
	if err := h.schedulePolity(ctx, tx, application.TickMotion, application.UnionReference, m.ID, "", by, now, closes); err != nil {
		return err
	}
	return h.auditUnion(ctx, tx, u.ID, by, "motion_opened", "", now, map[string]any{"motion": m.ID, "kind": kind, "subject": subject})
}

func (h *VillageHandler) unionMotionVote(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, req VillageUnionRequest, now time.Time) error {
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.UnionRatify); err != nil {
		return err
	}
	m, err := tx.Unions().Motion(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if m == nil {
		return refuseVillage(village.UnionNoMotion, village.AddrUnion)
	}
	if m.Status != "open" || !now.Before(m.ClosesAt) {
		return refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	mem, err := tx.Unions().MemberOf(ctx, m.UnionID, s.CityID)
	if err != nil {
		return err
	}
	if mem == nil || !mem.Active() || m.SubjectID == s.CityID {
		return refuseVillage(village.UnionNotMember, village.AddrUnion)
	}
	if fresh, err := tx.Unions().CastMotion(ctx, m.ID, s.CityID, p.ID, req.yes(), now); err != nil {
		return err
	} else if !fresh {
		return refuseVillage(village.UnionVoted, village.AddrUnion)
	}
	// every voting member has voted: the count is not waited for
	voters, err := h.motionVoters(ctx, tx, m)
	if err != nil {
		return err
	}
	votes, err := tx.Unions().MotionVotes(ctx, m.ID)
	if err != nil {
		return err
	}
	if len(votes) >= len(voters) {
		return h.settleMotion(ctx, tx, meta, m.ID, now, true)
	}
	return nil
}

func (h *VillageHandler) motionVoters(ctx context.Context, tx application.Tx, m *application.UnionMotion) ([]polity.Member, error) {
	exclude := ""
	if m.Kind == "expel" || m.Kind == "exit" {
		exclude = m.SubjectID
	}
	return h.pollMembers(ctx, tx, m.UnionID, exclude)
}

func (h *VillageHandler) tickMotion(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	return h.settleMotion(ctx, tx, meta, id, now, false)
}

func (h *VillageHandler) settleMotion(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time, force bool) error {
	m, err := tx.Unions().Motion(ctx, id, true)
	if err != nil || m == nil || m.Status != "open" {
		return err
	}
	if !force && now.Before(m.ClosesAt) {
		return nil
	}
	u, err := tx.Unions().ByID(ctx, m.UnionID, true)
	if err != nil || u == nil {
		return err
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return err
	}
	voters, err := h.motionVoters(ctx, tx, m)
	if err != nil {
		return err
	}
	votes, err := tx.Unions().MotionVotes(ctx, m.ID)
	if err != nil {
		return err
	}
	rules := polity.Rules{Mode: polity.Mode(u.VoteMode), WeightCapBPS: h.polity.WeightCapBPS, MembersBPS: h.polity.MembersMajorityBPS, PopulationBPS: h.polity.PopulationMajorityBPS}
	t := rules.Carries(voters, votes)
	passed := t.Carried
	if m.Kind == "dissolve" {
		// two thirds of the members (docs/adr/0076 4.5), whatever the vote mode
		yes := 0
		for _, v := range voters {
			if votes[v.ID] {
				yes++
			}
		}
		passed = len(voters) > 0 && yes*3 >= len(voters)*2
	}
	status := "failed"
	if passed {
		status = "passed"
	}
	res, _ := json.Marshal(map[string]any{"yes_members": t.YesMembers, "members": t.Members, "weight": t.Weight, "yes_weight": t.YesWeight})
	if ok, err := tx.Unions().SetMotion(ctx, m.ID, "open", status, res, now); err != nil || !ok {
		return err
	}
	if err := h.auditUnion(ctx, tx, u.ID, "", "motion_"+status, "", now, map[string]any{"motion": m.ID, "kind": m.Kind, "subject": m.SubjectID}); err != nil {
		return err
	}
	if !passed {
		return nil
	}
	switch m.Kind {
	case "expel":
		mem, err := tx.Unions().MemberOf(ctx, u.ID, m.SubjectID)
		if err != nil || mem == nil || !mem.Active() {
			return err
		}
		return h.removeMember(ctx, tx, meta, u, mem, "expelled", "expelled", now)
	case "dissolve":
		return h.dissolve(ctx, tx, meta, u, now)
	case "exit":
		return h.beginNotice(ctx, tx, meta, u, m.SubjectID, m.OpenedBy, now)
	}
	return nil // admit: the newcomer joins by its own act (accede)
}

// unionAccede is the admitted settlement's own act: its ratifier agrees, its assembly too when the union's delegations are deep.
func (h *VillageHandler) unionAccede(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, req VillageUnionRequest, now time.Time) (string, error) {
	if _, err := h.requireSettlementPower(ctx, tx, s, p.ID, charter.UnionRatify); err != nil {
		return "", err
	}
	u, err := tx.Unions().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || u == nil || u.Status != "active" {
		return "", refuseVillage(village.UnionNoUnion, village.AddrUnion)
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return "", err
	}
	if m, err := tx.Unions().MemberOf(ctx, u.ID, s.CityID); err != nil {
		return "", err
	} else if m != nil && m.Active() {
		return "", refuseVillage(village.UnionAlreadyMember, village.AddrUnion)
	}
	if ok, err := h.admitted(ctx, tx, u.ID, s.CityID, now); err != nil {
		return "", err
	} else if !ok {
		return "", refuseVillage(village.UnionNotAdmitted, village.AddrUnion)
	}
	if u.Kind == polity.KindCountry {
		if c, err := tx.Unions().CountryOf(ctx, s.CityID); err != nil {
			return "", err
		} else if c != nil {
			return "", refuseVillage(village.UnionOneCountry, village.AddrUnion)
		}
	}
	if polity.NeedsAssembly(u.Delegations, u.DuesShareBPS, h.polity.AssemblyDuesBoundBPS) {
		eligible, err := tx.Charters().Eligible(ctx, s.CityID, h.voterCut(now))
		if err != nil {
			return "", err
		}
		closes := now.Add(h.polity.VoteWindow)
		b := application.UnionBallot{ID: h.ids.NewID(), Kind: "ratify", SettlementID: s.CityID, UnionID: u.ID, OpenedBy: p.ID, OpensAt: now, ClosesAt: closes, Eligible: eligible, Status: "open"}
		if ok, err := tx.Unions().InsertBallot(ctx, b); err != nil {
			return "", err
		} else if !ok {
			return "", refuseVillage(village.UnionDecided, village.AddrUnion)
		}
		if err := h.schedulePolity(ctx, tx, application.TickBallot, application.UnionReference, b.ID, "", p.ID, now, closes); err != nil {
			return "", err
		}
		return village.UnionNoticeAssembly, nil
	}
	return village.UnionNoticeAcceded, h.joinMember(ctx, tx, meta, u.ID, s.CityID, now)
}

// admitted says the members passed a motion to admit this settlement (the most recent one, within the ratification window).
func (h *VillageHandler) admitted(ctx context.Context, tx application.Tx, unionID, settlementID string, now time.Time) (bool, error) {
	return tx.Unions().Admitted(ctx, unionID, settlementID, now.AddDate(0, 0, -int(h.polity.RatifyDays)))
}

func (h *VillageHandler) joinMember(ctx context.Context, tx application.Tx, meta envelope.Metadata, unionID, settlementID string, now time.Time) error {
	u, err := tx.Unions().ByID(ctx, unionID, true)
	if err != nil || u == nil || u.Status != "active" {
		return err
	}
	st, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return err
	}
	prev := ""
	if u.Kind == polity.KindCountry {
		if prev, err = tx.Unions().ParentOf(ctx, st.JurisdictionID); err != nil {
			return err
		}
	}
	ok, err := tx.Unions().AddMember(ctx, application.UnionMember{UnionID: u.ID, SettlementID: settlementID, UnionKind: u.Kind, JoinedAt: now, PrevParentID: prev})
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.UnionOneCountry, village.AddrUnion)
	}
	if u.Kind == polity.KindCountry {
		if err := tx.Unions().SetParent(ctx, st.JurisdictionID, u.JurisdictionID); err != nil {
			return err
		}
	}
	if err := h.auditUnion(ctx, tx, u.ID, "", "member_joined", "", now, map[string]any{"settlement": settlementID}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "union_member_joined", settlementID, map[string]any{"union": u.ID, "settlement": settlementID})
}

// ---- dues -------------------------------------------------------------------------------------------------------------------------

// tickDues charges the members for every period that has run since the union's last dues row (at most a few, so a long outage catches up
// without a flood), once each: the (union, member, period) row is the fence. It schedules the next period.
func (h *VillageHandler) tickDues(ctx context.Context, tx application.Tx, meta envelope.Metadata, unionID string, now time.Time) error {
	u, err := tx.Unions().ByID(ctx, unionID, true)
	if err != nil || u == nil || u.Status != "active" {
		return err
	}
	if err := tx.Unions().Lock(ctx, u.ID); err != nil {
		return err
	}
	period := h.polity.DuesPeriod
	if period <= 0 {
		return nil
	}
	current := int64(now.Sub(u.CreatedAt) / period) // periods completed since founding; period k is the k-th one, paid when it ends
	first := current - 3
	if first < 1 {
		first = 1
	}
	ms, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return err
	}
	purse, err := unionPurse(ctx, tx, u)
	if err != nil {
		return err
	}
	for k := first; k <= current; k++ {
		from, to := u.CreatedAt.Add(time.Duration(k-1)*period), u.CreatedAt.Add(time.Duration(k)*period)
		for _, m := range ms {
			if !m.Active() || m.JoinedAt.After(to) {
				continue
			}
			income, err := tx.Unions().TreasuryIncome(ctx, m.SettlementID, from, to)
			if err != nil {
				return err
			}
			due := u.Dues().Due(income, h.polity.MaxDuesBPS)
			if u.DuesFixed > 0 && income <= 0 {
				due = 0
			}
			bal, err := treasuryBalance(ctx, tx, m.SettlementID)
			if err != nil {
				return err
			}
			pay := min(due, max(bal, 0))
			txID := ""
			if pay > 0 {
				txID = h.ids.NewID()
			}
			fresh, err := tx.Unions().InsertDues(ctx, application.UnionDues{UnionID: u.ID, SettlementID: m.SettlementID, PeriodNo: k, Due: due, Paid: pay, LedgerTx: txID, At: now})
			if err != nil {
				return err
			}
			if !fresh {
				continue
			}
			if pay > 0 {
				treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, m.SettlementID)
				if err != nil {
					return err
				}
				if _, err := move(ctx, tx, treasury.ID, purse.ID, pay, application.ReasonUnionDues, application.UnionReference, u.ID, txID, now); err != nil {
					return err
				}
			}
			arrears := m.Arrears
			if pay < due {
				arrears++
			} else {
				arrears = 0
			}
			if arrears != m.Arrears {
				if err := tx.Unions().SetArrears(ctx, u.ID, m.SettlementID, arrears); err != nil {
					return err
				}
			}
		}
	}
	return h.schedulePolity(ctx, tx, application.TickDues, application.UnionReference, u.ID, "", "", now, u.CreatedAt.Add(time.Duration(current+1)*period))
}
