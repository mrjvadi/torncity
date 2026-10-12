package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Unions and countries (docs/adr/0076 4 and 5). A union is founded by the agreement of settlements: each ratifies by its own charter's
// rule, the assembly of a settlement approves the deep delegations, the union binds those who signed. Its charter is the same kind of
// object the settlements write, kept in the union's own tables.
//
//   - WHO WORKS THERE. The holders of its offices, named in the founding charter and appointed afterwards by those who may.
//   - WHAT IT CONSUMES. A filing fee (the proposer's treasury into the sink) and the dues its members pay each period.
//   - WHAT IT PROVIDES. A purse (a state treasury owned by its jurisdiction), the delegated powers (treaties, war, customs, money,
//     defence) exercised by its offices, and for a country the jurisdiction every member sits under.
//   - WHAT BREAKS. A delegation nobody ratified; a charter with no manager; a member that cannot pay (arrears, then expulsion).

// UnionOfficeDraft is an office of a founding charter.
type UnionOfficeDraft struct {
	Title       string                `json:"title"`
	Seats       CharterInt            `json:"seats,omitempty"`
	Grants      []VillageCharterGrant `json:"grants,omitempty"`
	Acquisition string                `json:"acquisition,omitempty"`
	TermDays    CharterInt            `json:"term_days,omitempty"`
	// Holders are public codes of the players who sit in it from the founding.
	Holders []string `json:"holders,omitempty"`
}

