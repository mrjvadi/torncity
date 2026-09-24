package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file serves player-held offices to players
// (docs/adr/0015-player-held-offices.md, phase 1 of ADR 0016): the city hall
// screen, the office holder's screen, the flow that changes a policy, and the
// public record of changes.
//
// Every policy value on these screens is read through application.PolicyReader
// and changed only through application.SetPolicy. Nothing here decides who may
// change a lever, what bounds it has or how long it waits: the handler asks,
// formats, and relays SetPolicy's refusals.

// PolicyChangedEvent is the event name of a policy change, and
// PolicyChangedSubject the subject it is published on.
const PolicyChangedEvent = "governance.policy.changed"

// PolicyChangedSubject is where a policy change is published.
var PolicyChangedSubject = subjects.Event("governance", "policy_changed")

// GovCityRequest is the payload of gov.city: a city code, or nothing for the
// player's own city.
type GovCityRequest struct {
	City string `json:"city,omitempty"`
}

// GovHistoryRequest is the payload of gov.history.
type GovHistoryRequest struct {
	City string `json:"city,omitempty"`
	Page string `json:"page,omitempty"`
}

// GovLeverRequest is the payload of gov.lever, gov.confirm and gov.set: the
// lever's content code, the code of the place it is set in and, where it
// applies, the value proposed.
type GovLeverRequest struct {
	Lever string `json:"lever,omitempty"`
	Place string `json:"place,omitempty"`
	Value string `json:"value,omitempty"`
}

// GovernanceSteps are the step sizes of the value buttons, as divisors of a
// lever's range: a range of 2500 with divisors 100 and 10 steps by 25 and 250.
// Tuning, from configuration.
type GovernanceSteps struct {
	FineDivisor   int64
	CoarseDivisor int64
}

// GovernanceHandler serves the gov.* commands.
type GovernanceHandler struct {
	uow    application.UnitOfWork
	msgs   Translator
	cities application.CityRepository
	dir    application.GovernanceDirectory
	policy application.PolicyReader
	steps  GovernanceSteps

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewGovernanceHandler wires the handler.
func NewGovernanceHandler(
	uow application.UnitOfWork,
	msgs Translator,
	cities application.CityRepository,
	dir application.GovernanceDirectory,
	policy application.PolicyReader,
	steps GovernanceSteps,
	pageSize int,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *GovernanceHandler {
	if cities == nil || dir == nil || policy == nil {
		panic("handlers: NewGovernanceHandler requires cities, a governance directory and a policy reader")
	}
	if steps.FineDivisor <= 0 || steps.CoarseDivisor <= 0 {
		panic("handlers: NewGovernanceHandler requires positive step divisors")
	}
	if pageSize <= 0 {
		panic("handlers: NewGovernanceHandler requires a positive page size")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewGovernanceHandler requires a positive idempotency ttl")
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &GovernanceHandler{
		uow: uow, msgs: msgs, cities: cities, dir: dir, policy: policy, steps: steps,
		pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now,
	}
}

// viewer reads the player behind a request, in a short read-only unit of
// work: the governance reads that follow run on the directory's own
// connection and need no transaction.
func (h *GovernanceHandler) viewer(ctx context.Context, meta envelope.Metadata) (*application.Player, string, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, meta.Language, err
	}
	var p *application.Player
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		p, err = tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		return err
	})
	if err != nil {
		return nil, meta.Language, err
	}
	return p, RenderLanguage(meta, p), nil
}

func (h *GovernanceHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
}

// City handles gov.city: the offices of a city and of every place above it,
// who holds them, and the policies in force there.
func (h *GovernanceHandler) City(ctx context.Context, meta envelope.Metadata, req GovCityRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)

	city, err := h.cityOf(ctx, p, req.City)
	if err != nil {
		return nil, err
	}
	if city == nil {
		return screens.CityGovernance(c, screens.CityGovView{NoCity: true}), nil
	}
	if city.JurisdictionID == "" {
		return screens.PolicyRefused(c, screens.PolicyRefusalView{Err: application.ErrJurisdictionNotFound}), nil
	}

	view, err := h.cityView(ctx, city)
	if err != nil {
		if screens.IsGovernanceRefusal(err) {
			return screens.PolicyRefused(c, screens.PolicyRefusalView{Err: err, Now: h.now()}), nil
		}
		return nil, err
	}
	held, err := h.dir.SeatsHeldBy(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	view.HoldsOffice = len(held) > 0
	return screens.CityGovernance(c, view), nil
}

