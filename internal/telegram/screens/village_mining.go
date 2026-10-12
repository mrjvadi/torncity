package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «معدن» (docs/adr/0075), drawn for Telegram: the deposits under the town, its mineheads with their levels and crew, the works in hand
// and the modules that could be ordered. Carriage and rail have their own screens (village_haul.go, village_rail.go).

// Screens and addresses.
const (
	ScreenMining = village.ScreenMining
	AddrMining   = village.AddrMining
)

// isLogisticsRefusal reports a refusal kind of the mine, the road and the rail screens.
func isLogisticsRefusal(kind string) bool {
	return strings.HasPrefix(kind, "mine_") || strings.HasPrefix(kind, "haul_") || strings.HasPrefix(kind, "rail_") || strings.HasPrefix(kind, "chain_")
}

// Mining renders the mining screen.
func Mining(c Context, v village.MiningView) *presenter.Response {
	return c.withView(renderMining(c, v), ScreenMining, v)
}

func renderMining(c Context, v village.MiningView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("mining.title", map[string]any{"name": v.Village})}
	if v.Notice != "" {
		blocks = append(blocks, c.T("mining.notice."+v.Notice, nil))
	}
	if !v.Open {
		date := ""
		if v.StartsAt != nil {
			date = FormatDate(c, *v.StartsAt)
		}
		blocks = append(blocks, c.T("mining.off", map[string]any{"date": date}))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrMining}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	var dep []string
	for _, d := range v.Deposits {
		name := c.named("resource."+d.Resource.Code, d.Resource.Name)
		if !d.Surveyed {
			dep = append(dep, c.T("mining.deposit_unknown", map[string]any{"resource": name}))
			continue
		}
		dep = append(dep, c.T("mining.deposit", map[string]any{"resource": name, "grade": FormatNumber(c, int64(d.GradePermille)/10), "band": c.T("mining.band."+d.Band, nil),
			"estimate": FormatNumber(c, d.CapacityEstimate), "taken": FormatNumber(c, d.TakenUnits),
			"s0": FormatNumber(c, d.LeftBPS[0]/100), "s1": FormatNumber(c, d.LeftBPS[1]/100), "s2": FormatNumber(c, d.LeftBPS[2]/100)}))
	}
	if len(dep) == 0 {
		blocks = append(blocks, c.T("mining.no_deposit", nil))
	} else {
		blocks = append(blocks, body(dep...))
	}
	if v.PitsFrom != nil {
		blocks = append(blocks, c.T("mining.pits_from", map[string]any{"date": FormatDate(c, *v.PitsFrom)}))
	}
	for _, m := range v.Mines {
		lines := []string{c.T("mining.mine", map[string]any{"name": c.SettlementBuildingName(m.Building), "shafts": FormatNumber(c, int64(m.Shafts))})}
		mods := []string{}
		if m.OreBin {
			mods = append(mods, c.T("module_kind.ore_bin", nil))
		}
		if m.Hoist >= 0 {
			key := "module_kind.hoist"
			if m.Hoist == 1 {
				key = "module_kind.hoist_gin"
			}
			mods = append(mods, c.T(key, nil))
		}
		if m.Pump >= 0 {
			mods = append(mods, c.T("module_kind.pump", nil))
		}
		if m.Vent {
			mods = append(mods, c.T("module_kind.vent_shaft", nil))
		}
		if m.AditM > 0 {
			mods = append(mods, c.T("mining.adit", map[string]any{"m": FormatNumber(c, m.AditM)}))
		}
		if len(mods) > 0 {
			lines = append(lines, strings.Join(mods, "، "))
		}
		for _, l := range m.Levels {
			lines = append(lines, c.T("mining.level", map[string]any{"depth": FormatNumber(c, l.DepthM), "props": FormatNumber(c, l.ConditionBPS/100), "water": FormatNumber(c, l.WaterBPS/100),
				"status": c.T("mining.status."+l.Status, nil), "left": FormatNumber(c, l.LeftBPS/100)}))
		}
		var crew []string
		for _, r := range m.Crew {
			crew = append(crew, c.T("mining.crew_line", map[string]any{"role": c.named("staff_role."+r.Role.Code, r.Role.Name), "working": FormatNumber(c, int64(r.Working)), "slots": FormatNumber(c, int64(r.Slots))}))
		}
		lines = append(lines, strings.Join(crew, "، "))
		lines = append(lines, c.T("mining.output", map[string]any{"face": FormatNumber(c, m.FaceKg), "hoist": FormatNumber(c, m.HoistKg), "haul": FormatNumber(c, m.HaulKg),
			"out": FormatNumber(c, m.OutputKg), "limit": c.T("mining.limit."+m.Limit, nil)}))
		for _, r := range m.Reasons {
			lines = append(lines, c.T("mining.reason."+r, nil))
		}
		blocks = append(blocks, body(lines...))
	}
	var works []string
	for _, w := range v.Works {
		works = append(works, c.T("mining.work", map[string]any{"module": c.named("module_kind."+w.Module.Code, w.Module.Name), "left": FormatDuration(c, w.Left)}))
	}
	if len(works) > 0 {
		blocks = append(blocks, c.T("mining.works_head", nil)+"\n"+body(works...))
	}
	if v.MayRun {
		for _, d := range v.Deposits {
			if !d.Surveyed {
				addBtn(kb, c.T("mining.button.survey", nil), village.AddrMining, village.MiningActionSurvey)
				break
			}
		}
		for _, m := range v.Mines {
			for _, mod := range v.Modules {
				if mod.CanOrder {
					addBtn(kb, c.T("mining.button.works", map[string]any{"module": c.named("module_kind."+mod.Module.Code, mod.Module.Name)}), village.AddrMining, village.MiningActionWorks, mod.Module.Code, m.BuildingID)
				}
			}
		}
	}
	_ = strconv.Itoa
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrMining}))
	return c.respond(paragraphs(blocks...), kb.Build())
}
