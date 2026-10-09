package screens

import (
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/errors"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Village-level knowledge and construction (docs/adr/0031-knowledge-and-
// village-progression.md): the settlement overview, knowledge list, build
// menu and construction progress screens (village.go), joining the shared
// snapshot harness as its own area, testdata/snapshots/<language>/
// village.txt. K2/W5's eight player commands are now real (internal/
// commands, cmd/game/commands.go), so this area is fully audited like every
// other: TestScreenSnapshots renders and lints it, TestViewSnapshots checks
// its (absent, since these are group screens — village.go's own file
// comment) structured view, and TestNavigationAudit's dead-button check
// confirms every button here names a command the game actually serves.
func init() { snapshotAreas["village"] = villageSnapshots }

// sampleNamed builds a Named with a Persian or Latin authored name so a
// code that has no catalogue entry of its own (most of the v1 catalogue,
// SettlementKnowledgeName/SettlementBuildingName's own fallback) still
// reads as a real word in either language, the same way the real content
// files author one per item.
func sampleNamed(lang, code, fa, en string) Named {
	if lang == "fa" {
		return Named{Code: code, Name: fa}
	}
	return Named{Code: code, Name: en}
}

func villageSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)

	canal := sampleNamed(c.Lang, "canal_irrigation", "آبیاری نهری", "Canal irrigation")
	recordKeeping := sampleNamed(c.Lang, "record_keeping", "دفترداری", "Record keeping")
	basicLiteracy := sampleNamed(c.Lang, "basic_literacy", "سوادآموزی پایه", "Basic literacy")
	carpentry := sampleNamed(c.Lang, "carpentry", "نجاری", "Carpentry")
	watchHut := sampleNamed(c.Lang, "watch_hut", "دیده‌بانی محله", "Watch hut")
	civicHall := sampleNamed(c.Lang, "civic_hall", "شهرداری", "Civic hall")
	carpentryWorkshop := sampleNamed(c.Lang, "carpentry_workshop", "کارگاه نجاری", "Carpentry workshop")
	policePost := sampleNamed(c.Lang, "police_post", "کلانتری", "Police post")

	add("Village overview · a young village", VillageOverview(g, VillageOverviewView{
		Name: villageNameFor(c), Tier: "village",
		Population: 340, PopulationCap: 500,
		FoodPercent: 62, JobPercent: 48, ServicePercent: 30, HappinessPercent: 55, SecurityPercent: 40,
		LiteracyPercent: 18, Treasury: 12_400,
		Buildings: []VillageRoleLine{{Role: "security", Building: watchHut, Tier: 1}, {Role: "craft", Building: carpentryWorkshop, Tier: 1}},
	}))
	add("Village overview · nothing built yet", VillageOverview(g, VillageOverviewView{
		Name: villageNameFor(c), Tier: "village",
		Population: 40, PopulationCap: 100,
		FoodPercent: 20, JobPercent: 10, ServicePercent: 0, HappinessPercent: 30, SecurityPercent: 15,
		LiteracyPercent: 2, Treasury: 900,
	}))

	add("Knowledge list · mixed states, research running", KnowledgeList(g, KnowledgeListView{
		Name: villageNameFor(c), Treasury: 12_400, LiteracyPercent: 18,
		Running: &KnowledgeResearchLine{Knowledge: recordKeeping, FinishAt: snapshotNow.Add(30 * time.Hour), Left: 30 * time.Hour},
		Lines: []KnowledgeLine{
			{Knowledge: sampleNamed(c.Lang, "oral_tradition", "دانش شفاهی", "Oral tradition"), State: KnowledgeHeld},
			{Knowledge: canal, State: KnowledgeAvailable, ResearchCost: 3000, ResearchTime: 24 * time.Hour, BuyPrice: 5400},
			{Knowledge: basicLiteracy, State: KnowledgeLocked, Missing: []Named{sampleNamed(c.Lang, "oral_tradition", "دانش شفاهی", "Oral tradition")}, TerrainOK: true},
			{Knowledge: sampleNamed(c.Lang, "shaft_irrigation", "آبیاری قناتی", "Shaft irrigation"), State: KnowledgeLocked, TerrainOK: false},
		},
		Hidden: 6,
	}))
	add("Knowledge list · two projects in two slots, with notes on pace and price", KnowledgeList(g, KnowledgeListView{
		Name: villageNameFor(c), Treasury: 12_400, LiteracyPercent: 18, Capacity: 3,
		Projects: []KnowledgeResearchLine{
			{Knowledge: recordKeeping, FinishAt: snapshotNow.Add(30 * time.Hour), Left: 30 * time.Hour, Slot: "free", SpeedBPS: 10_000},
			{Knowledge: basicLiteracy, FinishAt: snapshotNow.Add(12 * time.Hour), Left: 12 * time.Hour, Slot: "b-lab", SpeedBPS: 14_000},
		},
		Lines: []KnowledgeLine{
			{Knowledge: canal, State: KnowledgeAvailable, ResearchCost: 3900, ResearchTime: 21 * time.Hour, BuyPrice: 5400,
				SpeedBPS: 14_000, AheadBPS: 13_000, DiscountBPS: 1_000, ShareBPS: 2_000, Slot: "b-lab"},
		},
	}))
	add("Knowledge list · nothing to show yet", KnowledgeList(g, KnowledgeListView{
		Name: villageNameFor(c), Treasury: 900, LiteracyPercent: 2,
	}))

	add("Build menu · a mix of ready and locked", BuildMenu(g, BuildMenuView{
		Name: villageNameFor(c), Treasury: 12_400, RunningBuilds: 1, ConcurrentCap: 1,
		Lines: []BuildLine{
			{Building: civicHall, Role: "", State: BuildAvailable, CostMoney: 1000, BuildTime: 2 * time.Hour},
			{Building: policePost, Role: "security", State: BuildLocked, CostMoney: 5000, BuildTime: 4 * time.Hour,
				Missing: []Named{recordKeeping}},
		},
	}))

	add("Construction progress · one queued, one building", ConstructionProgress(g, ConstructionProgressView{
		Name: villageNameFor(c),
		Lines: []ConstructionLine{
			{Building: carpentry, LotX: 2, LotY: 3, State: ConstructionBuilding, FinishAt: snapshotNow.Add(90 * time.Minute), Left: 90 * time.Minute},
			{Building: watchHut, LotX: 4, LotY: 1, State: ConstructionQueued},
		},
	}))
	add("Construction progress · queue empty", ConstructionProgress(g, ConstructionProgressView{Name: villageNameFor(c)}))
	library := sampleNamed(c.Lang, "library", "کتابخانه", "Library")
	lab := sampleNamed(c.Lang, "laboratory", "آزمایشگاه", "Laboratory")
	wool := sampleNamed(c.Lang, "wool", "پشم", "Wool")
	add("Research desk · a laboratory at work, a library short of paper, a pact offered", ResearchBoard(g, ResearchBoardView{
		Name: villageNameFor(c), Capacity: 3, Running: 2, Frontier: 4, LiteracyPercent: 40, MayShare: true, ShareCapBPS: 5_000,
		Slots: []village.ResearchSlotLine{{Ref: "free", Capacity: 1, Used: 1}, {Ref: "b-lab", Building: lab, Capacity: 2, Used: 1, BonusBPS: 1_000, StaffBPS: 1_200}},
		Buildings: []village.ResearchBuildingLine{
			{ID: "b-lab", Building: lab, Open: true, Slots: 2, Needed: 2, Posts: 3, Players: 1, NPCs: 1, Wage: 130, BonusBPS: 1_000,
				Upkeep: []village.ResearchUpkeepLine{{Item: wool, Qty: 1, Have: 9}}, Mine: true},
			{ID: "b-lib", Building: library, Idle: village.ResearchIdleNoUpkeep, Slots: 1, Needed: 1, Posts: 1, Wage: 130, BonusBPS: 500,
				Upkeep: []village.ResearchUpkeepLine{{Item: wool, Qty: 1, Have: 0}}, CanTake: false},
		},
		Projects: []village.ResearchProjectLine{
			{Knowledge: recordKeeping, Slot: "free", SpeedBPS: 10_000, AheadBPS: 10_000, FinishAt: snapshotNow.Add(30 * time.Hour), Left: 30 * time.Hour},
			{Knowledge: basicLiteracy, Slot: "b-lab", Building: lab, SpeedBPS: 14_000, AheadBPS: 13_000, ShareBPS: 2_000, DiscountBPS: 1_000,
				FinishAt: snapshotNow.Add(12 * time.Hour), Left: 12 * time.Hour},
		},
		Pacts: []village.ResearchPactLine{
			{ID: "p1", Partner: sampleNamed(c.Lang, "hamsaye", "همسایه", "Neighbour"), State: village.ResearchPactActive},
			{ID: "p2", Partner: sampleNamed(c.Lang, "darya", "دریا", "Sea"), State: village.ResearchPactIncoming},
		},
		Neighbours: []Named{sampleNamed(c.Lang, "kuh", "کوه", "Hill")},
		Experience: []village.ResearchExperienceLine{{Field: "craft", Points: 240, Per: 100, MaxBPS: 4_000}},
	}))
	add("Research desk · no research building yet, the free slot only", ResearchBoard(g, ResearchBoardView{
		Name: villageNameFor(c), Capacity: 1, Frontier: 4, LiteracyPercent: 2, ShareCapBPS: 5_000,
		Slots: []village.ResearchSlotLine{{Ref: "free", Capacity: 1}},
	}))

	// The panel of one placed building, per type (village_building.go). The ids
	// are real uuids: the longest address a panel carries must fit Telegram's
	// 64 bytes.
	const bid = "3c1f2a4e-7b1d-4c39-8a55-0f6d1e2b9c44"
	granary := sampleNamed(c.Lang, "granary", "انبار غله", "Granary")
	teaching := sampleNamed(c.Lang, "teaching_circle", "حلقهٔ آموزش", "Teaching circle")
	schoolB := sampleNamed(c.Lang, "school", "مدرسه", "School")
	timber := sampleNamed(c.Lang, "timber", "چوب", "Timber")
	add("Building panel · a granary with its stock", BuildingPanel(g, BuildingView{
		ID: bid, Building: granary, Role: "storage", Tier: 1, Kind: BuildingKindStorage, State: BuildingStateComplete,
		X: 4, Y: 0, W: 1, H: 1, Upkeep: 20, CanManage: true, StockUsed: 135, StockCapacity: 360,
		Stock: []BuildingStockLine{{Item: timber, Kind: "component", Qty: 15}, {Item: sampleNamed(c.Lang, "wheat", "گندم", "Wheat"), Kind: "item", Qty: 120}},
	}))
	add("Building panel · an empty store", BuildingPanel(g, BuildingView{
		ID: bid, Building: granary, Role: "storage", Tier: 1, Kind: BuildingKindStorage, State: BuildingStateComplete, W: 1, H: 1,
	}))
	add("Building panel · a teaching circle, the head sees the upgrade", BuildingPanel(g, BuildingView{
		ID: bid, Building: teaching, Role: "education", Tier: 1, Kind: BuildingKindSchool, State: BuildingStateComplete,
		W: 1, H: 1, LiteracyPercent: 34, Teaching: true, Upkeep: 25, CanManage: true, HasUpgrade: true,
	}))
	add("Building panel · the upgrade, revealed when pressed", BuildingPanel(g, BuildingView{
		ID: bid, Building: teaching, Role: "education", Tier: 1, Kind: BuildingKindSchool, State: BuildingStateComplete,
		W: 1, H: 1, CanManage: true, HasUpgrade: true, Mode: BuildingModeUpgrade,
		Upgrades: []BuildingUpgradeLine{
			{Building: schoolB, Tier: 2, CostMoney: 9000, BuildTime: 4 * time.Hour, Available: false, Missing: []Named{recordKeeping},
				Materials: []village.WorkItemLine{{Item: sampleNamed(c.Lang, "timber", "چوب", "Timber"), Qty: 10}}, Shifts: 12,
				Staff:    []village.UpgradeStaffLine{{Role: sampleNamed(c.Lang, "teacher", "معلم", "Teacher"), Slots: 2}},
				Capacity: []village.UpgradeCapacityLine{{Kind: "research_slots", Value: 1}},
				Needs: []village.Prerequisite{
					{Kind: village.PrereqKnowledge, Item: recordKeeping, Need: 1, How: village.HowResearch},
					{Kind: village.PrereqItem, Item: sampleNamed(c.Lang, "timber", "چوب", "Timber"), Have: 4, Need: 10, How: village.HowBuy, Price: 18},
					{Kind: village.PrereqMoney, Have: 4_000, Need: 9_000, How: village.HowDonate},
					{Kind: village.PrereqLiteracy, Have: 3_000, Need: 5_000, How: village.HowTrain},
					{Kind: village.PrereqBuilding, Role: "education", Tier: 1, Need: 1, How: village.HowBuild, Options: []Named{teaching}},
				}},
		},
	}))
	add("Building panel · the civic hall", BuildingPanel(g, BuildingView{
		ID: bid, Building: civicHall, Kind: BuildingKindCivicHall, State: BuildingStateComplete, W: 2, H: 2,
		Treasury: 24_600, Population: 34, Upkeep: 20, CanManage: true,
		Research: &BuildingResearchLine{Knowledge: canal, Left: 41 * time.Minute},
	}))
	add("Building panel · a watch hut", BuildingPanel(g, BuildingView{
		ID: bid, Building: watchHut, Role: "security", Tier: 1, Kind: BuildingKindSecurity, State: BuildingStateComplete, W: 1, H: 1,
		Upkeep: 30, Effects: []BuildingEffectLine{{Target: "local_security_bps", Value: 300}},
	}))
	add("Building panel · a road", BuildingPanel(g, BuildingView{
		ID: bid, Building: sampleNamed(c.Lang, "road", "جاده", "Road"), Kind: BuildingKindRoad, State: BuildingStateComplete, W: 1, H: 1,
		Upkeep: 2, CanManage: true,
	}))
	add("Building panel · under construction, the head may cancel", BuildingPanel(g, BuildingView{
		ID: bid, Building: carpentryWorkshop, Role: "craft", Tier: 1, Kind: BuildingKindGeneric, State: BuildingStateBuilding, W: 2, H: 2,
		ProgressPercent: 42, FinishAt: snapshotNow.Add(90 * time.Minute), Left: 90 * time.Minute, CanManage: true,
	}))
	add("Building panel · demolishing asks first", BuildingPanel(g, BuildingView{
		ID: bid, Building: granary, Role: "storage", Kind: BuildingKindStorage, State: BuildingStateComplete, W: 1, H: 1,
		CanManage: true, Mode: BuildingModeDemolish,
	}))
	add("Building panel · cancelling asks first", BuildingPanel(g, BuildingView{
		ID: bid, Building: carpentryWorkshop, Kind: BuildingKindGeneric, State: BuildingStateBuilding, W: 2, H: 2,
		CanManage: true, Mode: BuildingModeCancel,
	}))
	add("Construction progress · with the standing buildings to open", ConstructionProgress(g, ConstructionProgressView{
		Name: villageNameFor(c),
		Lines: []ConstructionLine{
			{ID: bid, Building: carpentry, LotX: 2, LotY: 3, State: ConstructionBuilding, FinishAt: snapshotNow.Add(90 * time.Minute), Left: 90 * time.Minute},
		},
		Standing: []StandingLine{{ID: bid, Building: granary}, {ID: bid, Building: teaching}, {ID: bid, Building: civicHall}},
	}))

	road := sampleNamed(c.Lang, "road", "جاده", "Road")
	add("Batch · the total of three roads", LotBatchConfirm(g, LotBatchConfirmView{
		SettlementName: villageNameFor(c), Building: road, Count: 3, CostMoney: 150, BuildTime: 10 * time.Minute,
		Lots: []LotBatchLot{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 0}},
	}))
	add("Batch · refused as a whole, every lot named", VillageRefusal(g, VillageRefusalView{
		Kind: VillageBatch,
		Lots: []BatchLotFailure{{X: 1, Y: 0, Kind: VillageOccupied}, {X: 9, Y: 9, Kind: VillageOutOfBounds}, {X: 2, Y: 3, Kind: VillageUnbuildable}},
	}))
	add("Roads · no road can reach the building", VillageRefusal(g, VillageRefusalView{Kind: VillageNoRoad}))
	add("Lot grid · picking the first lot of a run of roads", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: road, GridLots: 5, Rows: sampleLotRows(), Multi: true, Line: LineStart,
	}))
	add("Lot grid · picking the last lot of the run", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: road, GridLots: 5, Rows: sampleLotRows(), Multi: true, Line: LineEnd, From: LotBatchLot{X: 1, Y: 0},
	}))
	add("Placement · the preview names the roads laid with the building", LotConfirm(g, LotConfirmView{
		SettlementName: villageNameFor(c), Building: sampleNamed(c.Lang, "cottage", "خانهٔ روستایی", "Village house"),
		X: 4, Y: 4, CostMoney: 720, BuildTime: 45 * time.Minute, AutoRoads: 2,
		Materials: []MaterialLine{{Component: timber, Quantity: 2}},
	}))
	add("Land · a grid wider than a keyboard row is a window over the land", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: sampleNamed(c.Lang, "cottage", "خانهٔ روستایی", "Village house"),
		GridLots: 10, Rows: wideLotRows(10), WinX: 4, WinY: 4,
	}))

	militiaCamp := sampleNamed(c.Lang, "militia_camp", "اردوگاه میلیشیا", "Militia camp")
	add("Lot grid · a mix of states, rotatable building", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: militiaCamp, CanRotate: true, Rotated: false,
		GridLots: 5, Rows: sampleLotRows(),
	}))
	add("Lot grid · rotated preview", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: militiaCamp, CanRotate: true, Rotated: true,
		GridLots: 5, Rows: sampleLotRows(),
	}))
	add("Lot grid · square building, no rotate button", LotGrid(g, LotGridView{
		SettlementName: villageNameFor(c), Building: civicHall, CanRotate: false,
		GridLots: 5, Rows: sampleLotRows(),
	}))

	add("Build confirm · with materials", LotConfirm(g, LotConfirmView{
		SettlementName: villageNameFor(c), Building: civicHall, X: 1, Y: 2, CostMoney: 1000, BuildTime: 2 * time.Hour,
		Materials: []MaterialLine{{Component: sampleNamed(c.Lang, "timber", "چوب", "Timber"), Quantity: 5}},
	}))
	add("Build confirm · rotated, no materials", LotConfirm(g, LotConfirmView{
		SettlementName: villageNameFor(c), Building: militiaCamp, X: 3, Y: 0, Rotated: true, CostMoney: 500, BuildTime: 45 * time.Minute,
	}))

	add("Village overview · a resident's view (no join button)", VillageOverview(g, VillageOverviewView{
		Name: villageNameFor(c), Tier: "village", Resident: true,
		Population: 12, PopulationCap: 100,
		FoodPercent: 20, JobPercent: 10, ServicePercent: 0, HappinessPercent: 30, SecurityPercent: 15,
		LiteracyPercent: 2, Treasury: 900,
	}))

	support := &VillageSupport{Code: "support", Name: supportNameFor(c), Services: []string{"bank", "market", "jobs", "knowledge", "hospital", "jail"}}
	add("Village home · the group hub, a resident, with Support's services a journey away", VillageOverview(g, VillageOverviewView{
		Name: villageNameFor(c), Tier: "village", Resident: true, Support: support,
		Population: 12, PopulationCap: 100,
		FoodPercent: 20, JobPercent: 10, ServicePercent: 0, HappinessPercent: 30, SecurityPercent: 15,
		LiteracyPercent: 2, Treasury: 10_900,
	}))
	add("Village home · a private chat (the village is home, run in its group)", VillageOverview(priv(c), VillageOverviewView{
		Name: villageNameFor(c), Tier: "village", Resident: true, Support: support,
		Population: 12, PopulationCap: 100,
		FoodPercent: 20, JobPercent: 10, ServicePercent: 0, HappinessPercent: 30, SecurityPercent: 15,
		LiteracyPercent: 2, Treasury: 10_900,
	}))
	add("Village home · the village head's hub (civic build and land terms)", VillageOverview(g, VillageOverviewView{
		Name: villageNameFor(c), Tier: "village", Resident: true, IsHead: true,
		Population: 12, PopulationCap: 100,
		FoodPercent: 20, JobPercent: 10, ServicePercent: 0, HappinessPercent: 30, SecurityPercent: 15,
		LiteracyPercent: 2, Treasury: 10_900,
	}))

	// The citizen loop (docs/adr/0033 sections 4.4-4.5): land, a private
	// house, one's own property, the head's terms.
	landRow := func(y int, states ...string) []LandCell {
		row := make([]LandCell, len(states))
		for x, st := range states {
			row[x] = LandCell{X: x, Y: y, State: st}
			if st == LandTaken {
				row[x].Owner = "Sara"
			}
		}
		return row
	}
	land := LandView{
		Village: villageNameFor(c), GridLots: 5, Price: 400, Cash: 5_000, Owned: 1, Max: 6, CanBuy: true, FreeLots: 14,
		Rows: [][]LandCell{
			landRow(0, LandFree, LandRoad, LandFree, LandFree, LandWater),
			landRow(1, LandFree, LandBuilding, LandBuilding, LandTaken, LandFree),
			landRow(2, LandFree, LandBuilding, LandBuilding, LandFree, LandFree),
			landRow(3, LandMine, LandFree, LandFree, LandFree, LandSteep),
			landRow(4, LandFree, LandFree, LandFree, LandTaken, LandFree),
		},
	}
	add("Land grid · free lots on offer", LandGrid(g, land))
	// a road drawn out of the first grid opened land beyond it (docs/adr/0044 5.5)
	pathClass := sampleNamed(c.Lang, "path", "راه مالرو", "Footpath")
	withRoads := land
	withRoads.Roads = []RoadPlanLine{{ID: "r1", Class: pathClass, Lots: 38, Built: 12, Open: 150, Sold: 5, To: village.LotRef{X: -14, Y: 19}}}
	withRoads.Outer = []LandCell{
		{X: 6, Y: 2, State: LandPlanned}, {X: 7, Y: 2, State: LandRoad, Building: "road"},
		{X: 7, Y: 3, State: LandFree, Access: "road"}, {X: 8, Y: 3, State: LandFree, Access: "needs_road", Roads: 2, Cost: 20},
		{X: 8, Y: 1, State: LandFree, Access: "needs_bridge", Roads: 2, Crossings: 1, Cost: 80}, {X: 9, Y: 3, State: LandTaken, Owner: "Sara"},
		{X: -3, Y: 8, State: LandWater}, {X: -3, Y: 9, State: LandSteep},
	}
	withRoads.FreeLots, withRoads.ServedLots, withRoads.CanDraw = 17, 17, true
	add("Land grid · roads opened land beyond the first grid", LandGrid(g, withRoads))
	roadQuote := RoadQuoteView{
		SettlementName: villageNameFor(c), From: village.LotRef{X: 2, Y: 4}, To: village.LotRef{X: -14, Y: 19}, Class: pathClass,
		Lots: 38, Crossings: 1, LengthM: 1160, ClimbM: 22, MaxGradeBPS: 640, LotCost: 10, CrossingCost: 60, FullCost: 430,
		Opens: 190, Usable: 171, Water: 9, Steep: 10,
	}
	add("Road · the quote of a road drawn out of the first grid", RoadQuote(g, roadQuote))
	roadQuote.PlanID = "r1"
	add("Road · the plan is stored", RoadPlanned(g, roadQuote))
	add("Road · a plan taken back", RoadCancelled(g, RoadCancelledView{SettlementName: villageNameFor(c), PlanID: "r1", Lots: 38}))
	for _, kind := range []string{RoadNoNetwork, RoadEndBlocked, RoadWater, RoadNoRoute, RoadNoBridge, RoadTooLong, RoadForeign, RoadOpenCap, RoadInUse, RoadSame, RoadClassLocked} {
		add("Road refusal · "+kind, VillageRefusal(g, VillageRefusalView{Kind: kind}))
	}
	buy := LotBuyView{Village: villageNameFor(c), X: 2, Y: 3, Price: 400, Cash: 5_000, Treasury: 10_900, Total: 400, Access: LotAccess{Kind: "road"}}
	add("Land purchase · confirm", LotBuyConfirm(g, buy))
	buy.Cash, buy.Treasury = 4_600, 11_300
	add("Land purchase · done", LotBuyDone(g, buy))
	// a lot that needs a road, one over water, and one no road can reach
	needsRoad := LotBuyView{Village: villageNameFor(c), X: 2, Y: 3, Price: 400, Cash: 5_000, Treasury: 10_900, Total: 430,
		Access: LotAccess{Kind: "needs_road", Roads: 3, Cost: 30}}
	add("Land purchase · confirm, road included", LotBuyConfirm(g, needsRoad))
	needsBridge := LotBuyView{Village: villageNameFor(c), X: 2, Y: 3, Price: 400, Cash: 5_000, Treasury: 10_900, Total: 560,
		Access: LotAccess{Kind: "needs_bridge", Roads: 2, Crossings: 1, Cost: 160}}
	add("Land purchase · confirm, culvert needed", LotBuyConfirm(g, needsBridge))
	landlocked := LotBuyView{Village: villageNameFor(c), X: 2, Y: 3, Price: 400, Cash: 5_000, Treasury: 10_900, Total: 400,
		Access: LotAccess{Kind: "none"}, Carve: &LotAccess{Kind: "needs_road", Roads: 2, Cost: 20, Carved: []village.LotRef{{X: 1, Y: 3}}},
		Nearby: []village.LotNearby{{X: 0, Y: 3, Distance: 2, Access: LotAccess{Kind: "road"}}, {X: 1, Y: 1, Distance: 3, Access: LotAccess{Kind: "needs_road", Roads: 1, Cost: 10}}}}
	add("Land purchase · confirm, no possible access", LotBuyConfirm(g, landlocked))
	repair := LotAccessView{Village: villageNameFor(c), X: 2, Y: 3, Own: true, Price: 400, Refund: 400, Cash: 5_000,
		Access:   LotAccess{Kind: "needs_bridge", Roads: 2, Crossings: 1, Cost: 160},
		Carve:    &LotAccess{Kind: "needs_road", Roads: 2, Cost: 20, Carved: []village.LotRef{{X: 1, Y: 3}}},
		Building: sampleNamed(c.Lang, "private_cottage", "کلبهٔ شخصی", "Private cottage")}
	add("Lot road · the fixes for a lot no road reaches", LotAccessScreen(g, repair))
	add("Lot road · connected", LotRepairDone(g, LotRepairView{Village: villageNameFor(c), X: 2, Y: 3, Option: "connect", Paid: 160, Cash: 4_840}))
	add("Lot road · refunded", LotRepairDone(g, LotRepairView{Village: villageNameFor(c), X: 2, Y: 3, Option: "refund", Refund: 400, Cash: 5_400}))

	timberMat := PrivateMaterial{Component: sampleNamed(c.Lang, "timber", "الوار", "Timber"), Need: 3, Have: 0, Buy: 3, BuyCost: 54}
	add("Citizen catalogue · only what can be built now", PrivateMenu(g, PrivateMenuView{
		Village: villageNameFor(c), Cash: 4_600, OwnedLots: 1, FreeLots: 1,
		Lines: []PrivateLine{
			{Building: sampleNamed(c.Lang, "private_cottage", "کلبهٔ شخصی", "Private cottage"), Home: true, Class: "residential", CostMoney: 800, PermitFee: 100,
				Materials: []PrivateMaterial{timberMat}, BuildTime: 2 * time.Hour, FootprintW: 1, FootprintH: 1, Total: 954, Affordable: true},
			{Building: sampleNamed(c.Lang, "market_stall", "غرفهٔ بازار", "Market stall"), Class: "commerce", CostMoney: 500, PermitFee: 100,
				Materials: []PrivateMaterial{{Component: timberMat.Component, Need: 2, Have: 2}}, BuildTime: time.Hour, FootprintW: 1, FootprintH: 1,
				Total: 600, Affordable: false},
		},
	}))
	add("Citizen catalogue · no land yet", PrivateMenu(g, PrivateMenuView{Village: villageNameFor(c), Cash: 4_600}))
	lotRow := func(y int, fits ...bool) []LotCell {
		row := make([]LotCell, len(fits))
		for x, f := range fits {
			st := LotFree
			if !f {
				st = LotOccupied
			}
			row[x] = LotCell{X: x, Y: y, State: st, Fits: f}
		}
		return row
	}
	add("Citizen lot choice · your own lots", PrivateLots(g, PrivateLotsView{
		Village: villageNameFor(c), Building: sampleNamed(c.Lang, "private_cottage", "کلبهٔ شخصی", "Private cottage"), GridLots: 2,
		Rows: [][]LotCell{lotRow(0, false, false), lotRow(1, true, false)},
	}))
	add("Citizen bill · cost, permit, materials", PrivateConfirm(g, PrivateConfirmView{
		Village: villageNameFor(c), Building: sampleNamed(c.Lang, "private_cottage", "کلبهٔ شخصی", "Private cottage"), X: 0, Y: 3,
		CostMoney: 800, PermitFee: 100, Materials: []PrivateMaterial{timberMat}, MaterialsCost: 54, Total: 954, Cash: 4_600, BuildTime: 2 * time.Hour,
	}))
	house := sampleNamed(c.Lang, "private_cottage", "کلبهٔ شخصی", "Private cottage")
	add("My property · a house to live in, tax due", Mine(g, MineView{
		Village: villageNameFor(c), Cash: 3_646, Home: &house, CanRest: true, Assessed: 1_245, TaxBPS: 200, TaxPerPeriod: 24,
		Lots: []MineLot{{X: 0, Y: 3, Building: "private_cottage", State: "built"}, {X: 2, Y: 3}},
	}))
	add("My property · house under construction, debt, rested", Mine(g, MineView{
		Village: villageNameFor(c), Cash: 40, CanRest: false, RestWait: 5 * time.Hour, Assessed: 1_245, TaxBPS: 200, TaxPerPeriod: 24,
		Debt: 48, DebtPeriods: 2, Notice: "rested",
		Lots: []MineLot{{X: 0, Y: 3, Building: "private_cottage", State: "under_construction", FinishAt: snapshotNow.Add(90 * time.Minute), Left: 90 * time.Minute}},
	}))
	add("Land terms · the head's levers", Terms(g, TermsView{
		Village: villageNameFor(c), LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5_000, PermitFee: 100, PermitFeeMax: 1_000,
		TaxBPS: 200, TaxBPSMax: 500, LotPresets: []int64{100, 400, 800}, PermitPresets: []int64{0, 100, 200}, TaxPresets: []int{0, 200, 400},
	}))
	for _, kind := range []string{CitizenLotTaken, CitizenLotLimit, CitizenZoning, CitizenNotOwner, CitizenNoCash, CitizenPrivateOnly,
		CitizenLotPrivate, CitizenRestWait, CitizenNoHouse, CitizenTermsRange, CitizenNoDebt, CitizenOff, CitizenNoLots} {
		add("Refusal · "+kind, VillageRefusal(g, VillageRefusalView{Kind: kind, Remaining: 3 * time.Hour}))
	}

	add("Village home · a group with no village: the call to found one", VillageHomeCall(g))
	add("Village home · private, living in no village", VillageHomeNone(priv(c)))
	add("Refusal · a city-only feature asked of a village", Error(c, application.ErrCityTierOnly.WithCause(stderrors.New("lever city.budget is set per city"))))
	add("Refusal · a raw internal failure never reaches the player", Error(c, errors.InvalidInput("INVALID_INPUT: that does not apply here: lever city.budget is set per city, and vinlar is a village")))

	joinView := ResidenceView{Village: villageNameFor(c), Cooldown: 72 * time.Hour, Population: 13}
	leaveView := ResidenceView{Leaving: true, Village: villageNameFor(c), Home: supportNameFor(c), Cooldown: 72 * time.Hour, Population: 12}
	add("Residence · asks before joining", ResidenceAsk(g, joinView))
	add("Residence · joined", ResidenceDone(g, joinView))
	add("Residence · asks before leaving", ResidenceAsk(g, leaveView))
	add("Residence · left", ResidenceDone(g, leaveView))
	add("Residence refused · already a resident", VillageRefusal(g, VillageRefusalView{Kind: VillageAlreadyResident}))
	add("Residence refused · not a resident", VillageRefusal(g, VillageRefusalView{Kind: VillageNotResident}))
	add("Residence refused · cool-down", VillageRefusal(g, VillageRefusalView{Kind: VillageResidenceWait, Remaining: 50*time.Hour + 20*time.Minute}))
	add("Residence refused · the head cannot leave", VillageRefusal(g, VillageRefusalView{Kind: VillageHoldsOffice}))

	add("Village news · one resident joined", VillageNews(g, VillageNewsView{Village: villageNameFor(c),
		Items: []VillageNewsItem{{Kind: NewsResidentJoined, Player: residentNameFor(c, 0)}}}))
	add("Village news · several joined and a building finished", VillageNews(g, VillageNewsView{Village: villageNameFor(c),
		Items: []VillageNewsItem{
			{Kind: NewsResidentJoined, Player: residentNameFor(c, 0)},
			{Kind: NewsResidentJoined, Player: residentNameFor(c, 1)},
			{Kind: NewsBuilt, Building: watchHut},
		}}))

	add("Village refused · not enough materials", VillageRefusal(g, VillageRefusalView{Kind: VillageMaterials}))
	add("Village refused · lot is water or too steep", VillageRefusal(g, VillageRefusalView{Kind: VillageUnbuildable}))
}