// cityOf is the city a request names, or the player's own; nil when the
// request names none and the player is in none.
func (h *GovernanceHandler) cityOf(ctx context.Context, p *application.Player, code string) (*application.City, error) {
	if code = strings.ToLower(strings.TrimSpace(code)); code != "" {
		return h.cities.ByCode(ctx, code)
	}
	if p.CityID == nil || *p.CityID == "" {
		return nil, nil
	}
	return h.cities.ByID(ctx, *p.CityID)
}

// cityView gathers the city hall screen.
func (h *GovernanceHandler) cityView(ctx context.Context, city *application.City) (screens.CityGovView, error) {
	now := h.now()
	places, err := h.dir.Ancestry(ctx, city.JurisdictionID)
	if err != nil {
		return screens.CityGovView{}, err
	}
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return screens.CityGovView{}, err
	}
	offices, err := h.dir.Offices(ctx)
	if err != nil {
		return screens.CityGovView{}, err
	}
	ids := make([]string, 0, len(places))
	for _, pl := range places {
		ids = append(ids, pl.ID)
	}
	seats, err := h.dir.Seats(ctx, ids)
	if err != nil {
		return screens.CityGovView{}, err
	}

	// Values first, so every player named anywhere on the screen is named
	// in one read.
	type leverAt struct {
		def   application.LeverDefinition
		value application.PolicyValue
	}
	values := map[string][]leverAt{}
	names := newNameSet()
	for _, pl := range places {
		for _, l := range levers {
			if l.Jurisdiction != pl.Kind {
				continue
			}
			v, err := h.policy.Get(ctx, pl.ID, l.Code)
			if stderrors.Is(err, application.ErrLeverKindUnsupported) {
				// Declared and stored, with no behaviour yet: there is no
				// value to show, and no reason to fail the screen over it.
				continue
			}
			if err != nil {
				return screens.CityGovView{}, err
			}
			values[pl.ID] = append(values[pl.ID], leverAt{def: l, value: v})
			names.addValue(v)
		}
	}
	for _, s := range seats {
		names.add(s.HolderPlayerID)
	}
	named, err := h.dir.PlayerNames(ctx, names.ids())
	if err != nil {
		return screens.CityGovView{}, err
	}

	view := screens.CityGovView{City: govPlace(places[0])}
	for _, pl := range places {
		section := screens.GovSection{Place: govPlace(pl)}
		for _, o := range officeOrder(offices, levers, pl.Kind) {
			section.Offices = append(section.Offices, officeView(o, offices, seats, pl.ID, named))
		}
		for _, lv := range values[pl.ID] {
			section.Levers = append(section.Levers, govLever(lv.def, lv.value, named, now))
		}
		view.Sections = append(view.Sections, section)
	}
	return view, nil
}

// officeOrder lists a level's offices the way a reader expects them: each
// office that decides a policy, followed by its deputies, then every other
// office by code.
func officeOrder(offices []application.OfficeDefinition, levers []application.LeverDefinition, kind string,
) []application.OfficeDefinition {
	byCode := map[string]application.OfficeDefinition{}
	for _, o := range offices {
		if o.Jurisdiction == kind {
			byCode[o.Code] = o
		}
	}
	var principals []string
	seen := map[string]bool{}
	for _, l := range levers {
		if _, ok := byCode[l.HeldBy]; ok && !seen[l.HeldBy] {
			seen[l.HeldBy] = true
			principals = append(principals, l.HeldBy)
		}
	}
	sort.Strings(principals)

	var out []application.OfficeDefinition
	placed := map[string]bool{}
	for _, code := range principals {
		for c := code; c != ""; c = byCode[c].Deputy {
			o, ok := byCode[c]
			if !ok || placed[c] {
				break
			}
			placed[c] = true
			out = append(out, o)
		}
	}
	var rest []string
	for code := range byCode {
		if !placed[code] {
			rest = append(rest, code)
		}
	}
	sort.Strings(rest)
	for _, code := range rest {
		out = append(out, byCode[code])
	}
	return out
}

