package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Village-level knowledge and construction (docs/adr/0031-knowledge-and-
// village-progression.md): the settlement overview, knowledge list, build
// menu and construction progress screens (village.go).
//
// DELIBERATELY NOT REGISTERED IN snapshotAreas. That shared map feeds three
// audits at once (TestScreenSnapshots, TestViewSnapshots and, critically,
// TestNavigationAudit's dead-button check), and the last one requires every
// button's address to name a command internal/commands actually serves to
// players today. These screens' buttons (settlement:knowledge.research,
// settlement:build.place, ...) are the addresses K2/W5's application layer
// is meant to serve once its handlers and repositories exist — real
// commands to grow into, not yet real commands, because binding them now
// with no repository behind them would be exactly the fake functionality
// this codebase's own tests exist to catch. So this file renders and lints
// the same fixtures screentest.Book gives every other screen (text
// correctness, no unfilled placeholder, no Latin inside Persian, every
// screen has a way out) directly, writing the identical golden shape to
// testdata/snapshots/<language>/village.txt for review:
//
//	go test ./internal/telegram/screens -run TestVillageScreenGoldens -update
//
// Once the commands are registered and bound, deleting this function's body
// and adding `snapshotAreas["village"] = villageSnapshots` is the entire
// migration into the shared, fully-audited harness.
func TestVillageScreenGoldens(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		who, ok := cast[lang]
		if !ok {
			t.Fatalf("no sample cast for the %s locale", lang)
		}
		t.Run(lang, func(t *testing.T) {
			book := screentest.NewBook(lang, who.me, who.friend, who.third,
				LanguageName(Context{Msgs: cat, Lang: lang}, "en"), LanguageName(Context{Msgs: cat, Lang: lang}, "fa"))
			c := Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone}
			villageSnapshots(c, who, book.Add)
			book.Check(t, snapshotDir, "village")
		})
	}
}

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
