package item

import (
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func TestDeriveRecipe(t *testing.T) {
	tests := []struct {
		name   string
		design Design
		want   Recipe
	}{
		{"phone", phoneDesign(), Recipe{
			{"battery_li", 1}, {"body_al", 1}, {"cpu_a7", 1}, {"screen_oled", 1}, {"speaker_s1", 2},
		}},
		{"rifle without sight", rifleDesign(""), Recipe{
			{"barrel_long", 1}, {"round_762", 1}, {"stock_wood", 1},
		}},
		{"medicine", medicineDesign(300, 100), Recipe{{"morphine_base", 300}, {"starch", 100}}},
		{"bread", breadDesign(), Recipe{
			{"flour_whole", 500}, {"salt_sea", 5}, {"water_tap", 300}, {"yeast_dry", 10},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeriveRecipe(tc.design)
			mustNil(t, err)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeriveRecipeMergesAndOverhead(t *testing.T) {
	// Two slots with the same component become one line.
	d := Design{Fills: map[string]Fill{
		"left":  {Component: "speaker_s1", Quantity: 1},
		"right": {Component: "speaker_s1", Quantity: 2},
	}}
	got, err := DeriveRecipe(d)
	mustNil(t, err)
	if want := (Recipe{{"speaker_s1", 3}}); !reflect.DeepEqual(got, want) {
		t.Errorf("merge: got %v, want %v", got, want)
	}

	// 10% overhead on 3 units is 3.3, rounded UP to 4: waste is never
	// rounded away.
	d.OverheadBPS = 1_000
	got, err = DeriveRecipe(d)
	mustNil(t, err)
	if want := (Recipe{{"speaker_s1", 4}}); !reflect.DeepEqual(got, want) {
		t.Errorf("overhead: got %v, want %v", got, want)
	}

	// An exact overhead is not rounded.
	d.OverheadBPS = 10_000
	got, err = DeriveRecipe(d)
	mustNil(t, err)
	if got[0].Quantity != 6 {
		t.Errorf("100%% overhead: got %d, want 6", got[0].Quantity)
	}

	// No slots, no inputs.
	got, err = DeriveRecipe(Design{})
	mustNil(t, err)
	if len(got) != 0 {
		t.Errorf("empty design: got %v", got)
	}
}

func TestDeriveRecipeRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		d    Design
		want error
	}{
		"zero quantity":     {Design{Fills: map[string]Fill{"a": {Component: "x", Quantity: 0}}}, ErrInvalidQuantity},
		"huge quantity":     {Design{Fills: map[string]Fill{"a": {Component: "x", Quantity: MaxQuantity + 1}}}, ErrInvalidQuantity},
		"no component":      {Design{Fills: map[string]Fill{"a": {Quantity: 1}}}, ErrUnknownComponent},
		"negative overhead": {Design{OverheadBPS: -1}, ErrInvalidDegradation},
	} {
		if _, err := DeriveRecipe(tc.d); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestRecipeTimes(t *testing.T) {
	r := Recipe{{"a", 2}, {"b", 5}}
	got, err := r.Times(3)
	mustNil(t, err)
	if want := (Recipe{{"a", 6}, {"b", 15}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if r[0].Quantity != 2 {
		t.Error("Times modified its receiver")
	}
	if _, err := r.Times(0); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("zero units: got %v", err)
	}
	if _, err := (Recipe{{"a", math.MaxInt64 / 2}}).Times(3); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflow: got %v", err)
	}
}

// TestDerivedRecipeProperty: for any design in range and any overhead, each
// component's line is ceil(total fill × (1 + overhead)), never less than the
// fills, and n units of the recipe is exactly n times each line.
func TestDerivedRecipeProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	a := breadArchetype()
	for i := 0; i < 2000; i++ {
		d := Design{Archetype: a.Code, Origin: OriginAuthored, Fills: map[string]Fill{}, OverheadBPS: rng.Int64N(MaxOverheadBPS + 1)}
		comps := []string{"flour_whole", "water_tap", "yeast_dry", "salt_sea"}
		fills := map[string]int64{}
		for j, s := range a.Slots {
			if s.Optional && rng.IntN(2) == 0 {
				continue
			}
			q := s.Quantity.Min + rng.Int64N(s.Quantity.Max-s.Quantity.Min+1)
			d.Fills[s.Name] = Fill{Component: comps[j], Quantity: q}
			fills[comps[j]] += q
		}
		mustNil(t, ValidateStructure(a, d, testComponents()))
		r, err := DeriveRecipe(d)
		mustNil(t, err)
		if len(r) != len(fills) {
			t.Fatalf("recipe %v has %d lines, design fills %d components", r, len(r), len(fills))
		}
		n := 1 + rng.Int64N(1000)
		scaled, err := r.Times(n)
		mustNil(t, err)
		for k, in := range r {
			f := fills[in.Component]
			if in.Quantity < f {
				t.Fatalf("line %v below the fill %d", in, f)
			}
			if in.Quantity*BPS < f*(BPS+d.OverheadBPS) || (in.Quantity-1)*BPS >= f*(BPS+d.OverheadBPS) {
				t.Fatalf("line %v is not ceil(%d × (1 + %d bps))", in, f, d.OverheadBPS)
			}
			if scaled[k].Quantity != in.Quantity*n || scaled[k].Component != in.Component {
				t.Fatalf("%d units: %v is not %d × %v", n, scaled[k], n, in)
			}
		}
	}
}

func TestItemizedCostAndPriceFloor(t *testing.T) {
	r, err := DeriveRecipe(medicineDesign(300, 100))
	mustNil(t, err)
	prices := map[string]money.Amount{"morphine_base": money.FromMinor(20), "starch": money.FromMinor(1)}

	cost, err := ItemizedCost(r, prices)
	mustNil(t, err)
	if cost.Minor() != 6_100 {
		t.Errorf("cost %d, want 300×20 + 100×1", cost.Minor())
	}

	floor, has, err := PriceFloor(medicineArchetype(), r, prices)
	mustNil(t, err)
	if !has || floor.Minor() != 6_100 {
		t.Errorf("derived floor: %d, %v", floor.Minor(), has)
	}

	film := Archetype{Code: "film", Method: MethodAuthor, Value: ValueSubjective, Cost: CostDiscretionary}
	if _, has, err := PriceFloor(film, nil, nil); err != nil || has {
		t.Errorf("a subjective product has no floor: %v, %v", has, err)
	}
	if _, _, err := PriceFloor(Archetype{}, nil, nil); !errors.Is(err, ErrUnknownValueKind) {
		t.Errorf("unset value kind: got %v", err)
	}

	if _, err := ItemizedCost(r, map[string]money.Amount{"starch": money.FromMinor(1)}); !errors.Is(err, ErrUnpriced) {
		t.Errorf("unpriced input: got %v", err)
	}
	if _, err := ItemizedCost(Recipe{{"x", 2}}, map[string]money.Amount{"x": money.FromMinor(math.MaxInt64)}); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("overflow: got %v", err)
	}
	if _, err := ItemizedCost(Recipe{{"x", 1}}, map[string]money.Amount{"x": money.FromMinor(-1)}); !errors.Is(err, ErrUnpriced) {
		t.Errorf("negative price: got %v", err)
	}
}
