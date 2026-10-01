package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// renderUnavailable is the answer of an economy screen whose service the
// settlement the player stands in does not offer (CLAUDE.md section 2): what is
// missing, from which stage it is offered, what a settlement needs to run it
// and the nearest place that has it, a journey away.
func renderUnavailable(c Context, u *economy.Unavailable, backAddr string) *presenter.Response {
	service := c.coded("unavailable.service.", u.Service, "unavailable.service_unknown")
	lines := []string{c.T("unavailable.title", map[string]any{"service": service})}
	if u.Stage == "support" && u.Nearest != nil {
		lines = append(lines, c.T("unavailable.only_support", map[string]any{
			"service": service, "place": c.CityName(u.Nearest.Code, u.Nearest.Name)}))
	} else {
		lines = append(lines, c.T("unavailable.from", map[string]any{
			"service": service,
			"stage":   c.coded("unavailable.stage.", u.Stage, "unavailable.stage_unknown"),
			"here":    c.coded("unavailable.stage.", u.Here, "unavailable.stage_unknown"),
		}))
	}
	var needs []string
	for _, b := range u.Requires {
		if b.Code != "" {
			needs = append(needs, c.SettlementBuildingName(Named{Code: b.Code, Name: b.Code}))
		}
	}
	if len(needs) > 0 {
		lines = append(lines, c.T("unavailable.requires", map[string]any{"buildings": joinWith(c, needs)}))
	}
	kb := keyboards.New()
	if u.Nearest != nil && u.Nearest.Code != "" {
		city := c.CityName(u.Nearest.Code, u.Nearest.Name)
		lines = append(lines, c.T("unavailable.nearest", map[string]any{"city": city}))
		if btn, ok := keyboards.Button(c.T("button.travel_to", map[string]any{"city": city}), economy.AddrTravelOptions, u.Nearest.Code); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: backAddr}))
	return c.respond(body(lines...), kb.Build())
}
