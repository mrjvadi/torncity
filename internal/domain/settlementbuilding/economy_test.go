package settlementbuilding

import (
	"errors"
	"testing"
	"time"
)

func workplace(produces, consumes map[string]int64) Def {
	return Def{Code: "camp", Role: "forestry", Tier: 1, FootprintW: 2, FootprintH: 2, CostMoney: 1, BuildTime: time.Hour,
		Work: Work{Produces: produces, Consumes: consumes, Workers: 3, Shift: time.Hour, Wage: 40}}
}

func TestWorkplaceIsValid(t *testing.T) {
	if err := ValidateCatalogue([]Def{workplace(map[string]int64{"timber": 4}, nil)}); err != nil {
		t.Fatalf("a well-formed workplace was refused: %v", err)
	}
	if err := ValidateCatalogue([]Def{workplace(map[string]int64{"plank": 2}, map[string]int64{"timber": 3})}); err != nil {
		t.Fatalf("a workshop was refused: %v", err)
	}
}

func TestWorkplaceRefusals(t *testing.T) {
	cases := map[string]Def{
		"consumes what it makes": workplace(map[string]int64{"timber": 4}, map[string]int64{"timber": 1}),
		"no workers":             func() Def { d := workplace(map[string]int64{"t": 1}, nil); d.Work.Workers = 0; return d }(),
		"no shift":               func() Def { d := workplace(map[string]int64{"t": 1}, nil); d.Work.Shift = 0; return d }(),
		"a shift of days":        func() Def { d := workplace(map[string]int64{"t": 1}, nil); d.Work.Shift = 48 * time.Hour; return d }(),
		"negative wage":          func() Def { d := workplace(map[string]int64{"t": 1}, nil); d.Work.Wage = -1; return d }(),
		"zero output":            workplace(map[string]int64{"t": 0}, nil),
		"inputs without output": func() Def {
			d := workplace(nil, map[string]int64{"t": 1})
			return d
		}(),
	}
	for name, d := range cases {
		if err := ValidateCatalogue([]Def{d}); !errors.Is(err, ErrInvalidBuilding) {
			t.Errorf("%s: err = %v, want ErrInvalidBuilding", name, err)
		}
	}
}

func TestListedAtFollowsTheTier(t *testing.T) {
	cases := []struct {
		tier                int
		village, town, city bool
	}{
		{0, true, true, true}, // a legacy row is a village's
		{1, true, true, true},
		{2, false, true, true},
		{3, false, false, true},
		{4, false, false, true},
	}
	for _, c := range cases {
		d := Def{Code: "x", Tier: c.tier}
		if d.ListedAt(SettlementVillage) != c.village || d.ListedAt(SettlementTown) != c.town || d.ListedAt(SettlementCity) != c.city {
			t.Errorf("tier %d listed at village/town/city = %v/%v/%v, want %v/%v/%v", c.tier,
				d.ListedAt(SettlementVillage), d.ListedAt(SettlementTown), d.ListedAt(SettlementCity), c.village, c.town, c.city)
		}
	}
	if (Def{Tier: 1}).ListedAt("moon") {
		t.Error("an unknown settlement tier lists something")
	}
}

func TestCanPlaceRefusesABiggerSettlementsBuilding(t *testing.T) {
	airport := Def{Code: "airport", Role: "transport", Tier: 3, FootprintW: 1, FootprintH: 1}
	s := openStanding()
	s.SettlementTier = SettlementVillage
	if err := CanPlace(airport, sampleGrid(), 0, 0, s); !errors.Is(err, ErrAboveTier) {
		t.Fatalf("a village may place an airport: err = %v", err)
	}
	s.SettlementTier = SettlementCity
	if err := CanPlace(airport, sampleGrid(), 0, 0, s); err != nil {
		t.Fatalf("a city may not place an airport: %v", err)
	}
	s.SettlementTier = "" // a caller that has no tier skips the rule
	if err := CanPlace(airport, sampleGrid(), 0, 0, s); err != nil {
		t.Fatalf("no tier: %v", err)
	}
}
