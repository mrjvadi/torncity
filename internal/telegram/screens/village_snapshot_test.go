package screens

import (
	"time"

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
