package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// ProfileView is the player's own record as the profile screen shows it.
//
// It carries only what a player understands and can act on. The public code
// is on it for that reason — it is the one identifier a player can DO
// something with: give it to a friend, who finds them with /social <code>.
// The record's identifier, stored language and account status are
// deliberately absent:
// none of them means anything to a player, and a value that is on screen ends
// up in a screenshot and then in a support request as if it were a fact about
// them. A city travels as its content CODE, which the screen turns into a
// name in the player's language, plus its authored name as the fallback for a
// city nobody has translated yet; the code itself is never shown.
type ProfileView struct {
	Name string
	// Code is the player's public code (internal/shared/playercode). Empty
	// only for a record that has none, and then the line is left out.
	Code string
	// CityCode and City are the player's city: its content code and its
	// authored name. Both empty when the player is nowhere yet; an empty city
	// is simply not shown.
	CityCode string
	City     string

	Level int
	XP    int64
	// NextLevelXP is the XP total at which the next level is reached, from
	// the domain's curve. Zero means there is no next level.
	NextLevelXP int64

	Energy    int
	MaxEnergy int
	// EnergyFullIn is how long until energy is full again, zero when it
	// already is.
	EnergyFullIn time.Duration
	Health       int
	MaxHealth    int

	// Travelling says a journey is in progress. TravelTo and TravelRemaining
	// describe it; the profile then shows the journey instead of a city the
	// player is no longer standing in.
	Travelling      bool
	TravelToCode    string
	TravelTo        string
	TravelRemaining time.Duration
}

// isNewPlayer reports whether this is someone who has not done anything yet,
// the one moment the welcome line earns its space.
func (v ProfileView) isNewPlayer() bool {
	return v.XP == 0 && v.Level <= 1 && !v.Travelling
}

// Profile renders the player's record. It is also the home screen: /start and
// every back button land here, so it carries the way to every other screen.
func Profile(c Context, v ProfileView) *presenter.Response {
	var welcome string
	if v.isNewPlayer() {
		welcome = c.T("profile.body", nil)
	}

	city := c.CityName(v.CityCode, v.City)
	travelTo := c.CityName(v.TravelToCode, v.TravelTo)

	var where string
	switch {
	case v.Travelling && travelTo != "":
		where = c.T("profile.travelling", map[string]any{
			"city":      travelTo,
			"remaining": FormatDuration(c, v.TravelRemaining),
		})
	case city != "":
		where = c.T("profile.city", map[string]any{"city": city})
	}

	var name string
	if v.Name != "" {
		name = c.T("profile.name", map[string]any{"name": v.Name})
	}

	var code string
	if v.Code != "" {
		code = body(
			c.T("profile.code", map[string]any{"code": v.Code}),
			c.T("profile.code_hint", map[string]any{"code": v.Code}),
		)
	}

	text := paragraphs(
		welcome,
		body(name, where),
		code,
		body(
			levelLine(c, v.Level, v.XP, v.NextLevelXP),
			energyLine(c, v.Energy, v.MaxEnergy, v.EnergyFullIn),
			c.T("profile.health", map[string]any{
				"health":     FormatNumber(int64(v.Health)),
				"max_health": FormatNumber(int64(v.MaxHealth)),
			}),
		),
	)

	return c.respond(text, hubKeyboard(c, city != "", v.Travelling).Build())
}

// levelLine shows the level and how far the next one is. A player at the top
// of the curve sees the level alone: "0 XP to level 101" would be a lie.
func levelLine(c Context, level int, xp, nextLevelXP int64) string {
	if level < 1 {
		level = 1
	}
	if nextLevelXP <= xp {
		return c.T("profile.level_max", map[string]any{"level": level})
	}
	return c.T("profile.level", map[string]any{
		"level":      level,
		"xp_to_next": FormatNumber(nextLevelXP - xp),
		"next_level": level + 1,
	})
}

// energyLine shows energy, and when it is not full, how long until it is —
// which is the one thing a player short of energy wants to know.
func energyLine(c Context, energy, maxEnergy int, fullIn time.Duration) string {
	args := map[string]any{
		"energy":     FormatNumber(int64(energy)),
		"max_energy": FormatNumber(int64(maxEnergy)),
	}
	if energy < maxEnergy && fullIn > 0 {
		args["duration"] = FormatDuration(c, fullIn)
		return c.T("profile.energy_refilling", args)
	}
	return c.T("profile.energy", args)
}

// hubKeyboard is the navigation of the home screen.
//
// It offers only what the player can do right now: the journey instead of
// the map while travelling, and no map at all for a player who is not in a
// city yet, because every departure would be refused. It has no back button,
// because it is the screen every back button leads to.
func hubKeyboard(c Context, hasCity, travelling bool) *keyboards.Builder {
	kb := keyboards.New()
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	switch {
	case travelling:
		journey, _ := keyboards.Button(c.T("button.journey", nil), AddrTravelStatus)
		kb.Row(skills, journey)
	case hasCity:
		worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
		kb.Row(skills, worldMap)
	default:
		kb.Row(skills)
	}

	social, _ := keyboards.Button(c.T("button.social", nil), AddrFriendList)
	settings, _ := keyboards.Button(c.T("button.settings", nil), AddrSettings)
	kb.Row(social, settings)

	refresh, _ := keyboards.Button(c.T("button.refresh", nil), AddrProfile)
	kb.Row(refresh)
	return kb
}
