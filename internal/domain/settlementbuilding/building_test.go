package settlementbuilding

import (
	"errors"
	"testing"
	"time"
)

// A small, realistic catalogue: the universal founding kit (no role), a
// tier-1 security branch (three peer codes) and a tier-2 promotion that
// needs any one of them.
func sampleCatalogue() []Def {
	return []Def{
		{Code: "road", FootprintW: 1, FootprintH: 1, CostMoney: 50, BuildTime: 10 * time.Minute},
		{Code: "civic_hall", FootprintW: 2, FootprintH: 2, CostMoney: 1000, BuildTime: 2 * time.Hour},

		{Code: "watch_hut", Role: "security", Tier: 1, FootprintW: 1, FootprintH: 1,
			RequiresKnowledge: []string{"communal_watch"}, CostMoney: 300, BuildTime: 30 * time.Minute},
		{Code: "militia_camp", Role: "security", Tier: 1, FootprintW: 2, FootprintH: 1,
			RequiresKnowledge: []string{"mounted_militia"}, CostMoney: 500, BuildTime: 45 * time.Minute},
		{Code: "police_post", Role: "security", Tier: 2, FootprintW: 2, FootprintH: 2,
			RequiresBuildingRole: &RoleTier{Role: "security", Tier: 1},
			RequiresKnowledge:    []string{"record_keeping"}, CostMoney: 5000, BuildTime: 4 * time.Hour},

		{Code: "port", FootprintW: 4, FootprintH: 4, TerrainTags: []string{"coastal_lot"}, TerrainMode: TerrainRequired,
			CostMoney: 80_000, BuildTime: 16 * time.Hour},
	}
}

func TestValidateCatalogueAcceptsTheSample(t *testing.T) {
	if err := ValidateCatalogue(sampleCatalogue()); err != nil {
		t.Fatalf("a well-formed catalogue was refused: %v", err)
	}
}

func TestValidateCatalogueUnreachableRole(t *testing.T) {
	defs := []Def{
		{Code: "police_post", Role: "security", Tier: 2, FootprintW: 1, FootprintH: 1,
			RequiresBuildingRole: &RoleTier{Role: "security", Tier: 1}, CostMoney: 1, BuildTime: time.Hour},
	}
	err := ValidateCatalogue(defs)
	if !errors.Is(err, ErrUnreachableRole) {
		t.Fatalf("err = %v, want ErrUnreachableRole", err)
	}
}

func TestValidateCatalogueRoleTierConsistency(t *testing.T) {
	// A tier without a role.
	err := ValidateCatalogue([]Def{{Code: "a", Tier: 1, FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour}})
	if !errors.Is(err, ErrInvalidBuilding) {
		t.Errorf("tier with no role: err = %v", err)
	}
	// A role without a valid tier.
	err = ValidateCatalogue([]Def{{Code: "a", Role: "security", Tier: 0, FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour}})
	if !errors.Is(err, ErrInvalidBuilding) {
		t.Errorf("role with tier 0: err = %v", err)
	}
}

func TestValidateCatalogueTerrainConsistency(t *testing.T) {
	err := ValidateCatalogue([]Def{{Code: "a", FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour,
		TerrainTags: []string{"coastal_lot"}}})
	if !errors.Is(err, ErrInvalidBuilding) {
		t.Errorf("tags with no mode: err = %v", err)
	}
	err = ValidateCatalogue([]Def{{Code: "a", FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour,
		TerrainMode: TerrainRequired}})
	if !errors.Is(err, ErrInvalidBuilding) {
		t.Errorf("mode with no tags: err = %v", err)
	}
}

func TestValidateCatalogueDuplicateCode(t *testing.T) {
	defs := []Def{
		{Code: "road", FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour},
		{Code: "road", FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour},
	}
	if err := ValidateCatalogue(defs); !errors.Is(err, ErrInvalidBuilding) {
		t.Errorf("err = %v, want ErrInvalidBuilding", err)
	}
}

func TestValidateCatalogueBadFootprintCostTime(t *testing.T) {
	if err := ValidateCatalogue([]Def{{Code: "a", FootprintW: 0, FootprintH: 1, CostMoney: 1, BuildTime: time.Hour}}); !errors.Is(err, ErrInvalidBuilding) {
		t.Error("zero footprint width accepted")
	}
	if err := ValidateCatalogue([]Def{{Code: "a", FootprintW: 1, FootprintH: 1, CostMoney: -1, BuildTime: time.Hour}}); !errors.Is(err, ErrInvalidBuilding) {
		t.Error("negative cost accepted")
	}
	if err := ValidateCatalogue([]Def{{Code: "a", FootprintW: 1, FootprintH: 1, CostMoney: 1, BuildTime: 0}}); !errors.Is(err, ErrInvalidBuilding) {
		t.Error("zero build time accepted")
	}
}
