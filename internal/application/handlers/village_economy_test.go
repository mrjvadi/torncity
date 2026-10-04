package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The owner's live village: a civic hall, a road, the four founding grants and
// no timber. What a housing block lacks must be named with both sources.
func TestPathNamesTimberSources(t *testing.T) {
	snap := shippedSnapshot(t)
	pc := pathContext{
		snap: snap, tier: "village",
		owned:    item.Set{"oral_tradition": {}, "communal_watch": {}, "kin_apprenticeship": {}, "barter_ring": {}},
		caps:     item.Set{"literacy_spread": {}, "local_security": {}, "skilled_labor": {}, "market_access": {}},
		standing: map[string]bool{"civic_hall": true, "road": true},
		stock:    map[string]int64{"timber": 2},
		markup:   12_000,
	}
	d, _ := snap.SettlementBuildingDef("housing_block")
	needs := pc.placementNeeds(d.Def())
	if len(needs) != 1 || needs[0].Kind != screens.NeedMaterial {
		t.Fatalf("needs = %+v, want just timber", needs)
	}
	n := needs[0]
	if n.Item.Code != "timber" || n.Need != 10 || n.Have != 2 {
		t.Errorf("need = %+v", n)
	}
	if len(n.Makers) == 0 || n.Makers[0].Building.Code != "woodcutter_camp" || n.Makers[0].Built {
		t.Errorf("makers = %+v, want the woodcutter's camp, not built yet", n.Makers)
	}
	if n.Price != 18 { // 15 x 12000 / 10000
		t.Errorf("price = %d, want 18", n.Price)
	}

	// Once the camp stands it is offered as the place to work.
	pc.standing["woodcutter_camp"] = true
	n = pc.placementNeeds(d.Def())[0]
	if !n.Makers[0].Built {
		t.Errorf("a standing camp is not marked built: %+v", n.Makers)
	}

	// With enough timber nothing is missing.
	pc.stock["timber"] = 10
	if needs := pc.placementNeeds(d.Def()); len(needs) != 0 {
		t.Errorf("needs with the timber in stock = %+v", needs)
	}
}

// A building needing a knowledge item names it; a promotion names the buildings
// of the role it needs; a bigger settlement's building is never a source.
func TestPathNamesKnowledgeAndRoles(t *testing.T) {
	snap := shippedSnapshot(t)
	pc := pathContext{
		snap: snap, tier: "village",
		owned: item.Set{"oral_tradition": {}, "kin_apprenticeship": {}}, caps: item.Set{},
		standing: map[string]bool{}, stock: map[string]int64{}, markup: 12_000,
	}
	d, _ := snap.SettlementBuildingDef("carpentry_workshop")
	needs := pc.placementNeeds(d.Def())
	if len(needs) < 2 || needs[0].Kind != screens.NeedKnowledge || needs[0].Item.Code != "carpentry" {
		t.Fatalf("needs = %+v, want the carpentry knowledge first, then timber", needs)
	}
	school, _ := snap.SettlementBuildingDef("school")
	pc.tier = "town"
	var role *screens.VillageNeed
	for _, n := range pc.placementNeeds(school.Def()) {
		if n.Kind == screens.NeedBuilding {
			n := n
			role = &n
		}
	}
	if role == nil || len(role.Options) == 0 || role.Options[0].Code != "teaching_circle" {
		t.Errorf("a school does not name the teaching circle: %+v", role)
	}
	for _, m := range pc.snap.SettlementProducers("timber") {
		if !m.Def().ListedAt("village") {
			t.Errorf("timber's source %s is not a village building", m.Code)
		}
	}
}

// The granary and the storehouse give their room as storage classes through the
// function catalogue (building_functions.yml), and only the kept ones count.
func TestStoresGiveRoomByClass(t *testing.T) {
	snap := shippedSnapshot(t)
	stores := storeBuildings(snap, []application.SettlementBuildingInstance{
		{ID: "b", TypeCode: "storehouse", Status: "complete"},
		{ID: "a", TypeCode: "granary", Status: "complete"},
		{ID: "c", TypeCode: "storehouse", Status: "building"},
		{ID: "d", TypeCode: "woodcutter_camp", Status: "complete"},
	}, StorageRules{})
	if len(stores) != 2 || stores[0].Type != "granary" || stores[1].Type != "storehouse" {
		t.Fatalf("stores = %+v, want the granary then the storehouse (a building under construction and a camp give no room)", stores)
	}
	if stores[0].Provides["food"] != 300 || stores[1].Provides["bulk"] != 200 || stores[1].Provides["goods"] != 150 {
		t.Errorf("provides = %+v / %+v", stores[0].Provides, stores[1].Provides)
	}
	if granary, _ := snap.SettlementBuildingDef("granary"); granary.Storage != 0 {
		t.Errorf("the granary still carries a flat storage of %d", granary.Storage)
	}
}

// Food spoils in whole units with the fraction carried, and nothing else does.
func TestSpoilageCarriesTheFraction(t *testing.T) {
	snap := shippedSnapshot(t)
	stacks := []application.OrgStack{{Item: "wheat", Qty: 100}, {Item: "timber", Qty: 100}}
	// 30 bps of 100 wheat a day is 0.3 of a unit: nothing yet, the carry grows.
	steps, total, carry := spoilage(snap, stacks, 30, 1, 0)
	if len(steps) != 0 || total != 0 || carry != 3000 {
		t.Fatalf("day 1: %+v %d carry %d, want nothing and 3000", steps, total, carry)
	}
	// Four days on, the carried fractions make a whole unit.
	steps, total, carry = spoilage(snap, stacks, 30, 3, carry)
	if total != 1 || len(steps) != 1 || steps[0].item != "wheat" || carry != 2000 {
		t.Fatalf("day 4: %+v %d carry %d, want one wheat and 2000 left", steps, total, carry)
	}
	if _, total, _ := spoilage(snap, stacks, 0, 5, 0); total != 0 {
		t.Error("a zero rate spoiled something")
	}
}