// VillageUnionRequest is the payload of settlement.union.
type VillageUnionRequest struct {
	// Action is "" (the screen) or one of village.UnionAction*.
	Action string `json:"action,omitempty"`
	// ID names a union, a proposal, a ballot or a motion; Office an office of the union.
	ID     string `json:"id,omitempty"`
	Office string `json:"office,omitempty"`
	// Name, Kind, Mode, Exit, Delegations, DuesFixed, DuesShare, Invite and Offices are a proposal: the union's name, league or country, how
	// the members vote (one_member, weighted, double_majority), how a member leaves (free, consent), the classes it is given (comma
	// separated), its dues, the invited settlements' codes (comma separated) and its first offices (a JSON list).
	Name        string          `json:"name,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Mode        string          `json:"mode,omitempty"`
	Exit        string          `json:"exit,omitempty"`
	Delegations string          `json:"delegations,omitempty"`
	DuesFixed   string          `json:"dues_fixed,omitempty"`
	DuesShare   string          `json:"dues_share,omitempty"`
	Invite      string          `json:"invite,omitempty"`
	Offices     json.RawMessage `json:"offices,omitempty"`
	// Title, Seats, Grants, Acquisition and TermDays are an office being saved.
	Title       string                `json:"title,omitempty"`
	Seats       CharterInt            `json:"seats,omitempty"`
	Grants      []VillageCharterGrant `json:"grants,omitempty"`
	Acquisition string                `json:"acquisition,omitempty"`
	TermDays    CharterInt            `json:"term_days,omitempty"`
	// Code is a settlement's code (a motion's subject); Player a public code of a player; Choice is yes or no.
	Code   string `json:"code,omitempty"`
	Player string `json:"player,omitempty"`
	Choice string `json:"choice,omitempty"`
}

func (r VillageUnionRequest) yes() bool {
	c := strings.ToLower(strings.TrimSpace(r.Choice))
	return c == "yes" || c == "y" || c == "1" || c == "true"
}

// Union handles settlement.union.
func (h *VillageHandler) Union(ctx context.Context, meta envelope.Metadata, req VillageUnionRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.UnionView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, hasSettlement, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" && act != village.UnionActionOpen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.unionAct(ctx, tx, meta, s, hasSettlement, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.unionView(ctx, tx, s, hasSettlement, p, strings.TrimSpace(req.ID))
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Union(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) unionAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool,
	p *application.Player, act string, req VillageUnionRequest,
) (string, error) {
	if !h.polityOpen() {
		return "", refuseVillage(village.UnionNotYet, village.AddrUnion)
	}
	now := h.now()
	switch act {
	case village.UnionActionPropose, village.UnionActionWithdraw, village.UnionActionRatify, village.UnionActionRefuse, village.UnionActionLeave,
		village.UnionActionStay, village.UnionActionAccede, village.UnionActionMotionVote:
		if !has {
			return "", refuseVillage(village.VillageNoSettlement)
		}
	}
	switch act {
	case village.UnionActionPropose:
		return village.UnionNoticeProposed, h.unionPropose(ctx, tx, meta, s, p, req, now)
	case village.UnionActionWithdraw:
		return village.UnionNoticeWithdrawn, h.unionWithdraw(ctx, tx, meta, s, p, req, now)
	case village.UnionActionRatify:
		return h.unionRatify(ctx, tx, meta, s, p, req, now, true)
	case village.UnionActionRefuse:
		return h.unionRatify(ctx, tx, meta, s, p, req, now, false)
	case village.UnionActionVote:
		return village.UnionNoticeVoted, h.unionBallotVote(ctx, tx, meta, p, req, now)
	case village.UnionActionLeave:
		return village.UnionNoticeNotice, h.unionLeave(ctx, tx, meta, s, p, req, now)
	case village.UnionActionStay:
		return village.UnionNoticeStayed, h.unionStay(ctx, tx, meta, s, p, req, now)
	case village.UnionActionMotion:
		return village.UnionNoticeMotion, h.unionMotionOpen(ctx, tx, meta, s, has, p, req, now)
	case village.UnionActionMotionVote:
		return village.UnionNoticeVoted, h.unionMotionVote(ctx, tx, meta, s, p, req, now)
	case village.UnionActionAccede:
		return h.unionAccede(ctx, tx, meta, s, p, req, now)
	case village.UnionActionOffice:
		return village.UnionNoticeOffice, h.unionOfficeSave(ctx, tx, meta, p, req, now)
	case village.UnionActionCloseOffice:
		return village.UnionNoticeOfficeShut, h.unionOfficeClose(ctx, tx, meta, p, req, now)
	case village.UnionActionAppoint:
		return village.UnionNoticeAppointed, h.unionAppoint(ctx, tx, meta, p, req, now)
	case village.UnionActionDismiss:
		return village.UnionNoticeDismissed, h.unionDismiss(ctx, tx, meta, p, req, now)
	case village.UnionActionRecall:
		return h.unionRecall(ctx, tx, meta, p, req, now)
	case village.UnionActionRules:
		return village.UnionNoticeRules, h.unionRules(ctx, tx, meta, p, req, now)
	}
	return "", refuseVillage(village.UnionBadArgs, village.AddrUnion)
}

// ---- founding ---------------------------------------------------------------------------------------------------------------------

type draftOffice struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Seats       int             `json:"seats"`
	Grants      []charter.Grant `json:"grants"`
	Acquisition string          `json:"acquisition"`
	TermDays    int             `json:"term_days"`
	Holders     []string        `json:"holders"`
}

func classesOf(text string) ([]polity.Class, error) {
	var in []polity.Class
	for _, c := range splitCodes(text) {
		in = append(in, polity.Class(strings.ToLower(c)))
	}
	return polity.Normalise(in)
}

func classTexts(cs []polity.Class) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, string(c))
	}
	return out
}

// parseOffices reads the founding charter of a proposal and checks it: the rails of the charter, the ceiling of the delegations, a manager
// who is held. It returns the offices with the holders resolved to player ids.
func (h *VillageHandler) parseOffices(ctx context.Context, tx application.Tx, raw json.RawMessage, delegations []polity.Class) ([]draftOffice, error) {
	var drafts []UnionOfficeDraft
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return nil, refuseVillage(village.UnionNoManager, village.AddrUnion)
	}
	if strings.HasPrefix(t, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, refuseVillage(village.UnionBadArgs, village.AddrUnion)
		}
		t = s
	}
	if err := json.Unmarshal([]byte(t), &drafts); err != nil {
		return nil, refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	lim := h.limits()
	var out []draftOffice
	var titles []string
	managerHeld := false
	for _, d := range drafts {
		title, err := charter.ValidateTitle(d.Title, titles, lim)
		if err != nil {
			return nil, refuseVillage(village.UnionTitle, village.AddrUnion)
		}
		titles = append(titles, title)
		grants, err := charter.NormaliseGrants(grantsOf(d.Grants))
		if err != nil {
			return nil, refuseCharter(err)
		}
		if err := polity.Ceiling(delegations, grants); err != nil {
			return nil, refuseVillage(village.UnionCeiling, village.AddrUnion)
		}
		seats := int(d.Seats)
		if seats == 0 {
			seats = 1
		}
		acq := charter.Acquisition(strings.ToLower(strings.TrimSpace(d.Acquisition)))
		if acq == "" {
			acq = charter.AcquireAppointment
		}
		// The first offices of a union are appointed by its founders; elected offices wait for the union's own elections (not built).
		if acq != charter.AcquireAppointment {
			return nil, refuseVillage(village.UnionBadArgs, village.AddrUnion)
		}
		o := charter.Office{ID: "x", Title: title, Seats: seats, Grants: grants, Acquisition: acq, TermDays: int(d.TermDays)}
		if err := charter.ValidateOffice(o, len(drafts), lim); err != nil {
			return nil, refuseCharter(err)
		}
		var holders []string
		for _, code := range d.Holders {
			id, ok, err := tx.Unions().PlayerByCode(ctx, strings.TrimSpace(code))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, refuseVillage(village.UnionNoPlayer, village.AddrUnion)
			}
			holders = append(holders, id)
		}
		if len(holders) > seats {
			return nil, refuseVillage(village.UnionSeatsFull, village.AddrUnion)
		}
		if charter.IsManager(grants) && len(holders) > 0 {
			managerHeld = true
		}
		out = append(out, draftOffice{ID: h.ids.NewID(), Title: title, Seats: seats, Grants: grants, Acquisition: string(acq), TermDays: int(d.TermDays), Holders: holders})
	}
	if len(out) == 0 || !managerHeld {
		return nil, refuseVillage(village.UnionNoManager, village.AddrUnion)
	}
	return out, nil
}

func (h *VillageHandler) unionPropose(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player,
	req VillageUnionRequest, now time.Time,
) error {
	if _, err := h.requireSettlementPower(ctx, tx, s, p.ID, charter.UnionPropose); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 2 || n > 32 {
		return refuseVillage(village.UnionBadName, village.AddrUnion)
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != polity.KindLeague && kind != polity.KindCountry {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	mode := polity.Mode(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode == "" {
		mode = polity.DoubleMajority
	}
	exit := strings.ToLower(strings.TrimSpace(req.Exit))
	if exit == "" {
		exit = "free"
	}
	if !mode.Valid() || (exit != "free" && exit != "consent") {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	classes, err := classesOf(req.Delegations)
	if err != nil {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	duesFixed, duesShare := atoi64(req.DuesFixed), atoi64(req.DuesShare)
	if duesFixed < 0 || duesShare < 0 || duesShare > h.polity.MaxDuesBPS {
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	if existing, err := tx.Unions().ProposalsOf(ctx, s.CityID); err != nil {
		return err
	} else {
		for _, e := range existing {
			if e.ProposerID == s.CityID {
				return refuseVillage(village.UnionOpenProposal, village.AddrUnion)
			}
		}
	}
	// the invited: the proposer and the others named by code
	invited := []string{s.CityID}
	seen := map[string]bool{s.CityID: true}
	for _, code := range splitCodes(req.Invite) {
		o, err := h.settlementByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if o == nil {
			return refuseVillage(village.UnionNoTarget, village.AddrUnion)
		}
		if !seen[o.CityID] {
			seen[o.CityID] = true
			invited = append(invited, o.CityID)
		}
	}
	if kind == polity.KindLeague && len(invited) < 2 {
		return refuseVillage(village.UnionNoTarget, village.AddrUnion)
	}
	var residents int64
	for _, id := range invited {
		n, err := tx.Settlements().ResidentCount(ctx, id)
		if err != nil {
			return err
		}
		residents += n
		if kind == polity.KindCountry {
			if c, err := tx.Unions().CountryOf(ctx, id); err != nil {
				return err
			} else if c != nil {
				return refuseVillage(village.UnionOneCountry, village.AddrUnion)
			}
		}
	}
	if err := polity.CheckKind(kind, classes, h.polity.RequiredDelegations, residents, h.polity.CountryMinResidents); err != nil {
		switch err {
		case polity.ErrTooSmall:
			return refuseVillage(village.UnionTooSmall, village.AddrUnion)
		case polity.ErrNotCountry:
			return refuseVillage(village.UnionNotCountry, village.AddrUnion)
		}
		return refuseVillage(village.UnionBadArgs, village.AddrUnion)
	}
	offices, err := h.parseOffices(ctx, tx, req.Offices, classes)
	if err != nil {
		return err
	}
	rawOffices, err := json.Marshal(offices)
	if err != nil {
		return err
	}
	fee := h.polity.LeagueFee
	if kind == polity.KindCountry {
		fee = h.polity.CountryFee
		if len(invited) == 1 {
			fee *= 2 // a country of one is the sovereign shortcut: it costs more (docs/adr/0076 5.5)
		}
	}
	if fee > 0 {
		if bal, err := treasuryBalance(ctx, tx, s.CityID); err != nil {
			return err
		} else if bal < fee {
			return refuseVillage(village.UnionNoMoney, village.AddrUnion)
		}
		if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonUnionRegistrationFee, fee, now); err != nil {
			return err
		}
	}
	id := h.ids.NewID()
	deadline := now.AddDate(0, 0, int(h.polity.RatifyDays))
	prop := application.UnionProposal{ID: id, ProposerID: s.CityID, ProposedBy: p.ID, Name: name, Kind: kind, VoteMode: string(mode), ExitRule: exit, Delegations: classes,
		DuesFixed: duesFixed, DuesShareBPS: duesShare, Fee: fee, Offices: rawOffices, CreatedAt: now, Deadline: deadline}
	if err := tx.Unions().InsertProposal(ctx, prop, invited); err != nil {
		return err
	}
	if err := h.schedulePolity(ctx, tx, application.TickProposal, application.UnionReference, id, "", p.ID, now, deadline); err != nil {
		return err
	}
	if err := h.auditUnion(ctx, tx, "", p.ID, "proposed", "", now, map[string]any{"proposal": id, "name": name, "kind": kind, "invited": invited, "settlement": s.CityID}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "union_proposed", s.CityID, map[string]any{"proposal": id, "name": name, "kind": kind, "invited": invited})
}

func (h *VillageHandler) unionWithdraw(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player,
	req VillageUnionRequest, now time.Time,
) error {
	if _, err := h.requireSettlementPower(ctx, tx, s, p.ID, charter.UnionPropose); err != nil {
		return err
	}
	prop, err := tx.Unions().Proposal(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if prop == nil || prop.ProposerID != s.CityID {
		return refuseVillage(village.UnionNoProposal, village.AddrUnion)
	}
	if ok, err := tx.Unions().SetProposal(ctx, prop.ID, "pending", "withdrawn", "", now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	return h.auditUnion(ctx, tx, "", p.ID, "proposal_withdrawn", "", now, map[string]any{"proposal": prop.ID})
}

// unionRatify is a settlement's answer to a proposal, by its own charter's rule: the holder of union.ratify signs; if the proposal
// delegates something deep, the settlement's assembly decides in a ballot instead.
func (h *VillageHandler) unionRatify(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player,
	req VillageUnionRequest, now time.Time, yes bool,
) (string, error) {
	if _, err := h.requireSettlementPower(ctx, tx, s, p.ID, charter.UnionRatify); err != nil {
		return "", err
	}
	prop, err := tx.Unions().Proposal(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return "", err
	}
	if prop == nil {
		return "", refuseVillage(village.UnionNoProposal, village.AddrUnion)
	}
	invites, err := tx.Unions().Invites(ctx, prop.ID)
	if err != nil {
		return "", err
	}
	var mine *application.UnionInvite
	for i := range invites {
		if invites[i].SettlementID == s.CityID {
			mine = &invites[i]
		}
	}
	if mine == nil {
		return "", refuseVillage(village.UnionNotInvited, village.AddrUnion)
	}
	if prop.Status != "pending" || mine.Decision == "yes" || mine.Decision == "no" || !now.Before(prop.Deadline) {
		return "", refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	if !yes {
		if _, err := tx.Unions().SetInvite(ctx, prop.ID, s.CityID, "no", p.ID, "", now); err != nil {
			return "", err
		}
		if err := h.auditUnion(ctx, tx, "", p.ID, "refused", "", now, map[string]any{"proposal": prop.ID, "settlement": s.CityID}); err != nil {
			return "", err
		}
		return village.UnionNoticeRefused, h.evaluateProposal(ctx, tx, meta, prop.ID, now)
	}
	// the answer must still be possible: a member may not already belong to another country
	if prop.Kind == polity.KindCountry {
		if c, err := tx.Unions().CountryOf(ctx, s.CityID); err != nil {
			return "", err
		} else if c != nil {
			return "", refuseVillage(village.UnionOneCountry, village.AddrUnion)
		}
	}
	if polity.NeedsAssembly(prop.Delegations, prop.DuesShareBPS, h.polity.AssemblyDuesBoundBPS) {
		if mine.Decision == "assembly" {
			return "", refuseVillage(village.UnionDecided, village.AddrUnion)
		}
		eligible, err := tx.Charters().Eligible(ctx, s.CityID, h.voterCut(now))
		if err != nil {
			return "", err
		}
		closes := now.Add(h.polity.VoteWindow)
		if closes.After(prop.Deadline) {
			closes = prop.Deadline
		}
		b := application.UnionBallot{ID: h.ids.NewID(), Kind: "ratify", SettlementID: s.CityID, ProposalID: prop.ID, OpenedBy: p.ID, OpensAt: now, ClosesAt: closes, Eligible: eligible, Status: "open"}
		if ok, err := tx.Unions().InsertBallot(ctx, b); err != nil {
			return "", err
		} else if !ok {
			return "", refuseVillage(village.UnionDecided, village.AddrUnion)
		}
		if _, err := tx.Unions().SetInvite(ctx, prop.ID, s.CityID, "assembly", p.ID, b.ID, now); err != nil {
			return "", err
		}
		if err := h.schedulePolity(ctx, tx, application.TickBallot, application.UnionReference, b.ID, "", p.ID, now, closes); err != nil {
			return "", err
		}
		return village.UnionNoticeAssembly, h.auditUnion(ctx, tx, "", p.ID, "assembly_opened", "", now, map[string]any{"proposal": prop.ID, "settlement": s.CityID, "ballot": b.ID, "eligible": eligible})
	}
	if _, err := tx.Unions().SetInvite(ctx, prop.ID, s.CityID, "yes", p.ID, "", now); err != nil {
		return "", err
	}
	if err := h.auditUnion(ctx, tx, "", p.ID, "ratified", "", now, map[string]any{"proposal": prop.ID, "settlement": s.CityID}); err != nil {
		return "", err
	}
	return village.UnionNoticeRatified, h.evaluateProposal(ctx, tx, meta, prop.ID, now)
}

// evaluateProposal counts the answers and acts on the phase: in force, refused, lapsed or still waiting.
func (h *VillageHandler) evaluateProposal(ctx context.Context, tx application.Tx, meta envelope.Metadata, proposalID string, now time.Time) error {
	prop, err := tx.Unions().Proposal(ctx, proposalID, true)
	if err != nil || prop == nil || prop.Status != "pending" {
		return err
	}
	invites, err := tx.Unions().Invites(ctx, prop.ID)
	if err != nil {
		return err
	}
	yes, no := 0, 0
	for _, i := range invites {
		switch i.Decision {
		case "yes":
			yes++
		case "no":
			no++
		}
	}
	switch polity.Decide(prop.Kind, len(invites), yes, no, prop.Deadline, now) {
	case polity.InForce:
		return h.enterIntoForce(ctx, tx, meta, prop, invites, now)
	case polity.Refused:
		if _, err := tx.Unions().SetProposal(ctx, prop.ID, "pending", "refused", "", now); err != nil {
			return err
		}
		return h.auditUnion(ctx, tx, "", "", "proposal_refused", "", now, map[string]any{"proposal": prop.ID})
	case polity.Lapsed:
		if _, err := tx.Unions().SetProposal(ctx, prop.ID, "pending", "lapsed", "", now); err != nil {
			return err
		}
		return h.auditUnion(ctx, tx, "", "", "proposal_lapsed", "", now, map[string]any{"proposal": prop.ID})
	}
	return nil
}

func (h *VillageHandler) tickProposal(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	prop, err := tx.Unions().Proposal(ctx, id, true)
	if err != nil || prop == nil || prop.Status != "pending" {
		return err
	}
	// ballots still open at the deadline are settled first
	invites, err := tx.Unions().Invites(ctx, id)
	if err != nil {
		return err
	}
	for _, i := range invites {
		if i.Decision == "assembly" && i.BallotID != "" {
			if err := h.settleUnionBallot(ctx, tx, meta, i.BallotID, now, true); err != nil {
				return err
			}
		}
	}
	return h.evaluateProposal(ctx, tx, meta, id, now)
}

// enterIntoForce founds the union from the signatories: the jurisdiction, the members (a country's sit under it now), the first offices
// and their holders, the dues clock. All in the transaction of the answer that completed the agreement.
func (h *VillageHandler) enterIntoForce(ctx context.Context, tx application.Tx, meta envelope.Metadata, prop *application.UnionProposal, invites []application.UnionInvite, now time.Time) error {
	var members []string
	var residents int64
	for _, i := range invites {
		if i.Decision == "yes" {
			members = append(members, i.SettlementID)
			n, err := tx.Settlements().ResidentCount(ctx, i.SettlementID)
			if err != nil {
				return err
			}
			residents += n
		}
	}
	if err := polity.CheckKind(prop.Kind, prop.Delegations, h.polity.RequiredDelegations, residents, h.polity.CountryMinResidents); err != nil {
		if _, e := tx.Unions().SetProposal(ctx, prop.ID, "pending", "refused", "", now); e != nil {
			return e
		}
		return h.auditUnion(ctx, tx, "", "", "proposal_refused", "", now, map[string]any{"proposal": prop.ID, "why": err.Error()})
	}
	unionID, jid := h.ids.NewID(), h.ids.NewID()
	code := slugCode(prop.Name) + "-" + strings.ReplaceAll(unionID, "-", "")[:6]
	if err := tx.Unions().NewJurisdiction(ctx, jid, prop.Kind, code, prop.Name, application.WorldJurisdictionID); err != nil {
		return err
	}
	u := application.Union{ID: unionID, JurisdictionID: jid, Kind: prop.Kind, Name: prop.Name, VoteMode: prop.VoteMode, ExitRule: prop.ExitRule, Delegations: prop.Delegations,
		DuesFixed: prop.DuesFixed, DuesShareBPS: prop.DuesShareBPS, CreatedAt: now}
	if err := tx.Unions().Insert(ctx, u); err != nil {
		return err
	}
	for _, sid := range members {
		st, err := tx.Settlements().ByID(ctx, sid)
		if err != nil {
			return err
		}
		prev := ""
		if prop.Kind == polity.KindCountry {
			if prev, err = tx.Unions().ParentOf(ctx, st.JurisdictionID); err != nil {
				return err
			}
		}
		ok, err := tx.Unions().AddMember(ctx, application.UnionMember{UnionID: unionID, SettlementID: sid, UnionKind: prop.Kind, JoinedAt: now, PrevParentID: prev})
		if err != nil {
			return err
		}
		if !ok {
			return refuseVillage(village.UnionOneCountry, village.AddrUnion)
		}
		if prop.Kind == polity.KindCountry {
			if err := tx.Unions().SetParent(ctx, st.JurisdictionID, jid); err != nil {
				return err
			}
		}
	}
	var drafts []draftOffice
	if err := json.Unmarshal(prop.Offices, &drafts); err != nil {
		return err
	}
	for _, d := range drafts {
		o := charter.Office{ID: d.ID, Title: d.Title, Seats: d.Seats, Grants: d.Grants, Acquisition: charter.Acquisition(d.Acquisition), TermDays: d.TermDays}
		if err := tx.Unions().SaveOffice(ctx, unionID, o, prop.ProposedBy, now); err != nil {
			return err
		}
		for _, holder := range d.Holders {
			if _, err := tx.Unions().Seat(ctx, application.CharterSeat{ID: h.ids.NewID(), OfficeID: d.ID, HolderID: holder, AppointedBy: prop.ProposedBy, Since: now}); err != nil {
				return err
			}
		}
	}
	if ok, err := tx.Unions().SetProposal(ctx, prop.ID, "pending", "in_force", unionID, now); err != nil {
		return err
	} else if !ok {
		return nil
	}
	if err := h.auditUnion(ctx, tx, unionID, prop.ProposedBy, "founded", "", now, map[string]any{"proposal": prop.ID, "kind": prop.Kind, "members": members, "delegations": classTexts(prop.Delegations)}); err != nil {
		return err
	}
	if err := h.schedulePolity(ctx, tx, application.TickDues, application.UnionReference, unionID, "", "", now, now.Add(h.polity.DuesPeriod)); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "union_founded", prop.ProposerID, map[string]any{"union": unionID, "name": prop.Name, "kind": prop.Kind, "members": members})
}

// slugCode makes the stable part of a union's jurisdiction code from its name.
func slugCode(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "union"
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// ---- the residents' ballots -------------------------------------------------------------------------------------------------------

// ballotVoterOK says whether the player may take part in the ballot: a resident long enough of the settlement whose assembly decides, or
// (a recall) of any member of the union.
func (h *VillageHandler) ballotVoterOK(ctx context.Context, tx application.Tx, b *application.UnionBallot, playerID string) (bool, error) {
	cut := h.voterCut(b.OpensAt)
	if b.Kind == "ratify" {
		return tx.Charters().IsEligible(ctx, b.SettlementID, playerID, cut)
	}
	home, err := tx.Unions().ResidenceOf(ctx, playerID)
	if err != nil || home == "" {
		return false, err
	}
	m, err := tx.Unions().MemberOf(ctx, b.UnionID, home)
	if err != nil || m == nil || !m.Active() {
		return false, err
	}
	return tx.Charters().IsEligible(ctx, home, playerID, cut)
}

func (h *VillageHandler) unionElectorate(ctx context.Context, tx application.Tx, unionID string, at time.Time) (int, error) {
	ms, err := tx.Unions().Members(ctx, unionID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, m := range ms {
		if !m.Active() {
			continue
		}
		n, err := tx.Charters().Eligible(ctx, m.SettlementID, h.voterCut(at))
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func (h *VillageHandler) unionBallotVote(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageUnionRequest, now time.Time) error {
	b, err := tx.Unions().Ballot(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if b == nil {
		return refuseVillage(village.UnionNoBallot, village.AddrUnion)
	}
	if b.Status == "open" && !now.Before(b.ClosesAt) {
		if err := h.settleUnionBallot(ctx, tx, meta, b.ID, now, true); err != nil {
			return err
		}
		return refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	if b.Status != "open" {
		return refuseVillage(village.UnionDecided, village.AddrUnion)
	}
	if ok, err := h.ballotVoterOK(ctx, tx, b, p.ID); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.UnionNotEligible, village.AddrUnion)
	}
	if fresh, err := tx.Unions().Cast(ctx, b.ID, p.ID, req.yes(), now); err != nil {
		return err
	} else if !fresh {
		return refuseVillage(village.UnionVoted, village.AddrUnion)
	}
	yes, no, err := tx.Unions().Tally(ctx, b.ID)
	if err != nil {
		return err
	}
	if yes+no >= int64(b.Eligible) { // everyone has voted: the count is not waited for
		return h.settleUnionBallot(ctx, tx, meta, b.ID, now, true)
	}
	return nil
}

func (h *VillageHandler) tickBallot(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	return h.settleUnionBallot(ctx, tx, meta, id, now, false)
}

// settleUnionBallot counts a ballot and applies it. force counts it before its time (everyone voted, or the proposal's deadline came).
func (h *VillageHandler) settleUnionBallot(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time, force bool) error {
	b, err := tx.Unions().Ballot(ctx, id, true)
	if err != nil || b == nil || b.Status != "open" {
		return err
	}
	if !force && now.Before(b.ClosesAt) {
		return nil
	}
	yes, no, err := tx.Unions().Tally(ctx, b.ID)
	if err != nil {
		return err
	}
	need := h.cset().QuorumVotes(b.Eligible)
	passed := charter.Carries(yes, no, need)
	status := "failed"
	if passed {
		status = "passed"
	}
	res, _ := json.Marshal(map[string]any{"yes": yes, "no": no, "eligible": b.Eligible, "need": need})
	if ok, err := tx.Unions().SetBallot(ctx, b.ID, "open", status, res, now); err != nil || !ok {
		return err
	}
	switch b.Kind {
	case "ratify":
		decision := "no"
		if passed {
			decision = "yes"
		}
		if b.ProposalID != "" {
			if _, err := tx.Unions().SetInvite(ctx, b.ProposalID, b.SettlementID, decision, "", "", now); err != nil {
				return err
			}
			if err := h.auditUnion(ctx, tx, "", "", "assembly_"+status, "", now, map[string]any{"proposal": b.ProposalID, "settlement": b.SettlementID, "yes": yes, "no": no}); err != nil {
				return err
			}
			return h.evaluateProposal(ctx, tx, meta, b.ProposalID, now)
		}
		// an accession: the assembly of the newcomer decided
		if passed {
			return h.joinMember(ctx, tx, meta, b.UnionID, b.SettlementID, now)
		}
		return h.auditUnion(ctx, tx, b.UnionID, "", "accession_refused", "", now, map[string]any{"settlement": b.SettlementID})
	case "recall":
		return h.applyRecall(ctx, tx, meta, b, passed, yes, no, now)
	}
	return nil
}

// ---- the screen -------------------------------------------------------------------------------------------------------------------

func (h *VillageHandler) unionLineOf(ctx context.Context, tx application.Tx, u application.Union, myStatus string) (village.UnionLine, error) {
	ms, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return village.UnionLine{}, err
	}
	n := 0
	for _, m := range ms {
		if m.Active() {
			n++
		}
	}
	purse := int64(0)
	if acct, err := unionPurse(ctx, tx, &u); err == nil {
		purse, _ = accountBalance(ctx, tx, acct)
	}
	return village.UnionLine{ID: u.ID, Name: u.Name, Kind: u.Kind, Members: n, Delegations: classTexts(u.Delegations), Purse: purse, VoteMode: u.VoteMode, ExitRule: u.ExitRule,
		DuesFixed: u.DuesFixed, DuesShare: u.DuesShareBPS, MyStatus: myStatus}, nil
}

func (h *VillageHandler) unionView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, openID string) (village.UnionView, error) {
	v := village.UnionView{Open: h.polityOpen(), OpenAt: h.polity.RuleAt, CountryMinResidents: h.polity.CountryMinResidents,
		FeeLeague: h.polity.LeagueFee, FeeCountry: h.polity.CountryFee}
	for _, c := range polity.Classes() {
		v.Classes = append(v.Classes, string(c))
	}
	if has {
		v.Village = s.Name
		_, v.MayPropose = hasPermissionMap(ctx, tx, s, p.ID, charter.UnionPropose)
		_, v.MayRatify = hasPermissionMap(ctx, tx, s, p.ID, charter.UnionRatify)
		ms, err := tx.Unions().MembershipsOf(ctx, s.CityID)
		if err != nil {
			return v, err
		}
		for _, m := range ms {
			u, err := tx.Unions().ByID(ctx, m.UnionID, false)
			if err != nil || u == nil {
				return v, err
			}
			line, err := h.unionLineOf(ctx, tx, *u, m.Status)
			if err != nil {
				return v, err
			}
			v.Unions = append(v.Unions, line)
		}
		props, err := tx.Unions().ProposalsOf(ctx, s.CityID)
		if err != nil {
			return v, err
		}
		for _, pr := range props {
			line, err := h.proposalLine(ctx, tx, pr, s, p, v.MayRatify)
			if err != nil {
				return v, err
			}
			v.Proposals = append(v.Proposals, line)
		}
		founded, err := tx.Settlements().Founded(ctx)
		if err != nil {
			return v, err
		}
		for _, f := range founded {
			if f.CityID != s.CityID {
				v.Targets = append(v.Targets, named(f.Code, f.Name))
			}
		}
	}
	if openID != "" {
		d, err := h.unionDetail(ctx, tx, openID, s, has, p)
		if err != nil {
			return v, err
		}
		v.Detail = d
	}
	return v, nil
}

// hasPermissionMap is hasPermission with the second result the callers of views want.
func hasPermissionMap(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, perm charter.Permission) (int64, bool) {
	limit, err := requirePermission(ctx, tx, s, playerID, perm)
	return limit, err == nil
}

func (h *VillageHandler) proposalLine(ctx context.Context, tx application.Tx, pr application.UnionProposal, s application.FoundedSettlement, p *application.Player, mayRatify bool) (village.UnionProposalLine, error) {
	l := village.UnionProposalLine{ID: pr.ID, Name: pr.Name, Kind: pr.Kind, Delegations: classTexts(pr.Delegations), Deadline: pr.Deadline, Fee: pr.Fee, Mine: pr.ProposerID == s.CityID,
		NeedsAssembly: polity.NeedsAssembly(pr.Delegations, pr.DuesShareBPS, h.polity.AssemblyDuesBoundBPS)}
	invites, err := tx.Unions().Invites(ctx, pr.ID)
	if err != nil {
		return l, err
	}
	for _, i := range invites {
		st, err := tx.Settlements().ByID(ctx, i.SettlementID)
		if err != nil {
			return l, err
		}
		l.Invites = append(l.Invites, village.UnionInviteLine{Settlement: named(st.Code, st.Name), Decision: i.Decision})
		if i.SettlementID == s.CityID {
			l.MayRatify = mayRatify && (i.Decision == "pending")
			if i.Decision == "assembly" && i.BallotID != "" {
				if b, err := tx.Unions().Ballot(ctx, i.BallotID, false); err == nil && b != nil {
					yes, no, _ := tx.Unions().Tally(ctx, b.ID)
					voted := false
					if ok, e := h.ballotVoterOK(ctx, tx, b, p.ID); e == nil && ok {
						l.Ballot = &village.UnionBallotLine{ID: b.ID, Kind: b.Kind, ClosesAt: b.ClosesAt, Yes: yes, No: no, MayVote: true, Voted: voted}
					} else {
						l.Ballot = &village.UnionBallotLine{ID: b.ID, Kind: b.Kind, ClosesAt: b.ClosesAt, Yes: yes, No: no}
					}
				}
			}
		}
	}
	return l, nil
}

func (h *VillageHandler) unionDetail(ctx context.Context, tx application.Tx, id string, s application.FoundedSettlement, has bool, p *application.Player) (*village.UnionDetail, error) {
	u, err := tx.Unions().ByID(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil // the id named a proposal, a ballot or a motion: there is no union page to open
	}
	status := ""
	if has {
		if m, err := tx.Unions().MemberOf(ctx, u.ID, s.CityID); err != nil {
			return nil, err
		} else if m != nil && m.Active() {
			status = m.Status
		}
	}
	line, err := h.unionLineOf(ctx, tx, *u, status)
	if err != nil {
		return nil, err
	}
	d := &village.UnionDetail{UnionLine: line}
	for _, def := range charter.Catalogue() {
		if !def.Active {
			continue // a permission no act asks for stays in code (docs/adr/0064)
		}
		pv := village.CharterPermissionView{Code: string(def.Code), Group: def.Group, Limited: def.Limited, Active: def.Active}
		if c, ok := polity.ClassOf(def.Code); ok {
			pv.Class = string(c)
		}
		d.Permissions = append(d.Permissions, pv)
	}
	held, err := tx.Unions().HeldIn(ctx, u.ID, p.ID)
	if err != nil {
		return nil, err
	}
	for _, g := range sortedHeld(held) {
		d.Held = append(d.Held, string(g.Permission))
	}
	_, d.MayOffice = held.Has(charter.OfficeEdit)
	_, d.MayAppoint = held.Has(charter.OfficeAppoint)
	_, d.MayDismiss = held.Has(charter.OfficeDismiss)
	_, d.MayAdmit = held.Has(charter.UnionAdmit)
	_, d.MayExpel = held.Has(charter.UnionExpel)
	d.MayVoteMember = status != "" && has
	d.MayLeave = status == "active"
	ms, err := tx.Unions().Members(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	paid, err := tx.Unions().DuesPaidBy(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		st, err := tx.Settlements().ByID(ctx, m.SettlementID)
		if err != nil {
			return nil, err
		}
		n, _ := tx.Settlements().ResidentCount(ctx, m.SettlementID)
		ml := village.UnionMemberLine{Settlement: named(st.Code, st.Name), Status: m.Status, Residents: n, Arrears: m.Arrears, JoinedAt: m.JoinedAt, Paid: paid[m.SettlementID]}
		if m.NoticeAt != nil {
			e := polity.ExitAt(*m.NoticeAt, h.polity.ExitNoticeDays)
			ml.NoticeEnds = &e
		}
		d.Roster = append(d.Roster, ml)
	}
	offices, err := tx.Unions().Offices(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	seats, err := tx.Unions().Seats(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, o := range offices {
		ol := village.UnionOfficeLine{ID: o.ID, Title: o.Title, Seats: o.Seats, Acquisition: string(o.Acquisition)}
		for _, g := range o.Grants {
			ol.Grants = append(ol.Grants, village.CharterGrantView{Permission: string(g.Permission), Limit: g.Limit})
		}
		for _, seat := range seats {
			if seat.OfficeID == o.ID {
				if pl, err := tx.Players().GetByID(ctx, seat.HolderID); err == nil && pl != nil {
					ol.Holders = append(ol.Holders, named(pl.PublicCode, h.playerName(ctx, tx, seat.HolderID)))
				}
			}
		}
		d.Offices = append(d.Offices, ol)
	}
	motions, err := tx.Unions().OpenMotions(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, m := range motions {
		votes, err := tx.Unions().MotionVotes(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		ml := village.UnionMotionLine{ID: m.ID, Kind: m.Kind, ClosesAt: m.ClosesAt}
		for _, y := range votes {
			if y {
				ml.Yes++
			} else {
				ml.No++
			}
		}
		if m.SubjectID != "" {
			if st, err := tx.Settlements().ByID(ctx, m.SubjectID); err == nil {
				ml.Subject = named(st.Code, st.Name)
			}
		}
		if has {
			if y, ok := votes[s.CityID]; ok {
				ml.MyVote = "no"
				if y {
					ml.MyVote = "yes"
				}
			}
			ml.MayVote = status != "" && ml.MyVote == "" && m.SubjectID != s.CityID
		}
		if voters, err := h.motionVoters(ctx, tx, &m); err == nil {
			rules := polity.Rules{Mode: polity.Mode(u.VoteMode), WeightCapBPS: h.polity.WeightCapBPS, MembersBPS: h.polity.MembersMajorityBPS, PopulationBPS: h.polity.PopulationMajorityBPS}
			ml.Mode = u.VoteMode
			switch {
			case m.Kind == "dissolve":
				ml.Mode, ml.MembersBPS = "two_thirds", 6667
			case rules.Mode == polity.DoubleMajority:
				ml.MembersBPS, ml.PopulationBPS = rules.MembersBPS, rules.PopulationBPS
			case rules.Mode == polity.Weighted:
				ml.PopulationBPS = 5000
			default:
				ml.MembersBPS = 5000
			}
			no := map[string]bool{}
			for id, y := range votes {
				no[id] = !y
			}
			ty, tn := rules.Carries(voters, votes), rules.Carries(voters, no)
			ml.Members, ml.YesMembers, ml.NoMembers = ty.Members, ty.YesMembers, tn.YesMembers
			ml.Weight, ml.YesWeight, ml.NoWeight = ty.Weight, ty.YesWeight, tn.YesWeight
		}
		d.Motions = append(d.Motions, ml)
	}
	audit, err := tx.Unions().AuditList(ctx, u.ID, 20)
	if err != nil {
		return nil, err
	}
	for _, a := range audit {
		d.Audit = append(d.Audit, village.UnionAuditLine{Action: a.Action, At: a.At})
	}
	sort.SliceStable(d.Roster, func(i, j int) bool { return d.Roster[i].Settlement.Code < d.Roster[j].Settlement.Code })
	_ = fmt.Sprint
	return d, nil
}