// officeView is one office of one place: its holders, or, while it is
// vacant, the deputy acting for it.
func officeView(o application.OfficeDefinition, offices []application.OfficeDefinition, seats []application.Office,
	jurisdictionID string, named map[string]application.PlayerName,
) screens.GovOffice {
	view := screens.GovOffice{Code: o.Code, Seats: o.Seats}

	deputy := map[string]string{}
	for _, d := range offices {
		deputy[d.Code] = d.Deputy
	}
	var chain []application.OfficeLink
	seen := map[string]bool{}
	for code := o.Code; code != "" && !seen[code]; code = deputy[code] {
		seen[code] = true
		link := application.OfficeLink{OfficeCode: code}
		for _, s := range seats {
			if s.OfficeCode == code && s.JurisdictionID == jurisdictionID {
				link.Seats = append(link.Seats, s)
			}
		}
		chain = append(chain, link)
	}

	acting := application.ActingForChain(chain)
	switch {
	case acting == nil:
	case !acting.Deputy:
		view.Holders = govPlayers(acting.Holders, named)
	default:
		view.ActingCode = acting.OfficeCode
		view.Acting = govPlayers(acting.Holders, named)
	}
	return view
}

// Office handles gov.office: every seat the player holds and the policies it
// lets them change now.
func (h *GovernanceHandler) Office(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	now := h.now()

	held, err := h.dir.SeatsHeldBy(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var levers []application.LeverDefinition
	if len(held) > 0 {
		if levers, err = h.dir.Levers(ctx); err != nil {
			return nil, err
		}
	}

	type seatValues struct {
		seat   application.Office
		place  application.Jurisdiction
		act    []application.LeverDefinition
		actV   []application.PolicyValue
		vote   []application.LeverDefinition
		voteV  []application.PolicyValue
		acting string
	}
	var rows []seatValues
	names := newNameSet()
	for _, s := range held {
		places, err := h.dir.Ancestry(ctx, s.JurisdictionID)
		if err != nil {
			return nil, err
		}
		row := seatValues{seat: s, place: places[0]}
		for _, l := range levers {
			if l.Jurisdiction != row.place.Kind {
				continue
			}
			vote := l.DecisionRule != "" && l.DecisionRule != application.DecisionSingle
			if vote && l.HeldBy != s.OfficeCode {
				continue
			}
			v, err := h.policy.Get(ctx, row.place.ID, l.Code)
			if stderrors.Is(err, application.ErrLeverKindUnsupported) {
				continue
			}
			if err != nil {
				return nil, err
			}
			switch {
			case vote:
				row.vote, row.voteV = append(row.vote, l), append(row.voteV, v)
			case actsFor(v.Acting, s, p.ID):
				row.act, row.actV = append(row.act, l), append(row.actV, v)
				if v.Acting.Deputy {
					row.acting = l.HeldBy
				}
			default:
				continue
			}
			names.addValue(v)
		}
		rows = append(rows, row)
	}
	named, err := h.dir.PlayerNames(ctx, names.ids())
	if err != nil {
		return nil, err
	}

	appointees, err := h.appointees(ctx, held, named)
	if err != nil {
		return nil, err
	}

	var view screens.MyOfficeView
	for i, r := range rows {
		seat := screens.GovSeat{Office: r.seat.OfficeCode, Place: govPlace(r.place), ActingFor: r.acting,
			Appointees: appointees[i]}
		for i, l := range r.act {
			seat.Levers = append(seat.Levers, govLever(l, r.actV[i], named, now))
		}
		for i, l := range r.vote {
			seat.VoteLevers = append(seat.VoteLevers, govLever(l, r.voteV[i], named, now))
		}
		view.Seats = append(view.Seats, seat)
	}
	return screens.MyOffice(c, view), nil
}

// appointees lists, for each seat held, the seats of the same place whose
// office it appoints to (a vacant one may be filled) or may remove the
// holder of (a held one may be vacated). named gains the holders.
func (h *GovernanceHandler) appointees(ctx context.Context, held []application.Office,
	named map[string]application.PlayerName,
) ([][]screens.GovAppointee, error) {
	out := make([][]screens.GovAppointee, len(held))
	if len(held) == 0 {
		return out, nil
	}
	defs, err := h.dir.Offices(ctx)
	if err != nil {
		return nil, err
	}
	for i, seat := range held {
		places, err := h.dir.Ancestry(ctx, seat.JurisdictionID)
		if err != nil {
			return nil, err
		}
		var related []application.OfficeDefinition
		for _, d := range defs {
			if d.AppointedBy == seat.OfficeCode || hasCode(d.CanBeRemovedBy, seat.OfficeCode) {
				related = append(related, d)
			}
		}
		if len(related) == 0 {
			continue
		}
		seats, err := h.dir.Seats(ctx, []string{seat.JurisdictionID})
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, s := range seats {
			if s.HolderPlayerID != "" {
				if _, ok := named[s.HolderPlayerID]; !ok {
					ids = append(ids, s.HolderPlayerID)
				}
			}
		}
		if len(ids) > 0 {
			more, err := h.dir.PlayerNames(ctx, ids)
			if err != nil {
				return nil, err
			}
			for k, v := range more {
				named[k] = v
			}
		}
		for _, d := range related {
			for _, s := range seats {
				if s.OfficeCode != d.Code || s.HolderPlayerID == seat.HolderPlayerID {
					continue
				}
				a := screens.GovAppointee{Office: d.Code, Place: govPlace(places[0]), Seat: s.Seat,
					Holder: govPlayer(s.HolderPlayerID, named)}
				if s.Vacant() {
					a.CanAppoint = d.AppointedBy == seat.OfficeCode
				} else {
					a.CanDismiss = hasCode(d.CanBeRemovedBy, seat.OfficeCode)
				}
				if a.CanAppoint || a.CanDismiss || !s.Vacant() || d.AppointedBy == seat.OfficeCode {
					out[i] = append(out[i], a)
				}
			}
		}
	}
	return out, nil
}

// actsFor reports whether the acting office for a lever is this seat's and
// the player sits in it.
func actsFor(acting *application.ActingOffice, seat application.Office, playerID string) bool {
	if acting == nil || acting.OfficeCode != seat.OfficeCode {
		return false
	}
	for _, h := range acting.Holders {
		if h.ID == seat.ID && h.HolderPlayerID == playerID {
			return true
		}
	}
	return false
}

// leverTarget is a lever request resolved: the definition, the place, and
// the value the resolver answers for it now.
type leverTarget struct {
	def   application.LeverDefinition
	place application.Jurisdiction
	value application.PolicyValue
	named map[string]application.PlayerName
}

func (t leverTarget) view(now time.Time) (screens.GovPlace, screens.GovLever) {
	return govPlace(t.place), govLever(t.def, t.value, t.named, now)
}

// target resolves a lever request and checks that the player is the one who
// may act on it — the same questions SetPolicy will ask again, asked here so
// the screens before the change do not offer what would be refused.
func (h *GovernanceHandler) target(ctx context.Context, p *application.Player, req GovLeverRequest) (*leverTarget, error) {
	code := strings.TrimSpace(req.Lever)
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return nil, err
	}
	var def *application.LeverDefinition
	for i := range levers {
		if levers[i].Code == code {
			def = &levers[i]
			break
		}
	}
	if def == nil {
		return nil, application.ErrUnknownLever
	}
	t := &leverTarget{def: *def}
	if t.place, err = h.dir.JurisdictionByCode(ctx, def.Jurisdiction, strings.TrimSpace(req.Place)); err != nil {
		return nil, err
	}
	if err := application.CheckLeverSupported(*def); err != nil {
		return t, err
	}
	if rule := def.DecisionRule; rule != "" && rule != application.DecisionSingle {
		return t, application.ErrPolicyRequiresVote.WithDetail("body", def.HeldBy)
	}
	if t.value, err = h.policy.Get(ctx, t.place.ID, def.Code); err != nil {
		return t, err
	}
	if !holds(t.value.Acting, p.ID) {
		return t, application.ErrNotOfficeHolder.WithDetail("office", def.HeldBy)
	}
	names := newNameSet()
	names.addValue(t.value)
	if t.named, err = h.dir.PlayerNames(ctx, names.ids()); err != nil {
		return t, err
	}
	return t, nil
}

