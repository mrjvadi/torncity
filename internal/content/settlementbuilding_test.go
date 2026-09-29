package content

import (
	"errors"
	"testing"
)

// The shipped building catalogue's own shape: counts per role, and the
// universal no-role founding-kit/city-tier rows.
func TestShippedSettlementBuildingCatalogueShape(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	defs := pack.SettlementBuildings
	if len(defs) < 30 {
		t.Fatalf("only %d settlement buildings shipped, want at least 30", len(defs))
	}

	byRole := map[string]int{}
	noRole := 0
	for _, d := range defs {
		if d.Role == "" {
			noRole++
			continue
		}
		byRole[d.Role]++
	}
	for _, want := range []string{"security", "craft", "extraction", "water_infra", "food", "health", "education", "market", "storage"} {
		if byRole[want] < 1 {
			t.Errorf("role %q has no building", want)
		}
	}
	if noRole < 2 {
		t.Errorf("only %d no-role buildings, want at least road and civic_hall", noRole)
	}
	byCode := map[string]bool{}
	for _, d := range defs {
		byCode[d.Code] = true
	}
	for _, code := range []string{"road", "civic_hall"} {
		if !byCode[code] {
			t.Errorf("the founding kit's %q is not in the catalogue", code)
		}
	}
	t.Logf("settlement buildings: %d total (%d no-role), by role: %v", len(defs), noRole, byRole)
}

// A tier-2 promotion's requires_building_role must name a role/tier a
// tier-1 row of the shipped catalogue actually declares (cross-checked by
// internal/domain/settlementbuilding.ValidateCatalogue, exercised here
// against the real file).
func TestShippedPolicePostPromotesFromAnyTier1Security(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var police SettlementBuildingDef
	found := false
	for _, d := range pack.SettlementBuildings {
		if d.Code == "police_post" {
			police, found = d, true
		}
	}
	if !found {
		t.Fatal("police_post is not in the shipped catalogue")
	}
	if police.RequiresBuildingRole == nil || police.RequiresBuildingRole.Role != "security" || police.RequiresBuildingRole.Tier != 1 {
		t.Fatalf("police_post.requires_building_role = %+v, want security tier 1", police.RequiresBuildingRole)
	}
}

// A building naming an unknown knowledge code is refused at load.
func TestSettlementBuildingUnknownKnowledgeRefused(t *testing.T) {
	p := &Pack{
		SettlementBuildings: []SettlementBuildingDef{
			{Code: "x", Name: "X", Footprint: [2]int{1, 1}, CostMoney: 1, BuildTime: "1h",
				RequiresKnowledge: []string{"ghost_knowledge"}},
		},
	}
	err := p.Validate()
	if !errors.Is(err, ErrInvalidSettlementBuildingContent) {
		t.Fatalf("err = %v, want ErrInvalidSettlementBuildingContent", err)
	}
}

// A building costing an unknown component is refused at load.
func TestSettlementBuildingUnknownComponentRefused(t *testing.T) {
	p := &Pack{
		SettlementBuildings: []SettlementBuildingDef{
			{Code: "x", Name: "X", Footprint: [2]int{1, 1}, CostMoney: 1, BuildTime: "1h",
				CostMaterials: map[string]int64{"unobtainium": 1}},
		},
	}
	err := p.Validate()
	if !errors.Is(err, ErrInvalidSettlementBuildingContent) {
		t.Fatalf("err = %v, want ErrInvalidSettlementBuildingContent", err)
	}
}
