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

func TestListedAtIgnoresTheSize(t *testing.T) {
	for tier := 0; tier <= 4; tier++ {
		d := Def{Code: "x", Tier: tier}
		for _, label := range []string{SettlementVillage, SettlementTown, SettlementCity} {
			if !d.ListedAt(label) {
				t.Errorf("a level %d building is not listed for a settlement labelled %s: the label gates nothing", tier, label)
			}
		}
	}
	if (Def{Tier: 1}).ListedAt("moon") {
		t.Error("an unknown settlement label lists something")
	}
}

func TestCanPlaceDoesNotJudgeBySize(t *testing.T) {
	airport := Def{Code: "airport", Role: "transport", Tier: 3, FootprintW: 1, FootprintH: 1}
	s := openStanding()
	s.SettlementTier = SettlementVillage
	if err := CanPlace(airport, sampleGrid(), 0, 0, s); err != nil {
		t.Fatalf("the size label refused a building whose own prerequisites hold: %v", err)
	}
}
