package lotbuild

import (
	"errors"
	"testing"
)

func testMods() map[string]Module {
	return map[string]Module{
		"bedroom":   {Code: "bedroom", Area: 2, Provides: map[string]int64{"housing_capacity": 2}, Materials: map[string]int64{"timber": 3}, Shifts: 3},
		"hearth":    {Code: "hearth", Area: 1, Materials: map[string]int64{"stone": 2}, Shifts: 2},
		"storeroom": {Code: "storeroom", Area: 1, Provides: map[string]int64{"personal_storage": 40}, Materials: map[string]int64{"timber": 2}, Shifts: 2},
		"cellar":    {Code: "cellar", Area: 0, Provides: map[string]int64{"personal_storage": 60}, Materials: map[string]int64{"stone": 3, "timber": 1}, Shifts: 3},
		"workbench": {Code: "workbench", Area: 2},
		"shelves":   {Code: "shelves", Area: 2, Provides: map[string]int64{"stall_slots": 1, "personal_storage": 60}, Materials: map[string]int64{"timber": 2}, Shifts: 2},
	}
}

func testSpecs() map[string]Spec {
	return map[string]Spec{
		"dwelling": {Code: "dwelling", MaxW: 1, MaxD: 1,
			Slots: map[string]int{"bedroom": 3, "hearth": 1, "storeroom": 2, "cellar": 1, "workbench": 1},
			Levels: []Level{
				{Level: 1, Adds: []string{"bedroom", "hearth", "storeroom"}, Building: "private_cottage", CostMoney: 800, Materials: map[string]int64{"timber": 3}, Hours: 2},
				{Level: 2, Adds: []string{"bedroom"}, Building: "private_house", CostMoney: 1800, Materials: map[string]int64{"timber": 8}, Hours: 4},
			}},
		"stall": {Code: "stall", MaxW: 1, MaxD: 1, Slots: map[string]int{"shelves": 3},
			Levels: []Level{{Level: 1, Adds: []string{"shelves"}, Building: "market_stall", CostMoney: 500, Materials: map[string]int64{"timber": 2}, Hours: 1}}},
	}
}

func perHour(h int) int { return h * 2 }

var storeyRules = StoreyRules{AreaPerCell: 6, TimberPerCell: 4, StonePerCell: 6, StoneFrom: 3, ShiftsPerCell: 4}

func cottage() Composition {
	return Composition{Function: "dwelling", Level: 1, Storeys: 1, W: 1, D: 1, Modules: Included(testSpecs()["dwelling"], 1)}
}

func TestIncludedAndExtras(t *testing.T) {
	spec := testSpecs()["dwelling"]
	if got := Included(spec, 1); got["bedroom"] != 1 || got["hearth"] != 1 || got["storeroom"] != 1 {
		t.Errorf("level 1 includes %v", got)
	}
	if got := Included(spec, 2); got["bedroom"] != 2 {
		t.Errorf("level 2 includes %v", got)
	}
	c := cottage()
	if len(Extras(spec, c)) != 0 {
		t.Error("a cottage as built has no extras")
	}
	c.Modules["bedroom"] = 2
	c.Modules["cellar"] = 1
	e := Extras(spec, c)
	if e["bedroom"] != 1 || e["cellar"] != 1 || len(e) != 2 {
		t.Errorf("extras %v", e)
	}
	// the legacy catalogue counts the included bedroom: only the extra adds housing
	if got := Provided(spec, testMods(), c, "housing_capacity", false); got != 2 {
		t.Errorf("housing from extras %d, want 2", got)
	}
	if got := Provided(spec, testMods(), c, "personal_storage", false); got != 60 {
		t.Errorf("storage from extras %d, want the cellar's 60", got)
	}
}

func TestAreaDecidesWhatFits(t *testing.T) {
	mods, spec := testMods(), testSpecs()["dwelling"]
	c := cottage() // bedroom 2 + hearth 1 + storeroom 1 = 4 of 6
	if got, want := Used(c, mods), 4; got != want {
		t.Fatalf("used %d, want %d", got, want)
	}
	if err := CanAdd(spec, mods, c, "bedroom", 1, 6); err != nil {
		t.Errorf("a bedroom (2) fits the 2 left: %v", err)
	}
	if err := CanAdd(spec, mods, c, "bedroom", 2, 6); !errors.Is(err, ErrNoArea) {
		t.Errorf("two bedrooms do not: %v", err)
	}
	if err := CanAdd(spec, mods, c, "cellar", 1, 6); err != nil {
		t.Errorf("a cellar takes no floor: %v", err)
	}
	if err := CanAdd(spec, mods, c, "workbench", 1, 6); !errors.Is(err, ErrNotBuildable) {
		t.Errorf("the workbench has no rule yet: %v", err)
	}
	if err := CanAdd(spec, mods, c, "shelves", 1, 6); !errors.Is(err, ErrUnknownModule) {
		t.Errorf("a dwelling takes no shelves: %v", err)
	}
	c.Modules["hearth"] = 1
	if err := CanAdd(spec, mods, c, "hearth", 1, 6); !errors.Is(err, ErrSlotFull) {
		t.Errorf("one hearth only: %v", err)
	}
	// a second storey doubles the area
	c.Storeys = 2
	if err := CanAdd(spec, mods, c, "bedroom", 2, 6); err != nil {
		t.Errorf("with two storeys two bedrooms fit: %v", err)
	}
}

