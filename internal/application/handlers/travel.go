package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"github.com/mrjvadi/torncity/internal/domain/vehicle"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// TravelActionType is the value written into game_actions.action_type for a
// journey's arrival.
//
// It must stay equal to the action type the scheduler routes, which is the
// single-token "travel" mapped to the command travel.arrive. It is restated
// here rather than imported because internal/workers sits outside this layer
// and a use case must not depend on the worker that happens to drive it; the
// tests on both sides pin the spelling.
const TravelActionType = "travel"

// TransitFareLever is the city policy that sets the share, in basis points,
// of the full content fare a rider pays on every public transport journey
// departing from the city (configs/content/governance.yml). It is read only
// through application.PolicyReader: a mayor may have moved it.
const TransitFareLever = "city.transit_fare"

// Travel statuses as they are stored. They are the domain's values, written
// through so a row and a rule cannot disagree about what "in transit" is.
const (
	statusInTransit = string(travel.StatusInTransit)
	statusPending   = "pending"
)

// TravelOptionsRequest is the payload of travel.options: the destination, by
// city CODE.
type TravelOptionsRequest struct {
	City string `json:"city"`
}

// StartTravelRequest is the payload of travel.start.
//
// City is a city CODE and Mode a transport mode CODE: stable authored keys,
// never database identifiers, short enough for a callback address.
//
// Max is the fare the player was shown and agreed to, in minor units, as the
// button carried it. It is a ceiling, not a price: the journey is priced
// again and the handler's own fare is charged, and only if it is not above
// Max. A request without a mode or a readable Max departs nowhere; it is
// answered with the choice of transport, so no journey ever starts at a
// price the player did not see.
type StartTravelRequest struct {
	City string `json:"city"`
	Mode string `json:"mode,omitempty"`
	Max  string `json:"max,omitempty"`
	// Method is how the fare is paid: cash or card. Without one, a journey
	// that costs something is answered with its price and a button per way
	// to pay it; a free one departs.
	Method string `json:"method,omitempty"`
}

// TravelActionPayload is the jsonb this handler writes onto the game_actions
// row, and the shape it expects to read back when the arrival comes due.
//
// It repeats the player and the journey that the row's actor_id and
// reference_id already carry. That redundancy is deliberate: the scheduler
// forwards the jsonb untouched and promises nothing about the columns beyond
// them, so a payload that stands on its own cannot be made unreadable by a
// change to how the schedule is dispatched. Rows written before transport
// modes carry "speed" instead of "mode"; decoding ignores it.
type TravelActionPayload struct {
	TravelID string `json:"travel_id"`
	PlayerID string `json:"player_id"`
	ToCityID string `json:"to_city_id"`
	Mode     string `json:"mode,omitempty"`
}

// ArriveTravelRequest is the command the scheduler publishes when a journey
// comes due. It mirrors the scheduler's dispatch payload: the identity of the
// action, plus the jsonb the departure wrote.
type ArriveTravelRequest struct {
	ActionID      string          `json:"action_id"`
	ActorID       string          `json:"actor_id"`
	ReferenceType string          `json:"reference_type"`
	ReferenceID   string          `json:"reference_id"`
	Payload       json.RawMessage `json:"payload"`
}

// TransportOption is one mode that connects two cities, with the distance by
// that mode's own network and the name the content authored for it.
type TransportOption struct {
	Mode       travel.Mode
	Name       string
	DistanceKM int
	// Accepts is how its fare may be paid (payments.yml, narrowed by the
	// mode's own list); nil takes every method.
	Accepts payment.Accepts
}

// TransportNetwork answers which modes connect two cities.
//
// It is declared here rather than taken as a content snapshot because content
// reloads while the process runs: the composition layer hands in something
// that reads the current snapshot per call. Options answers from ONE
// snapshot, and reports that snapshot's content version, so one request never
// prices with two versions of the world.
type TransportNetwork interface {
	Options(fromCode, toCode string) (options []TransportOption, contentVersion int)
}

// TravelHandler serves the travel commands: the choice of transport a player
// opens, the departure they confirm, the journey they check on, and the
// arrival a clock produces.
type TravelHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	cities  application.CityRepository
	network TransportNetwork
	policy  application.PolicyReader

	timeScale      int
	arrivalXP      int64
	idempotencyTTL time.Duration
	now            func() time.Time

	// places is the live content the city places are read from: a journey
	// departs from the place its mode stops at, and lands there. Nil (a
	// test without places) departs from anywhere and lands at the default.
	places ContentSource
}

// WithPlaces makes the handler honour city places: a departure only from the
// place its mode stops at, an arrival put at that place of the destination.
func (h *TravelHandler) WithPlaces(source ContentSource) *TravelHandler {
	h.places = source
	return h
}

