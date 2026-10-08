package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// The floating VC/SUP book of a settlement's own money (docs/adr/0033 6.8).
type fxHandlers struct {
	book *handlers.FXHandler
}

// bindFX maps the book's commands to their handlers.
func (h phaseHandlers) bindFX() map[string]commandFunc {
	f := h.fx.book
	return map[string]commandFunc{
		"fx.book":    decoded(f.Book),
		"fx.place":   decoded(f.Place),
		"fx.cancel":  decoded(f.Cancel),
		"fx.history": decoded(f.History),
		"fx.convert": decoded(f.Convert),
		// The scheduler's: a period of the reference rate ending.
		"fx.settle": decoded(f.Settle),
	}
}