// holds reports whether the player sits in the acting office.
func holds(acting *application.ActingOffice, playerID string) bool {
	if acting == nil {
		return false
	}
	for _, s := range acting.Holders {
		if s.HolderPlayerID == playerID {
			return true
		}
	}
	return false
}

// nextChangeIn is how long until the lever may change again, zero if now.
// Display only: SetPolicy is what enforces the cooldown.
func (h *GovernanceHandler) nextChangeIn(ctx context.Context, t *leverTarget, now time.Time) (time.Duration, error) {
	last, err := h.dir.LastPolicyChange(ctx, t.place.ID, t.def.Code)
	if err != nil || last == nil {
		return 0, err
	}
	if wait := last.Add(t.def.ChangeCooldown).Sub(now); wait > 0 {
		return wait, nil
	}
	return 0, nil
}

// refused renders a governance refusal as an answer, or reports that err is
// not one and must travel up as it is.
func (h *GovernanceHandler) refused(c screens.Context, t *leverTarget, err error, now time.Time) (*presenter.Response, bool) {
	if !screens.IsGovernanceRefusal(err) {
		return nil, false
	}
	v := screens.PolicyRefusalView{Err: err, Now: now}
	if t != nil && t.place.ID != "" {
		place, lever := t.view(now)
		v.Place, v.Lever = &place, &lever
	}
	return screens.PolicyRefused(c, v), true
}