func TestQuoteSumsTheModulesAndTheLevel(t *testing.T) {
	mods, specs := testMods(), testSpecs()
	c := cottage()
	// one bedroom and a cellar: 3+1 timber, 3 stone, 3+3 shifts
	cost, err := QuoteOrder(specs, mods, c, Order{Adds: map[string]int{"bedroom": 1, "cellar": 1}}, storeyRules, perHour)
	if err != nil {
		t.Fatal(err)
	}
	if cost.Materials["timber"] != 4 || cost.Materials["stone"] != 3 || cost.Shifts != 6 || cost.Money != 0 {
		t.Errorf("cost %+v", cost)
	}
	// the level costs its own money, materials and hours (4 hours at 2 shifts an hour)
	cost, err = QuoteOrder(specs, mods, c, Order{LevelTo: 2}, storeyRules, perHour)
	if err != nil {
		t.Fatal(err)
	}
	if cost.Money != 1800 || cost.Materials["timber"] != 8 || cost.Shifts != 8 {
		t.Errorf("level cost %+v", cost)
	}
	// a storey: 4 timber a cell, 4 shifts a cell; the third is stone
	cost, err = QuoteOrder(specs, mods, c, Order{StoreysTo: 2}, storeyRules, perHour)
	if err != nil || cost.Materials["timber"] != 4 || cost.Shifts != 4 {
		t.Errorf("storey cost %+v %v", cost, err)
	}
	cost, _ = QuoteOrder(specs, mods, c, Order{StoreysTo: 3}, storeyRules, perHour)
	if cost.Materials["timber"] != 4 || cost.Materials["stone"] != 6 {
		t.Errorf("third storey cost %+v", cost)
	}
	// a storey makes room for a second extra bedroom in the same order
	if _, err := QuoteOrder(specs, mods, c, Order{StoreysTo: 2, Adds: map[string]int{"bedroom": 2}}, storeyRules, perHour); err != nil {
		t.Errorf("storey then bedrooms: %v", err)
	}
	if _, err := QuoteOrder(specs, mods, c, Order{}, storeyRules, perHour); !errors.Is(err, ErrNothing) {
		t.Errorf("empty order: %v", err)
	}
	// a conversion costs the new function's first level, and its included modules replace the old ones
	cost, err = QuoteOrder(specs, mods, c, Order{ConvertTo: "stall", Adds: map[string]int{"shelves": 1}}, storeyRules, perHour)
	if err != nil {
		t.Fatal(err)
	}
	if cost.Money != 500 || cost.Materials["timber"] != 2+2 || cost.Shifts != 2+2 {
		t.Errorf("conversion cost %+v", cost)
	}
}

func TestMaxStoreysFollowsKnowledge(t *testing.T) {
	rules := []StoreyKnowledge{{"carpentry_ii", 2}, {"masonry", 2}, {"masonry_ii", 3}, {"masonry_iii", 5}}
	has := func(owned ...string) func(string) bool {
		return func(c string) bool {
			for _, o := range owned {
				if o == c {
					return true
				}
			}
			return false
		}
	}
	if MaxStoreys(rules, has()) != 1 || MaxStoreys(rules, has("carpentry_ii")) != 2 || MaxStoreys(rules, has("masonry", "masonry_ii")) != 3 || MaxStoreys(rules, has("masonry_iii")) != 5 {
		t.Error("the support table is not followed")
	}
	if StabilityBPS(1, 3) != 10000 || StabilityBPS(3, 3) != 3334 || StabilityBPS(4, 3) != 0 {
		t.Errorf("stability %d %d %d", StabilityBPS(1, 3), StabilityBPS(3, 3), StabilityBPS(4, 3))
	}
}

func TestATemplateReachesThePlanAndNeverRemoves(t *testing.T) {
	mods, specs := testMods(), testSpecs()
	house := Composition{Function: "dwelling", Level: 2, Storeys: 2, W: 1, D: 1, Modules: map[string]int{"bedroom": 3, "hearth": 1, "storeroom": 1, "cellar": 1}}
	plan := TemplateOf(house)
	if !plan.Valid() {
		t.Fatal("the plan is not valid")
	}
	// on a fresh cottage: the level, the storey, one more bedroom beyond the level's, and the cellar
	o, skipped := OrderToReach(specs, mods, cottage(), plan)
	if len(skipped) != 0 || o.LevelTo != 2 || o.StoreysTo != 2 || o.Adds["bedroom"] != 1 || o.Adds["cellar"] != 1 || o.ConvertTo != "" {
		t.Errorf("order %+v skipped %v", o, skipped)
	}
	// a building that already has everything needs nothing
	if o, _ := OrderToReach(specs, mods, house, plan); !o.Empty() {
		t.Errorf("order for a finished plan: %+v", o)
	}
	// a plan of another function converts, and a module no rule builds is skipped
	stallPlan := Template{Function: "stall", Level: 1, Storeys: 1, Modules: map[string]int{"shelves": 2}}
	o, _ = OrderToReach(specs, mods, cottage(), stallPlan)
	if o.ConvertTo != "stall" || o.Adds["shelves"] != 1 {
		t.Errorf("conversion order %+v", o)
	}
	dw := Template{Function: "dwelling", Level: 1, Storeys: 1, Modules: map[string]int{"workbench": 1}}
	if _, skipped := OrderToReach(specs, mods, cottage(), dw); len(skipped) != 1 || skipped[0] != "workbench" {
		t.Errorf("skipped %v", skipped)
	}
	if (Template{}).Valid() {
		t.Error("an empty template is valid")
	}
}
