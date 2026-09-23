package item

import "testing"

// Everything in this file is content. The archetypes and components are the
// literal equivalent of what the loader builds from YAML, and building four
// very different products — an assembled phone, an assembled rifle with an
// optional part, a formulated medicine and a formulated bread — from the same
// types, with no product named anywhere in the package, is what proves that a
// new product needs data and not code.

func testVocab() Vocabulary {
	return Vocabulary{
		Categories: NewSet(
			"processor", "battery", "display", "casing", "speaker",
			"barrel", "stock", "optic", "cartridge",
			"alkaloid", "binder",
			"flour", "water", "leavening", "salt",
		),
		Skills: NewSet("electronics", "engineering", "chemistry", "cooking"),
	}
}

func phoneArchetype() Archetype {
	return Archetype{
		Code:   "phone",
		Method: MethodAssemble,
		Slots: []Slot{
			{Name: "cpu", Accepts: "processor", Quantity: Fixed(1)},
			{Name: "battery", Accepts: "battery", Quantity: Fixed(1)},
			{Name: "screen", Accepts: "display", Quantity: Fixed(1)},
			{Name: "body", Accepts: "casing", Quantity: Fixed(1)},
			{Name: "speakers", Accepts: "speaker", Quantity: Fixed(2)},
		},
		Attributes: []Attribute{
			{Name: "battery_life", Aggregate: AggregateSum, From: []string{"battery"}, Input: "capacity", Observable: true},
			{Name: "performance", Aggregate: AggregateMin, From: []string{"cpu", "screen"}, Observable: true},
			{Name: "weight", Aggregate: AggregateScaled, FromAll: true},
			{Name: "quality", Aggregate: AggregateWeighted, FromAll: true},
		},
		Value:             ValueDerived,
		Cost:              CostItemized,
		Durability:        DurabilityDurable,
		ReverseDifficulty: 50,
		ReverseSkill:      "electronics",
	}
}

func rifleArchetype() Archetype {
	return Archetype{
		Code:   "rifle",
		Method: MethodAssemble,
		Slots: []Slot{
			{Name: "barrel", Accepts: "barrel", Quantity: Fixed(1)},
			{Name: "stock", Accepts: "stock", Quantity: Fixed(1)},
			{Name: "sight", Accepts: "optic", Quantity: Fixed(1), Optional: true},
			{Name: "ammo", Accepts: "cartridge", Quantity: Fixed(1)},
		},
		Attributes: []Attribute{
			{Name: "damage", Aggregate: AggregateSum, From: []string{"barrel", "ammo"}, Observable: true},
			{Name: "accuracy", Aggregate: AggregateMin, From: []string{"barrel", "sight"}, Observable: true},
			{Name: "weight", Aggregate: AggregateSum, FromAll: true},
		},
		Value:             ValueDerived,
		Cost:              CostItemized,
		Durability:        DurabilityDurable,
		ReverseDifficulty: 40,
		ReverseSkill:      "engineering",
	}
}

func medicineArchetype() Archetype {
	return Archetype{
		Code:   "painkiller",
		Method: MethodFormulate,
		Slots: []Slot{
			{Name: "active", Accepts: "alkaloid", Quantity: Range(50, 500, "mg")},
			{Name: "carrier", Accepts: "binder", Quantity: Range(10, 200, "mg")},
		},
		Attributes: []Attribute{
			// potency is per 10 mg of the active ingredient.
			{Name: "heal_amount", Aggregate: AggregateScaled, From: []string{"active"}, Input: "potency", Divisor: 10, Observable: true},
			{Name: "side_effects", Aggregate: AggregateAvg, FromAll: true, Input: "toxicity"},
			// stability is a percentage, so the product is taken with base 100.
			{Name: "stability", Aggregate: AggregateProduct, FromAll: true, Divisor: 100},
		},
		Value:             ValueDerived,
		Cost:              CostItemized,
		Durability:        DurabilityConsumable,
		ReverseDifficulty: 80,
		ReverseSkill:      "chemistry",
	}
}

func breadArchetype() Archetype {
	return Archetype{
		Code:   "bread",
		Method: MethodFormulate,
		Slots: []Slot{
			{Name: "flour", Accepts: "flour", Quantity: Range(300, 600, "g")},
			{Name: "water", Accepts: "water", Quantity: Range(150, 400, "ml")},
			{Name: "yeast", Accepts: "leavening", Quantity: Range(5, 20, "g")},
			{Name: "salt", Accepts: "salt", Quantity: Range(2, 12, "g"), Optional: true},
		},
		Attributes: []Attribute{
			// energy is per 100 g of flour.
			{Name: "energy", Aggregate: AggregateScaled, From: []string{"flour"}, Divisor: 100, Observable: true},
			{Name: "quality", Aggregate: AggregateWeighted, FromAll: true},
		},
		Value:             ValueDerived,
		Cost:              CostItemized,
		Durability:        DurabilityConsumable,
		ReverseDifficulty: 10,
		ReverseSkill:      "cooking",
	}
}

