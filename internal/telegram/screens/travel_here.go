package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TravelHere renders the refusal, with the way on: the map of the places the
// player can go to instead.
func TravelHere(c Context, v TravelHereView) *presenter.Response {
	return c.withView(renderTravelHere(c, v), ScreenTravelHere, v)
}

func renderTravelHere(c Context, v TravelHereView) *presenter.Response {
	key := "travel.here.no_village"
	switch v.Reason {
	case TravelHereAlreadyThere:
		key = "travel.here.already_there"
	case TravelHereGroupOnly:
		key = "travel.here.group_only"
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.travel_elsewhere", nil), AddrCities); ok {
		kb.Row(btn)
	}
	return c.respond(c.T(key, map[string]any{"village": v.Village}), kb.Build())
}
