package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// A «not available here» card names the nearest place that has the thing, and
// how far it is: the cheapest way there, with its fare and its wait. The quote
// is the travel screen's own (planTrip), read without departing or charging
// anything; a place that cannot be reached right now (travelling, jailed, no
// route) has no hint and the card shows the name alone.

// TripHinter quotes the cheapest journey from where a player stands to a city.
type TripHinter interface {
	Hint(ctx context.Context, tx application.Tx, p *application.Player, cityCode string) *presentation.TripHint
}

// Hint is the cheapest way to cityCode from where the player stands, or nil.
func (h *TravelHandler) Hint(ctx context.Context, tx application.Tx, p *application.Player, cityCode string) *presentation.TripHint {
	t, err := h.planTrip(ctx, tx, p, cityCode, h.now())
	if err != nil || len(t.options) == 0 {
		return nil
	}
	best := t.options[0]
	for _, o := range t.options[1:] {
		if o.quote.Fare.Minor() < best.quote.Fare.Minor() ||
			(o.quote.Fare.Minor() == best.quote.Fare.Minor() && o.quote.Wait < best.quote.Wait) {
			best = o
		}
	}
	return &presentation.TripHint{Mode: best.quote.Mode, ModeName: best.name, Fare: best.quote.Fare.Minor(), Wait: best.quote.Wait}
}

// withTrip adds the journey to a nearest place; a nil place or hinter changes
// nothing.
func withTrip(ctx context.Context, tx application.Tx, hinter TripHinter, p *application.Player, near *presentation.Named) *presentation.Named {
	if near == nil || hinter == nil || p == nil || near.Code == "" {
		return near
	}
	out := *near
	out.Trip = hinter.Hint(ctx, tx, p, near.Code)
	return &out
}
