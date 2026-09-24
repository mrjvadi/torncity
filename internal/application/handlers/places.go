package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// PlacesHandler serves the map of the player's own city and the walks
// between its places (docs: configs/content/places.yml, internal/domain/place):
// map.list is the city — where the player stands, every place with the walk
// to it and what is there, and the way to other cities; place.go starts a
// walk; place.arrive, from the scheduler, ends one exactly once.
//
// # What needs a place, and what does not
//
// A service is found at its place and only there: the market and the shops
// at the bazaar, courses at the university quarter, a departure at the
// station its mode stops at, a crime at the places it names. What is the
// player's own business works from anywhere in the city and beyond: the
// profile, settings, skills, friends, messages, the city's offices and
// history, the market's order book to read, card payments to another player,
// and a report to the police (a phone call). The bank's counter stays
// reachable from anywhere in the city for now; its place is on the map.
type PlacesHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	// scale is the game clock: a walk's length is game time.
	scale gametime.Scale

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewPlacesHandler wires the handler. A missing dependency or a game clock
// outside 1..gametime.MaxScale is a wiring mistake and panics.
func NewPlacesHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, idempotencyTTL time.Duration, now func() time.Time,
) *PlacesHandler {
	if source == nil || cities == nil || ids == nil {
		panic("handlers: NewPlacesHandler requires content, cities and ids")
	}
	if scale.Validate() != nil {
		panic("handlers: NewPlacesHandler requires a game clock within 1..gametime.MaxScale")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewPlacesHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &PlacesHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		idempotencyTTL: idempotencyTTL, now: now}
}

// PlaceRequest names a place by its content code, for place.go.
type PlaceRequest struct {
	Place string `json:"place"`
}

// PlaceScheduledRequest is the scheduler's dispatch payload for place.arrive.
type PlaceScheduledRequest = CrimeScheduledRequest