// Lever handles gov.lever: one policy the player may change, a proposed value
// and the buttons that move it.
func (h *GovernanceHandler) Lever(ctx context.Context, meta envelope.Metadata, req GovLeverRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	return h.lever(ctx, h.screen(meta, lang), p, req)
}

func (h *GovernanceHandler) lever(ctx context.Context, c screens.Context, p *application.Player, req GovLeverRequest,
) (*presenter.Response, error) {
	now := h.now()
	t, err := h.target(ctx, p, req)
	if err != nil {
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	wait, err := h.nextChangeIn(ctx, t, now)
	if err != nil {
		return nil, err
	}

	d := t.def
	draft := t.value.Value
	if t.value.Pending != nil {
		draft = t.value.Pending.Value
	}
	if v, ok := parseValue(req.Value); ok {
		draft = v
	}
	draft = min(max(draft, d.Min), d.Max)

	span := d.Max - d.Min
	fine := span / h.steps.FineDivisor
	coarse := span / h.steps.CoarseDivisor
	if span > 0 && fine < 1 {
		fine = 1
	}
	if coarse <= fine {
		coarse = 0
	}

	place, lever := t.view(now)
	return screens.LeverEdit(c, screens.LeverEditView{
		Place: place, Lever: lever, Draft: draft,
		FineStep: fine, CoarseStep: coarse, NextChangeIn: wait,
	}), nil
}

// Confirm handles gov.confirm: the change spelled out, before it is made.
func (h *GovernanceHandler) Confirm(ctx context.Context, meta envelope.Metadata, req GovLeverRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	now := h.now()
	value, ok := parseValue(req.Value)
	if !ok {
		return nil, errors.InvalidInput("gov.confirm names no value")
	}

	t, err := h.target(ctx, p, req)
	if err == nil {
		err = h.precheck(ctx, t, value, now)
	}
	if err != nil {
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	place, lever := t.view(now)
	return screens.PolicyConfirm(c, screens.PolicyConfirmView{Place: place, Lever: lever, NewValue: value}), nil
}

// precheck refuses, with SetPolicy's own sentinels, a change the confirm
// screen would otherwise offer only for SetPolicy to refuse it a press later.
// SetPolicy asks again, under a lock, and is the one that counts.
func (h *GovernanceHandler) precheck(ctx context.Context, t *leverTarget, value int64, now time.Time) error {
	if value < t.def.Min || value > t.def.Max {
		return application.ErrPolicyOutOfBounds.
			WithDetail("min", t.def.Min).WithDetail("max", t.def.Max).WithDetail("value", value)
	}
	wait, err := h.nextChangeIn(ctx, t, now)
	if err != nil {
		return err
	}
	if wait > 0 {
		return application.ErrPolicyCooldown.WithDetail("available_at", now.Add(wait))
	}
	return nil
}

// Set handles gov.set: the change itself, through SetPolicy, with its public
// record and its event in the same transaction as the command's idempotency
// key. A redelivered press changes nothing and shows the policy as it stands.
func (h *GovernanceHandler) Set(ctx context.Context, meta envelope.Metadata, req GovLeverRequest) (*presenter.Response, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, err
	}
	value, ok := parseValue(req.Value)
	if !ok {
		return nil, errors.InvalidInput("gov.set names no value")
	}
	now := h.now()
	lang := meta.Language

	// The lever and the place are looked up first, to address the change;
	// who may make it, within which bounds and how often is SetPolicy's
	// question, asked inside the transaction.
	t := &leverTarget{}
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range levers {
		if l.Code == strings.TrimSpace(req.Lever) {
			t.def = l
		}
	}

	var (
		p      *application.Player
		change application.PolicyChange
		replay bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		if p, err = tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID); err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if t.def.Code == "" {
			return application.ErrUnknownLever
		}
		if t.place, err = h.dir.JurisdictionByCode(ctx, t.def.Jurisdiction, strings.TrimSpace(req.Place)); err != nil {
			return err
		}

		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			replay = true
			return nil
		}

		change, err = application.SetPolicy(ctx, tx, p.ID, t.place.ID, t.def.Code, value, now)
		if err != nil {
			return err
		}
		return appendPolicyChanged(ctx, tx, meta, t.place, change)
	})
	c := h.screen(meta, lang)
	if err != nil {
		// The refusal is written with the lever's bounds, which t carries
		// once the place is known.
		if resp, ok := h.refused(c, t, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	if replay {
		return h.lever(ctx, c, p, GovLeverRequest{Lever: t.def.Code, Place: t.place.Code})
	}

	place := govPlace(t.place)
	lever := screens.GovLever{Code: t.def.Code, Type: t.def.Type, HeldBy: t.def.HeldBy}
	return screens.PolicyAnnounced(c, screens.PolicyAnnouncedView{
		Place: place, Lever: lever,
		Old: change.OldValue, New: change.Setting.Value,
		In: change.Setting.EffectiveAt.Sub(now),
	}), nil
}

// appendPolicyChanged queues the public event of a change in the change's
// own transaction: a change is never announced without its event, nor its
// event without the change.
func appendPolicyChanged(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	place application.Jurisdiction, change application.PolicyChange,
) error {
	s := change.Setting
	ev, err := events.New(PolicyChangedEvent, "jurisdiction", place.ID, map[string]any{
		"change_id":         change.ID,
		"policy_value_id":   s.ID,
		"jurisdiction_id":   place.ID,
		"jurisdiction_kind": place.Kind,
		"jurisdiction_code": place.Code,
		"lever":             s.LeverCode,
		"office":            change.OfficeCode,
		"office_id":         s.OfficeID,
		"player_id":         s.SetByPlayerID,
		"old_value":         change.OldValue,
		"new_value":         s.Value,
		"set_at":            s.SetAt.UTC(),
		"effective_at":      s.EffectiveAt.UTC(),
	})
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  PolicyChangedSubject,
		Metadata: meta,
		Payload:  ev.Payload,
	})
}