// departure refuses a departure by mode unless the player stands at the
// place of their city the mode leaves from. It is asked after every other
// refusal, just before the money.
func (h *TravelHandler) departure(ctx context.Context, tx application.Tx, p *application.Player, mode string, now time.Time) error {
	if h.places == nil {
		return nil
	}
	snap := h.places.Current()
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil || !w.placed() {
		return err
	}
	target, _ := w.cmap.ForMode(mode)
	err = needAt(w, snap, target, "place.need.departure", nil, gametime.Scale(h.timeScale), now)
	if n, ok := err.(*notHere); ok {
		n.view.Mode = mode
	}
	return err
}

// NewTravelHandler wires the handler.
//
// timeScale is the game clock (config game.time_scale) mapping game time to
// the wall clock, and
// arrivalXP is what landing awards. Both are tuning, injected rather than
// written here; both are rejected below one, because a journey that never
// scales down and an arrival worth nothing are wiring mistakes that look like
// design decisions once they are live.
//
// idempotencyTTL is rejected at zero for the reason NewProfileHandler gives:
// a zero TTL reads as an expiry already past, so every redelivery would run
// as if it were new.
//
// Stats, journeys, the schedule and the ledger are not constructor arguments:
// every one of them is written, and a write belongs to the unit of work, so
// the handler reaches them through the Tx it is given. Cities are read-only
// content and stay injected; see application.Tx. The policy reader is the one
// way a city's transit fare policy is read (ADR 0015).
func NewTravelHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	cities application.CityRepository,
	network TransportNetwork,
	policy application.PolicyReader,
	timeScale int,
	arrivalXP int64,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *TravelHandler {
	if network == nil {
		panic("handlers: NewTravelHandler requires a transport network")
	}
	if policy == nil {
		panic("handlers: NewTravelHandler requires a policy reader")
	}
	if timeScale < 1 || timeScale > travel.MaxTimeScale {
		panic("handlers: NewTravelHandler requires a time scale within 1..travel.MaxTimeScale")
	}
	if arrivalXP <= 0 {
		panic("handlers: NewTravelHandler requires a positive arrival xp award")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewTravelHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &TravelHandler{
		uow:            uow,
		ids:            ids,
		msgs:           msgs,
		cities:         cities,
		network:        network,
		policy:         policy,
		timeScale:      timeScale,
		arrivalXP:      arrivalXP,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

// screen builds the rendering context for a player's request, in lang (see
// RenderLanguage).
func (h *TravelHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// quotedOption is one priced way to make a journey.
type quotedOption struct {
	quote   travel.Quote
	name    string
	accepts payment.Accepts
	// own is the player's vehicle the mode is driven in, nil for a hire or
	// a seat (docs/adr/0024).
	own *ownVehicle
}

// ownVehicle is a vehicle of the player's that drives a journey: the piece,
// the good it is, and what is left of it.
type ownVehicle struct {
	piece     application.Piece
	item      screens.Named
	condition int64
}

// trip is what both the choice of transport and the departure work out
// before they diverge: who, from where, to where, and every priced option.
type trip struct {
	player         *application.Player
	from, to       *application.City
	options        []quotedOption
	contentVersion int
}

// planTrip runs the refusals a player meets before any choice — already
// travelling, nowhere to leave from, no such city, the same city, no way
// there — and prices every mode that makes the journey, at now.
//
// The price of each option reads two things that change: the departures on
// that route by that mode within the mode's demand window, and — for a public
// mode — the origin city's transit fare policy, through the resolver. Both
// are read the same way for the screen and for the departure, so the number a
// player is shown and the number the departure compares against come from
// one function.
func (h *TravelHandler) planTrip(ctx context.Context, tx application.Tx, p *application.Player, cityCode string, now time.Time) (trip, error) {
	t := trip{player: p}

	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return t, application.ErrAlreadyTravelling
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return t, err
	}
	if err := refuseAtWork(ctx, tx, p.ID); err != nil {
		return t, err
	}
	// Jail and a timed crime keep a player in town (docs/adr/0019).
	if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
		return t, err
	}

	if p.CityID == nil || *p.CityID == "" {
		// A player who is nowhere has no origin to plan from. It is the
		// same shape of problem as a missing destination, so it gets the
		// same sentinel.
		return t, application.ErrCityNotFound
	}
	from, err := h.cities.ByID(ctx, *p.CityID)
	if err != nil {
		return t, err
	}
	to, err := h.cities.ByCode(ctx, cityCode)
	if err != nil {
		return t, err
	}
	t.from, t.to = from, to
	if from.ID == to.ID {
		return t, errors.InvalidInput("travel cannot be planned").WithCause(travel.ErrSameCity)
	}
	// A travel ban between the two cities' countries closes the route
	// (docs/adr/0022): the one sanctions check.
	fromCountry, err := tx.Diplomacy().CountryOfCity(ctx, from.ID)
	if err != nil {
		return t, err
	}
	toCountry, err := tx.Diplomacy().CountryOfCity(ctx, to.ID)
	if err != nil {
		return t, err
	}
	if err := checkSanctions(ctx, tx, application.CheckSanctions(ctx, tx, diplomacy.Travel, fromCountry, toCountry, now),
		screens.AddrCities); err != nil {
		return t, err
	}
	// War closes the border between countries at war, and a city struck
	// lately to arrivals (docs/adr/0022, part two). Leaving is never
	// refused.
	if err := checkWarTravel(ctx, tx, h.cities, application.CheckWarTravel(ctx, tx, fromCountry, toCountry, to.ID, now),
		now, screens.AddrCities); err != nil {
		return t, err
	}

	options, version := h.network.Options(from.Code, to.Code)
	t.contentVersion = version
	if len(options) == 0 {
		// No mode's network reaches there: for the player, there is no
		// route. The screen reads the cause to choose the sentence.
		return t, errors.InvalidInput("travel cannot be planned").
			WithCause(fmt.Errorf("%w: no transport mode connects %q and %q", world.ErrNoRoute, from.Code, to.Code))
	}

	policyBPS := -1 // read once, and only if a public mode needs it
	for _, o := range options {
		pricing := travel.Pricing{PolicyBPS: travel.BasisPoints}
		if o.Mode.Public {
			if policyBPS < 0 {
				if policyBPS, err = h.transitFare(ctx, from); err != nil {
					return t, err
				}
				// The city's transport subsidy (its budget's transit line)
				// takes its share off what a rider pays.
				subsidy, err := budgetEffect(ctx, tx, from.ID, budget.EffectTransitFare)
				if err != nil {
					return t, err
				}
				policyBPS = int(max(budget.Lower(int64(policyBPS), subsidy), 1))
			}
			pricing.PolicyBPS = policyBPS
		}
		pricing.RecentDepartures, err = tx.Travels().RecentDepartures(ctx, from.ID, to.ID, o.Mode.Code,
			now.Add(-o.Mode.Demand.Window))
		if err != nil {
			return t, err
		}
		q, err := travel.QuoteJourney(worldCity(*from), worldCity(*to), o.Mode, o.DistanceKM, pricing, h.timeScale)
		if err != nil {
			// Every refusal the player can cause is behind us; what is left
			// is content or policy the rule cannot price, which is a fault.
			return t, errors.Internal(err)
		}
		opt := quotedOption{quote: q, name: o.Name, accepts: o.Accepts}
		if !o.Mode.Public {
			// The player's own vehicle of this mode drives it for its fuel
			// instead of the hire's fare (docs/adr/0024).
			own, fuel, err := h.ownVehicle(ctx, tx, p.ID, o.Mode.Code, o.DistanceKM)
			if err != nil {
				return t, err
			}
			if own != nil {
				opt.own, opt.quote.Fare = own, money.FromMinor(fuel)
			}
		}
		t.options = append(t.options, opt)
	}
	return t, nil
}

// ownVehicle is the player's best vehicle of a mode that still drives —
// the one with the most journeys left — and the fuel a journey of distance
// burns in it; nil when they have none.
func (h *TravelHandler) ownVehicle(ctx context.Context, tx application.Tx, playerID, mode string, distance int,
) (*ownVehicle, int64, error) {
	if h.places == nil {
		return nil, 0, nil
	}
	snap := h.places.Current()
	if len(snap.Vehicles(mode)) == 0 {
		return nil, 0, nil
	}
	_, pieces, err := tx.Items().Holdings(ctx, playerID, application.HoldCarried)
	if err != nil {
		return nil, 0, err
	}
	var best *ownVehicle
	var fuel int64
	for _, pc := range pieces {
		def, ok := snap.ItemDef(pc.Item)
		if !ok {
			continue
		}
		v, ok := def.VehicleOf()
		if !ok || v.Mode != mode || !vehicle.Drives(pc.UsesLeft) {
			continue
		}
		if best == nil || pc.UsesLeft > best.piece.UsesLeft {
			best = &ownVehicle{piece: pc, item: named(def.Code, def.Name), condition: v.ConditionBPS(pc.UsesLeft)}
			fuel = v.Fuel(distance)
		}
	}
	return best, fuel, nil
}

// transitFare reads the origin city's public transport fare policy.
func (h *TravelHandler) transitFare(ctx context.Context, from *application.City) (int, error) {
	if from.JurisdictionID == "" {
		return 0, errors.Internal(fmt.Errorf("city %q has no jurisdiction to read %s from", from.Code, TransitFareLever))
	}
	v, err := h.policy.Get(ctx, from.JurisdictionID, TransitFareLever)
	if err != nil {
		return 0, err
	}
	return int(v.Value), nil
}

// cashOf reads the player's cash on hand, opening the account on first use.
func cashOf(ctx context.Context, tx application.Tx, playerID string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID)
}

// optionsView turns a priced trip into the choice-of-transport screen.
func optionsView(t trip, cash int64, requoted bool) screens.TravelOptionsView {
	v := screens.TravelOptionsView{
		FromCode: t.from.Code, From: t.from.Name,
		ToCode: t.to.Code, To: t.to.Name,
		Cash: cash, Requoted: requoted,
	}
	for _, o := range t.options {
		var own *screens.Named
		var condition int64
		if o.own != nil {
			own, condition = &o.own.item, o.own.condition
		}
		v.Options = append(v.Options, screens.TravelOption{
			Vehicle:   own,
			Condition: condition,
			ModeCode:  o.quote.Mode,
			ModeName:  o.name,
			Fare:      o.quote.Fare.Minor(),
			Wait:      o.quote.Wait,
			Energy:    o.quote.Energy,
			Busy:      o.quote.Surged(),
		})
	}
	return v
}

// Options handles travel.options: the choice of transport to one city, each
// mode with its price, its real wait and its energy. It departs nowhere and
// charges nothing, so it reserves no idempotency key: a refresh must always
// show the prices of now.
func (h *TravelHandler) Options(ctx context.Context, meta envelope.Metadata, req TravelOptionsRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.City == "" {
		return nil, errors.InvalidInput("travel.options requires a destination city")
	}

	var view screens.TravelOptionsView
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		t, err := h.planTrip(ctx, tx, p, req.City, h.now())
		if err != nil {
			return err
		}
		cash, err := cashOf(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		view = optionsView(t, cash.Balance.Minor(), false)
		return nil
	})
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(h.screen(meta, lang), v), nil
	}
	if v, ok := asWarBlocked(err); ok {
		return screens.WarBlocked(h.screen(meta, lang), v), nil
	}
	if err != nil {
		return nil, err
	}
	return screens.TravelOptions(h.screen(meta, lang), view), nil
}

