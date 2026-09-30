package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
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
	if school.Def().ListedAt("village") {
		t.Error("a school is a town's building")
	}
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

func TestStockCapacityAddsGranary(t *testing.T) {
	snap := shippedSnapshot(t)
	granary, ok := snap.SettlementBuildingDef("granary")
	if !ok || granary.Storage <= 0 {
		t.Fatalf("the granary has no storage: %+v", granary)
	}
	def := granary.Def()
	if def.Storage != granary.Storage {
		t.Error("Def() drops the storage")
	}
	if err := settlementbuilding.ValidateCatalogue([]settlementbuilding.Def{def}); err != nil {
		t.Errorf("the granary is invalid: %v", err)
	}
}
