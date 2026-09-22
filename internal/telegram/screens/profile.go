package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// ProfileView is the player's own record as the profile screen shows it.
//
// City is the resolved NAME of the city, never its identifier: an identifier
// means nothing to a player and, once it is on screen, it ends up in a
// screenshot and then in a support request as if it were a fact about them.
type ProfileView struct {
	ID       string
	Language string
	Status   string
	City     string

	Level     int
	XP        int64
	Energy    int
	MaxEnergy int
	Health    int
	MaxHealth int
}

// Profile renders the player's record.
func Profile(c Context, v ProfileView) *presenter.Response {
	city := v.City
	if city == "" {
		city = c.T("profile.city_unknown", nil)
	}

	// The record and the condition are two messages, not one.
	//
	// profile.body is who the player IS: the identity fields that were there
	// before this screen grew. profile.condition is how they ARE right now,
	// and it changes on every read as energy accrues. Keeping them apart
	// means a translator can rework the condition block — which is a table
	// of numbers and the hardest part to word well — without touching the
	// identity block, and it keeps each message's placeholder set small
	// enough to check by eye.
	text := paragraphs(
		c.T("profile.body", map[string]any{
			"id":       v.ID,
			"language": v.Language,
			"status":   v.Status,
		}),
		c.T("profile.condition", map[string]any{
			"city":       city,
			"level":      v.Level,
			"xp":         v.XP,
			"energy":     v.Energy,
			"max_energy": v.MaxEnergy,
			"health":     v.Health,
			"max_health": v.MaxHealth,
		}),
	)

	kb := keyboards.New()
	skills, _ := keyboards.Button(c.T("button.skills", nil), AddrSkills)
	worldMap, _ := keyboards.Button(c.T("button.map", nil), AddrMap)
	kb.Row(skills, worldMap)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrProfile}))

	return c.respond(text, kb.Build())
}
