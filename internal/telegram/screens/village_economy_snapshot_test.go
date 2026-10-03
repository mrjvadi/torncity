package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
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
	civicHall := sampleNamed(c.Lang, "civic_hall", "شهرداری", "Civic hall")
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

	// The village shop (docs/adr/0046 section 5).
	bread := sampleNamed(c.Lang, "bread", "نان", "Bread")
	sack := sampleNamed(c.Lang, "bag_sack", "گونی دوشی", "Shoulder sack")
	daypack := sampleNamed(c.Lang, "bag_daypack", "کولهٔ کوچک", "Small rucksack")
	store := sampleNamed(c.Lang, "general_store", "دکان", "General store")
	shopLines := []village.VillageShopLine{
		{Item: bread, Kind: "item", Price: 44, Reference: 40, Stock: 7, LeftToday: 3, Fits: 12, MaxBuy: 3},
		{Item: sack, Kind: "item", Price: 92, Reference: 80, Stock: 2, LeftToday: 2, Fits: 12, MaxBuy: 2},
		{Item: timber, Kind: "component", Price: 18, Reference: 15, Stock: 0, LeftToday: 2, Fits: 3, MaxBuy: 0},
	}
	shopLocked := []village.ShopLockedLine{{Item: daypack, Kind: "item", NeedsBuildings: []Named{store}}}
	shopBase := village.VillageShopView{
		Village: villageNameFor(c), NextDelivery: snapshotNow.Add(20 * time.Minute), DeliveryHour: 6, Wage: 30, TaxBPS: 300, TaxMaxBPS: 1500,
		PriceCapBPS: 15_000, CapMinBPS: 10_000, CapMaxBPS: 15_000, Presets: []int64{1, 3}, Resident: true,
		FreeSpace: 12, Capacity: 20, FreeG: 31_000, Lines: shopLines, Locked: shopLocked,
	}
	add("Shop · open, a resident", VillageShop(g, shopBase))
	head := shopBase
	head.CanSetCap, head.CapPresets, head.TaxPresets = true, []int64{10_000, 12_500, 15_000}, []int64{100, 300, 500}
	head.PriceCapBPS = 12_500
	add("Shop · the head sees the two levers", VillageShop(g, head))
	built := shopBase
	built.Building, built.CanRepair, built.Locked = true, true, nil
	built.Repairs = []village.ShopRepairLine{{Item: sack, Serial: "SACK000001", Slot: "back", Wear: 12, WearMax: 40, Cost: 6}}
	add("Shop · a shop building, a sack to mend", VillageShop(g, built))
	for _, closed := range []string{village.ShopNoShopkeeper, village.ShopUnpaid, village.ShopNotYet} {
		shut := shopBase
		shut.Closed = closed
		for i := range shut.Lines {
			shut.Lines[i].MaxBuy = 0
		}
		add("Shop · shut: "+closed, VillageShop(g, shut))
	}
	stranger := shopBase
	stranger.Resident = false
	add("Shop · a visitor cannot buy", VillageShop(g, stranger))
	justBought := shopBase
	justBought.Bought = &village.ShopBought{Item: bread, Kind: "item", Qty: 2, Total: 88, Tax: 3}
	add("Shop · just bought", VillageShop(g, justBought))
	add("Checkout · village shop", VillageShopCheckout(g, village.VillageShopCheckoutView{
		Village: villageNameFor(c), Item: bread, Kind: "item", Qty: 2, Unit: 44, Total: 88, Tax: 3, TaxBPS: 300, Stock: 7, Space: 2, FreeSpace: 12,
		Grams: 600, Payment: PaymentChoice{Amount: 91, Accepted: []string{"cash", "card"}, Usable: []string{"cash", "card"}, Cash: 400, Bank: 1000},
		Nonce: "0a1b2c3d4e5f",
	}))
	for _, r := range []village.VillageShopRefusalView{
		{Kind: village.ShopRefusedClosed, Closed: village.ShopNoShopkeeper, NextDelivery: snapshotNow.Add(20 * time.Minute)},
		{Kind: village.ShopRefusedNotThere, Item: bread},
		{Kind: village.ShopRefusedSoldOut, Item: bread, Stock: 1},
		{Kind: village.ShopRefusedCap, Item: bread, LeftToday: 0, NextDelivery: snapshotNow.Add(20 * time.Minute)},
		{Kind: village.ShopRefusedNoSpace, Item: timber, FreeSpace: 1, NeedSpace: 4},
		{Kind: village.ShopRefusedTooHeavy, Item: timber, FreeG: 3000, NeedG: 16_000},
		{Kind: village.ShopRefusedNoBuilding},
		{Kind: village.ShopRefusedCapRange, Min: 10_000, Max: 15_000},
	} {
		add("ShopRefused · "+r.Kind, VillageShopRefusal(g, r))
	}
	add("Money · the panel, a basket partly on the shelf", VillageMoney(g, village.MoneyView{
		Village: villageNameFor(c), Currency: village.MoneyCurrency{Code: "TAL", Name: sampleNamed(c.Lang, "tal", "تالار", "Talar").Name, Symbol: "T"},
		Market: village.MoneyNone, Reserve: village.MoneyNone, NilUnitSup: 100, NilPerUnitMicro: 10_000,
		Examples: []village.NilExample{{Amount: 100, NilMicro: 1_000_000}, {Amount: 1000, NilMicro: 10_000_000}, {Amount: 30, NilMicro: 300_000}},
		Treasury: 8_400, TreasuryNilMicro: 84_000_000, Output: 960, OutputNilMicro: 9_600_000, OutputDays: 7, Residents: 3,
		Basket: []village.MoneyBasketLine{
			{Item: sampleNamed(c.Lang, "rice", "برنج", "Rice"), Kind: "item", WeekMilli: 1400, Reference: 35, Price: 38, OnShelf: true},
			{Item: sampleNamed(c.Lang, "tea", "چای", "Tea"), Kind: "item", WeekMilli: 560, Reference: 45, OnShelf: false},
		},
		IndexBPS: 10_857, CoverBPS: 6_364,
	}))
	add("Money · nothing on the shelf, an old village without a reserved money", VillageMoney(g, village.MoneyView{
		Village: villageNameFor(c), Market: village.MoneyNone, Reserve: village.MoneyNone, NilUnitSup: 100, NilPerUnitMicro: 10_000,
		Examples: []village.NilExample{{Amount: 100, NilMicro: 1_000_000}},
		Treasury: 0, OutputDays: 7,
		Basket: []village.MoneyBasketLine{{Item: sampleNamed(c.Lang, "rice", "برنج", "Rice"), Kind: "item", WeekMilli: 1400, Reference: 35}},
	}))
}
