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

// MapHandler serves map.list: the cities of the world, what each one charges
// to live in, and which of them can be reached from where the player stands.
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
// what "no content loaded yet" looks like — and every city then reads as
// unreachable, which is the honest answer rather than a guess.
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
// Cities are sorted by code before paging. The repository returns them in
// whatever order the query produced, and a list that reorders itself between
// two presses of "next" would show some cities twice and hide others
// entirely.
func (h *MapHandler) List(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	page := parsePage(req.Page)

	var view screens.MapView

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

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

		// A player already on the road is shown the map without departure
		// buttons; see MapView.Travelling.
		travelling := false
		if _, err := h.travels.Active(ctx, p.ID); err == nil {
			travelling = true
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}

		start, end, pages := pageWindow(len(all), page, h.pageSize)
		rows := make([]screens.MapCity, 0, end-start)
		for _, c := range all[start:end] {
			row := screens.MapCity{
				Code:         c.Code,
				Name:         c.Name,
				TaxPercent:   screens.PercentFromBPS(c.TaxRateBPS),
				CostOfLiving: c.CostOfLiving,
			}
			switch {
			case origin == nil:
				// Nowhere to measure from. Every city is listed and none is
				// offered, which is what a player with no city can act on.
			case c.ID == origin.ID:
				row.Current = true
			default:
				if distance, err := h.routes.DistanceBetween(origin.Code, c.Code); err == nil {
					row.Reachable = true
					row.DistanceKM = distance
				}
				// A route that does not exist is not a failure: two islands
				// with no link between them load fine and only a journey
				// that needs the missing link is refused. See world.NewRoutes.
			}
			rows = append(rows, row)
		}

		view = screens.MapView{
			Cities:     rows,
			Page:       page,
			Pages:      pages,
			Travelling: travelling,
		}
		if origin != nil {
			view.Origin = origin.Name
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Map(screens.Context{
		Msgs:      h.msgs,
		Lang:      meta.Language,
		MessageID: editableMessageID(meta),
	}, view), nil
}
