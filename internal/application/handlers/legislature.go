package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/legislature"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// LegislatureHandler serves votes of a body
// (docs/adr/0024-property-and-politics.md): the proposals of the player's
// city and country, one proposal, a member's vote and — from the scheduler —
// the close of a proposal's window.
//
// A proposal is opened by the use case that needs it: a change of a lever
// decided by a body, or one the lever's body must confirm (GovernanceHandler),
// an action a body must approve (WarHandler's declaration). It is decided
// exactly once: by the vote that settles it, or at its close, always under
// its row lock, and only while it is open. A passed lever proposal is applied
// through application.ApplyVotedPolicy; a passed action through the
// executor registered for it.
type LegislatureHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	content        ContentSource
	cities         application.CityRepository
	dir            application.GovernanceDirectory
	rules          LegislatureRules
	executors      map[string]ProposalExecutor
	idempotencyTTL time.Duration
	now            func() time.Time
}

// LegislatureRules is the tuning of votes (config legislature.*).
type LegislatureRules struct {
	// VoteWindow is how long a proposal is open, REAL time.
	VoteWindow time.Duration
	ListSize   int
}

// ProposalExecutor takes an action a body approved, in the transaction that
// decides the vote. It returns a ProposalLapse when the action no longer
// makes sense (the target is already at war); any other error fails the
// decision, which the scheduler retries.
type ProposalExecutor interface {
	ExecuteProposal(ctx context.Context, tx application.Tx, meta envelope.Metadata, p application.Proposal,
		now time.Time) error
}

// ProposalLapse is a passed proposal that could not be applied, and why: a
// screen word (legislature.lapse.<reason>).
type ProposalLapse struct{ Reason string }

func (l *ProposalLapse) Error() string { return "handlers: the proposal lapsed: " + l.Reason }

// Lapse reasons.
const (
	LapseCooldown = "cooldown"
	LapseBounds   = "bounds"
	LapseGone     = "gone"
	LapseChanged  = "changed"
)

