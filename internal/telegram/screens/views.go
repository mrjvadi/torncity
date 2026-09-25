package screens

import "github.com/mrjvadi/torncity/internal/telegram/presenter"

// The screens a game client draws from structured data (cmd/clientapi): each
// attaches the view it was rendered from, under one of these names, and the
// client decides how to draw it. The names are part of the client contract
// (api/client-api.md); renaming one breaks every client in the field.
const (
	ScreenProfile       = "profile"
	ScreenDashboard     = "dashboard"
	ScreenCityMap       = "city_map"
	ScreenMap           = "cities"
	ScreenTravelOptions = "travel_options"
	ScreenTravelStatus  = "travel_status"
	ScreenBank          = "bank"
	ScreenInventory     = "inventory"
	ScreenJobStatus     = "job_status"
	ScreenLife          = "life"

	// ScreenError is a refusal or a failure: its text says what went
	// wrong. It carries no view.
	ScreenError = "error"
)

// withView attaches the view to a screen shown to the player alone. A screen
// shown in a group carries none: its text leaves the player's money out, and
// so must everything that travels with it.
func (c Context) withView(r *presenter.Response, screen string, view any) *presenter.Response {
	if c.Shared {
		return r
	}
	return presenter.WithView(r, screen, view)
}

// asError marks a response as the error screen.
func asError(r *presenter.Response) *presenter.Response {
	if r != nil {
		r.Screen = ScreenError
	}
	return r
}
