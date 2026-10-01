package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"sort"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// This file holds two village screens that belong together: the panel of one
// placed building (what it is, what it does, what it holds), and the batch
// placement of many one-lot buildings at once (a run of roads).
//
// The panel discloses progressively: it shows what the building IS and does
// now; the next level of its role and that level's prerequisites are shown
// only after "upgrade" is pressed (mode "up"), and demolition is a small,
// last, confirmed action (mode "dm"), never the panel's face.

// Screens.
const (
	ScreenBuildingView    = village.ScreenBuildingView
	ScreenLotBatchConfirm = village.ScreenLotBatchConfirm
)

// BuildingPanel renders one building's panel.
func BuildingPanel(c Context, v BuildingView) *presenter.Response {
	return c.withGroupView(renderBuildingPanel(c, v), ScreenBuildingView, v)
}

func buildingDescKey(v BuildingView) string {
	switch {
	case v.Kind == BuildingKindRoad || v.Kind == BuildingKindCivicHall:
		return "building.desc." + v.Kind
	case v.Role != "":
		return "building.desc.role." + v.Role
	default:
		return "building.desc.code." + v.Building.Code
	}
}

func renderBuildingPanel(c Context, v BuildingView) *presenter.Response {
	name := c.SettlementBuildingName(v.Building)
	blocks := []string{c.T("building.view.title", map[string]any{"building": name})}

	switch v.Mode {
	case BuildingModeDemolish:
		blocks = append(blocks, c.T("building.view.demolish_ask", map[string]any{"building": name}))
		return c.respond(paragraphs(blocks...), confirmKeyboard(c, v, "building.view.demolish_yes", AddrBuildDemolish))
	case BuildingModeCancel:
		blocks = append(blocks, c.T("building.view.cancel_ask", map[string]any{"building": name}))
		return c.respond(paragraphs(blocks...), confirmKeyboard(c, v, "building.view.cancel_yes", AddrBuildCancel))
	case BuildingModeUpgrade:
		return renderBuildingUpgrade(c, v, name, blocks)
	}

	blocks = append(blocks, c.T(buildingDescKey(v), nil))
	if v.State == BuildingStateBuilding {
		blocks = append(blocks, c.T("building.view.under_construction", map[string]any{
			"percent": v.ProgressPercent, "time": FormatClock(c, v.FinishAt), "duration": FormatDuration(c, v.Left),
		}))
	} else {
		blocks = append(blocks, c.T("building.view.state_complete", nil))
		blocks = append(blocks, buildingBody(c, v)...)
	}

	kb := keyboards.New()
	if v.State == BuildingStateComplete && v.Kind == BuildingKindCivicHall {
		kb.Row(villageButtons(c, "village.button.overview", AddrVillageOverview, "village.button.knowledge", AddrKnowledgeList)...)
		kb.Row(villageButtons(c, "village.button.build", AddrBuildMenu, "village.button.progress", AddrConstructionProgress)...)
	}
	if v.CanManage {
		switch {
		case v.State == BuildingStateBuilding:
			if b, ok := keyboards.Button(c.T("building.button.cancel", nil), AddrBuildingView, v.ID, BuildingModeCancel); ok {
				kb.Row(b)
			}
		case v.HasUpgrade:
			if b, ok := keyboards.Button(c.T("building.button.upgrade", nil), AddrBuildingView, v.ID, BuildingModeUpgrade); ok {
				kb.Row(b)
			}
		}
		if v.State == BuildingStateComplete {
			// The one destructive action, last and quiet.
			if b, ok := keyboards.Button(c.T("building.button.demolish", nil), AddrBuildingView, v.ID, BuildingModeDemolish); ok {
				kb.Row(b)
			}
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrConstructionProgress, RefreshData: keyboards.Data(AddrBuildingView, v.ID)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// confirmKeyboard is the yes/back pair under a destructive action's warning.
func confirmKeyboard(c Context, v BuildingView, yesLabel, addr string) *presenter.Keyboard {
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T(yesLabel, nil), addr, v.ID); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildingView, v.ID)}))
	return kb.Build()
}

