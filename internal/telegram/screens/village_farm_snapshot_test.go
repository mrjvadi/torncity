package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The farm cycle, the water works, the mill and the pasture (village_farm.go): the lines of the work screen and the building
// panel, the sowing order and the miller's toll. Its own snapshot area, testdata/snapshots/<language>/village_farm.txt.
func init() { snapshotAreas["village_farm"] = villageFarmSnapshots }

func villageFarmSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	farm := sampleNamed(c.Lang, "farm_canal", "مزرعهٔ نهری", "Canal farm")
	canal := sampleNamed(c.Lang, "canal_channel", "کانال آبرسانی", "Irrigation canal")
	mill := sampleNamed(c.Lang, "mill", "آسیاب", "Mill")
	pasture := sampleNamed(c.Lang, "pasture_range", "چراگاه", "Pasture")
	wheat := sampleNamed(c.Lang, "wheat", "گندم", "Wheat")
	ripe := snapshotNow.Add(2 * time.Hour)
	spoil := ripe.Add(12 * time.Hour)
	growing := &village.FarmLine{Stage: "growing", SowDone: 8, SowNeed: 8, Tended: 3, TendMax: 6, HarvestNeed: 12, RipeAt: &ripe, SpoilAt: &spoil,
		Seed: 50, SeedHave: 120, Expected: 430, Factors: village.FarmFactor{Soil: 10_000, Water: 10_000, Tending: 10_600},
		Water: &village.FarmWater{Work: &canal, Served: true, Open: true, ConditionBPS: 9_400}}
	thirsty := &village.FarmLine{Stage: "sowing", SowDone: 2, SowNeed: 8, TendMax: 6, HarvestNeed: 12, Seed: 50, SeedHave: 120, Expected: 240,
		Factors: village.FarmFactor{Soil: 10_000, Water: 6_000, Tending: 10_000}, Water: &village.FarmWater{Reason: "no_work"}, CanSow: false}
	legacy := &village.FarmLine{Stage: "idle", Legacy: true, LegacyUntil: &spoil, SowNeed: 8, TendMax: 6, HarvestNeed: 12}
	idle := &village.FarmLine{Stage: "idle", SowNeed: 8, TendMax: 6, HarvestNeed: 12, Seed: 50, SeedHave: 120, CanSow: true}

	add("Work · a farm growing, a farm with no water, a mill and a legacy farm", VillageWork(g, WorkView{
		Village: villageNameFor(c), Resident: true, Used: 14, Capacity: 60,
		Places: []WorkplaceLine{
			{ID: "f1", Building: farm, Produces: []MaterialLine{{Component: wheat, Quantity: 12}}, Wage: 50, Shift: 30 * time.Minute, Workers: 4, Ready: true, Farm: growing},
			{ID: "f2", Building: farm, Produces: []MaterialLine{{Component: wheat, Quantity: 12}}, Wage: 50, Shift: 30 * time.Minute, Workers: 4, Ready: true, Farm: thirsty},
			{ID: "f3", Building: farm, Produces: []MaterialLine{{Component: wheat, Quantity: 12}}, Wage: 50, Shift: 30 * time.Minute, Workers: 4, Ready: true, Farm: legacy},
			{ID: "f4", Building: farm, Produces: []MaterialLine{{Component: wheat, Quantity: 12}}, Wage: 50, Shift: 30 * time.Minute, Workers: 4, Farm: idle},
			{ID: "m1", Building: mill, Produces: []MaterialLine{{Component: sampleNamed(c.Lang, "flour_sack", "کیسهٔ آرد", "Flour sack"), Quantity: 8}},
				Consumes: []MaterialLine{{Component: wheat, Quantity: 8}}, Wage: 30, Shift: 15 * time.Minute, Workers: 1, Ready: true,
				Mill: &village.MillLine{TollBPS: 500, MinBPS: 333, MaxBPS: 1000, Batch: 8, Have: 20, TollUnits: 1}},
		},
	}))
	add("Sowing ordered", FarmSow(g, village.FarmSowView{Village: villageNameFor(c), Farm: farm, Line: *idle}))
	add("Toll set", MillToll(g, village.MillTollView{Village: villageNameFor(c), TollBPS: 833, MinBPS: 333, MaxBPS: 1000}))
	add("Building panel · a canal, its master on duty", BuildingPanel(g, BuildingView{
		ID: "w1", Building: canal, Role: "water_infra", Tier: 1, State: BuildingStateComplete, W: 1, H: 1,
		Work: &village.WorkNode{Kind: "service", Status: village.NodeWorking, Water: &village.WaterWork{Open: true, ConditionBPS: 8_200, Serves: []Named{farm}}},
	}))
	add("Building panel · a farm and a pasture without land", BuildingPanel(g, BuildingView{
		ID: "f1", Building: farm, Role: "food", Tier: 1, State: BuildingStateComplete, W: 3, H: 3,
		Work: &village.WorkNode{Kind: "production", Status: village.NodeIdle, Farm: growing,
			Reasons: []village.WorkReason{{Code: village.FarmWaiting}}},
	}))
	add("Building panel · a pasture with too little open land", BuildingPanel(g, BuildingView{
		ID: "p1", Building: pasture, Role: "food", Tier: 1, State: BuildingStateComplete, W: 3, H: 3,
		Work: &village.WorkNode{Kind: "production", Status: village.NodeIdle, Grazing: &village.GrazingLine{Open: 3, Need: 6, Radius: 4},
			Reasons: []village.WorkReason{{Code: village.PastureNoGrazing}}},
	}))
}
