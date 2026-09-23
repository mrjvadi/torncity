package item

import (
	"errors"
	"testing"
)

// TestEveryProductFromDataAlone runs the whole item pipeline over four
// unrelated products. Nothing in the package knows any of them; they differ
// only in the literal content they were built from.
func TestEveryProductFromDataAlone(t *testing.T) {
	for _, p := range allProducts() {
		t.Run(p.name, func(t *testing.T) {
			mustNil(t, ValidateArchetype(p.archetype, testVocab()))
			mustNil(t, ValidateDesign(p.archetype, p.design, testComponents(), fullAccess()))
			attrs, err := ComputeAttributes(p.archetype, p.design, testComponents())
			mustNil(t, err)
			if len(attrs) != len(p.archetype.Attributes) {
				t.Errorf("got %d attributes, archetype declares %d", len(attrs), len(p.archetype.Attributes))
			}
			recipe, err := DeriveRecipe(p.design)
			mustNil(t, err)
			if len(recipe) == 0 {
				t.Error("a material product derived an empty recipe")
			}
		})
	}
}

func TestValidateArchetypeRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Archetype)
		want   error
	}{
		// ADR 0005 §10, one case per rule.
		{"unknown method", func(a *Archetype) { a.Method = "weave" }, ErrUnknownMethod},
		{"empty method", func(a *Archetype) { a.Method = "" }, ErrUnknownMethod},
		{"slot of unknown category", func(a *Archetype) { a.Slots[0].Accepts = "plasma" }, ErrUnknownCategory},
		{"unknown aggregate", func(a *Archetype) { a.Attributes[0].Aggregate = "median" }, ErrUnknownAggregate},
		{"attribute from missing slot", func(a *Archetype) { a.Attributes[1].From = []string{"barrel", "bayonet"} }, ErrUnknownSlot},
		{"min greater than max", func(a *Archetype) {
			a.Method = MethodFormulate
			a.Slots[0].Quantity = Range(10, 5, "g")
		}, ErrQuantityRange},
		{"no slots for assemble", func(a *Archetype) { a.Slots = nil; a.Attributes = nil }, ErrNoSlots},
		{"no slots for extract", func(a *Archetype) { a.Method = MethodExtract; a.Slots = nil; a.Attributes = nil }, ErrNoSlots},
		{"unknown reverse skill", func(a *Archetype) { a.ReverseSkill = "alchemy" }, ErrUnknownReverseSkill},
		{"missing reverse skill", func(a *Archetype) { a.ReverseSkill = "" }, ErrUnknownReverseSkill},

		// Structural rules that keep the ones above meaningful.
		{"empty code", func(a *Archetype) { a.Code = "" }, ErrEmptyCode},
		{"unnamed slot", func(a *Archetype) { a.Slots[0].Name = "" }, ErrEmptyCode},
		{"unnamed attribute", func(a *Archetype) { a.Attributes[0].Name = "" }, ErrEmptyCode},
		{"duplicate slot", func(a *Archetype) { a.Slots[1].Name = "barrel" }, ErrDuplicate},
		{"duplicate attribute", func(a *Archetype) { a.Attributes[1].Name = "damage" }, ErrDuplicate},
		{"zero quantity", func(a *Archetype) { a.Slots[0].Quantity = Fixed(0) }, ErrInvalidQuantity},
		{"quantity above max", func(a *Archetype) { a.Slots[0].Quantity = Fixed(MaxQuantity + 1) }, ErrInvalidQuantity},
		{"range on an assembly slot", func(a *Archetype) { a.Slots[0].Quantity = Range(1, 3, "") }, ErrAssemblyRange},
		{"attribute with no source", func(a *Archetype) { a.Attributes[0].From = nil }, ErrAttributeSource},
		{"attribute with both sources", func(a *Archetype) { a.Attributes[0].FromAll = true }, ErrAttributeSource},
		{"negative divisor", func(a *Archetype) { a.Attributes[0].Divisor = -1 }, ErrInvalidDivisor},
		{"divisor above max", func(a *Archetype) { a.Attributes[0].Divisor = MaxDivisor + 1 }, ErrInvalidDivisor},
		{"unknown value kind", func(a *Archetype) { a.Value = "" }, ErrUnknownValueKind},
		{"unknown cost kind", func(a *Archetype) { a.Cost = "free" }, ErrUnknownCostKind},
		{"unknown durability", func(a *Archetype) { a.Durability = "" }, ErrUnknownDurability},
		{"derived value with declared cost", func(a *Archetype) { a.Cost = CostDiscretionary }, ErrValueCostMismatch},
		{"negative difficulty", func(a *Archetype) { a.ReverseDifficulty = -1 }, ErrInvalidReverseDifficulty},
		{"difficulty above max", func(a *Archetype) { a.ReverseDifficulty = MaxReverseDifficulty + 1 }, ErrInvalidReverseDifficulty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := rifleArchetype()
			tc.mutate(&a)
			err := ValidateArchetype(a, testVocab())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateArchetypeAllowed(t *testing.T) {
	tests := []struct {
		name string
		a    Archetype
	}{
		{"author without slots", Archetype{
			Code: "film", Method: MethodAuthor,
			Value: ValueSubjective, Cost: CostDiscretionary, Durability: DurabilityDurable,
			ReverseDifficulty: 90, ReverseSkill: "engineering",
		}},
		{"serve without slots or reverse skill", Archetype{
			Code: "surgery", Method: MethodServe,
			Value: ValueSubjective, Cost: CostItemized, Durability: DurabilityConsumable,
		}},
		{"subjective with itemized cost", func() Archetype {
			a := rifleArchetype()
			a.Value = ValueSubjective
			return a
		}()},
		{"range on a formulation slot", medicineArchetype()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mustNil(t, ValidateArchetype(tc.a, testVocab()))
		})
	}
}

// TestValidateArchetypeReportsEverything: a content author fixing a file
// should see every problem in one run, not one per reload.
func TestValidateArchetypeReportsEverything(t *testing.T) {
	a := rifleArchetype()
	a.Method = "weave"
	a.Slots[0].Accepts = "plasma"
	a.Attributes[0].Aggregate = "median"
	a.ReverseSkill = "alchemy"
	err := ValidateArchetype(a, testVocab())
	for _, want := range []error{ErrUnknownMethod, ErrUnknownCategory, ErrUnknownAggregate, ErrUnknownReverseSkill} {
		if !errors.Is(err, want) {
			t.Errorf("joined error %v is missing %v", err, want)
		}
	}
}

func TestValidateComponent(t *testing.T) {
	cats := testVocab().Categories
	mustNil(t, ValidateComponent(testComponents()["cpu_a7"], cats))

	if err := ValidateComponent(Component{Category: "barrel"}, cats); !errors.Is(err, ErrEmptyCode) {
		t.Errorf("no code: got %v", err)
	}
	if err := ValidateComponent(Component{Code: "x", Category: "plasma"}, cats); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("unknown category: got %v", err)
	}
	if err := ValidateComponent(Component{Code: "x", Category: "barrel", RequiresTechnology: []string{""}}, cats); !errors.Is(err, ErrEmptyCode) {
		t.Errorf("unnamed technology: got %v", err)
	}
}

func TestClosedSets(t *testing.T) {
	if got := len(Methods()); got != 7 {
		t.Errorf("%d methods, ADR 0005 §2 has seven", got)
	}
	if got := len(Aggregates()); got != 7 {
		t.Errorf("%d aggregates, ADR 0005 §4 has seven", got)
	}
	Methods()[0] = "mutated"
	if Methods()[0] != MethodExtract {
		t.Error("Methods returned the shared slice")
	}
	for _, m := range Methods() {
		mustNil(t, m.Validate())
		if m.NeedsInputs() == (m == MethodAuthor || m == MethodServe) {
			t.Errorf("%s: NeedsInputs = %v", m, m.NeedsInputs())
		}
		if m.ProducesGoods() == (m == MethodServe) {
			t.Errorf("%s: ProducesGoods = %v", m, m.ProducesGoods())
		}
	}
}
