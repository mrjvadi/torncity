package settlement

import "testing"

func TestCapabilitiesStandingAndNeeds(t *testing.T) {
	c := Compute(CapabilityInput{
		Buildings: []StandingBuilding{
			{Code: "cottage", Role: "housing", Level: 1, Complete: true},
			{Code: "market", Role: "market", Level: 2, Complete: true, Staffed: true},
			{Code: "clinic", Role: "health", Level: 2, Complete: false},
			{Code: "school", Role: "education", Level: 2, Complete: true, DamageBPS: 10000},
			{Code: "road", Complete: true},
		},
		Knowledge:  []string{"masonry", "market_access"},
		StaffRoles: []string{"teacher"},
	})
	for _, tc := range []struct {
		name string
		n    Need
		want bool
	}{
		{"empty is met", Need{}, true},
		{"code stands", Need{Buildings: []BuildingNeed{{Code: "cottage"}}}, true},
		{"role at level or better", Need{Buildings: []BuildingNeed{{Role: "market", Level: 1}}}, true},
		{"role above level", Need{Buildings: []BuildingNeed{{Role: "market", Level: 3}}}, false},
		{"unfinished does not stand", Need{Buildings: []BuildingNeed{{Role: "health", Level: 1}}}, false},
		{"a ruin does not stand", Need{Buildings: []BuildingNeed{{Code: "school"}}}, false},
		{"roads have no role but stand by code", Need{Buildings: []BuildingNeed{{Code: "road"}}}, true},
		{"knowledge held", Need{Knowledge: []string{"masonry"}}, true},
		{"knowledge missing", Need{Knowledge: []string{"masonry_ii"}}, false},
		{"staff filled", Need{Staff: []string{"teacher"}}, true},
		{"staff empty", Need{Staff: []string{"nurse"}}, false},
		{"all parts must hold", Need{Knowledge: []string{"masonry"}, Staff: []string{"nurse"}}, false},
	} {
		if got := c.Satisfies(tc.n); got != tc.want {
			t.Errorf("%s: Satisfies = %v, want %v", tc.name, got, tc.want)
		}
	}
	if c.RoleLevel("market") != 2 || c.StaffedLevel("market") != 2 || c.StaffedLevel("housing") != 0 {
		t.Fatalf("role levels wrong: %d %d %d", c.RoleLevel("market"), c.StaffedLevel("market"), c.StaffedLevel("housing"))
	}
	if c.Buildings() != 3 {
		t.Fatalf("standing = %d, want 3", c.Buildings())
	}
}

func TestMissingNamesWhatIsAbsent(t *testing.T) {
	c := Compute(CapabilityInput{Knowledge: []string{"a"}})
	m := c.Missing(Need{Knowledge: []string{"a", "b"}, Buildings: []BuildingNeed{{Role: "health", Level: 2}}, Staff: []string{"nurse"}})
	if len(m.Knowledge) != 1 || m.Knowledge[0] != "b" || len(m.Buildings) != 1 || len(m.Staff) != 1 {
		t.Fatalf("missing = %+v", m)
	}
	if !c.Satisfies(Need{}.Merge(Need{Knowledge: []string{"a"}})) {
		t.Fatal("merge of met needs must be met")
	}
}
