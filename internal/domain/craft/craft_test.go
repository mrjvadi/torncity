package craft

import "testing"

var ladder = Tools{Items: []Tier{{0, "stone_tools"}, {1, "tools"}, {2, "forged_tools"}}, ShortBPS: 6000, WearDivisor: 2}

func TestTheBestToolIsTheHighestHeld(t *testing.T) {
	if _, ok := ladder.Best(map[string]int64{}); ok {
		t.Error("no tool, no tier")
	}
	if tier, ok := ladder.Best(map[string]int64{"stone_tools": 3, "tools": 1}); !ok || tier != 1 {
		t.Errorf("iron beats stone: %d", tier)
	}
	if tier, _ := ladder.Best(map[string]int64{"stone_tools": 1, "forged_tools": 1, "tools": 5}); tier != 2 {
		t.Errorf("forged beats all: %d", tier)
	}
}

func TestADueWearTakesTheLowestToolThatIsEnough(t *testing.T) {
	all := map[string]int64{"stone_tools": 2, "tools": 2, "forged_tools": 2}
	if item, _, _ := ladder.Pick(all, 1); item != "tools" {
		t.Errorf("work that needs iron wears iron, not forged: %s", item)
	}
	if item, _, _ := ladder.Pick(all, 2); item != "forged_tools" {
		t.Errorf("work that needs forged wears forged: %s", item)
	}
	if item, tier, ok := ladder.Pick(map[string]int64{"stone_tools": 1}, 2); !ok || item != "stone_tools" || tier != 0 {
		t.Errorf("with only stone the stone tool wears: %s %d", item, tier)
	}
	if item, _, _ := ladder.Pick(map[string]int64{"stone_tools": 1, "tools": 1}, 2); item != "tools" {
		t.Errorf("the best of the lesser ones wears: %s", item)
	}
	if _, _, ok := ladder.Pick(map[string]int64{}, 1); ok {
		t.Error("nothing to wear")
	}
}

func TestALesserToolKeepsASharePerTierShort(t *testing.T) {
	for _, tc := range []struct {
		have, need int
		want       int64
	}{{1, 1, 10_000}, {2, 1, 10_000}, {0, 1, 6_000}, {1, 2, 6_000}, {0, 2, 3_600}} {
		if got := ladder.Factor(tc.have, tc.need); got != tc.want {
			t.Errorf("tool tier %d for work of tier %d: %d, want %d", tc.have, tc.need, got, tc.want)
		}
	}
}

func TestABetterToolWearsSlower(t *testing.T) {
	for _, tc := range []struct {
		tier, need int
		want       int64
	}{{1, 1, 100}, {2, 1, 50}, {2, 0, 25}, {0, 1, 100}} {
		if got := ladder.Wear(100, tc.tier, tc.need); got != tc.want {
			t.Errorf("tier %d for need %d: %d, want %d", tc.tier, tc.need, got, tc.want)
		}
	}
	if got := ladder.Wear(1, 2, 0); got != 1 {
		t.Errorf("never below one: %d", got)
	}
}

func TestHomeYieldCarriesTheFraction(t *testing.T) {
	var carry, total int64
	for i := 0; i < 10; i++ {
		var made int64
		made, carry = HomeYield(3, 9000, carry)
		total += made
	}
	if total != 27 || carry != 0 {
		t.Errorf("ten batches of 3 at 90 percent make 27: %d, carry %d", total, carry)
	}
}
