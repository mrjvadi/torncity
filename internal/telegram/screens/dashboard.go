package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
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

// Dashboard renders the hub.
func Dashboard(c Context, v DashboardView) *presenter.Response {
	city := v.City
	if city == "" {
		city = c.T("profile.city_unknown", nil)
	}

	text := c.T("dashboard.body", map[string]any{
		"name":       v.Name,
		"city":       city,
		"level":      v.Level,
		"energy":     v.Energy,
		"max_energy": v.MaxEnergy,
	})

	kb := keyboards.New()
	profile, _ := keyboards.Button(c.T("button.profile", nil), AddrProfile)
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	kb.Row(profile, skills)

	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	travelLabel, travelAddr := c.T("button.travel", nil), AddrMap
	if v.Travelling {
		// A player already on the road wants the journey, not a list of
		// places they cannot leave for.
		travelLabel, travelAddr = c.T("button.journey", nil), AddrTravelStatus
	}
	travel, _ := keyboards.Button(travelLabel, travelAddr)
	kb.Row(worldMap, travel)

	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	kb.Row(social)

	// The hub has no parent, so it carries refresh alone: a back button here
	// would point at the screen the player is already looking at.
	refresh, _ := keyboards.Button(c.T("button.refresh", nil), AddrHome)
	kb.Row(refresh)

	return c.respond(text, kb.Build())
}