// errRequote ends a departure's transaction without writing anything, so the
// handler can answer with a screen rather than a refusal. It never leaves
// this file.
var errRequote = stderrors.New("handlers: the fare rose above the price the player accepted")

// errPaymentDeclined ends a departure whose chosen way to pay does not cover
// the fare, so the handler can answer with the refusal screen.
var errPaymentDeclined = stderrors.New("handlers: the chosen payment method does not cover the fare")

// Start handles travel.start: a player confirming one way to travel, at a
// price they were shown.
//
// The order of the refusals is the order a player meets them: already
// travelling, the destination, the route, the chosen mode, the price, the
// energy, the money. Nothing is written until every refusal has been passed,
// and the fare moves in the SAME transaction as the journey it pays for, so a
// refused departure leaves no travel row, no scheduled action, no spent
// energy and no money moved.
//
// Two outcomes are answers rather than refusals, and both write nothing:
//
//   - the fare rose above what the player accepted (demand grew, or a policy
//     change took effect): the choice of transport is shown again at the new
//     prices, and nothing is charged. A price shown is honoured or
//     re-quoted, never exceeded.
//   - the chosen way to pay does not cover the fare: the fare and both
//     balances are shown privately (screens.PaymentDeclined), with the way
//     back to the choice of transport.
//
// A fare lower than the one accepted is charged as it stands: the player
// agreed to pay up to Max, and pays the price of now. The fare is paid from
// the purse the player chose — cash or card — whichever the mode accepts; a
// press without a method is answered with the fare and a button per way to
// pay it (Checkout), and a free journey departs without one.
func (h *TravelHandler) Start(ctx context.Context, meta envelope.Metadata, req StartTravelRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	if req.City == "" {
		return nil, errors.InvalidInput("travel.start requires a destination city")
	}
	maxFare, err := strconv.ParseInt(strings.TrimSpace(req.Max), 10, 64)
	if req.Mode == "" || err != nil || maxFare < 0 {
		// No mode chosen, or no price agreed to: show the choice instead of
		// departing at a price nobody saw.
		return h.Options(ctx, meta, TravelOptionsRequest{City: req.City})
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen {
		resp, free, err := h.checkout(ctx, meta, req, maxFare)
		if err != nil || !free {
			return resp, err
		}
		// A free journey: nothing is paid, so no way to pay is asked.
		method = payment.Cash
	}

	var (
		started  screens.TravelStartedView
		options  screens.TravelOptionsView
		declined screens.PaymentDeclinedView
		replayed bool
		lang     = meta.Language
	)

	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
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
			// The same press arriving twice. The first one already left.
			replayed = true
			return nil
		}

		t, err := h.planTrip(ctx, tx, p, req.City, now)
		if err != nil {
			return err
		}
		chosen, ok := t.option(req.Mode)
		if !ok {
			return errors.InvalidInput("travel mode does not serve this journey").
				WithCause(fmt.Errorf("%w: %q from %q to %q", travel.ErrModeUnavailable, req.Mode, t.from.Code, t.to.Code))
		}
		q := chosen.quote

		cash, err := cashOf(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if q.Fare.Minor() > maxFare {
			options = optionsView(t, cash.Balance.Minor(), true)
			return errRequote
		}

		row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
		if err != nil {
			return err
		}
		// Asked again now that the stats row is locked: a shift starting
		// takes the same lock, so whichever of the two came first is seen
		// here and a player never leaves town in the middle of a shift.
		if err := refuseAtWork(ctx, tx, p.ID); err != nil {
			return err
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if err := h.departure(ctx, tx, p, q.Mode, now); err != nil {
			return thenFor(err, "travel.options", t.to.Code)
		}
		// Regenerate before charging. A player who has been away has the
		// energy the clock owes them, and charging them before paying it out
		// would refuse a departure they can afford.
		regenerated, _ := regenerateEnergy(*row, now)
		spent, err := domainStats(regenerated).SpendEnergy(q.Energy)
		if err != nil {
			return errors.InvalidInput("not enough energy to travel").
				WithCause(err).
				WithDetail("needed", q.Energy).
				WithDetail("current", regenerated.Energy)
		}
		// Saved whatever the energy cost, zero included: this write is also
		// what orders a departure against a cash hand-over, which locks the
		// same stats row.
		next := storedStats(regenerated, spent)
		next.UpdatedAt = now
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}

		travelID := h.ids.NewID()
		actionID := h.ids.NewID()

		ledgerTxID, err := h.chargeFare(ctx, tx, p.ID, t.from.ID, t.to.Code, travelID, q, chosen.accepts, method,
			chosen.own != nil, now)
		if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
			declined = v
			return errPaymentDeclined
		}
		if err != nil {
			return err
		}

		journey, err := q.Depart(now)
		if err != nil {
			return errors.Internal(err)
		}

		payload, err := json.Marshal(TravelActionPayload{
			TravelID: travelID,
			PlayerID: p.ID,
			ToCityID: t.to.ID,
			Mode:     q.Mode,
		})
		if err != nil {
			return err
		}

		// The schedule row comes first because the travel row points at it.
		// It is also the row that makes the journey survive a restart: the
		// process can die the instant after this commits and the player still
		// lands, because the work outlived the process that started it.
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID:            actionID,
			ActionType:    TravelActionType,
			ActorType:     "player",
			ActorID:       p.ID,
			ReferenceType: "travel",
			ReferenceID:   travelID,
			Payload:       payload,
			Status:        statusPending,
			StartedAt:     journey.DepartedAt,
			FinishAt:      journey.ArrivesAt,
		}); err != nil {
			return err
		}

		// A journey in the player's own vehicle wears it by one.
		vehicleID := ""
		if chosen.own != nil {
			vehicleID = chosen.own.piece.ID
			if err := tx.Items().SetUses(ctx, vehicleID, chosen.own.piece.UsesLeft-1); err != nil {
				return err
			}
		}
		if err := tx.Travels().Start(ctx, application.Travel{
			VehicleID:           vehicleID,
			ID:                  travelID,
			PlayerID:            p.ID,
			FromCityID:          t.from.ID,
			ToCityID:            t.to.ID,
			Cost:                q.Fare.Minor(),
			GameActionID:        actionID,
			Status:              statusInTransit,
			DepartedAt:          journey.DepartedAt,
			ArrivesAt:           journey.ArrivesAt,
			Mode:                q.Mode,
			LedgerTransactionID: ledgerTxID,
			ContentVersion:      t.contentVersion,
		}); err != nil {
			return err
		}

		ev, err := events.New("travel.started", "travel", travelID, map[string]any{
			"travel_id":             travelID,
			"player_id":             p.ID,
			"from_city_id":          t.from.ID,
			"to_city_id":            t.to.ID,
			"mode":                  q.Mode,
			"public":                q.Public,
			"distance_km":           q.DistanceKM,
			"fare":                  q.Fare.Minor(),
			"base_fare":             q.BaseFare.Minor(),
			"policy_bps":            q.PolicyBPS,
			"demand_bps":            q.DemandBPS,
			"ledger_transaction_id": ledgerTxID,
			"energy":                q.Energy,
			"game_duration_seconds": int64(q.TravelTime / time.Second),
			"wait_seconds":          int64(q.Wait / time.Second),
			"arrives_at":            journey.ArrivesAt,
			"content_version":       t.contentVersion,
		})
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, application.OutboxRecord{
			EventID:  ev.ID,
			Subject:  subjects.Event("travel", "started"),
			Metadata: meta,
			Payload:  ev.Payload,
		}); err != nil {
			return err
		}

		started = screens.TravelStartedView{
			FromCode:  t.from.Code,
			From:      t.from.Name,
			ToCode:    t.to.Code,
			To:        t.to.Name,
			ModeCode:  q.Mode,
			ModeName:  chosen.name,
			Duration:  journey.Duration(),
			ArrivesAt: journey.ArrivesAt,
			Energy:    q.Energy,
			Fare:      q.Fare.Minor(),
		}
		return nil
	})
	switch {
	case err == errRequote:
		return screens.TravelOptions(h.screen(meta, lang), options), nil
	case err == errPaymentDeclined:
		return screens.PaymentDeclined(h.screen(meta, lang), declined), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(h.screen(meta, lang), v), nil
	}
	if v, ok := asWarBlocked(err); ok {
		return screens.WarBlocked(h.screen(meta, lang), v), nil
	}
	if err != nil {
		return nil, err
	}

	if replayed {
		// A replay shows the journey rather than pretending to start a
		// second one. The player pressed the button twice; they get the trip
		// they are on.
		return h.Status(ctx, meta)
	}
	return screens.TravelStarted(h.screen(meta, lang), started), nil
}