// History handles gov.history: one page of the public record of policy
// changes in a city and the places above it, newest first.
func (h *GovernanceHandler) History(ctx context.Context, meta envelope.Metadata, req GovHistoryRequest) (*presenter.Response, error) {
	p, lang, err := h.viewer(ctx, meta)
	if err != nil {
		return nil, err
	}
	c := h.screen(meta, lang)
	now := h.now()
	page := parsePage(req.Page)

	city, err := h.cityOf(ctx, p, req.City)
	if err != nil {
		return nil, err
	}
	if city == nil {
		return screens.CityGovernance(c, screens.CityGovView{NoCity: true}), nil
	}
	places, err := h.dir.Ancestry(ctx, city.JurisdictionID)
	if err != nil {
		if resp, ok := h.refused(c, nil, err, now); ok {
			return resp, nil
		}
		return nil, err
	}
	byID := map[string]application.Jurisdiction{}
	ids := make([]string, 0, len(places))
	for _, pl := range places {
		byID[pl.ID] = pl
		ids = append(ids, pl.ID)
	}

	records, total, err := h.dir.PolicyHistory(ctx, ids, h.pageSize, (page-1)*h.pageSize)
	if err != nil {
		return nil, err
	}
	levers, err := h.dir.Levers(ctx)
	if err != nil {
		return nil, err
	}
	types := map[string]string{}
	for _, l := range levers {
		types[l.Code] = l.Type
	}
	names := newNameSet()
	for _, r := range records {
		names.add(r.SetByPlayerID)
	}
	named, err := h.dir.PlayerNames(ctx, names.ids())
	if err != nil {
		return nil, err
	}

	_, _, pages := pageWindow(total, page, h.pageSize)
	view := screens.GovHistoryView{City: govPlace(places[0]), Page: page, Pages: pages}
	for _, r := range records {
		view.Entries = append(view.Entries, screens.GovHistoryEntry{
			Place:       govPlace(byID[r.JurisdictionID]),
			Lever:       r.LeverCode,
			Type:        types[r.LeverCode],
			Office:      r.OfficeCode,
			By:          govPlayer(r.SetByPlayerID, named),
			Old:         r.OldValue,
			New:         r.NewValue,
			Ago:         now.Sub(r.SetAt),
			EffectiveIn: r.EffectiveAt.Sub(now),
		})
	}
	return screens.GovHistory(c, view), nil
}

