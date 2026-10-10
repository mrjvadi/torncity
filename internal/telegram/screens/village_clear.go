package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The clearing order (docs/adr/0065), drawn for Telegram: one line of what stands on the lot and what is ordered.
const ScreenClearOrder = village.ScreenClearOrder

// ClearOrder renders the screen.
func ClearOrder(c Context, v village.ClearOrderView) *presenter.Response {
	key := "village.clear.ordered"
	switch {
	case v.Cancelled:
		key = "village.clear.cancelled"
	case v.Private:
		key = "village.clear.ordered_private"
	}
	text := c.T(key, map[string]any{"x": FormatNumber(c, int64(v.X)), "y": FormatNumber(c, int64(v.Y)),
		"trees": FormatNumber(c, int64(v.Trees)), "rocks": FormatNumber(c, int64(v.Rocks))})
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	return c.withView(c.respond(text, kb.Build()), ScreenClearOrder, v)
}
