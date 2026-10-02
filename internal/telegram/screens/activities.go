package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The Activities hub, the work home and the health home (docs/adr/0038).
// They are web screens first; Telegram draws the same facts as plain text and
// the same entries as buttons, from its own catalogue (activities.*).

// ActivitiesHub renders the hub of activities.
func ActivitiesHub(c Context, v life.ActivitiesHubView) *presenter.Response {
	return c.withView(renderActivitiesHub(c, v), life.ScreenActivitiesHub, v)
}

func renderActivitiesHub(c Context, v life.ActivitiesHubView) *presenter.Response {
	kb := keyboards.New()
	buttons := make([]presenter.Button, 0, len(v.Entries))
	for _, e := range v.Entries {
		if b, ok := keyboards.Button(c.T("activities.entry."+e.Code, nil), presentation.Do(e.Command).Address()); ok {
			buttons = append(buttons, b)
		}
	}
	kb.Grid(2, buttons...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: life.AddrActivitiesHub}))
	return c.respond(c.T("activities.hub.title", nil), kb.Build())
}

// WorkHome renders the work home.
func WorkHome(c Context, v village.WorkHomeView) *presenter.Response {
	return c.withView(renderWorkHome(c, v), village.ScreenWorkHome, v)
}

func renderWorkHome(c Context, v village.WorkHomeView) *presenter.Response {
	var working, empty string
	if v.Working != nil {
		working = c.T("activities.work.working", map[string]any{
			"building": c.SettlementBuildingName(v.Working.Building), "duration": FormatDuration(c, v.Working.Left),
			"wage": FormatMoney(c, v.Working.Wage),
		})
	}
	if v.Empty != "" {
		empty = c.T("activities.work.empty."+v.Empty, nil)
	}
	counts := c.T("activities.work.counts", map[string]any{
		"jobs": len(v.Jobs), "places": len(v.Workplaces), "village": v.Place.Name,
	})
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: life.AddrActivitiesHub, RefreshData: village.AddrWorkHome}))
	return c.respond(paragraphs(c.T("activities.work.title", nil), counts, working, empty), kb.Build())
}

// HealthHome renders the health home.
func HealthHome(c Context, v life.HealthHomeView) *presenter.Response {
	return c.withView(renderHealthHome(c, v), life.ScreenHealthHome, v)
}

func renderHealthHome(c Context, v life.HealthHomeView) *presenter.Response {
	lines := []string{c.T("activities.health.health", map[string]any{
		"health": FormatNumber(c, int64(v.Health)), "max": FormatNumber(c, int64(v.MaxHealth)),
	})}
	if v.Admitted != nil {
		lines = append(lines, c.T("activities.health.admitted", map[string]any{"duration": FormatDuration(c, v.Admitted.Remaining)}))
	}
	for _, f := range v.Facilities {
		lines = append(lines, c.T("activities.health.facility."+f.Kind, map[string]any{"name": c.SettlementBuildingName(f.Building)}))
	}
	if v.Empty != "" {
		lines = append(lines, c.T("activities.health.empty."+v.Empty, nil))
	}
	if v.Refer != nil {
		lines = append(lines, c.T("activities.health.refer", map[string]any{"city": c.CityName(v.Refer.Code, v.Refer.Name)}))
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: life.AddrActivitiesHub, RefreshData: life.AddrHealthHome}))
	return c.respond(paragraphs(c.T("activities.health.title", nil), body(lines...)), kb.Build())
}
