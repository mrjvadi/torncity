package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/military"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// StateRetrofit renders a branch commander's retrofit of a state asset: the
// plan, or its start. It is worded as a company's retrofit is, but leads back
// to the forces, since the state has no orders to go back to.
func StateRetrofit(c Context, v military.StateRetrofitView) *presenter.Response {
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrForces, v.Country)}))
	if v.Started {
		return c.respond(c.T("production.retrofit_started", map[string]any{"good": c.GoodName(v.Good),
			"from": FormatNumber(c, v.FromVer), "to": FormatNumber(c, v.ToVer), "time": FormatClock(c, v.FinishAt),
			"duration": FormatDuration(c, v.Duration)}), kb.Build())
	}
	return c.respond(c.T("production.retrofit_plan", map[string]any{"good": c.GoodName(v.Good),
		"from": FormatNumber(c, v.FromVer), "to": FormatNumber(c, v.ToVer), "duration": FormatDuration(c, v.Duration)}), kb.Build())
}
