package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The village economy (village_economy.go): the stock and Support's market,
// the workplaces, and the refusals that name what is missing and where it
// comes from. Its own snapshot area, testdata/snapshots/<language>/
// village_economy.txt.
func init() { snapshotAreas["village_economy"] = villageEconomySnapshots }

func villageEconomySnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)

	timber := sampleNamed(c.Lang, "timber", "الوار", "Timber")
	stone := sampleNamed(c.Lang, "stone", "سنگ", "Stone")
	wool := sampleNamed(c.Lang, "wool", "پشم", "Wool")
	plank := sampleNamed(c.Lang, "plank", "تخته", "Planks")
	wheat := sampleNamed(c.Lang, "wheat", "گندم", "Wheat")
	camp := sampleNamed(c.Lang, "woodcutter_camp", "کارگاه هیزم‌شکنی", "Woodcutter's camp")
	pit := sampleNamed(c.Lang, "small_pit", "گودال استخراج", "Small pit")
	joinery := sampleNamed(c.Lang, "carpentry_workshop", "کارگاه نجاری", "Carpentry workshop")
	housing := sampleNamed(c.Lang, "housing_block", "بلوک مسکونی", "Housing block")
	civicHall := sampleNamed(c.Lang, "civic_hall", "خانهٔ دهیاری", "Civic hall")
	school := sampleNamed(c.Lang, "school", "مدرسه", "School")
	carpentry := sampleNamed(c.Lang, "carpentry", "نجاری", "Carpentry")

	add("Stock and market · the head, a few goods", VillageStock(g, MaterialsView{
		Village: villageNameFor(c), Treasury: 58_000, Used: 14, Capacity: 60, CanBuy: true, Presets: []int64{5, 20, 50},
		Stock:  []MaterialStockLine{{Item: timber, Qty: 9}, {Item: stone, Qty: 5}},
		Market: []MaterialMarketLine{{Item: timber, Price: 18}, {Item: stone, Price: 8}, {Item: wool, Price: 15}},
	}))
	add("Stock and market · a resident, empty stock, just bought", VillageStock(g, MaterialsView{
		Village: villageNameFor(c), Treasury: 57_820, Used: 0, Capacity: 60, Presets: []int64{5, 20, 50},
		Market: []MaterialMarketLine{{Item: timber, Price: 18}},
		Bought: &MaterialBought{Item: timber, Qty: 10, Total: 180},
	}))
	add("Buy confirm · ten timber", VillageMaterialBuyConfirm(g, MaterialBuyView{
		Village: villageNameFor(c), Item: timber, Qty: 10, Unit: 18, Total: 180, Treasury: 58_000, Free: 46,
	}))

	add("Work · a woodcutter's camp and a joinery, my shift running", VillageWork(g, WorkView{
		Village: villageNameFor(c), Resident: true, Used: 14, Capacity: 60,
		Places: []WorkplaceLine{
			{ID: "b1", Building: camp, Produces: []MaterialLine{{Component: timber, Quantity: 4}}, Wage: 40, Shift: time.Hour, Workers: 3, Busy: 1, Ready: true},
			{ID: "b2", Building: joinery, Produces: []MaterialLine{{Component: plank, Quantity: 2}}, Consumes: []MaterialLine{{Component: timber, Quantity: 3}},
				Wage: 30, Shift: 90 * time.Minute, Workers: 2, Busy: 0, Ready: true},
		},
		Mine: &WorkShiftLine{Building: camp, FinishAt: snapshotNow.Add(40 * time.Minute), Left: 40 * time.Minute, Wage: 40,
			Produces: []MaterialLine{{Component: timber, Quantity: 4}}},
	}))
	add("Work · a resident may start a shift", VillageWork(g, WorkView{
		Village: villageNameFor(c), Resident: true, Used: 0, Capacity: 60,
		Places: []WorkplaceLine{
			{ID: "b1", Building: camp, Produces: []MaterialLine{{Component: timber, Quantity: 4}}, Wage: 40, Shift: time.Hour, Workers: 3, Ready: true},
		},
	}))
	add("Work · no workplace yet, what to build", VillageWork(g, WorkView{
		Village: villageNameFor(c), Resident: true, Capacity: 60, Suggest: []Named{camp, pit},
	}))
	add("Work · not a resident", VillageWork(g, WorkView{
		Village: villageNameFor(c), Capacity: 60,
		Places: []WorkplaceLine{
			{ID: "b1", Building: camp, Produces: []MaterialLine{{Component: timber, Quantity: 4}}, Wage: 40, Shift: time.Hour, Workers: 3},
		},
	}))
	add("Work started", VillageWork(g, WorkView{
		Village: villageNameFor(c), Resident: true, Used: 14, Capacity: 60, Started: true,
		Places: []WorkplaceLine{
			{ID: "b1", Building: camp, Produces: []MaterialLine{{Component: timber, Quantity: 4}}, Wage: 40, Shift: time.Hour, Workers: 3, Busy: 1},
		},
		Mine: &WorkShiftLine{Building: camp, FinishAt: snapshotNow.Add(time.Hour), Left: time.Hour, Wage: 40,
			Produces: []MaterialLine{{Component: timber, Quantity: 4}}},
	}))

	// The attempt views: what is missing, and where it comes from.
	add("Refusal · civic hall needs timber that a camp makes and Support sells", VillageRefusal(g, VillageRefusalView{
		Kind: VillageMaterials, Back: presentation.RefOfAddress(AddrBuildMenu), Action: NeedsForBuild, Subject: housing,
		Needs: []VillageNeed{{Kind: NeedMaterial, Item: timber, Need: 10, Have: 2, Price: 18,
			Makers: []VillageMaker{{Building: camp}}}},
	}))
	add("Refusal · the camp is already standing, work there", VillageRefusal(g, VillageRefusalView{
		Kind: VillageMaterials, Back: presentation.RefOfAddress(AddrBuildMenu), Action: NeedsForBuild, Subject: civicHall,
		Needs: []VillageNeed{{Kind: NeedMaterial, Item: timber, Need: 5, Have: 0, Price: 18,
			Makers: []VillageMaker{{Building: camp, Built: true}}}},
	}))
	add("Refusal · a material only a workshop makes and Support does not sell", VillageRefusal(g, VillageRefusalView{
		Kind: VillageMaterials, Back: presentation.RefOfAddress(AddrWork), Action: NeedsForWork, Subject: joinery,
		Needs: []VillageNeed{{Kind: NeedMaterial, Item: wheat, Need: 3, Have: 1,
			Makers: []VillageMaker{{Building: sampleNamed(c.Lang, "farm_canal", "مزرعهٔ نهری", "Canal farm")}}}},
	}))
	add("Refusal · a school needs a circle first and timber", VillageRefusal(g, VillageRefusalView{
		Kind: VillagePrerequisite, Back: presentation.RefOfAddress(AddrBuildMenu), Action: NeedsForBuild, Subject: school,
		Needs: []VillageNeed{
			{Kind: NeedBuilding, Options: []Named{sampleNamed(c.Lang, "teaching_circle", "حلقهٔ آموزش", "Teaching circle")}},
			{Kind: NeedMaterial, Item: timber, Need: 10, Have: 0, Price: 18, Makers: []VillageMaker{{Building: camp}}},
		},
	}))
	add("Refusal · research needs knowledge first", VillageRefusal(g, VillageRefusalView{
		Kind: VillagePrerequisite, Back: presentation.RefOfAddress(AddrKnowledgeList), Action: NeedsForResearch, Subject: carpentry,
		Needs: []VillageNeed{{Kind: NeedKnowledge, Item: sampleNamed(c.Lang, "basic_literacy", "سوادآموزی پایه", "Basic literacy")}},
	}))
	add("Refusal · the stock is full", VillageRefusal(g, VillageRefusalView{Kind: VillageStorageFull, Back: presentation.RefOfAddress(AddrMaterials)}))
	add("Refusal · already working", VillageRefusal(g, VillageRefusalView{Kind: VillageAlreadyWorking, Back: presentation.RefOfAddress(AddrWork)}))
	add("Refusal · workplace full", VillageRefusal(g, VillageRefusalView{Kind: VillageWorkplaceFull, Back: presentation.RefOfAddress(AddrWork)}))

	add("Build menu · listed by tier, materials short, roles missing", BuildMenu(g, BuildMenuView{
		Name: villageNameFor(c), Treasury: 58_000, RunningBuilds: 0, ConcurrentCap: 1,
		Lines: []BuildLine{
			{Building: camp, Role: "forestry", State: BuildAvailable, CostMoney: 300, BuildTime: 30 * time.Minute},
			{Building: civicHall, Role: "governance", State: BuildAvailable, CostMoney: 1000, BuildTime: 2 * time.Hour,
				Materials: []MaterialLine{{Component: timber, Quantity: 5}}, Short: []MaterialLine{{Component: timber, Quantity: 5}}},
			{Building: school, Role: "education", State: BuildLocked, CostMoney: 6000, BuildTime: 5 * time.Hour,
				MissingBuildings: []Named{sampleNamed(c.Lang, "teaching_circle", "حلقهٔ آموزش", "Teaching circle")}},
		},
	}))
}
