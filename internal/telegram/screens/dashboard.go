package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

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
	return c.respond(text, hubKeyboard(c, city != "", v.Travelling, v.Jail != nil, nil, nil).Build())
}