func testComponents() Components {
	list := []Component{
		{Code: "cpu_a7", Category: "processor", RequiresTechnology: []string{"semiconductor"},
			Attributes: map[string]int64{"performance": 80, "weight": 5, "quality": 90}},
		{Code: "battery_li", Category: "battery", RequiresTechnology: []string{"battery_chemistry"},
			Attributes: map[string]int64{"capacity": 4000, "weight": 40, "quality": 80}},
		{Code: "screen_oled", Category: "display", RequiresTechnology: []string{"display"},
			Attributes: map[string]int64{"performance": 70, "weight": 30, "quality": 85}},
		{Code: "body_al", Category: "casing",
			Attributes: map[string]int64{"weight": 20, "quality": 60}},
		{Code: "speaker_s1", Category: "speaker", RequiresTechnology: []string{"electronics"},
			Attributes: map[string]int64{"weight": 3, "quality": 50}},

		{Code: "barrel_long", Category: "barrel",
			Attributes: map[string]int64{"damage": 40, "accuracy": 80, "weight": 1200}},
		{Code: "stock_wood", Category: "stock",
			Attributes: map[string]int64{"weight": 800}},
		{Code: "scope_x4", Category: "optic",
			Attributes: map[string]int64{"accuracy": 60, "weight": 300}},
		{Code: "scope_x8", Category: "optic",
			Attributes: map[string]int64{"accuracy": 95, "weight": 450}},
		{Code: "round_762", Category: "cartridge",
			Attributes: map[string]int64{"damage": 25, "weight": 10}},

		{Code: "morphine_base", Category: "alkaloid", RequiresTechnology: []string{"pharmacology"},
			Attributes: map[string]int64{"potency": 3, "toxicity": 20, "stability": 90}},
		{Code: "starch", Category: "binder",
			Attributes: map[string]int64{"toxicity": 2, "stability": 95}},

		{Code: "flour_whole", Category: "flour",
			Attributes: map[string]int64{"energy": 350, "quality": 70}},
		{Code: "water_tap", Category: "water",
			Attributes: map[string]int64{"quality": 100}},
		{Code: "yeast_dry", Category: "leavening",
			Attributes: map[string]int64{"quality": 80}},
		{Code: "salt_sea", Category: "salt",
			Attributes: map[string]int64{"quality": 90}},
	}
	out := make(Components, len(list))
	for _, c := range list {
		out[c.Code] = c
	}
	return out
}

func phoneDesign() Design {
	return Design{
		ID: "nil-mobile-x1", Archetype: "phone", Origin: OriginAuthored,
		Fills: map[string]Fill{
			"cpu":      {Component: "cpu_a7", Quantity: 1},
			"battery":  {Component: "battery_li", Quantity: 1},
			"screen":   {Component: "screen_oled", Quantity: 1},
			"body":     {Component: "body_al", Quantity: 1},
			"speakers": {Component: "speaker_s1", Quantity: 2},
		},
	}
}

func rifleDesign(sight string) Design {
	d := Design{
		ID: "r-1", Archetype: "rifle", Origin: OriginAuthored,
		Fills: map[string]Fill{
			"barrel": {Component: "barrel_long", Quantity: 1},
			"stock":  {Component: "stock_wood", Quantity: 1},
			"ammo":   {Component: "round_762", Quantity: 1},
		},
	}
	if sight != "" {
		d.Fills["sight"] = Fill{Component: sight, Quantity: 1}
	}
	return d
}

func medicineDesign(active, carrier int64) Design {
	return Design{
		ID: "m-1", Archetype: "painkiller", Origin: OriginAuthored,
		Fills: map[string]Fill{
			"active":  {Component: "morphine_base", Quantity: active},
			"carrier": {Component: "starch", Quantity: carrier},
		},
	}
}

func breadDesign() Design {
	return Design{
		ID: "b-1", Archetype: "bread", Origin: OriginAuthored,
		Fills: map[string]Fill{
			"flour": {Component: "flour_whole", Quantity: 500},
			"water": {Component: "water_tap", Quantity: 300},
			"yeast": {Component: "yeast_dry", Quantity: 10},
			"salt":  {Component: "salt_sea", Quantity: 5},
		},
	}
}

// fullAccess unlocks every technology the fixtures use.
func fullAccess() TechAccess {
	return TechAccess{Unlocked: NewSet("semiconductor", "battery_chemistry", "display", "electronics", "pharmacology")}
}

type product struct {
	name      string
	archetype Archetype
	design    Design
}

func allProducts() []product {
	return []product{
		{"phone", phoneArchetype(), phoneDesign()},
		{"rifle", rifleArchetype(), rifleDesign("scope_x4")},
		{"medicine", medicineArchetype(), medicineDesign(300, 100)},
		{"bread", breadArchetype(), breadDesign()},
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