// NewLegislatureHandler builds the handler.
func NewLegislatureHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, dir application.GovernanceDirectory, rules LegislatureRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *LegislatureHandler {
	if source == nil || ids == nil || dir == nil || cities == nil {
		panic("handlers: NewLegislatureHandler requires content, ids, cities and a governance directory")
	}
	if rules.VoteWindow <= 0 || rules.ListSize <= 0 || idempotencyTTL <= 0 {
		panic("handlers: NewLegislatureHandler requires a vote window, a list size and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &LegislatureHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, dir: dir, rules: rules,
		executors: map[string]ProposalExecutor{}, idempotencyTTL: idempotencyTTL, now: now}
}

// WithExecutor registers what takes an action once a body approves it.
func (h *LegislatureHandler) WithExecutor(action string, e ProposalExecutor) *LegislatureHandler {
	h.executors[action] = e
	return h
}

// LegislatureRequest names a proposal by its public number, and a vote.
type LegislatureRequest struct {
	No   string `json:"no,omitempty"`
	Vote string `json:"vote,omitempty"`
}

// LegislatureActionPayload is the jsonb of a proposal's scheduled close.
type LegislatureActionPayload struct {
	ReferenceID string `json:"reference_id"`
}

func (h *LegislatureHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// billRefusal carries a refused request out of a unit of work.
type billRefusal struct{ view screens.BillRefusalView }

func (r *billRefusal) Error() string { return "handlers: proposal refused: " + r.view.Kind }

func (h *LegislatureHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *billRefusal
	if stderrors.As(err, &r) {
		return screens.BillRefusal(h.screen(meta, lang), r.view), nil
	}
	if isSentinel(err, application.ErrBillNotFound) {
		return screens.BillRefusal(h.screen(meta, lang), screens.BillRefusalView{Kind: screens.BillRefusedNotFound}), nil
	}
	return nil, err
}

// ---------------------------------------------------------------------------
// Opening a proposal.

// BillDraft is a proposal before it is opened.
type BillDraft struct {
	Kind           string
	JurisdictionID string
	Subject        string
	Value          *int64
	Allocation     map[string]int64
	Args           map[string]string
	// Seat is the proposer's seat; ProposerID the player.
	Seat       application.Office
	ProposerID string
	// Body decides by Rule, Threshold and Quorum.
	Body, Rule, Threshold, Quorum string
}

// PolicyBill is the proposal of a lever change a vote must decide.
func PolicyBill(d application.PolicyDraft, proposerID string) BillDraft {
	b := BillDraft{Kind: application.ProposalLever, JurisdictionID: d.Jurisdiction.ID, Subject: d.Lever.Code,
		Seat: d.Seat, ProposerID: proposerID, Body: d.Body, Rule: d.Rule, Threshold: d.Threshold, Quorum: d.Quorum}
	if d.Lever.IsAllocation() {
		b.Allocation = d.Value.Allocation
		if b.Allocation == nil {
			b.Allocation = map[string]int64{}
		}
	} else {
		v := d.Value.Value
		b.Value = &v
	}
	return b
}

// OpenBill opens a proposal: the body's seats counted, the close put on the
// schedule at the end of the window (REAL time), the groups of the place's
// cities told. A proposer who sits in the body votes yes at once, which may
// already decide it. It refuses a second proposal of the same subject in the
// same place with a refusal the screens name.
func (h *LegislatureHandler) OpenBill(ctx context.Context, tx application.Tx, meta envelope.Metadata, b BillDraft,
	now time.Time,
) (application.Proposal, error) {
	chain, err := tx.Governance().ActingChain(ctx, b.Body, b.JurisdictionID)
	if err != nil {
		return application.Proposal{}, err
	}
	seats := 0
	var member *application.Office
	if len(chain) > 0 {
		seats = len(chain[0].Seats)
		for i, s := range chain[0].Seats {
			if s.HolderPlayerID == b.ProposerID && b.ProposerID != "" {
				member = &chain[0].Seats[i]
			}
		}
	}
	if seats < 1 {
		return application.Proposal{}, errors.Internal(stderrors.New("handlers: a proposal to a body with no seats"))
	}
	rule := b.Rule
	if rule == "" {
		rule = legislature.Majority
	}
	p := application.Proposal{
		ID: h.ids.NewID(), Kind: b.Kind, JurisdictionID: b.JurisdictionID, Subject: b.Subject,
		Value: b.Value, Allocation: b.Allocation, Args: b.Args,
		ProposedBy: b.ProposerID, ProposerOfficeID: b.Seat.ID, ProposerOffice: b.Seat.OfficeCode,
		Body: b.Body, Rule: rule, Threshold: b.Threshold, Quorum: b.Quorum, Seats: seats,
		OpenedAt: now, ClosesAt: now.Add(h.rules.VoteWindow), CloseActionID: h.ids.NewID(),
		ContentVersion: h.content.Current().Version(),
	}
	payload, err := json.Marshal(LegislatureActionPayload{ReferenceID: p.ID})
	if err != nil {
		return p, err
	}
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: p.CloseActionID, ActionType: application.LegislatureCloseActionType, ActorType: "system",
		ReferenceType: application.ProposalReference, ReferenceID: p.ID, Payload: payload,
		StartedAt: now, FinishAt: p.ClosesAt,
	}); err != nil {
		return p, err
	}
	stored, err := tx.Legislature().Open(ctx, p)
	if isSentinel(err, application.ErrBillUnderWay) {
		return p, &billRefusal{view: screens.BillRefusalView{Kind: screens.BillRefusedUnderWay, Body: b.Body}}
	}
	if err != nil {
		return p, err
	}
	view, cities, err := h.billView(ctx, tx, stored, now)
	if err != nil {
		return stored, err
	}
	if err := appendDomainEvent(ctx, tx, meta, "legislature", "proposed", stored.ID, billEvent(view, cities,
		map[string]any{"window_seconds": int64(h.rules.VoteWindow / time.Second)})); err != nil {
		return stored, err
	}
	if member != nil {
		if _, err := tx.Legislature().CastVote(ctx, application.ProposalVote{ProposalID: stored.ID,
			OfficeID: member.ID, PlayerID: b.ProposerID, Vote: application.VoteYes, CastAt: now}); err != nil {
			return stored, err
		}
		if _, err := h.settle(ctx, tx, meta, &stored, false, now); err != nil {
			return stored, err
		}
	}
	return stored, nil
}

