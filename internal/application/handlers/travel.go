package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
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

// Travel statuses as they are stored. They are the domain's values, written
// through so a row and a rule cannot disagree about what "in transit" is.
const (
	statusInTransit = string(travel.StatusInTransit)
	statusPending   = "pending"
)

// StartTravelRequest is the payload of travel.start.
//
// City is a city CODE, the stable authored key, never a database identifier:
// a code is what a content file, a button and a player's typed command all
// agree on, and it is short enough to fit a callback address.
type StartTravelRequest struct {
	City  string `json:"city"`
	Speed string `json:"speed,omitempty"`
}

// TravelActionPayload is the jsonb this handler writes onto the game_actions
// row, and the shape it expects to read back when the arrival comes due.
//
// It repeats the player and the journey that the row's actor_id and
// reference_id already carry. That redundancy is deliberate: the scheduler
// forwards the jsonb untouched and promises nothing about the columns beyond
// them, so a payload that stands on its own cannot be made unreadable by a
// change to how the schedule is dispatched.
type TravelActionPayload struct {
	TravelID string `json:"travel_id"`
	PlayerID string `json:"player_id"`
	ToCityID string `json:"to_city_id"`
	Speed    string `json:"speed"`
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

// TravelHandler serves the three travel commands: a departure a player asks
// for, the journey they check on, and the arrival a clock produces.
type TravelHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	cities  application.CityRepository
	stats   application.StatsRepository
	travels application.TravelRepository
	actions application.GameActionRepository
	planner TravelPlanner

	energyCost     int
	arrivalXP      int64
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewTravelHandler wires the handler.
//
// energyCost is what a departure costs in energy and arrivalXP is what
// landing awards. Both are tuning, injected rather than written here for the
// same reason the tariff and the route network are injected into the planner:
// they are numbers somebody balances, not rules. Both are rejected at zero,
// because a free journey removes the only limit phase 1 puts on travelling
// and an arrival worth no XP makes the whole trip pointless — either would be
// a wiring mistake that looks like a design decision once it is live.
//
// idempotencyTTL is rejected at zero for the reason NewProfileHandler gives:
// a zero TTL reads as an expiry already past, so every redelivery would run
// as if it were new.
func NewTravelHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	cities application.CityRepository,
	stats application.StatsRepository,
	travels application.TravelRepository,
	actions application.GameActionRepository,
	planner TravelPlanner,
	energyCost int,
	arrivalXP int64,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *TravelHandler {
	if planner == nil {
		panic("handlers: NewTravelHandler requires a planner")
	}
	if energyCost <= 0 {
		panic("handlers: NewTravelHandler requires a positive energy cost")
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
		stats:          stats,
		travels:        travels,
		actions:        actions,
		planner:        planner,
		energyCost:     energyCost,
		arrivalXP:      arrivalXP,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

// screen builds the rendering context for a player's request.
func (h *TravelHandler) screen(meta envelope.Metadata) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: meta.Language, MessageID: editableMessageID(meta)}
}

// Start handles travel.start: a player asking to leave for another city.
//
// The order of the refusals is the order a player meets them. Already
// travelling comes first because it is the one that makes every later check
// meaningless, then the destination, then the route, then the energy. Nothing
// is written until every refusal has been passed, so a refused departure
// leaves no travel row, no scheduled action and no spent energy behind.
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

	speed := travel.SpeedStandard
	if req.Speed != "" {
		speed = travel.Speed(req.Speed)
	}

	var view screens.TravelStartedView
	replayed := false

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

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

		if _, err := h.travels.Active(ctx, p.ID); err == nil {
			return application.ErrAlreadyTravelling
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}

		if p.CityID == nil || *p.CityID == "" {
			// A player who is nowhere has no origin to plan from. It is the
			// same shape of problem as a missing destination, so it gets the
			// same sentinel rather than a new one this layer would have to
			// invent outside ports_phase1.go.
			return application.ErrCityNotFound
		}

		from, err := h.cities.ByID(ctx, *p.CityID)
		if err != nil {
			return err
		}
		to, err := h.cities.ByCode(ctx, req.City)
		if err != nil {
			return err
		}

		journey, cost, err := h.planner.Plan(worldCity(*from), worldCity(*to), speed, h.now())
		if err != nil {
			// The planner's refusals are the player's mistakes — the same
			// city, no route, a speed nobody sells — so they are classified
			// as bad input with the domain error kept as the cause. The
			// screen reads the cause to choose the sentence.
			return errors.InvalidInput("travel cannot be planned").WithCause(err)
		}

		row, err := h.stats.EnsureDefaults(ctx, p.ID, defaultStats(p.ID, h.now()))
		if err != nil {
			return err
		}
		// Regenerate before charging. A player who has been away has the
		// energy the clock owes them, and charging them before paying it out
		// would refuse a departure they can afford.
		regenerated, _ := regenerateEnergy(*row, h.now())
		spent, err := domainStats(regenerated).SpendEnergy(h.energyCost)
		if err != nil {
			return errors.InvalidInput("not enough energy to travel").
				WithCause(err).
				WithDetail("needed", h.energyCost).
				WithDetail("current", regenerated.Energy)
		}

		next := storedStats(regenerated, spent)
		next.UpdatedAt = h.now()
		if err := h.stats.Save(ctx, next); err != nil {
			return err
		}

		travelID := h.ids.NewID()
		actionID := h.ids.NewID()

		payload, err := json.Marshal(TravelActionPayload{
			TravelID: travelID,
			PlayerID: p.ID,
			ToCityID: to.ID,
			Speed:    string(speed),
		})
		if err != nil {
			return err
		}

		// The schedule row comes first because the travel row points at it.
		// It is also the row that makes the journey survive a restart: the
		// process can die the instant after this commits and the player still
		// lands, because the work outlived the process that started it.
		if err := h.actions.Schedule(ctx, application.GameAction{
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

		if err := h.travels.Start(ctx, application.Travel{
			ID:           travelID,
			PlayerID:     p.ID,
			FromCityID:   from.ID,
			ToCityID:     to.ID,
			Cost:         cost.Fare.Minor(),
			GameActionID: actionID,
			Status:       statusInTransit,
			DepartedAt:   journey.DepartedAt,
			ArrivesAt:    journey.ArrivesAt,
		}); err != nil {
			return err
		}

		ev, err := events.New("travel.started", "travel", travelID, map[string]any{
			"travel_id":    travelID,
			"player_id":    p.ID,
			"from_city_id": from.ID,
			"to_city_id":   to.ID,
			"speed":        string(speed),
			"distance_km":  cost.DistanceKM,
			"arrives_at":   journey.ArrivesAt,
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

		view = screens.TravelStartedView{
			From:     from.Name,
			To:       to.Name,
			Duration: journey.Duration(),
			Energy:   h.energyCost,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if replayed {
		// A replay shows the journey rather than pretending to start a
		// second one. The player pressed the button twice; they get the trip
		// they are on.
		return h.Status(ctx, meta)
	}
	return screens.TravelStarted(h.screen(meta), view), nil
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

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

		t, err := h.travels.Active(ctx, p.ID)
		if err != nil {
			return err
		}

		journey := travel.Journey{
			FromCityID: t.FromCityID,
			ToCityID:   t.ToCityID,
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
			From:      from.Name,
			To:        to.Name,
			Remaining: travel.Remaining(journey, h.now()),
			ArrivesAt: t.ArrivesAt,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.TravelStatus(h.screen(meta), view), nil
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

		t, err := h.travels.Active(ctx, playerID)
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

		// Complete marks the journey arrived AND moves the player, in one
		// transaction of its own. Splitting those two is what strands a
		// player between two cities when a process dies in between, which is
		// why the port promises them together.
		if err := h.travels.Complete(ctx, t.ID); err != nil {
			return err
		}

		row, err := h.stats.EnsureDefaults(ctx, playerID, defaultStats(playerID, h.now()))
		if err != nil {
			return err
		}
		regenerated, _ := regenerateEnergy(*row, h.now())
		awarded, ups := domainStats(regenerated).AddXP(h.arrivalXP)
		next := storedStats(regenerated, awarded)
		next.UpdatedAt = h.now()
		if err := h.stats.Save(ctx, next); err != nil {
			return err
		}

		to, err := h.cities.ByID(ctx, t.ToCityID)
		if err != nil {
			return err
		}

		ev, err := events.New("travel.completed", "travel", t.ID, map[string]any{
			"travel_id":  t.ID,
			"player_id":  playerID,
			"to_city_id": t.ToCityID,
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

		arrived = true
		view = screens.TravelArrivedView{City: to.Name, XP: h.arrivalXP}
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