// buildingBody is the type-specific part of a finished building's panel.
func buildingBody(c Context, v BuildingView) []string {
	var out []string
	switch v.Kind {
	case BuildingKindStorage:
		if v.StockCapacity > 0 {
			out = append(out, c.T("building.storage.capacity", map[string]any{"used": FormatNumber(c, v.StockUsed), "capacity": FormatNumber(c, v.StockCapacity)}))
		}
		if len(v.Stock) == 0 {
			out = append(out, c.T("building.storage.empty", nil))
			break
		}
		lines := make([]string, 0, len(v.Stock))
		stock := append([]BuildingStockLine(nil), v.Stock...)
		sort.SliceStable(stock, func(i, j int) bool { return stock[i].Item.Code < stock[j].Item.Code })
		for _, s := range stock {
			label := c.ItemName(s.Item)
			if s.Kind == "component" {
				label = c.ComponentName(s.Item)
			}
			lines = append(lines, c.T("building.storage.line", map[string]any{"item": label, "qty": FormatNumber(c, s.Qty)}))
		}
		out = append(out, c.T("building.storage.title", nil), body(lines...))
	case BuildingKindSchool:
		out = append(out, c.T("building.school.literacy", map[string]any{"percent": v.LiteracyPercent}))
		if v.Teaching {
			out = append(out, c.T("building.school.teaching", nil))
		} else {
			out = append(out, c.T("building.school.idle", nil))
		}
	case BuildingKindCivicHall:
		out = append(out,
			c.T("building.civic.population", map[string]any{"population": FormatNumber(c, int64(v.Population))}),
			c.T("village.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		)
		if v.Research != nil {
			out = append(out, c.T("building.civic.research", map[string]any{
				"knowledge": c.SettlementKnowledgeName(v.Research.Knowledge), "duration": FormatDuration(c, v.Research.Left),
			}))
		} else {
			out = append(out, c.T("building.civic.no_research", nil))
		}
	}
	var effects []string
	for _, e := range v.Effects {
		val := e.Value
		key := "building.effect." + e.Target
		if e.Target != "housing_capacity" {
			val /= 100
		}
		effects = append(effects, c.T(key, map[string]any{"value": FormatNumber(c, val)}))
	}
	if len(effects) > 0 {
		out = append(out, body(effects...))
	}
	if v.Upkeep > 0 {
		out = append(out, c.T("building.view.upkeep", map[string]any{"amount": FormatMoney(c, v.Upkeep)}))
	}
	return out
}

func renderBuildingUpgrade(c Context, v BuildingView, name string, blocks []string) *presenter.Response {
	blocks = append(blocks, c.T("building.upgrade.intro", map[string]any{"building": name}))
	kb := keyboards.New()
	var lines []string
	for _, u := range v.Upgrades {
		args := map[string]any{
			"building": c.SettlementBuildingName(u.Building), "cost": FormatMoney(c, u.CostMoney), "time": FormatDuration(c, u.BuildTime),
		}
		if u.Available {
			lines = append(lines, c.T("building.upgrade.line", args))
			if b, ok := keyboards.Button(c.T("build.button.place", args), AddrBuildLots, u.Building.Code); ok {
				kb.Row(b)
			}
			continue
		}
		if u.NeedsTier != "" {
			args["tier"] = c.T("village.tier_name."+u.NeedsTier, nil)
			lines = append(lines, c.T("building.upgrade.needs_tier", args))
			continue
		}
		names := make([]string, 0, len(u.Missing))
		for _, m := range u.Missing {
			names = append(names, c.SettlementKnowledgeName(m))
		}
		args["missing"] = c.list(names)
		if len(names) > 0 {
			lines = append(lines, c.T("building.upgrade.locked", args))
		} else {
			lines = append(lines, c.T("building.upgrade.locked_other", args))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("building.upgrade.none", nil))
	}
	blocks = append(blocks, body(lines...))
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildingView, v.ID)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// ---------------------------------------------------------------------
// Batch placement: several 1x1 lots in one command
// ---------------------------------------------------------------------

// LotBatchConfirm renders the batch confirmation.
func LotBatchConfirm(c Context, v LotBatchConfirmView) *presenter.Response {
	return c.withGroupView(renderLotBatchConfirm(c, v), ScreenLotBatchConfirm, v)
}

func renderLotBatchConfirm(c Context, v LotBatchConfirmView) *presenter.Response {
	var materialLines []string
	for _, m := range v.Materials {
		materialLines = append(materialLines, c.T("build.confirm.material_line", map[string]any{
			"component": c.ComponentName(m.Component), "quantity": m.Quantity,
		}))
	}
	text := paragraphs(
		c.T("build.batch.title", map[string]any{"building": c.SettlementBuildingName(v.Building), "count": FormatNumber(c, int64(v.Count))}),
		c.T("build.batch.body", map[string]any{"cost": FormatMoney(c, v.CostMoney), "time": FormatDuration(c, v.BuildTime)}),
		body(materialLines...),
	)
	kb := keyboards.New()
	// The lots travel as the line's two ends when they are a straight or bent
	// run; the confirm button repeats exactly what the grid sent.
	if len(v.Lots) > 0 {
		first, last := v.Lots[0], v.Lots[len(v.Lots)-1]
		if b, ok := keyboards.Button(c.T("build.batch.button", nil), AddrBuildPlaceMany, v.Building.Code,
			LotToken(first.X, first.Y, false), LotToken(last.X, last.Y, false), VillageBuildConfirm); ok {
			kb.Row(b)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildMenu)}))
	return c.respond(text, kb.Build())
}

// ---------------------------------------------------------------------
// Land: buying more grid
// ---------------------------------------------------------------------

// AddrGridGrow and ScreenGridGrow are the land purchase's address and screen.
const (
	AddrGridGrow   = village.AddrGridGrow
	ScreenGridGrow = village.ScreenGridGrow
)

// GridGrowConfirm renders the land purchase confirmation.
func GridGrowConfirm(c Context, v GridGrowView) *presenter.Response {
	return c.withGroupView(renderGridGrow(c, v), ScreenGridGrow, v)
}

func renderGridGrow(c Context, v GridGrowView) *presenter.Response {
	text := paragraphs(
		c.T("grow.title", map[string]any{"name": v.SettlementName}),
		c.T("grow.body", map[string]any{
			"side": FormatNumber(c, int64(v.Side)), "new_side": FormatNumber(c, int64(v.NewSide)),
			"lots": FormatNumber(c, int64(v.LotsGained)), "buildable": FormatNumber(c, int64(v.BuildableGained)),
			"price": FormatMoney(c, v.Price), "treasury": FormatMoney(c, v.Treasury),
		}),
		c.T("grow.note", nil),
	)
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("grow.button", nil), AddrGridGrow, VillageBuildConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrBuildMenu)}))
	return c.respond(text, kb.Build())
}
