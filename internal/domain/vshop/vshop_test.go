package vshop

import (
	"errors"
	"testing"
)

func rules() Rules {
	return Rules{
		MarkupMinBPS: 10_000, MarkupMaxBPS: 15_000, StockDays: 2, FoodShareBPS: 4_000, OtherShareBPS: 3_000,
		PlayerDayFood: 3, PlayerDayOther: 2, SupplyValuePerResident: 400, BuildingBoostBPS: 15_000,
		FreeSales: 3, StepBPS: 100, MaxExtraBPS: 2_500,
	}
}

var bread = Line{Code: "bread", Class: ClassFood, Ref: 40, MarkupBPS: 11_000, PerHeadMilli: 300, Floor: 5}
var sack = Line{Code: "bag_sack", Class: ClassOther, Ref: 80, MarkupBPS: 11_500, PerHeadMilli: 30, Floor: 3}

func TestRulesValidate(t *testing.T) {
	if err := rules().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := rules()
	bad.MarkupMinBPS = 9_000
	bad.StockDays = 0
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("a shop that sells under the reference price was accepted: %v", err)
	}
}

func TestUnitsPerDayFollowThePeopleAndNeverFallUnderTheFloor(t *testing.T) {
	r := rules()
	if got := r.UnitsPerDay(bread, 3, false); got != 5 {
		t.Errorf("3 people: %d, want the floor 5", got)
	}
	// 100 people x 0.30 a day x 40 % = 12 loaves.
	if got := r.UnitsPerDay(bread, 100, false); got != 12 {
		t.Errorf("100 people: %d, want 12", got)
	}
	if got := r.UnitsPerDay(bread, 100, true); got != 18 {
		t.Errorf("100 people with a shop building: %d, want 12 x 1.5 = 18", got)
	}
	if got := r.UnitsPerDay(sack, 10, false); got != 3 {
		t.Errorf("sacks for 10 people: %d, want the floor 3", got)
	}
	if got := r.UnitsPerDay(bread, 0, false); got != 5 {
		t.Errorf("nobody: %d, want the floor", got)
	}
}

func TestAShelfHoldsTwoDaysAndTrimsTheRest(t *testing.T) {
	r := rules()
	capacity := r.StockCap(5)
	if capacity != 10 {
		t.Fatalf("cap %d, want 10", capacity)
	}
	after, added, trimmed := Deliver(0, 5, capacity)
	if after != 5 || added != 5 || trimmed != 0 {
		t.Fatalf("first day: %d/%d/%d", after, added, trimmed)
	}
	after, added, trimmed = Deliver(8, 5, capacity)
	if after != 10 || added != 2 || trimmed != 3 {
		t.Fatalf("a nearly full shelf: %d/%d/%d", after, added, trimmed)
	}
	// Units in = stock out + trimmed, always.
	if after-8+trimmed != 5 {
		t.Fatal("a unit was lost")
	}
}

func TestPriceStaysBetweenTheReferenceAndTheCeiling(t *testing.T) {
	r := rules()
	if got := r.Price(bread, 0, 0); got != 44 { // 40 x 1.10
		t.Errorf("usual price %d, want 44", got)
	}
	// Three sales are free; the fourth adds 1 %, ten more add up to the 25 % ceiling of the demand step.
	if got := r.Price(bread, 4, 0); got != 44 { // +100 bps on 11000 = 11100 -> 44.4 -> 44 (rounded down)
		t.Errorf("after 4 sales %d, want 44", got)
	}
	got := r.Price(bread, 1000, 0)
	if got != 40*13_500/10_000 { // 11000 + 2500 = 13500
		t.Errorf("demand has a ceiling of its own: %d, want %d", got, 40*13_500/10_000)
	}
	// The head's cap holds the price down, but never under the reference.
	if got := r.Price(bread, 1000, 12_000); got != 48 {
		t.Errorf("capped at 1.2x: %d, want 48", got)
	}
	if got := r.Price(bread, 0, 10_000); got != 40 {
		t.Errorf("capped at the reference: %d, want 40", got)
	}
	if got := r.Price(bread, 0, 5_000); got != 40 {
		t.Errorf("a cap under the reference cannot lower the price below it: %d", got)
	}
	// A line marked up past the ceiling is held to it.
	greedy := Line{Code: "g", Class: ClassOther, Ref: 100, MarkupBPS: 30_000}
	if got := r.Price(greedy, 0, 0); got != 150 {
		t.Errorf("a greedy markup: %d, want the 1.5x ceiling 150", got)
	}
	// A cheap good still costs at least its reference (rounding never gives it away).
	cheap := Line{Code: "c", Class: ClassOther, Ref: 1, MarkupBPS: 11_000}
	if got := r.Price(cheap, 0, 0); got != 1 {
		t.Errorf("a one-unit good: %d", got)
	}
}

func TestBounds(t *testing.T) {
	r := rules()
	if !r.Bounds(40, 40) || !r.Bounds(60, 40) {
		t.Error("the reference and 1.5x are inside")
	}
	if r.Bounds(39, 40) || r.Bounds(61, 40) {
		t.Error("under the reference or over the ceiling is outside")
	}
}

func TestPlayerCapAndShelf(t *testing.T) {
	r := rules()
	if got := r.PlayerDayCap(bread, 5); got != 3 {
		t.Errorf("a food line: %d, want 3", got)
	}
	if got := r.PlayerDayCap(sack, 3); got != 2 {
		t.Errorf("another line: %d, want 2", got)
	}
	if err := r.CanSell(bread, 5, 10, 2, 1); err != nil {
		t.Errorf("third loaf of the day: %v", err)
	}
	if err := r.CanSell(bread, 5, 10, 3, 1); !errors.Is(err, ErrPlayerCap) {
		t.Errorf("fourth loaf of the day: %v", err)
	}
	if err := r.CanSell(bread, 5, 1, 0, 2); !errors.Is(err, ErrSoldOut) {
		t.Errorf("more than the shelf: %v", err)
	}
	if err := r.CanSell(bread, 5, 10, 0, 0); err == nil {
		t.Error("selling nothing was accepted")
	}
}

func TestPlanScalesToTheBudgetAndKeepsEveryLine(t *testing.T) {
	r := rules()
	r.SupplyValuePerResident = 10 // a budget far under the floors
	lines := []Line{bread, sack}
	plan := r.Plan(lines, 1, false)
	if len(plan) != 2 {
		t.Fatalf("plan has %d lines", len(plan))
	}
	for _, p := range plan {
		if p.Units < 1 || p.Cap != p.Units*2 {
			t.Errorf("%s: units %d cap %d", p.Line.Code, p.Units, p.Cap)
		}
	}
	// A big enough budget leaves the units alone.
	r.SupplyValuePerResident = 10_000
	plan = r.Plan(lines, 1, false)
	if plan[0].Units != 5 || plan[1].Units != 3 || Value(plan) != 5*40+3*80 {
		t.Errorf("plan %+v value %d", plan, Value(plan))
	}
}
