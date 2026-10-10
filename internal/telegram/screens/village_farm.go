package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The farm cycle, the water works and the mill (docs/adr/0067), drawn for Telegram: lines on the work screen and the building
// panel, and the two small answers of the sowing order and the miller's toll.
const (
	ScreenFarmSow  = village.ScreenFarmSow
	ScreenMillToll = village.ScreenMillToll
)

// FarmSow renders the answer of a sowing order.
func FarmSow(c Context, v village.FarmSowView) *presenter.Response {
	text := c.T("village.farm.sow", map[string]any{"farm": c.SettlementBuildingName(v.Farm), "seed": FormatNumber(c, v.Line.Seed),
		"need": FormatNumber(c, int64(v.Line.SowNeed))})
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrWork}))
	return c.withView(c.respond(text, kb.Build()), ScreenFarmSow, v)
}

// MillToll renders the answer of a toll statute.
func MillToll(c Context, v village.MillTollView) *presenter.Response {
	text := c.T("village.mill.toll", map[string]any{"village": v.Village, "percent": percentOf(c, v.TollBPS)})
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrWork}))
	return c.withView(c.respond(text, kb.Build()), ScreenMillToll, v)
}

// farmNeedsSowing says a farm of the cycle has no crop in the ground, so there is no shift to start there.
func farmNeedsSowing(p village.WorkplaceLine) bool {
	if p.Farm == nil || p.Farm.Legacy {
		return false
	}
	switch p.Farm.Stage {
	case "idle", "harvested", "rotted":
		return true
	}
	return false
}

// percentOf writes basis points as a percent with up to one decimal place.
func percentOf(c Context, bps int64) string {
	if bps%100 == 0 {
		return FormatNumber(c, bps/100)
	}
	return fmt.Sprintf("%s.%d", FormatNumber(c, bps/100), (bps%100)/10)
}

// farmStageName is the name of a crop's stage.
func farmStageName(c Context, stage string) string {
	return c.T("building.work.stage."+stage, nil)
}

// farmLines are the lines the building panel shows for a farm: the crop and where its water comes from.
func farmLines(c Context, f *village.FarmLine) []string {
	if f == nil {
		return nil
	}
	if f.Legacy {
		until := time.Time{}
		if f.LegacyUntil != nil {
			until = *f.LegacyUntil
		}
		return []string{c.T("building.work.farm_legacy", map[string]any{"until": FormatDate(c, until)})}
	}
	out := []string{c.T("building.work.farm", map[string]any{
		"stage":    farmStageName(c, f.Stage),
		"sow":      fmt.Sprintf("%s/%s", FormatNumber(c, int64(f.SowDone)), FormatNumber(c, int64(f.SowNeed))),
		"tend":     fmt.Sprintf("%s/%s", FormatNumber(c, int64(f.Tended)), FormatNumber(c, int64(f.TendMax))),
		"harvest":  fmt.Sprintf("%s/%s", FormatNumber(c, int64(f.HarvestDone)), FormatNumber(c, int64(f.HarvestNeed))),
		"expected": FormatNumber(c, f.Expected), "soil": percentOf(c, f.Factors.Soil), "water": percentOf(c, f.Factors.Water),
	})}
	if w := f.Water; w != nil {
		switch w.Reason {
		case "":
			name := ""
			if w.Work != nil {
				name = c.SettlementBuildingName(*w.Work)
			}
			out = append(out, c.T("building.work.farm_water_ok", map[string]any{"work": name}))
		default:
			out = append(out, c.T("building.work.farm_water_"+w.Reason, map[string]any{"percent": percentOf(c, w.ConditionBPS)}))
		}
	}
	return out
}

// extraLines are the lines the building panel shows beyond the node's common ones: the farm, the water work, the mill, the
// pasture.
func extraLines(c Context, w *village.WorkNode) []string {
	var out []string
	out = append(out, farmLines(c, w.Farm)...)
	if wk := w.Water; wk != nil {
		var names []string
		for _, n := range wk.Serves {
			names = append(names, c.SettlementBuildingName(n))
		}
		key := "building.work.water_work_closed"
		if wk.Open {
			key = "building.work.water_work_open"
		}
		farms := c.list(names)
		if farms == "" {
			farms = "-"
		}
		out = append(out, c.T(key, map[string]any{"percent": percentOf(c, wk.ConditionBPS), "farms": farms}))
	}
	if m := w.Mill; m != nil {
		out = append(out, c.T("building.work.mill", map[string]any{"percent": percentOf(c, m.TollBPS),
			"min": percentOf(c, m.MinBPS), "max": percentOf(c, m.MaxBPS), "have": FormatNumber(c, m.Have)}))
	}
	if g := w.Grazing; g != nil {
		out = append(out, c.T("building.work.grazing", map[string]any{"open": FormatNumber(c, int64(g.Open)), "need": FormatNumber(c, int64(g.Need))}))
	}
	if t := toolText(c, w.Tool); t != "" {
		out = append(out, t)
	}
	if rs := recipeLinesOf(c, w.Recipes); len(rs) > 0 {
		out = append(out, c.T("village.recipe.head", nil)+"\n"+body(rs...))
	}
	return out
}

// workPlaceExtras are the farm and the mill on a line of the work screen, with the buttons of the sowing and the grinding.
func workPlaceExtras(c Context, p village.WorkplaceLine, kb *keyboards.Builder) []string {
	var out []string
	if rs := recipeLinesOf(c, p.Recipes); len(rs) > 0 {
		out = append(out, c.T("village.recipe.head", nil)+"\n"+body(rs...))
		workRecipeButtons(c, p, kb)
	}
	if f := p.Farm; f != nil {
		if f.Legacy {
			until := time.Time{}
			if f.LegacyUntil != nil {
				until = *f.LegacyUntil
			}
			out = append(out, c.T("village.work.farm_legacy", map[string]any{"until": FormatDate(c, until)}))
		} else {
			out = append(out, c.T("village.work.farm", map[string]any{"stage": farmStageName(c, f.Stage),
				"sow":     fmt.Sprintf("%d/%d", f.SowDone, f.SowNeed),
				"tend":    fmt.Sprintf("%d/%d", f.Tended, f.TendMax),
				"harvest": fmt.Sprintf("%d/%d", f.HarvestDone, f.HarvestNeed)}))
		}
		if f.CanSow {
			if b, ok := keyboards.Button(c.T("village.work.button.farm_sow", map[string]any{"building": c.SettlementBuildingName(p.Building)}),
				village.AddrFarmSow, p.ID); ok {
				kb.Row(b)
			}
		}
	}
	if m := p.Mill; m != nil {
		out = append(out, c.T("village.work.mill", map[string]any{"percent": percentOf(c, m.TollBPS), "have": FormatNumber(c, m.Have)}))
		if m.Have >= m.Batch && m.Batch > 0 {
			if b, ok := keyboards.Button(c.T("village.work.button.grind", map[string]any{"building": c.SettlementBuildingName(p.Building)}),
				village.AddrMillGrind, p.ID); ok {
				kb.Row(b)
			}
		}
	}
	return out
}

var _ = strings.TrimSpace
