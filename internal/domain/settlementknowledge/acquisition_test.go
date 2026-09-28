package settlementknowledge

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

func TestTerrainGrantPicksFirstMatchingPriority(t *testing.T) {
	tree := treeOf(t, sampleTree())
	priority := []string{"canal_irrigation", "shaft_irrigation", "terrace_irrigation"}

	code, ok := TerrainGrant(tree, []string{"desert"}, priority)
	if !ok || code != "shaft_irrigation" {
		t.Fatalf("desert terrain: got (%q, %v), want shaft_irrigation", code, ok)
	}
	code, ok = TerrainGrant(tree, []string{"river_lot"}, priority)
	if !ok || code != "canal_irrigation" {
		t.Fatalf("river terrain: got (%q, %v), want canal_irrigation", code, ok)
	}
}

func TestTerrainGrantNoMatch(t *testing.T) {
	tree := treeOf(t, sampleTree())
	_, ok := TerrainGrant(tree, []string{"polar_ice"}, []string{"canal_irrigation", "shaft_irrigation"})
	if ok {
		t.Fatal("no terrain tag matches; ok should be false")
	}
}

func TestHoldersShareBPS(t *testing.T) {
	for _, tc := range []struct {
		holders, total int
		want           int64
	}{
		{0, 10, 0},
		{1, 10, 1000},
		{5, 10, 5000},
		{10, 10, 10000},
		{3, 0, 0},       // no settlements at all
		{20, 10, 10000}, // holders cannot exceed total; clamp
	} {
		if got := HoldersShareBPS(tc.holders, tc.total); got != tc.want {
			t.Errorf("HoldersShareBPS(%d,%d) = %d, want %d", tc.holders, tc.total, got, tc.want)
		}
	}
}

// The owner's own worked example (ADR 0031 section 10 point 3): "while only
// one settlement or country holds it (e.g. rifle-making), its price is
// high... as more settlements hold it, the price falls toward a floor."
// k=10000 (1x), floor=3000 (0.3x), cap=80000 (8x) — a plausible content
// tuning — must produce a monotonically FALLING price as adoption rises,
// bottoming out at the floor and topping out at the cap.
func TestScarcityPriceFallsAsAdoptionRises(t *testing.T) {
	const baseCost = 10_000
	const kBPS, floorBPS, capBPS = 10_000, 3_000, 80_000

	cases := []struct {
		name    string
		holders int64
		want    int64
	}{
		{"unprecedented (nobody holds it)", 0, 80_000 * baseCost / 10_000},                    // clamped to cap: 80,000
		{"one of ten settlements (10%)", 1_000, 10_000 * 10_000 / 1_000 * baseCost / 10_000},  // mult 100000 -> cap 80000
		{"half of all settlements (50%)", 5_000, 10_000 * 10_000 / 5_000 * baseCost / 10_000}, // mult 20000, under cap
		{"every settlement (100%)", 10_000, 10_000 * 10_000 / 10_000 * baseCost / 10_000},     // mult 10000 = 1x
	}
	var prices []int64
	for _, c := range cases {
		got := ScarcityPrice(baseCost, c.holders, kBPS, floorBPS, capBPS)
		prices = append(prices, got)
		t.Logf("%-32s holders_share=%5d bps -> price %d", c.name, c.holders, got)
	}
	// Monotonically non-increasing as adoption rises.
	for i := 1; i < len(prices); i++ {
		if prices[i] > prices[i-1] {
			t.Errorf("price rose from %d to %d as adoption increased (%s -> %s)",
				prices[i-1], prices[i], cases[i-1].name, cases[i].name)
		}
	}
	if prices[0] != 80_000 {
		t.Errorf("an unprecedented item should be capped at 8x base_cost (80000), got %d", prices[0])
	}
}

func TestScarcityPriceRespectsFloorAndCap(t *testing.T) {
	const baseCost = 5_000
	// k small enough that even at 100% adoption the multiplier is below
	// floor, so the floor itself is what must be returned.
	got := ScarcityPrice(baseCost, 10_000, 1_000 /* k=0.1 */, 3_000 /* floor=0.3x */, 80_000)
	var want int64 = baseCost * 3_000 / 10_000
	if got != want {
		t.Errorf("floor not applied: got %d, want %d", got, want)
	}
	// k huge enough that even at 100% adoption the multiplier exceeds cap.
	got = ScarcityPrice(baseCost, 10_000, 1_000_000 /* k=100 */, 3_000, 80_000 /* cap=8x */)
	want = baseCost * 80_000 / 10_000
	if got != want {
		t.Errorf("cap not applied: got %d, want %d", got, want)
	}
}

func TestScarcityPriceNeverFree(t *testing.T) {
	if got := ScarcityPrice(1, 10_000, 1, 1, 1); got < 1 {
		t.Errorf("price = %d, must never be below 1", got)
	}
}

func TestWithinSellerBand(t *testing.T) {
	// +-5%.
	if !WithinSellerBand(10_000, 9_500, 500) {
		t.Error("9500 should be within a 5%% band of 10000")
	}
	if !WithinSellerBand(10_000, 10_500, 500) {
		t.Error("10500 should be within a 5%% band of 10000")
	}
	if WithinSellerBand(10_000, 9_400, 500) {
		t.Error("9400 should be outside a 5%% band of 10000")
	}
	if WithinSellerBand(10_000, 10_600, 500) {
		t.Error("10600 should be outside a 5%% band of 10000")
	}
}

// AdvanceLiteracy: a well-taught, well-staffed, well-capacitated school
// moves an illiterate settlement noticeably in one period, slows as it
// approaches full literacy, and never exceeds 10000.
func TestAdvanceLiteracyMonotonicAndBounded(t *testing.T) {
	const teachRate, teacherSkill, capacity = 4_000, 8_000, 10_000 // all bps

	share := int64(0)
	prevDelta := int64(item.BPS)
	for period := 0; period < 20; period++ {
		next := AdvanceLiteracy(share, teachRate, teacherSkill, capacity)
		if next < share {
			t.Fatalf("period %d: literacy fell from %d to %d", period, share, next)
		}
		delta := next - share
		if delta > prevDelta {
			t.Errorf("period %d: delta grew from %d to %d; diminishing returns expected as literacy rises", period, prevDelta, delta)
		}
		prevDelta = delta
		share = next
	}
	if share > 10_000 {
		t.Fatalf("literacy share exceeded 10000: %d", share)
	}
	if share == 0 {
		t.Fatal("literacy never advanced at all")
	}
}

func TestAdvanceLiteracyNoTeachingNoChange(t *testing.T) {
	if got := AdvanceLiteracy(2_000, 0, 8_000, 10_000); got != 2_000 {
		t.Errorf("zero teach_rate should not move literacy: got %d", got)
	}
	if got := AdvanceLiteracy(2_000, 4_000, 0, 10_000); got != 2_000 {
		t.Errorf("a settlement with nobody skilled enough to teach should not move literacy: got %d", got)
	}
}

func TestAdvanceLiteracyCapsAt10000(t *testing.T) {
	if got := AdvanceLiteracy(9_999, 10_000, 10_000, 10_000); got > 10_000 {
		t.Errorf("literacy exceeded 10000: %d", got)
	}
	if got := AdvanceLiteracy(10_000, 10_000, 10_000, 10_000); got != 10_000 {
		t.Errorf("fully literate settlement should stay at 10000: got %d", got)
	}
}
