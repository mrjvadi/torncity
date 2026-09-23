package item

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestComputeAttributesProducts(t *testing.T) {
	tests := []struct {
		name      string
		archetype Archetype
		design    Design
		want      map[string]int64
	}{
		{"phone", phoneArchetype(), phoneDesign(), map[string]int64{
			"battery_life": 4000,
			// min(cpu 80, screen 70): the screen holds the phone back.
			"performance": 70,
			// scaled counts the two speakers: 5+40+30+20+3×2.
			"weight": 101,
			// (90+80+85+60+50×2) / 6 = 415/6.
			"quality": 69,
		}},
		{"rifle without sight", rifleArchetype(), rifleDesign(""), map[string]int64{
			"damage":   65,   // barrel 40 + ammo 25
			"accuracy": 80,   // only the barrel contributes
			"weight":   2010, // the empty sight slot adds nothing
		}},
		{"rifle with a weak sight", rifleArchetype(), rifleDesign("scope_x4"), map[string]int64{
			"damage": 65,
			// weakest link: the 60 sight caps the 80 barrel.
			"accuracy": 60,
			"weight":   2310,
		}},
		{"rifle with a strong sight", rifleArchetype(), rifleDesign("scope_x8"), map[string]int64{
			"damage": 65,
			// a better sight cannot lift the barrel's limit.
			"accuracy": 80,
			"weight":   2460,
		}},
		{"medicine 300 mg", medicineArchetype(), medicineDesign(300, 100), map[string]int64{
			"heal_amount":  90, // 3 per 10 mg × 300 mg
			"side_effects": 11, // (20 + 2) / 2
			"stability":    85, // 90% × 95% = 85.5%, truncated
		}},
		{"medicine 100 mg", medicineArchetype(), medicineDesign(100, 100), map[string]int64{
			"heal_amount":  30, // a third of the dose, a third of the effect
			"side_effects": 11,
			"stability":    85,
		}},
		{"bread", breadArchetype(), breadDesign(), map[string]int64{
			"energy": 1750, // 350 per 100 g × 500 g
			// (70×500 + 100×300 + 80×10 + 90×5) / 815 = 66250/815.
			"quality": 81,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComputeAttributes(tc.archetype, tc.design, testComponents())
			mustNil(t, err)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeAttributesRejectsBadDesign(t *testing.T) {
	d := rifleDesign("")
	d.Fills["barrel"] = Fill{Component: "scope_x4", Quantity: 1}
	if _, err := ComputeAttributes(rifleArchetype(), d, testComponents()); !errors.Is(err, ErrCategoryMismatch) {
		t.Errorf("got %v, want ErrCategoryMismatch", err)
	}
}

// TestComputeAttributesSlotOrderIrrelevant: truncation happens once, so
// listing the slots differently cannot change a number.
func TestComputeAttributesSlotOrderIrrelevant(t *testing.T) {
	a := phoneArchetype()
	want, err := ComputeAttributes(a, phoneDesign(), testComponents())
	mustNil(t, err)
	for i, j := 0, len(a.Slots)-1; i < j; i, j = i+1, j-1 {
		a.Slots[i], a.Slots[j] = a.Slots[j], a.Slots[i]
	}
	got, err := ComputeAttributes(a, phoneDesign(), testComponents())
	mustNil(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reversed slots: got %v, want %v", got, want)
	}
}

func TestAggregateFunctions(t *testing.T) {
	three := []term{{value: 10, quantity: 1}, {value: -4, quantity: 3}, {value: 7, quantity: 2}}
	tests := []struct {
		name  string
		at    Attribute
		terms []term
		want  int64
	}{
		{"sum ignores quantity", Attribute{Aggregate: AggregateSum}, three, 13},
		{"min is the weakest link", Attribute{Aggregate: AggregateMin}, three, -4},
		{"max", Attribute{Aggregate: AggregateMax}, three, 10},
		{"avg truncates", Attribute{Aggregate: AggregateAvg}, three, 4},                                // 13/3
		{"avg truncates toward zero", Attribute{Aggregate: AggregateAvg}, []term{{-5, 1}, {0, 1}}, -2}, // -5/2
		{"weighted", Attribute{Aggregate: AggregateWeighted}, three, 2},                                // (10-12+14)/6
		{"scaled", Attribute{Aggregate: AggregateScaled}, three, 12},                                   // 10-12+14
		{"scaled with divisor", Attribute{Aggregate: AggregateScaled, Divisor: 5}, three, 2},
		{"product", Attribute{Aggregate: AggregateProduct}, three, -280},
		{"product fixed point", Attribute{Aggregate: AggregateProduct, Divisor: 10_000},
			[]term{{5_000, 1}, {5_000, 1}, {10_000, 1}}, 2_500}, // 0.5 × 0.5 × 1
		{"single term product ignores divisor", Attribute{Aggregate: AggregateProduct, Divisor: 100}, []term{{42, 1}}, 42},
		{"no terms", Attribute{Aggregate: AggregateMin}, nil, 0},
		{"big intermediate, fitting result", Attribute{Aggregate: AggregateWeighted},
			[]term{{math.MaxInt64, 2}, {math.MaxInt64, 3}}, math.MaxInt64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := aggregate(tc.at, tc.terms)
			mustNil(t, err)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAggregateOverflow(t *testing.T) {
	huge := []term{{math.MaxInt64, 1}, {1, 1}}
	for _, g := range []Aggregate{AggregateSum, AggregateScaled, AggregateProduct} {
		at := Attribute{Aggregate: g}
		terms := huge
		if g == AggregateProduct {
			terms = []term{{math.MaxInt64, 1}, {2, 1}}
		}
		if _, err := aggregate(at, terms); !errors.Is(err, ErrOverflow) {
			t.Errorf("%s: got %v, want ErrOverflow", g, err)
		}
	}
	if _, err := aggregate(Attribute{Aggregate: "median"}, huge); !errors.Is(err, ErrUnknownAggregate) {
		t.Errorf("unknown aggregate: got %v", err)
	}
}

func TestComputeAttributesOverflowReported(t *testing.T) {
	comps := testComponents()
	c := comps["body_al"]
	c.Attributes = map[string]int64{"weight": math.MaxInt64, "quality": 1}
	comps["body_al"] = c
	if _, err := ComputeAttributes(phoneArchetype(), phoneDesign(), comps); !errors.Is(err, ErrOverflow) {
		t.Errorf("got %v, want ErrOverflow", err)
	}
}

// TestObservableAttributes: a competitor sees the observable attributes and
// nothing that reveals the bill of materials.
func TestObservableAttributes(t *testing.T) {
	got, err := ObservableAttributes(phoneArchetype(), phoneDesign(), testComponents())
	mustNil(t, err)
	want := map[string]int64{"battery_life": 4000, "performance": 70}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	got, err = ObservableAttributes(medicineArchetype(), medicineDesign(300, 100), testComponents())
	mustNil(t, err)
	if _, leaked := got["side_effects"]; leaked {
		t.Error("a hidden attribute was observable")
	}
	if want := (map[string]int64{"heal_amount": 90}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for k := range got {
		if _, isSlot := medicineArchetype().Slot(k); isSlot {
			t.Errorf("observable view names slot %q", k)
		}
		if _, isComponent := testComponents()[k]; isComponent {
			t.Errorf("observable view names component %q", k)
		}
	}

	if _, err := ObservableAttributes(rifleArchetype(), phoneDesign(), testComponents()); !errors.Is(err, ErrArchetypeMismatch) {
		t.Errorf("mismatched design: got %v", err)
	}
}
