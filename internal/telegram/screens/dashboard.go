package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// DashboardView is the hub a player lands on.
//
// It carries only what the hub shows. Anything a player has to press through
// to see belongs to that screen's own view, not to this one: a hub that knows
// every number on every screen is a hub that has to be rebuilt whenever any
// of them changes.
type DashboardView struct {
	Name string
	// City is the resolved city name, empty when the player is nowhere yet.
	City      string
	Level     int
	Energy    int
	MaxEnergy int
	// Travelling says whether a journey is in progress, so the hub can point
	// at the journey instead of at the departures board.
	Travelling bool
}

// Dashboard renders the hub: the same lines and the same buttons as the
// profile, minus what the dashboard does not carry.
func Dashboard(c Context, v DashboardView) *presenter.Response {
	var name, city string
	if v.Name != "" {
		name = c.T("profile.name", map[string]any{"name": v.Name})
	}
	if v.City != "" && !v.Travelling {
		city = c.T("profile.city", map[string]any{"city": v.City})
	}

	text := paragraphs(
		body(name, city),
		body(
			c.T("profile.level_plain", map[string]any{"level": max(v.Level, 1)}),
			energyLine(c, v.Energy, v.MaxEnergy, 0),
		),
	)
	return c.respond(text, hubKeyboard(c, v.City != "", v.Travelling).Build())
}
