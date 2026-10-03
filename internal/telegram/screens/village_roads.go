package screens

import (
	"sort"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Roads that open land (docs/adr/0044 5.5, owner decision 2026-10-03). The
// road itself is drawn on the web (a drag over the world); Telegram shows the
// quote and the stored plan, lists the drawn roads on the land screen and
// offers the lots they opened for purchase.

const (
	RoadNoNetwork   = village.RoadNoNetwork
	RoadEndBlocked  = village.RoadEndBlocked
	RoadWater       = village.RoadWater
	RoadNoRoute     = village.RoadNoRoute
	RoadNoBridge    = village.RoadNoBridge
	RoadTooLong     = village.RoadTooLong
	RoadForeign     = village.RoadForeign
	RoadOpenCap     = village.RoadOpenCap
	RoadInUse       = village.RoadInUse
	RoadSame        = village.RoadSame
	RoadClassLocked = village.RoadClassLocked

	AddrRoadPlan   = village.AddrRoadPlan
	AddrRoadCancel = village.AddrRoadCancel

	ScreenRoadQuote     = village.ScreenRoadQuote
	ScreenRoadPlanned   = village.ScreenRoadPlanned
	ScreenRoadCancelled = village.ScreenRoadCancelled
)

type (
	RoadQuoteView     = village.RoadQuoteView
	RoadCancelledView = village.RoadCancelledView
	RoadPlanLine      = village.RoadPlanLine
)

// isRoadRefusal reports whether a refusal kind is one of the road kinds.
func isRoadRefusal(kind string) bool {
	switch kind {
	case RoadNoNetwork, RoadEndBlocked, RoadWater, RoadNoRoute, RoadNoBridge, RoadTooLong, RoadForeign, RoadOpenCap,
		RoadInUse, RoadSame, RoadClassLocked:
		return true
	}
	return false
}

func roadArgs(c Context, v RoadQuoteView) map[string]any {
	return map[string]any{
		"name": v.SettlementName, "class": v.Class.Name,
		"lots": FormatNumber(c, int64(v.Lots)), "length": FormatNumber(c, int64(v.LengthM)),
		"climb": FormatNumber(c, int64(v.ClimbM)), "grade": FormatNumber(c, int64(v.MaxGradeBPS/100)),
		"crossings": FormatNumber(c, int64(v.Crossings)),
		"lot_cost":  FormatMoney(c, v.LotCost), "full": FormatMoney(c, v.FullCost),
		"opens": FormatNumber(c, int64(v.Opens)), "usable": FormatNumber(c, int64(v.Usable)),
		"water": FormatNumber(c, int64(v.Water)), "steep": FormatNumber(c, int64(v.Steep)),
		"to_row": FormatNumber(c, int64(v.To.Y+1)), "to_col": FormatNumber(c, int64(v.To.X+1)),
	}
}

func roadBody(c Context, v RoadQuoteView) []string {
	args := roadArgs(c, v)
	blocks := []string{c.T("roadland.quote.body", args)}
	if v.Crossings > 0 {
		blocks = append(blocks, c.T("roadland.quote.crossings", args))
	}
	blocks = append(blocks, c.T("roadland.quote.opens", args))
	return blocks
}

// RoadQuote renders the quote of a road drawn out of the village.
func RoadQuote(c Context, v RoadQuoteView) *presenter.Response {
	return c.withGroupView(renderRoadQuote(c, v), ScreenRoadQuote, v)
}

func renderRoadQuote(c Context, v RoadQuoteView) *presenter.Response {
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("roadland.quote.button_yes", nil), AddrRoadPlan,
		LotToken(v.To.X, v.To.Y, false), LotToken(v.From.X, v.From.Y, false), v.Class.Code, VillageBuildConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	blocks := append([]string{c.T("roadland.quote.title", roadArgs(c, v))}, roadBody(c, v)...)
	blocks = append(blocks, c.T("roadland.quote.note", nil))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// RoadPlanned renders the stored plan.
func RoadPlanned(c Context, v RoadQuoteView) *presenter.Response {
	return c.withGroupView(renderRoadPlanned(c, v), ScreenRoadPlanned, v)
}

func renderRoadPlanned(c Context, v RoadQuoteView) *presenter.Response {
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	args := roadArgs(c, v)
	return c.respond(paragraphs(c.T("roadland.planned.title", args), c.T("roadland.planned.body", args)), kb.Build())
}

// RoadCancelled renders a road taken back.
func RoadCancelled(c Context, v RoadCancelledView) *presenter.Response {
	return c.withGroupView(renderRoadCancelled(c, v), ScreenRoadCancelled, v)
}

func renderRoadCancelled(c Context, v RoadCancelledView) *presenter.Response {
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLand}))
	return c.respond(c.T("roadland.cancelled", map[string]any{"name": v.SettlementName, "lots": FormatNumber(c, int64(v.Lots))}), kb.Build())
}

// roadLinesBlock lists the drawn roads on the land screen.
func roadLinesBlock(c Context, v LandView) string {
	if len(v.Roads) == 0 {
		return ""
	}
	lines := []string{c.T("roadland.land.title", nil)}
	for _, r := range v.Roads {
		lines = append(lines, c.T("roadland.land.line", map[string]any{
			"class": r.Class.Name, "lots": FormatNumber(c, int64(r.Lots)), "built": FormatNumber(c, int64(r.Built)),
			"open": FormatNumber(c, int64(r.Open)), "sold": FormatNumber(c, int64(r.Sold)),
		}))
	}
	return paragraphs(lines...)
}

// outerBuyRows offers, as buttons, the cheapest lots the roads opened that the
// viewer can buy now (a keyboard cannot hold a map of them).
func outerBuyRows(c Context, v LandView) [][]presenter.Button {
	if !v.CanBuy {
		return nil
	}
	var free []village.LandCell
	for _, cell := range v.Outer {
		if cell.State == LandFree && cell.Access != village.AccessNone {
			free = append(free, cell)
		}
	}
	sort.SliceStable(free, func(i, j int) bool {
		if free[i].Cost != free[j].Cost {
			return free[i].Cost < free[j].Cost
		}
		if free[i].Y != free[j].Y {
			return free[i].Y < free[j].Y
		}
		return free[i].X < free[j].X
	})
	const limit = 6
	if len(free) > limit {
		free = free[:limit]
	}
	var buttons []presenter.Button
	for _, cell := range free {
		label := c.T("roadland.land.lot_button", map[string]any{"row": FormatNumber(c, int64(cell.Y+1)), "col": FormatNumber(c, int64(cell.X+1)), "cost": FormatMoney(c, cell.Cost)})
		if b, ok := keyboards.Button(label, AddrLotBuy, LotToken(cell.X, cell.Y, false)); ok {
			buttons = append(buttons, b)
		}
	}
	var rows [][]presenter.Button
	for i := 0; i < len(buttons); i += 2 {
		end := i + 2
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, buttons[i:end])
	}
	return rows
}
