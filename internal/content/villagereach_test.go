package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/settlement"
)

// reachPack is a tiny valid village: a founding kit, one grant, timber for
// sale, and a timber-costing building.
func reachPack() *Pack {
	no := false
	return &Pack{
		Components: []ComponentDef{
			{Code: "timber", Name: "Timber", Category: "wood", BasePrice: 15, VillageBuy: true},
			{Code: "stone", Name: "Stone", Category: "mineral", BasePrice: 6},
		},
		SettlementKnowledge: []SettlementKnowledgeDef{
			{Code: "oral_tradition", Name: "O", ModeEligible: &no},
		},
		SettlementBuildings: []SettlementBuildingDef{
			{Code: "road", Name: "Road", Footprint: [2]int{1, 1}, CostMoney: 1, BuildTime: "1h"},
			{Code: "civic_hall", Name: "Hall", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
				CostMaterials: map[string]int64{"timber": 5}},
		},
	}
}

func mustReach(t *testing.T, p *Pack) {
	t.Helper()
	var problems []error
	p.validateVillageReachability(&problems)
	if len(problems) != 0 {
		t.Fatalf("a reachable village was refused: %v", errors.Join(problems...))
	}
}

func reachProblems(p *Pack) string {
	var problems []error
	p.validateVillageReachability(&problems)
	return errors.Join(problems...).Error()
}

func TestReachabilityAcceptsBoughtMaterial(t *testing.T) {
	mustReach(t, reachPack())
}

func TestReachabilityRefusesAMaterialNobodyCanGet(t *testing.T) {
	p := reachPack()
	p.Components[0].VillageBuy = false // timber is now neither bought nor produced
	got := reachProblems(p)
	if !strings.Contains(got, `material "timber"`) {
		t.Fatalf("problems = %q, want the timber material named", got)
	}
}

// A producer whose own build needs its own output, with no other source, is a
// deadlock the owner hit live: the lint must call it out.
func TestReachabilityRefusesAProducerThatNeedsItsOwnOutput(t *testing.T) {
	p := reachPack()
	p.Components[0].VillageBuy = false
	p.SettlementBuildings = append(p.SettlementBuildings, SettlementBuildingDef{
		Code: "sawmill", Name: "Sawmill", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
		CostMaterials: map[string]int64{"timber": 2},
		Produces:      map[string]int64{"timber": 3}, Workers: 1, Shift: "1h", Wage: 1,
	})
	got := reachProblems(p)
	if !strings.Contains(got, `building "sawmill"`) {
		t.Fatalf("problems = %q, want sawmill named as unreachable", got)
	}
}

// The same producer is fine once the village can buy its first timber.
func TestReachabilityAcceptsProducerBootstrappedByPurchase(t *testing.T) {
	p := reachPack()
	p.SettlementBuildings = append(p.SettlementBuildings, SettlementBuildingDef{
		Code: "sawmill", Name: "Sawmill", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
		CostMaterials: map[string]int64{"timber": 2},
		Produces:      map[string]int64{"timber": 3}, Workers: 1, Shift: "1h", Wage: 1,
	})
	mustReach(t, p)
}

func TestReachabilityFollowsAProducedMaterial(t *testing.T) {
	p := reachPack()
	p.Components = append(p.Components, ComponentDef{Code: "plank", Name: "Plank", Category: "wood", BasePrice: 30})
	p.SettlementBuildings = append(p.SettlementBuildings,
		SettlementBuildingDef{Code: "camp", Name: "Camp", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
			Produces: map[string]int64{"timber": 3}, Workers: 1, Shift: "1h", Wage: 1},
		SettlementBuildingDef{Code: "joinery", Name: "Joinery", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
			Consumes: map[string]int64{"timber": 3}, Produces: map[string]int64{"plank": 2}, Workers: 1, Shift: "1h", Wage: 1},
		SettlementBuildingDef{Code: "hall2", Name: "Hall 2", Footprint: [2]int{2, 2}, CostMoney: 1, BuildTime: "1h",
			CostMaterials: map[string]int64{"plank": 4}},
	)
	mustReach(t, p)
	r := p.VillageReachability()
	if r.Materials["plank"] == "" || r.Buildings["hall2"] == "" {
		t.Fatalf("plank/hall2 not reached: %+v", r)
	}
}

func TestReachabilityRefusesKnowledgeNobodyCanObtain(t *testing.T) {
	p := reachPack()
	p.SettlementKnowledge = append(p.SettlementKnowledge,
		SettlementKnowledgeDef{Code: "a", Name: "A", Requires: []string{"b"}},
		SettlementKnowledgeDef{Code: "b", Name: "B", Requires: []string{"a"}},
	)
	if got := reachProblems(p); !strings.Contains(got, `knowledge "a"`) {
		t.Fatalf("problems = %q, want the knowledge cycle named", got)
	}
}

func TestVillageBuyNeedsAPrice(t *testing.T) {
	p := reachPack()
	p.Components[0].BasePrice = 0
	if got := reachProblems(p); !strings.Contains(got, "base_price") {
		t.Fatalf("problems = %q", got)
	}
}

// The shipped content passes the lint (Validate runs it), and the first
// material the owner's village hit, timber, is both buyable and produced by a
// building that needs no timber.
func TestShippedVillageIsFullyReachable(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r := pack.VillageReachability()
	if n := len(r.UnreachableBuildings) + len(r.UnreachableKnowledge) + len(r.UnreachableMaterials); n != 0 {
		t.Fatalf("unreachable: %+v", r)
	}
	for _, m := range []string{"timber", "stone"} {
		if r.Materials[m] == "" {
			t.Errorf("material %s is not reachable", m)
		}
	}
	for _, b := range pack.SettlementBuildings {
		if b.Code == "woodcutter_camp" {
			if len(b.CostMaterials) != 0 || len(b.RequiresKnowledge) != 0 {
				t.Errorf("woodcutter_camp must need no material and no knowledge, has %v %v", b.CostMaterials, b.RequiresKnowledge)
			}
			if b.Produces["timber"] == 0 {
				t.Errorf("woodcutter_camp does not produce timber")
			}
		}
	}
}

func TestVillageFoundingKitMatchesTheDomain(t *testing.T) {
	var kit []string
	for _, b := range settlement.FoundingKitBuildings {
		kit = append(kit, b.TypeCode)
	}
	if strings.Join(kit, ",") != strings.Join(villageFoundingKit, ",") {
		t.Fatalf("kit = %v, lint assumes %v", kit, villageFoundingKit)
	}
}
