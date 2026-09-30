package screens

import (
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
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
	civicHall := sampleNamed(c.Lang, "civic_hall", "خانهٔ دهیاری", "Civic hall")
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

	support := &VillageSupport{Code: "support", Name: supportNameFor(c)}
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
	buy := LotBuyView{Village: villageNameFor(c), X: 2, Y: 3, Price: 400, Cash: 5_000, Treasury: 10_900}
	add("Land purchase · confirm", LotBuyConfirm(g, buy))
	buy.Cash, buy.Treasury = 4_600, 11_300
	add("Land purchase · done", LotBuyDone(g, buy))

	timberMat := PrivateMaterial{Component: sampleNamed(c.Lang, "timber", "الوار", "Timber"), Need: 3, Have: 0, Buy: 3, BuyCost: 54}
	add("Citizen catalogue · only what can be built now", PrivateMenu(g, PrivateMenuView{
		Village: villageNameFor(c), Cash: 4_600, OwnedLots: 1, FreeLots: 1,
		Lines: []PrivateLine{
			{Building: sampleNamed(c.Lang, "cottage", "کلبهٔ روستایی", "Cottage"), Home: true, Class: "residential", CostMoney: 800, PermitFee: 100,
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
		Village: villageNameFor(c), Building: sampleNamed(c.Lang, "cottage", "کلبهٔ روستایی", "Cottage"), GridLots: 2,
		Rows: [][]LotCell{lotRow(0, false, false), lotRow(1, true, false)},
	}))
	add("Citizen bill · cost, permit, materials", PrivateConfirm(g, PrivateConfirmView{
		Village: villageNameFor(c), Building: sampleNamed(c.Lang, "cottage", "کلبهٔ روستایی", "Cottage"), X: 0, Y: 3,
		CostMoney: 800, PermitFee: 100, Materials: []PrivateMaterial{timberMat}, MaterialsCost: 54, Total: 954, Cash: 4_600, BuildTime: 2 * time.Hour,
	}))
	house := sampleNamed(c.Lang, "cottage", "کلبهٔ روستایی", "Cottage")
	add("My property · a house to live in, tax due", Mine(g, MineView{
		Village: villageNameFor(c), Cash: 3_646, Home: &house, CanRest: true, Assessed: 1_245, TaxBPS: 200, TaxPerPeriod: 24,
		Lots: []MineLot{{X: 0, Y: 3, Building: "cottage", State: "built"}, {X: 2, Y: 3}},
	}))
	add("My property · house under construction, debt, rested", Mine(g, MineView{
		Village: villageNameFor(c), Cash: 40, CanRest: false, RestWait: 5 * time.Hour, Assessed: 1_245, TaxBPS: 200, TaxPerPeriod: 24,
		Debt: 48, DebtPeriods: 2, Notice: "rested",
		Lots: []MineLot{{X: 0, Y: 3, Building: "cottage", State: "under_construction", FinishAt: snapshotNow.Add(90 * time.Minute), Left: 90 * time.Minute}},
	}))
	add("Land terms · the head's levers", Terms(g, TermsView{
		Village: villageNameFor(c), LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5_000, PermitFee: 100, PermitFeeMax: 1_000,
		TaxBPS: 200, TaxBPSMax: 500, LotPresets: []int64{100, 400, 800}, PermitPresets: []int64{0, 100, 200}, TaxPresets: []int{0, 200, 400},
	}))
	add("Work · until the village has workshops, jobs are in Support", VillageWork(g, WorkView{
		Village: villageNameFor(c), Support: supportNameFor(c), SupportCode: "support",
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
