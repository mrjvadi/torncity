package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
)

// Go, then do (internal/application/handlers/places_then.go): a screen whose
// service is at another place of the city offers one press that walks there
// and opens the screen again on arrival, instead of sending the player to
// the map.

// Way is the walk to the place a screen's service is at.
type Way struct {
	Place Named
	// Walk is the real time the walk takes.
	Walk time.Duration
}

// wayButton adds «🚶 رفتن به {place} ({walk})», which walks to the way's
// place and then runs then with its arguments. Nothing is added without a
// way.
func (c Context) wayButton(kb *keyboards.Builder, w *Way, then string, args ...string) {
	if w == nil || w.Place.Code == "" {
		return
	}
	label := c.T("place.button.walk_then", map[string]any{
		"place": c.SpotName(w.Place), "walk": FormatDuration(c, w.Walk),
	})
	if btn, ok := goThenButton(label, w.Place.Code, then, args...); ok {
		kb.Row(btn)
	}
}
