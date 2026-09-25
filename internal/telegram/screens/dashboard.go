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
	// CityCode and City are the player's city: its content code, which the
	// screen resolves to a name in the player's language, and its authored
	// name as the fallback. Both empty when the player is nowhere yet.
	CityCode string
	City     string
	// Place is where in the city the player stands; Walk a walk under way.
	Place     Named
	Walk      *WalkView
	Level     int
	Energy    int
	MaxEnergy int
	// Travelling says whether a journey is in progress, so the hub can point
	// at the journey instead of at the departures board.
	Travelling bool
	// Cash and Bank are the player's money, in minor units: what they carry
	// and their bank balance. Private to the player.
	Cash int64
	Bank int64
	// Jail is the sentence the player is serving, nil when free.
	Jail *ProfileJail
}

// Dashboard renders the hub: the same lines and the same buttons as the
// profile, minus what the dashboard does not carry.
func Dashboard(c Context, v DashboardView) *presenter.Response {
	return c.withView(renderDashboard(c, v), ScreenDashboard, v)
}

func renderDashboard(c Context, v DashboardView) *presenter.Response {
	var name, where string
	if v.Name != "" {
		name = c.T("profile.name", map[string]any{"name": v.Name})
	}
	city := c.CityName(v.CityCode, v.City)
	if city != "" && !v.Travelling {
		where = placeLines(c, city, v.Place, v.Walk)
	}

	text := paragraphs(
		body(name, where),
		jailLines(c, v.Jail),
		body(
			c.T("profile.level_plain", map[string]any{"level": max(v.Level, 1)}),
			energyLine(c, v.Energy, v.MaxEnergy, 0),
		),
		moneyLines(c, v.Cash, v.Bank),
	)
	return c.respond(text, hubKeyboard(c, city != "", v.Travelling, v.Jail != nil, nil).Build())
}