// billEvent is the payload of a legislature event: what a group line and a
// private notice need, and the cities whose groups read it.
func billEvent(v screens.BillView, cities []string, extra map[string]any) map[string]any {
	out := map[string]any{
		"no": v.No, "status": v.Status, "lapse": v.LapsedWhy, "yes": v.Yes, "nay": v.Nay,
		"place_kind": v.Place.Kind, "place_code": v.Place.Code, "place_name": v.Place.Name,
		"subject_kind": v.Subject.Kind, "subject": v.Subject.Code, "lever_type": v.Subject.LeverType,
		"value": v.Subject.Value, "allocation": v.Subject.Allocation, "categories": v.Subject.Categories,
		"office": v.Office, "by_name": v.By.Name, "by_code": v.By.Code, "body": v.Body, "city_ids": cities,
	}
	if v.Subject.Target != nil {
		out["target_code"], out["target_name"] = v.Subject.Target.Code, v.Subject.Target.Name
	}
	for k, x := range extra {
		out[k] = x
	}
	return out
}

// ---------------------------------------------------------------------------
// Reading.

// placeCities lists the cities whose groups hear of a place's proposal: the
// city itself, or every city of a country.
func (h *LegislatureHandler) placeCities(ctx context.Context, tx application.Tx, j application.Jurisdiction) ([]string, error) {
	if j.Kind == "city" {
		c, err := h.cities.ByCode(ctx, j.Code)
		if isSentinel(err, application.ErrCityNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []string{c.ID}, nil
	}
	return cityIDsOf(ctx, tx, j.ID)
}

// billView gathers one proposal for a screen, and the cities of its place.
func (h *LegislatureHandler) billView(ctx context.Context, tx application.Tx, p application.Proposal, now time.Time,
) (screens.BillView, []string, error) {
	j, err := tx.Governance().Jurisdiction(ctx, p.JurisdictionID)
	if err != nil {
		return screens.BillView{}, nil, err
	}
	by, err := playerNamed(ctx, tx, p.ProposedBy)
	if err != nil {
		return screens.BillView{}, nil, err
	}
	v := screens.BillView{No: p.No, Place: govPlace(j), Office: p.ProposerOffice, By: by, Body: p.Body,
		Rule: p.Rule, Threshold: p.Threshold, Quorum: p.Quorum, Seats: p.Seats, Status: p.Status,
		LapsedWhy: p.LapseReason, ClosesAt: p.ClosesAt, Remaining: max(p.ClosesAt.Sub(now), 0)}
	if v.Subject, err = h.subject(ctx, tx, p); err != nil {
		return v, nil, err
	}
	votes, err := tx.Legislature().Votes(ctx, p.ID)
	if err != nil {
		return v, nil, err
	}
	for _, vote := range votes {
		who, err := playerNamed(ctx, tx, vote.PlayerID)
		if err != nil {
			return v, nil, err
		}
		v.Votes = append(v.Votes, screens.BillVoteLine{Player: who, Yes: vote.Vote == application.VoteYes})
		if vote.Vote == application.VoteYes {
			v.Yes++
		} else {
			v.Nay++
		}
	}
	if p.YesVotes != nil {
		v.Yes, v.Nay = *p.YesVotes, *p.NoVotes
	}
	held, err := heldSeats(ctx, tx, p.Body, p.JurisdictionID)
	if err != nil {
		return v, nil, err
	}
	v.Held = len(held)
	if rule, err := billRule(p); err == nil {
		v.Needs = legislature.Needs(rule, v.Held)
	}
	cities, err := h.placeCities(ctx, tx, j)
	return v, cities, err
}

// subject describes what a proposal would do.
func (h *LegislatureHandler) subject(ctx context.Context, tx application.Tx, p application.Proposal) (screens.BillSubject, error) {
	s := screens.BillSubject{Kind: p.Kind, Code: p.Subject, Allocation: p.Allocation}
	if p.Value != nil {
		s.Value = *p.Value
	}
	if p.Kind == application.ProposalAction {
		if code := p.Args["target"]; code != "" {
			if t, err := tx.Diplomacy().CountryByCode(ctx, code); err == nil {
				place := govPlace(t)
				s.Target = &place
			}
		}
		return s, nil
	}
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return s, err
	}
	for _, l := range levers {
		if l.Code == p.Subject {
			s.LeverType, s.Categories = l.Type, l.Categories
		}
	}
	if s.LeverType == "" && p.Allocation != nil {
		s.LeverType = application.LeverAllocation
	}
	return s, nil
}