// option finds the priced option for one mode code.
func (t trip) option(mode string) (quotedOption, bool) {
	for _, o := range t.options {
		if o.quote.Mode == mode {
			return o, true
		}
	}
	return quotedOption{}, false
}

// chargeFare moves the fare, in the caller's transaction, from the purse the
// player chose, and returns the ledger transaction id, or "" for a free
// journey.
//
// Where the money goes (ADR 0009 section 2):
//
//   - a PUBLIC mode's fare is the city's transit revenue. The city set its
//     price (city.transit_fare), so it is paid into the origin city's
//     treasury — a transfer, reason transit_fare, money supply unchanged;
//   - a private mode's fare (a car hire's fuel, an airline ticket) is not the
//     city's; it leaves the economy into system_sink — a drain, reason
//     travel_fare.
//
// Cash or card changes only which of the player's accounts is debited; the
// reason is the fare's. The reference is the journey, so the ledger answers
// "what paid for this trip" and the trip answers "which transaction paid for
// me". A method that does not cover the fare is refused with both balances.
func (h *TravelHandler) chargeFare(ctx context.Context, tx application.Tx, playerID, originCityID, toCode, travelID string,
	q travel.Quote, accepts payment.Accepts, method payment.Method, own bool, now time.Time,
) (string, error) {
	if q.Fare.IsZero() {
		return "", nil
	}
	w, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
	if err != nil {
		return "", err
	}
	plan := w.Plan(q.Fare, accepts)
	back, addr := "button.travel_options", []string{screens.AddrTravelOptions, toCode}
	if err := checkMethod(plan, method, w, back, addr...); err != nil {
		return "", err
	}
	reason, payee := application.ReasonTravelFare, application.SystemSinkAccountID
	if own {
		// The player's own vehicle burns fuel, not a fare (docs/adr/0024).
		reason = application.ReasonFuel
	}
	if q.Public {
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, originCityID)
		if err != nil {
			return "", err
		}
		reason, payee = application.ReasonTransitFare, treasury.ID
	}
	id, err := w.Pay(ctx, tx.Ledger(), application.Charge{
		Method:        method,
		Accepted:      plan.Accepted,
		Reason:        reason,
		ReferenceType: "travels",
		ReferenceID:   travelID,
		To:            []application.LedgerEntry{{AccountID: payee, Amount: q.Fare}},
		CreatedAt:     now,
	})
	if stderrors.Is(err, application.ErrPaymentDeclined) {
		return "", declined(plan, w, back, addr...)
	}
	return id, err
}