// parseValue reads a proposed value off a callback argument.
func parseValue(raw string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return v, err == nil
}

func govPlace(j application.Jurisdiction) screens.GovPlace {
	return screens.GovPlace{Kind: j.Kind, Code: j.Code, Name: j.Name}
}

// govLever turns the resolver's answer into what a screen shows.
func govLever(d application.LeverDefinition, v application.PolicyValue, named map[string]application.PlayerName,
	now time.Time,
) screens.GovLever {
	l := screens.GovLever{
		Code: d.Code, Type: d.Type, Value: v.Value,
		Default: d.Default, Min: d.Min, Max: d.Max,
		FromOffice: v.Source == application.PolicyFromOffice,
		HeldBy:     d.HeldBy,
		Notice:     d.Notice, Cooldown: d.ChangeCooldown,
		Vote: d.DecisionRule != "" && d.DecisionRule != application.DecisionSingle,
	}
	if v.InForce != nil {
		l.SetBy = govPlayer(v.InForce.SetByPlayerID, named)
	}
	if v.Pending != nil {
		l.Pending = &screens.GovPending{
			Value: v.Pending.Value,
			In:    v.Pending.EffectiveAt.Sub(now),
			By:    govPlayer(v.Pending.SetByPlayerID, named),
		}
	}
	return l
}

func govPlayer(id string, named map[string]application.PlayerName) *screens.GovPlayer {
	n, ok := named[id]
	if !ok {
		return nil
	}
	return &screens.GovPlayer{Name: n.DisplayName, Code: n.PublicCode}
}

func govPlayers(seats []application.Office, named map[string]application.PlayerName) []screens.GovPlayer {
	out := make([]screens.GovPlayer, 0, len(seats))
	for _, s := range seats {
		if p := govPlayer(s.HolderPlayerID, named); p != nil {
			out = append(out, *p)
		} else {
			out = append(out, screens.GovPlayer{})
		}
	}
	return out
}

// nameSet collects the players a screen will name, so they are read at once.
type nameSet map[string]struct{}

func newNameSet() nameSet { return nameSet{} }

func (n nameSet) add(id string) {
	if id != "" {
		n[id] = struct{}{}
	}
}

func (n nameSet) addValue(v application.PolicyValue) {
	if v.InForce != nil {
		n.add(v.InForce.SetByPlayerID)
	}
	if v.Pending != nil {
		n.add(v.Pending.SetByPlayerID)
	}
	if v.Acting != nil {
		for _, s := range v.Acting.Holders {
			n.add(s.HolderPlayerID)
		}
	}
}

func (n nameSet) ids() []string {
	out := make([]string, 0, len(n))
	for id := range n {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
