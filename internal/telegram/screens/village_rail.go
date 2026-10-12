package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «ریل» (docs/adr/0075 7), drawn for Telegram: the lines the town is on, their trains and what they wait for, the freight booked, and the
// classes of track that could be laid. Drawing a line, a schedule and a booking are for the web client; here are the daily acts.

// Screens and addresses.
const (
	ScreenRail = village.ScreenRail
	AddrRail   = village.AddrRail
)

// Rail renders the rail screen.
func Rail(c Context, v village.RailView) *presenter.Response {
	return c.withView(renderRail(c, v), ScreenRail, v)
}

func renderRail(c Context, v village.RailView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("rail.title", map[string]any{"name": v.Village})}
	if v.Notice != "" {
		blocks = append(blocks, c.T("rail.notice."+v.Notice, nil))
	}
	if !v.Open {
		blocks = append(blocks, c.T("rail.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrRail}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	if q := v.Quote; q != nil {
		blocks = append(blocks, c.T("rail.quote", map[string]any{"class": c.named("rail_class."+q.Class.Code, q.Class.Name), "from": q.From.Name, "to": q.To.Name,
			"km": FormatNumber(c, q.LengthM/1000), "sections": FormatNumber(c, int64(q.Sections)), "wage": FormatMoney(c, q.Wage), "minutes": FormatNumber(c, q.Minutes)}))
	}
	var ls []string
	for _, l := range v.Lines {
		line := c.T("rail.line", map[string]any{"name": l.Name, "class": c.named("rail_class."+l.Class.Code, l.Class.Name), "km": FormatNumber(c, l.LengthM/1000), "built": FormatNumber(c, int64(l.Built)),
			"sections": FormatNumber(c, int64(l.Sections)), "status": c.T("rail.line_status."+l.Status, nil), "rate": FormatMoney(c, l.RatePerTonneKm)})
		ls = append(ls, line)
	}
	if len(ls) == 0 {
		blocks = append(blocks, c.T("rail.no_lines", nil))
	} else {
		blocks = append(blocks, c.T("rail.lines_head", nil)+"\n"+body(ls...))
	}
	var ts []string
	for _, t := range v.Trains {
		line := c.T("rail.train", map[string]any{"name": t.Name, "wagons": FormatNumber(c, int64(t.Wagons)), "kg": FormatNumber(c, t.CapacityKg), "status": c.T("rail.train_status."+t.Status, nil), "at": t.At.Name})
		if t.Reason != "" {
			line = body(line, c.T("rail.wait."+t.Reason, nil))
		}
		for _, cg := range t.Cargo {
			line = body(line, c.T("rail.cargo", map[string]any{"qty": FormatNumber(c, cg.Qty), "item": cg.Item.Name, "to": cg.To.Name}))
		}
		ts = append(ts, line)
		if v.MayDraw && t.Mine && t.Status == "idle" && len(t.Steps) >= 2 {
			addBtn(kb, c.T("rail.button.start", map[string]any{"name": t.Name}), village.AddrRail, village.RailActionStart, t.ID)
		}
	}
	if len(ts) > 0 {
		blocks = append(blocks, c.T("rail.trains_head", nil)+"\n"+body(ts...))
	}
	if v.MayDraw {
		for _, l := range v.Lines {
			if l.Mine && l.Built < l.Sections && !l.Laying {
				addBtn(kb, c.T("rail.button.build", map[string]any{"name": l.Name}), village.AddrRail, village.RailActionBuild, l.ID)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrRail}))
	return c.respond(paragraphs(blocks...), kb.Build())
}