// checkout answers a departure pressed without a way to pay: the fare of the
// chosen mode with a button per way the player can pay it. It writes
// nothing. free reports a journey that costs nothing, which departs without
// asking; a fare risen above the accepted ceiling re-quotes, like Start.
func (h *TravelHandler) checkout(ctx context.Context, meta envelope.Metadata, req StartTravelRequest, maxFare int64) (*presenter.Response, bool, error) {
	var (
		view     screens.TravelCheckoutView
		options  screens.TravelOptionsView
		requoted bool
		free     bool
		lang     = meta.Language
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		t, err := h.planTrip(ctx, tx, p, req.City, h.now())
		if err != nil {
			return err
		}
		chosen, ok := t.option(req.Mode)
		if !ok {
			return errors.InvalidInput("travel mode does not serve this journey").
				WithCause(fmt.Errorf("%w: %q from %q to %q", travel.ErrModeUnavailable, req.Mode, t.from.Code, t.to.Code))
		}
		q := chosen.quote
		if err := h.departure(ctx, tx, p, q.Mode, h.now()); err != nil {
			return thenFor(err, "travel.options", t.to.Code)
		}
		w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		if q.Fare.Minor() > maxFare {
			options, requoted = optionsView(t, w.Cash.Balance.Minor(), true), true
			return nil
		}
		if q.Fare.IsZero() {
			free = true
			return nil
		}
		view = screens.TravelCheckoutView{
			FromCode: t.from.Code, From: t.from.Name, ToCode: t.to.Code, To: t.to.Name,
			ModeCode: q.Mode, ModeName: chosen.name, Fare: q.Fare.Minor(),
			Wait: q.Wait, Energy: q.Energy, Busy: q.Surged(),
			Payment: paymentChoice(w.Plan(q.Fare, chosen.accepts), w),
		}
		return nil
	})
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), false, nil
	}
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(h.screen(meta, lang), v), false, nil
	}
	if v, ok := asWarBlocked(err); ok {
		return screens.WarBlocked(h.screen(meta, lang), v), false, nil
	}
	switch {
	case err != nil:
		return nil, false, err
	case requoted:
		return screens.TravelOptions(h.screen(meta, lang), options), false, nil
	case free:
		return nil, true, nil
	}
	return screens.TravelCheckout(h.screen(meta, lang), view), false, nil
}

