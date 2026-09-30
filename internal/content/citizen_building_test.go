package content

import (
	"errors"
	"testing"
)

// The citizen catalogue (docs/adr/0033 section 4.5): a private building needs
// a permit class and may not be a role building or a promotion; a civic
// building has neither a permit class nor a home; the owner is one of two.
func TestCitizenBuildingRules(t *testing.T) {
	bad := func(name string, d SettlementBuildingDef) {
		t.Helper()
		p := &Pack{SettlementBuildings: []SettlementBuildingDef{d}}
		if err := p.Validate(); !errors.Is(err, ErrInvalidSettlementBuildingContent) {
			t.Errorf("%s: err = %v, want ErrInvalidSettlementBuildingContent", name, err)
		}
	}
	base := SettlementBuildingDef{Code: "x", Name: "X", Footprint: [2]int{1, 1}, CostMoney: 1, BuildTime: "1h"}
	d := base
	d.Owner = BuildingOwnerCitizen
	bad("a citizen building with no permit class", d)
	d = base
	d.Owner, d.PermitClass, d.Role = BuildingOwnerCitizen, "residential", "craft"
	bad("a citizen building with a role", d)
	d = base
	d.PermitClass = "residential"
	bad("a civic building with a permit class", d)
	d = base
	d.Home = true
	bad("a civic building that is a home", d)
	d = base
	d.Owner = "company"
	bad("an unknown owner", d)
}

// The shipped citizen catalogue: houses and stalls exist, are private, and a
// house is a home.
func TestShippedCitizenCatalogue(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatal(err)
	}
	homes, private := 0, 0
	for _, d := range pack.SettlementBuildings {
		if d.Private() {
			private++
			if d.PermitClass == "" || d.Role != "" {
				t.Errorf("%s: a private building needs a permit class and has no role", d.Code)
			}
			if d.Home {
				homes++
			}
		}
	}
	if private < 3 || homes < 1 {
		t.Errorf("citizen catalogue: %d private buildings, %d homes", private, homes)
	}
}
