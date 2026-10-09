package content

import (
	"strings"
	"testing"
	"time"
)

// A function row with a workplace block is the one source of its working building (ADR 0051).
func TestFunctionRowsBecomeWorkplaces(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"well", "mill", "bakery", "charcoal_clamp", "iron_pit", "bloomery", "smithy"} {
		d, ok := snap.SettlementBuildingDef(code)
		if !ok || !d.Generated {
			t.Fatalf("%s is not a generated workplace: %+v", code, d)
		}
		if len(d.Produces) == 0 || d.Workers < 1 || d.Wage <= 0 || d.CostMoney <= 0 {
			t.Errorf("%s: %+v", code, d)
		}
	}
	// the bakery's inputs and fuel are written once, in its row: flour, water and the firewood it bakes with
	b, _ := snap.SettlementBuildingDef("bakery")
	if b.Consumes["flour_sack"] != 8 || b.Consumes["spring_water"] != 2 || b.Consumes["firewood"] != 1 || b.Produces["bread"] != 8 {
		t.Errorf("the bakery: %+v -> %+v", b.Consumes, b.Produces)
	}
	if got := b.Def().Work.Shift; got != 2*time.Hour {
		t.Errorf("the bakery's shift is the staff's 2 game hours: %s", got)
	}
	// bread is the crews' best meal, so the bakery feeds the shifts of every other workplace
	if foods := snap.MealFoods(); len(foods) == 0 || foods[0].Item != "bread" {
		t.Errorf("the first meal food is %+v", foods)
	}
	// the smithy sits where the ore lies and needs the smithing tradition
	sm, _ := snap.SettlementBuildingDef("smithy")
	if sm.TerrainMode != "required" || len(sm.RequiresKnowledge) == 0 || sm.Consumes["bloom"] != 1 || sm.Produces["tools"] != 3 {
		t.Errorf("the smithy: %+v", sm)
	}
}

// The same code in both files is refused: a workplace is written once.
func TestAWorkplaceIsWrittenOnce(t *testing.T) {
	p := shippedPack(t)
	p.SettlementBuildings = append(p.SettlementBuildings, SettlementBuildingDef{Code: "mill", Name: "x", Role: "food", Tier: 1, Footprint: [2]int{1, 1}, BuildTime: "1h"})
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "written once") && !strings.Contains(err.Error(), "also in settlement_buildings.yml") {
		t.Fatalf("a mill in both files was accepted: %v", err)
	}
}