// heldSeats is the held seats of a body in a place.
func heldSeats(ctx context.Context, tx application.Tx, body, jurisdictionID string) ([]application.Office, error) {
	chain, err := tx.Governance().ActingChain(ctx, body, jurisdictionID)
	if err != nil || len(chain) == 0 {
		return nil, err
	}
	var out []application.Office
	for _, s := range chain[0].Seats {
		if !s.Vacant() {
			out = append(out, s)
		}
	}
	return out, nil
}

// billRule is a proposal's rule for the domain.
func billRule(p application.Proposal) (legislature.Rule, error) {
	r := legislature.Rule{Kind: p.Rule}
	if p.Threshold != "" {
		num, den, err := content.ParseFraction(p.Threshold)
		if err != nil {
			return r, err
		}
		r.Num, r.Den = num, den
	}
	if p.Quorum != "" {
		num, den, err := content.ParseFraction(p.Quorum)
		if err != nil {
			return r, err
		}
		r.QuorumNum, r.QuorumDen = num, den
	}
	return r, r.Validate()
}

// places are the jurisdictions whose proposals a player sees: those of the
// city they stand in and of the city they live in, every one above them,
// and those of every seat they hold.
func (h *LegislatureHandler) places(ctx context.Context, tx application.Tx, p *application.Player) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(cityID string) error {
		if cityID == "" {
			return nil
		}
		c, err := h.cities.ByID(ctx, cityID)
		if err != nil {
			return err
		}
		up, err := chain(ctx, tx, c.JurisdictionID)
		if err != nil {
			return err
		}
		for _, j := range up {
			if j.Kind != "world" && !seen[j.ID] {
				seen[j.ID] = true
				out = append(out, j.ID)
			}
		}
		return nil
	}
	if p.CityID != nil {
		if err := add(*p.CityID); err != nil {
			return nil, err
		}
	}
	home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if err := add(home); err != nil {
		return nil, err
	}
	held, err := tx.Governance().SeatsHeldBy(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, s := range held {
		if !seen[s.JurisdictionID] {
			seen[s.JurisdictionID] = true
			out = append(out, s.JurisdictionID)
		}
	}
	return out, nil
}

