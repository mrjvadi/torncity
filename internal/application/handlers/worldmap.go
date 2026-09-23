package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// MapHandler serves map.list: the cities a player can travel to from where
// they stand, nearest first.
type MapHandler struct {
	uow     application.UnitOfWork
	msgs    Translator
	cities  application.CityRepository
	travels application.TravelRepository
	routes  RouteNetwork

	pageSize int
	now      func() time.Time
}

// NewMapHandler wires the handler.
//
// routes is the loaded network. It may be the zero world.Routes — that is
// what "no content loaded yet" looks like — and the map then offers no
// destinations, which is the honest answer rather than a guess.
func NewMapHandler(
	uow application.UnitOfWork,
	msgs Translator,
	cities application.CityRepository,
	travels application.TravelRepository,
	routes RouteNetwork,
	pageSize int,
	now func() time.Time,
) *MapHandler {
	if routes == nil {
		panic("handlers: NewMapHandler requires a route network")
	}
	if pageSize <= 0 {
		panic("handlers: NewMapHandler requires a positive page size")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &MapHandler{
		uow:      uow,
		msgs:     msgs,
		cities:   cities,
		travels:  travels,
		routes:   routes,
		pageSize: pageSize,
		now:      now,
	}
}

// List handles map.list.
//
// Destinations are sorted by distance, then code, before paging. The
// repository returns cities in whatever order the query produced, and a list
// that reorders itself between two presses of "next" would show some cities
// twice and hide others entirely.
func (h *MapHandler) List(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	page := parsePage(req.Page)

	var view screens.MapView
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		all, err := h.cities.List(ctx)
		if err != nil {
			return err
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Code < all[j].Code })

		var origin *application.City
		if p.CityID != nil && *p.CityID != "" {
			for i := range all {
				if all[i].ID == *p.CityID {
					origin = &all[i]
					break
				}
			}
		}

		// A player already on the road is shown their journey instead of a
		// departures board; see MapView.Travelling.
		view = screens.MapView{Page: page, Pages: 1}
		if t, err := h.travels.Active(ctx, p.ID); err == nil {
			view.Travelling = true
			for i := range all {
				if all[i].ID == t.ToCityID {
					view.TravellingToCode = all[i].Code
					view.TravellingTo = all[i].Name
					break
				}
			}
			return nil
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}

		if origin == nil {
			// Nowhere to measure from, so nowhere to go.
			return nil
		}
		view.OriginCode = origin.Code
		view.Origin = origin.Name

		// Only destinations are listed: cities a route reaches from here.
		// A city with no route from here is not a choice the player has,
		// and listing it would bury the choices they do have. A route that
		// does not exist is not a failure either: two islands with no link
		// between them load fine. See world.NewRoutes.
		destinations := make([]screens.MapCity, 0, len(all))
		for _, c := range all {
			if c.ID == origin.ID {
				continue
			}
			distance, err := h.routes.DistanceBetween(origin.Code, c.Code)
			if err != nil {
				continue
			}
			destinations = append(destinations, screens.MapCity{
				Code:       c.Code,
				Name:       c.Name,
				DistanceKM: distance,
			})
		}
		// Nearest first, the order a traveller weighs them in; the code
		// breaks ties so the order is stable between two presses of "next".
		sort.SliceStable(destinations, func(i, j int) bool {
			if destinations[i].DistanceKM != destinations[j].DistanceKM {
				return destinations[i].DistanceKM < destinations[j].DistanceKM
			}
			return destinations[i].Code < destinations[j].Code
		})

		start, end, pages := pageWindow(len(destinations), page, h.pageSize)
		view.Destinations = destinations[start:end]
		view.Pages = pages
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Map(screens.Context{
		Msgs:      h.msgs,
		Lang:      lang,
		MessageID: editableMessageID(meta),
	}, view), nil
}
