package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The development readout (docs/adr/0044-organic-growth-alliances-countries.md
// section 4.5): what the settlement carries against what it can carry, the
// service buildings it has, and the goals still ahead. A group screen with no
// act. It names no stage; the overview lists its button only while
// growth.capabilities is on.

// ScreenVillageDevelopment is the structured screen for clients.
const ScreenVillageDevelopment = village.ScreenVillageDevelopment

// AddrVillageDevelopment addresses the readout.
const AddrVillageDevelopment = village.AddrVillageDevelopment

// VillageDevelopment renders the readout.
func VillageDevelopment(c Context, v village.DevelopmentView) *presenter.Response {
	return c.withView(renderVillageDevelopment(c, v), ScreenVillageDevelopment, v)
}

func developmentLines(c Context, v village.DevelopmentView) string {
	var lines []string
	for _, d := range v.Dimensions {
		args := map[string]any{"load": FormatNumber(c, d.Load), "capacity": FormatNumber(c, d.Capacity)}
		key := "village.development.dimension." + d.Code
		if d.Capacity > 0 {
			key += "_of"
		}
		lines = append(lines, c.T(key, args))
	}
	return body(lines...)
}

func developmentRoles(c Context, v village.DevelopmentView) string {
	if len(v.Roles) == 0 {
		return c.T("village.development.no_roles", nil)
	}
	lines := []string{c.T("village.development.roles_title", nil)}
	for _, r := range v.Roles {
		lines = append(lines, c.T("village.development.role", map[string]any{
			"role":  c.T("village.development.role_name."+r.Role, nil),
			"level": FormatNumber(c, int64(r.Level)),
		}))
	}
	return body(lines...)
}

func developmentNext(c Context, v village.DevelopmentView) string {
	if len(v.Next) == 0 {
		return c.T("village.development.next_none", nil)
	}
	lines := []string{c.T("village.development.next_title", nil)}
	for _, n := range v.Next {
		name := Named{Code: n.Code, Name: n.Name}
		if n.Kind == "research" {
			lines = append(lines, c.T("village.development.next_research", map[string]any{"name": c.SettlementKnowledgeName(name)}))
		} else {
			lines = append(lines, c.T("village.development.next_build", map[string]any{"name": c.SettlementBuildingName(name)}))
		}
	}
	return body(lines...)
}

func renderVillageDevelopment(c Context, v village.DevelopmentView) *presenter.Response {
	blocks := []string{
		c.T("village.development.title", map[string]any{"village": v.Village}),
		developmentLines(c, v),
		developmentRoles(c, v),
		developmentNext(c, v),
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrVillageDevelopment}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// developmentButton is the overview's button to the readout.
func developmentButton(c Context, kb *keyboards.Builder) {
	kb.Add(c.T("village.development.button", nil), AddrVillageDevelopment)
}