// List handles law.list: the proposals of the player's places, open
// first.
func (h *LegislatureHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.BillsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		ids, err := h.places(ctx, tx, p)
		if err != nil {
			return err
		}
		bills, err := tx.Legislature().List(ctx, ids, h.rules.ListSize)
		if err != nil {
			return err
		}
		now := h.now()
		for _, b := range bills {
			v, _, err := h.billView(ctx, tx, b, now)
			if err != nil {
				return err
			}
			view.Bills = append(view.Bills, v)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Bills(h.screen(meta, lang), view), nil
}

// parseBillNo reads a proposal's public number.
func parseBillNo(raw string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#")), 10, 64)
	return n, err == nil && n > 0
}

// View handles law.view: one proposal.
func (h *LegislatureHandler) View(ctx context.Context, meta envelope.Metadata, req LegislatureRequest) (*presenter.Response, error) {
	return h.view(ctx, meta, req, "")
}

func (h *LegislatureHandler) view(ctx context.Context, meta envelope.Metadata, req LegislatureRequest, notice string,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	if !ok {
		return h.List(ctx, meta)
	}
	lang := meta.Language
	var view screens.BillView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		b, err := tx.Legislature().ProposalByNo(ctx, no, false)
		if err != nil {
			return err
		}
		if view, _, err = h.billView(ctx, tx, *b, h.now()); err != nil {
			return err
		}
		view.CanVote, err = h.mayVote(ctx, tx, *b, p.ID)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	view.Notice = notice
	return screens.Bill(h.screen(meta, lang), view), nil
}

// mayVote reports whether the player sits in the body and has not voted.
func (h *LegislatureHandler) mayVote(ctx context.Context, tx application.Tx, b application.Proposal, playerID string) (bool, error) {
	if b.Status != application.ProposalOpen {
		return false, nil
	}
	seat, err := memberSeat(ctx, tx, b, playerID)
	if err != nil || seat == nil {
		return false, err
	}
	votes, err := tx.Legislature().Votes(ctx, b.ID)
	if err != nil {
		return false, err
	}
	for _, v := range votes {
		if v.PlayerID == playerID || v.OfficeID == seat.ID {
			return false, nil
		}
	}
	return true, nil
}

// memberSeat is the player's seat in the body a proposal is before, nil for
// none.
func memberSeat(ctx context.Context, tx application.Tx, b application.Proposal, playerID string) (*application.Office, error) {
	held, err := heldSeats(ctx, tx, b.Body, b.JurisdictionID)
	if err != nil {
		return nil, err
	}
	for i := range held {
		if held[i].HolderPlayerID == playerID {
			return &held[i], nil
		}
	}
	return nil, nil
}

// Vote handles law.vote: a member's vote, once per seat; the vote
// that settles the proposal decides it.
func (h *LegislatureHandler) Vote(ctx context.Context, meta envelope.Metadata, req LegislatureRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := parseBillNo(req.No)
	vote := strings.TrimSpace(req.Vote)
	if !ok || (vote != application.VoteYes && vote != application.VoteNo) {
		return nil, errors.InvalidInput("law.vote names no proposal or no vote")
	}
	lang := meta.Language
	notice := screens.BillNoticeVoted
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			notice = ""
			return nil
		}
		b, err := tx.Legislature().ProposalByNo(ctx, no, true)
		if err != nil {
			return err
		}
		if b.Status != application.ProposalOpen {
			notice = screens.BillNoticeClosed
			return nil
		}
		seat, err := memberSeat(ctx, tx, *b, p.ID)
		if err != nil {
			return err
		}
		if seat == nil {
			return &billRefusal{view: screens.BillRefusalView{Kind: screens.BillRefusedNotMember, No: b.No, Body: b.Body}}
		}
		now := h.now()
		cast, err := tx.Legislature().CastVote(ctx, application.ProposalVote{ProposalID: b.ID, OfficeID: seat.ID,
			PlayerID: p.ID, Vote: vote, CastAt: now})
		if err != nil {
			return err
		}
		if !cast {
			notice = screens.BillNoticeAlreadyVoted
			return nil
		}
		_, err = h.settle(ctx, tx, meta, b, false, now)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.view(ctx, meta, LegislatureRequest{No: strconv.FormatInt(no, 10)}, notice)
}