// Status handles travel.status: the journey in progress, and how much of it
// is left.
//
// It writes nothing and reserves no idempotency key. A read has no side
// effect to suppress, and reserving a key for one would block the refresh
// button the screen ships with.
func (h *TravelHandler) Status(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var view screens.TravelStatusView
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		t, err := tx.Travels().Active(ctx, p.ID)
		if err != nil {
			return err
		}

		journey := travel.Journey{
			FromCityID: t.FromCityID,
			ToCityID:   t.ToCityID,
			Mode:       t.Mode,
			DepartedAt: t.DepartedAt,
			ArrivesAt:  t.ArrivesAt,
			Status:     travel.Status(t.Status),
		}

		from, err := h.cities.ByID(ctx, t.FromCityID)
		if err != nil {
			return err
		}
		to, err := h.cities.ByID(ctx, t.ToCityID)
		if err != nil {
			return err
		}

		view = screens.TravelStatusView{
			FromCode:  from.Code,
			From:      from.Name,
			ToCode:    to.Code,
			To:        to.Name,
			ModeCode:  t.Mode,
			Remaining: travel.Remaining(journey, h.now()),
			ArrivesAt: t.ArrivesAt,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.TravelStatus(h.screen(meta, lang), view), nil
}

// Complete lands a journey. It arrives from the SCHEDULER, not from a player.
//
// The scheduler publishes it as the command travel.arrive, carrying the
// game_actions row that came due. There is no Telegram user on the metadata
// and none is required: the journey names its own player.
//
// # Why this is idempotent three times over
//
// The schedule is at-least-once and every dispatch mints a fresh request id,
// so a key derived from the request would not recognise the second delivery
// at all. The key is derived from the JOURNEY instead, which is the thing
// that may happen once.
//
// Behind that key stand two more refusals that do not depend on it. The
// player's active journey is already gone once it has landed, so a second
// delivery finds nothing to complete; and travel.Journey.TransitionTo refuses
// to move an arrived journey to arrived again, because the domain treats a
// repeat as the duplicate it is rather than as a harmless no-op.
//
// A second delivery returns a nil response and no error: there is nothing to
// announce twice, and telling the player they arrived again would be worse
// than saying nothing.
func (h *TravelHandler) Complete(ctx context.Context, meta envelope.Metadata, req ArriveTravelRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}

	playerID, travelID := req.ActorID, req.ReferenceID
	if playerID == "" || travelID == "" {
		// The columns were empty, so fall back to the jsonb the departure
		// wrote. See TravelActionPayload for why it repeats them.
		var inner TravelActionPayload
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &inner); err != nil {
				return nil, errors.InvalidInput("travel arrival payload is unreadable").WithCause(err)
			}
		}
		if playerID == "" {
			playerID = inner.PlayerID
		}
		if travelID == "" {
			travelID = inner.TravelID
		}
	}
	if playerID == "" || travelID == "" {
		return nil, errors.InvalidInput("travel arrival names no journey")
	}

	var (
		view     screens.TravelArrivedView
		arrived  bool
		language = meta.Language
	)

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		// The journey, not the request, is what may happen once. See the
		// doc comment: the scheduler mints a new request id per dispatch.
		key := idempotency.Derive(playerID, meta.Command, travelID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}

		t, err := tx.Travels().Active(ctx, playerID)
		if err != nil {
			if isSentinel(err, application.ErrNoActiveTravel) {
				// Already landed, or cancelled. Nothing to do and nothing
				// to complain about: the schedule simply arrived late.
				return nil
			}
			return err
		}
		if t.ID != travelID {
			// The row that came due is not the journey this player is on.
			// Completing it would land them from a trip they are not taking.
			return nil
		}

		journey := travel.Journey{
			FromCityID: t.FromCityID,
			ToCityID:   t.ToCityID,
			Mode:       t.Mode,
			DepartedAt: t.DepartedAt,
			ArrivesAt:  t.ArrivesAt,
			Status:     travel.Status(t.Status),
		}
		if _, err := journey.TransitionTo(travel.StatusArrived); err != nil {
			if stderrors.Is(err, travel.ErrIllegalTransition) {
				return nil
			}
			return errors.Internal(err)
		}

		// Complete marks the journey arrived AND moves the player together.
		// Splitting those two is what strands a player between two cities
		// when a process dies in between, which is why the port promises
		// them together.
		//
		// It runs on tx, so the arrival is part of THIS unit of work and not
		// committed on its own. That matters for everything after it: if the
		// XP award or the outbox record below fails, the arrival and the
		// idempotency reservation roll back with it, and the redelivery
		// finds the journey still in transit and lands it — XP included —
		// instead of finding nothing to do and dropping the award.
		if err := tx.Travels().Complete(ctx, t.ID); err != nil {
			return err
		}

		row, err := tx.Stats().EnsureDefaults(ctx, playerID, defaultStats(playerID, h.now()))
		if err != nil {
			return err
		}
		regenerated, _ := regenerateEnergy(*row, h.now())
		awarded, ups := domainStats(regenerated).AddXP(h.arrivalXP)
		next := storedStats(regenerated, awarded)
		next.UpdatedAt = h.now()
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}

		to, err := h.cities.ByID(ctx, t.ToCityID)
		if err != nil {
			return err
		}
		// Off the bus, the train or the plane: at the place of the
		// destination the mode stops at, or its default place.
		arrivalPlace := ""
		if h.places != nil {
			if pl, ok := h.places.Current().CityMap(to.Code).ForMode(t.Mode); ok && !pl.Default {
				arrivalPlace = pl.Code
			}
		}
		if err := tx.Places().Put(ctx, playerID, arrivalPlace, h.now()); err != nil {
			return err
		}

		ev, err := events.New("travel.completed", "travel", t.ID, map[string]any{
			"travel_id":  t.ID,
			"player_id":  playerID,
			"to_city_id": t.ToCityID,
			"mode":       t.Mode,
			"xp":         h.arrivalXP,
			"levels":     levelNumbers(ups),
		})
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, application.OutboxRecord{
			EventID:  ev.ID,
			Subject:  subjects.Event("travel", "completed"),
			Metadata: meta,
			Payload:  ev.Payload,
		}); err != nil {
			return err
		}

		// The scheduler knows no Telegram user and stamps the configured
		// default language, so the arrival is written in the language the
		// player chose, read from their record. A record that cannot be found
		// is no reason to lose an arrival that has already happened: the
		// notification then goes out in the default.
		if p, err := tx.Players().GetByID(ctx, playerID); err == nil {
			language = RenderLanguage(meta, p)
		} else if !isSentinel(err, application.ErrPlayerNotFound) {
			return err
		}

		arrived = true
		view = screens.TravelArrivedView{CityCode: to.Code, City: to.Name, XP: h.arrivalXP}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !arrived {
		return nil, nil
	}

	// An arrival is always SENT: the player did not press anything, so there
	// is no message of theirs to edit.
	return screens.TravelArrived(screens.Context{Msgs: h.msgs, Lang: language}, view), nil
}

// levelNumbers flattens the domain's level-up records for the event payload.
// The event carries the levels reached, not the LevelUp values themselves, so
// a consumer never has to import the domain to read it.
func levelNumbers(ups []player.LevelUp) []int {
	if len(ups) == 0 {
		return nil
	}
	out := make([]int, 0, len(ups))
	for _, up := range ups {
		out = append(out, up.Level)
	}
	return out
}

// refuseAtWork refuses a departure while the player is working a shift
// (docs/adr/0018-game-clock.md): they are busy at the workplace until it
// ends.
func refuseAtWork(ctx context.Context, tx application.Tx, playerID string) error {
	shift, err := activeShift(ctx, tx, playerID)
	if err != nil {
		return err
	}
	if shift != nil {
		return application.ErrShiftInProgress
	}
	return nil
}