// sampleLotRows is a 5x5 grid mixing every lot state and both Fits
// outcomes, for the lot-grid golden.
func sampleLotRows() [][]LotCell {
	c := func(x, y int, state string, fits bool) LotCell { return LotCell{X: x, Y: y, State: state, Fits: fits} }
	return [][]LotCell{
		{c(0, 0, LotFree, true), c(1, 0, LotFree, false), c(2, 0, LotOccupied, false), c(3, 0, LotWater, false), c(4, 0, LotFree, true)},
		{c(0, 1, LotFree, true), c(1, 1, LotFree, true), c(2, 1, LotOccupied, false), c(3, 1, LotFree, true), c(4, 1, LotFree, false)},
		{c(0, 2, LotRoad, false), c(1, 2, LotFree, true), c(2, 2, LotFree, true), c(3, 2, LotFree, true), c(4, 2, LotSteep, false)},
		{c(0, 3, LotFree, true), c(1, 3, LotFree, true), c(2, 3, LotFree, true), c(3, 3, LotOccupied, false), c(4, 3, LotFree, true)},
		{c(0, 4, LotFree, true), c(1, 4, LotSteep, false), c(2, 4, LotFree, true), c(3, 4, LotFree, true), c(4, 4, LotFree, true)},
	}
}

// villageNameFor gives the snapshot a language-appropriate sample village
// name, the same rule settlementSnapshots already follows for a founded
// settlement's own name.
func villageNameFor(c Context) string {
	if c.Lang == "fa" {
		return "کورندال"
	}
	return "Korendal"
}

func supportNameFor(c Context) string {
	if c.Lang == "fa" {
		return "ساپورت"
	}
	return "Support"
}

func residentNameFor(c Context, i int) string {
	fa := []string{"سارا", "کامران"}
	en := []string{"Sara", "Kamran"}
	if c.Lang == "fa" {
		return fa[i]
	}
	return en[i]
}

// priv is the same context in a private chat: not shared.
func priv(c Context) Context {
	c.Shared = false
	return c
}

// wideLotRows is an n x n grid of free lots that all fit.
func wideLotRows(n int) [][]LotCell {
	rows := make([][]LotCell, n)
	for y := range rows {
		rows[y] = make([]LotCell, n)
		for x := range rows[y] {
			rows[y][x] = LotCell{X: x, Y: y, State: LotFree, Fits: true}
		}
	}
	return rows
}