// Close handles law.close from the SCHEDULER: a proposal's window
// ended. It runs once: only an open proposal moves, under its row lock.
func (h *LegislatureHandler) Close(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	id := req.ReferenceID
	if id == "" && len(req.Payload) > 0 {
		var in LegislatureActionPayload
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("proposal close payload is unreadable").WithCause(err)
		}
		id = in.ReferenceID
	}
	if id == "" {
		return nil, errors.InvalidInput("proposal close names nothing")
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		b, err := tx.Legislature().Proposal(ctx, id, true)
		if isSentinel(err, application.ErrBillNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if b.Status != application.ProposalOpen {
			return nil
		}
		now := h.now()
		if now.Before(b.ClosesAt) {
			return errors.Internal(stderrors.New("handlers: a proposal closed before its window ended"))
		}
		_, err = h.settle(ctx, tx, meta, b, true, now)
		return err
	})
}

// settle decides a proposal if its votes, or its close, settle it: applied
// when it passed, recorded, and made public. It reports whether it decided.
// The proposal must be locked by the caller's transaction (or just opened
// in it).
func (h *LegislatureHandler) settle(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	b *application.Proposal, final bool, now time.Time,
) (bool, error) {
	rule, err := billRule(*b)
	if err != nil {
		return false, errors.Internal(err)
	}
	votes, err := tx.Legislature().Votes(ctx, b.ID)
	if err != nil {
		return false, err
	}
	held, err := heldSeats(ctx, tx, b.Body, b.JurisdictionID)
	if err != nil {
		return false, err
	}
	t := legislature.Tally{Seats: b.Seats, Held: len(held)}
	for _, v := range votes {
		if v.Vote == application.VoteYes {
			t.Yes++
		} else {
			t.No++
		}
	}
	outcome := legislature.Decide(rule, t, final)
	if outcome == legislature.Pending {
		return false, nil
	}
	b.YesVotes, b.NoVotes, b.DecidedAt = &t.Yes, &t.No, &now
	b.Status = application.ProposalFailed
	if outcome == legislature.Passed {
		b.Status = application.ProposalPassed
		if err := h.apply(ctx, tx, meta, b, now); err != nil {
			var lapse *ProposalLapse
			if !stderrors.As(err, &lapse) {
				return false, err
			}
			b.Status, b.LapseReason = application.ProposalLapsed, lapse.Reason
		}
	}
	fresh, err := tx.Legislature().Decide(ctx, *b)
	if err != nil || !fresh {
		return false, err
	}
	view, cities, err := h.billView(ctx, tx, *b, now)
	if err != nil {
		return true, err
	}
	payload := billEvent(view, cities, map[string]any{"player_id": b.ProposedBy})
	return true, appendDomainEvent(ctx, tx, meta, "legislature", "decided", b.ID, payload)
}

// apply carries out a passed proposal.
func (h *LegislatureHandler) apply(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	b *application.Proposal, now time.Time,
) error {
	if b.Kind == application.ProposalAction {
		e, ok := h.executors[b.Subject]
		if !ok {
			return &ProposalLapse{Reason: LapseGone}
		}
		return e.ExecuteProposal(ctx, tx, meta, *b, now)
	}
	v := application.ProposedValue{Allocation: b.Allocation}
	if b.Value != nil {
		v.Value = *b.Value
	}
	change, err := application.ApplyVotedPolicy(ctx, tx, b.ProposedBy, b.ProposerOfficeID, b.ProposerOffice,
		b.JurisdictionID, b.Subject, v, now)
	switch {
	case isSentinel(err, application.ErrPolicyCooldown):
		return &ProposalLapse{Reason: LapseCooldown}
	case isSentinel(err, application.ErrPolicyOutOfBounds), isSentinel(err, application.ErrInvalidAllocation):
		return &ProposalLapse{Reason: LapseBounds}
	case isSentinel(err, application.ErrUnknownLever), isSentinel(err, application.ErrWrongJurisdiction),
		isSentinel(err, application.ErrLeverKindUnsupported):
		return &ProposalLapse{Reason: LapseGone}
	case err != nil:
		return err
	}
	b.PolicyValueID = change.Setting.ID
	place, err := tx.Governance().Jurisdiction(ctx, b.JurisdictionID)
	if err != nil {
		return err
	}
	return appendPolicyChanged(ctx, tx, meta, place, change)
}