func (h *PlacesHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// whereabouts is where a player is: their city, its places, the place they
// stand at, and a walk under way.
type whereabouts struct {
	city *application.City
	cmap place.Map
	here place.Place
	walk *application.PlaceMove
}

// placed reports whether the player is in a city that has places.
func (w whereabouts) placed() bool { return w.city != nil && len(w.cmap.Places) > 0 }

// locate reads where a player is. A player who is nowhere, or in a city whose
// content has no places, is reported with no map; every place check then
// passes, because there is nowhere to be.
func locate(ctx context.Context, tx application.Tx, cities application.CityRepository, snap *content.Snapshot,
	p *application.Player,
) (whereabouts, error) {
	var w whereabouts
	if p.CityID == nil || *p.CityID == "" {
		return w, nil
	}
	city, err := cities.ByID(ctx, *p.CityID)
	if err != nil {
		return w, err
	}
	w.city = city
	w.cmap = snap.CityMap(city.Code)
	code, err := tx.Places().Where(ctx, p.ID)
	if err != nil {
		return w, err
	}
	w.here, _ = w.cmap.Current(code)
	walk, err := tx.Places().ActiveMove(ctx, p.ID)
	switch {
	case err == nil:
		w.walk = walk
	case !isSentinel(err, application.ErrNotMoving):
		return w, err
	}
	return w, nil
}

// placeNamed is a place as a screen names it.
func placeNamed(snap *content.Snapshot, code string) screens.Named {
	def, _ := snap.PlaceDef(code)
	return screens.Named{Code: code, Name: def.Name}
}

// notHere carries "that is not here" out of a unit of work: the service or
// the departure the player asked for is at another place, with the walk
// there one press away.
type notHere struct{ view screens.NotHereView }

func (n *notHere) Error() string { return "handlers: not at the place the request needs" }

// asNotHere reports whether err is a place refusal and returns its view.
func asNotHere(err error) (screens.NotHereView, bool) {
	var n *notHere
	if stderrors.As(err, &n) {
		return n.view, true
	}
	return screens.NotHereView{}, false
}

// refuseWalking refuses what a player on their way somewhere cannot do.
func refuseWalking(w whereabouts, snap *content.Snapshot, now time.Time) error {
	if w.walk == nil {
		return nil
	}
	return &notHere{view: screens.NotHereView{
		Walking: true, Place: placeNamed(snap, w.walk.To),
		Remaining: w.walk.ArrivesAt.Sub(now), ArrivesAt: w.walk.ArrivesAt,
	}}
}

// needAt refuses unless the player stands at target, naming what needed it
// (need is a catalogue key under place.need.*, args its placeholders). A
// player in a city without places, or nowhere, passes.
func needAt(w whereabouts, snap *content.Snapshot, target place.Place, need string, args map[string]any,
	scale gametime.Scale, now time.Time,
) error {
	if !w.placed() {
		return nil
	}
	if err := refuseWalking(w, snap, now); err != nil {
		return err
	}
	if w.here.Code == target.Code {
		return nil
	}
	return &notHere{view: screens.NotHereView{
		Need: need, NeedArgs: args, Place: placeNamed(snap, target.Code), Here: placeNamed(snap, w.here.Code),
		Walk: scale.RealWait(target.MoveTime),
	}}
}

// needService refuses unless the player stands where the city offers s. A
// city without it has nowhere to send them: the service is simply not
// offered, which the caller's own checks already say.
func needService(w whereabouts, snap *content.Snapshot, s place.Service, scale gametime.Scale, now time.Time) error {
	if !w.placed() {
		return nil
	}
	target, ok := w.cmap.ForService(s)
	if !ok {
		return nil
	}
	return needAt(w, snap, target, "place.need."+string(s), nil, scale, now)
}

// Map handles map.list: the player's own city.
func (h *PlacesHandler) Map(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CityMapView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		if t, err := tx.Travels().Active(ctx, p.ID); err == nil {
			to, err := h.cities.ByID(ctx, t.ToCityID)
			if err != nil {
				return err
			}
			view.Travelling, view.TravellingToCode, view.TravellingTo = true, to.Code, to.Name
			return nil
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			view.NoCity = true
			return nil
		}
		view.CityCode, view.City = w.city.Code, w.city.Name
		if !w.placed() {
			return nil
		}
		view.Here = placeNamed(snap, w.here.Code)
		if w.walk != nil {
			view.Walking = &screens.WalkView{
				To: placeNamed(snap, w.walk.To), Remaining: w.walk.ArrivesAt.Sub(now), ArrivesAt: w.walk.ArrivesAt,
			}
		}
		count, err := tx.Places().Headcount(ctx, w.city.ID)
		if err != nil {
			return err
		}
		here := count[w.here.Code]
		if w.here.Default {
			here += count[""]
		}
		if w.walk == nil && here > 0 {
			view.Others = here - 1
		}
		for _, pl := range w.cmap.Places {
			line := screens.PlaceLine{
				Place: placeNamed(snap, pl.Code), Walk: h.scale.RealWait(pl.MoveTime), Energy: pl.Energy,
				Here: w.walk == nil && pl.Code == w.here.Code,
			}
			for _, s := range pl.Services {
				line.Services = append(line.Services, string(s))
			}
			for _, m := range pl.Arrivals {
				line.Departures = append(line.Departures, m)
			}
			view.Places = append(view.Places, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.CityMap(h.screen(meta, lang), view), nil
}

// PlaceActionPayload is the jsonb a walk writes onto its game_actions row.
type PlaceActionPayload = CrimeActionPayload

// Go handles place.go: a walk to another place of the player's city. The walk
// costs the destination's energy at once and takes its time on the game
// clock; the player is on the way until it ends and stands nowhere
// meanwhile. One walk at a time; none while travelling, jailed, in the
// middle of a timed crime or on a shift.
func (h *PlacesHandler) Go(ctx context.Context, meta envelope.Metadata, req PlaceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view     screens.WalkStartedView
		replayed bool
		there    bool
	)
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
			replayed = true
			return nil
		}
		now := h.now()
		if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
			return application.ErrAlreadyTravelling
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}
		// Every walk runs after the stats row is locked: a departure and a
		// shift take the same lock, so whichever comes first is seen.
		row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
		if err != nil {
			return err
		}
		if err := refuseAtWork(ctx, tx, p.ID); err != nil {
			return err
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			return application.ErrCityNotFound
		}
		if err := refuseWalking(w, snap, now); err != nil {
			return err
		}
		mv, err := place.StartMove(w.cmap, w.here.Code, req.Place, now, h.scale)
		switch {
		case stderrors.Is(err, place.ErrAlreadyThere):
			there = true
			return nil
		case stderrors.Is(err, place.ErrUnknownPlace):
			return errors.NotFound("no such place in this city").WithCause(err)
		case err != nil:
			return errors.Internal(err)
		}
		regenerated, _ := regenerateEnergy(*row, now)
		spent, err := domainStats(regenerated).SpendEnergy(mv.Energy)
		if err != nil {
			return errors.InvalidInput("not enough energy to walk there").
				WithCause(err).WithDetail("needed", mv.Energy).WithDetail("current", regenerated.Energy)
		}
		next := storedStats(regenerated, spent)
		next.UpdatedAt = now
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}

		moveID, actionID := h.ids.NewID(), h.ids.NewID()
		payload, err := json.Marshal(PlaceActionPayload{ReferenceID: moveID, PlayerID: p.ID})
		if err != nil {
			return err
		}
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID: actionID, ActionType: application.PlaceMoveActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: application.PlaceMoveReference, ReferenceID: moveID, Payload: payload,
			StartedAt: mv.StartedAt, FinishAt: mv.ArrivesAt,
		}); err != nil {
			return err
		}
		if err := tx.Places().StartMove(ctx, application.PlaceMove{
			ID: moveID, PlayerID: p.ID, CityID: w.city.ID, From: mv.From, To: mv.To, Energy: mv.Energy,
			GameActionID: actionID, StartedAt: mv.StartedAt, ArrivesAt: mv.ArrivesAt,
		}); err != nil {
			return err
		}
		ev, err := events.New("place.walk_started", "place_move", moveID, map[string]any{
			"move_id": moveID, "player_id": p.ID, "city_id": w.city.ID, "from": mv.From, "to": mv.To,
			"energy": mv.Energy, "arrives_at": mv.ArrivesAt, "content_version": snap.Version(),
		})
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, application.OutboxRecord{
			EventID: ev.ID, Subject: subjects.Event("place", "walk_started"), Metadata: meta, Payload: ev.Payload,
		}); err != nil {
			return err
		}
		view = screens.WalkStartedView{
			To: placeNamed(snap, mv.To), From: placeNamed(snap, mv.From),
			Duration: mv.Duration(), ArrivesAt: mv.ArrivesAt, Energy: mv.Energy,
		}
		return nil
	})
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if err != nil {
		return nil, err
	}
	if replayed || there {
		return h.Map(ctx, meta)
	}
	return screens.WalkStarted(h.screen(meta, lang), view), nil
}

// Arrive ends a walk. It arrives from the SCHEDULER and runs exactly once:
// the key is derived from the walk, and only a walk still under way moves.
// Nothing is announced: a walk is short, and the map shows where the player
// stands.
func (h *PlacesHandler) Arrive(ctx context.Context, meta envelope.Metadata, req PlaceScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, moveID, err := req.ids()
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, moveID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		m, err := tx.Places().ActiveMove(ctx, playerID)
		if isSentinel(err, application.ErrNotMoving) {
			return nil
		}
		if err != nil {
			return err
		}
		if m.ID != moveID {
			return nil
		}
		now := h.now()
		if now.Before(m.ArrivesAt) {
			return errors.Internal(stderrors.New("handlers: a walk ended before its time"))
		}
		if _, err := tx.Places().FinishMove(ctx, moveID, now); err != nil && !isSentinel(err, application.ErrNotMoving) {
			return err
		}
		return nil
	})
}
