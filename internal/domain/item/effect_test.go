package item

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestApplyEffects(t *testing.T) {
	base := map[string]int64{"city.inspection_power": 40, "cargo.concealment": 10}
	tests := []struct {
		name    string
		effects []Effect
		want    map[string]int64
	}{
		{"none", nil, map[string]int64{"city.inspection_power": 40, "cargo.concealment": 10}},
		{"add", []Effect{{"city.inspection_power", EffectAdd, 25}},
			map[string]int64{"city.inspection_power": 65, "cargo.concealment": 10}},
		{"negative add", []Effect{{"cargo.concealment", EffectAdd, -15}},
			map[string]int64{"city.inspection_power": 40, "cargo.concealment": -5}},
		{"multiply", []Effect{{"city.inspection_power", EffectMultiply, 15_000}},
			map[string]int64{"city.inspection_power": 60, "cargo.concealment": 10}},
		{"cap", []Effect{{"city.inspection_power", EffectCap, 30}},
			map[string]int64{"city.inspection_power": 30, "cargo.concealment": 10}},
		{"cap above the value does nothing", []Effect{{"city.inspection_power", EffectCap, 90}},
			map[string]int64{"city.inspection_power": 40, "cargo.concealment": 10}},
		{"lowest cap wins", []Effect{{"cargo.concealment", EffectCap, 8}, {"cargo.concealment", EffectCap, 5}},
			map[string]int64{"city.inspection_power": 40, "cargo.concealment": 5}},
		{"missing target starts at zero", []Effect{{"player.healing", EffectAdd, 7}},
			map[string]int64{"city.inspection_power": 40, "cargo.concealment": 10, "player.healing": 7}},
		// adds, then multiplies, then caps: (40 + 20) × 1.5 = 90, capped at 75.
		{"all three", []Effect{
			{"city.inspection_power", EffectCap, 75},
			{"city.inspection_power", EffectMultiply, 15_000},
			{"city.inspection_power", EffectAdd, 20},
		}, map[string]int64{"city.inspection_power": 75, "cargo.concealment": 10}},
		// Two ×1.5 multiplies on 5: exact is 11.25, truncated once to 11,
		// where step-by-step truncation would give 7 then 10.
		{"multiplies combine exactly", []Effect{
			{"cargo.concealment", EffectAdd, -5},
			{"cargo.concealment", EffectMultiply, 15_000},
			{"cargo.concealment", EffectMultiply, 15_000},
			{"cargo.concealment", EffectAdd, 5},
		}, map[string]int64{"city.inspection_power": 40, "cargo.concealment": 22}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyEffects(base, tc.effects)
			mustNil(t, err)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	if base["city.inspection_power"] != 40 || len(base) != 2 {
		t.Error("ApplyEffects modified its input")
	}
}

// TestApplyEffectsOrderIndependent: equipping items in a different order
// never changes the result, and a cap always holds.
func TestApplyEffectsOrderIndependent(t *testing.T) {
	effects := []Effect{
		{"v", EffectAdd, 13},
		{"v", EffectMultiply, 12_500},
		{"v", EffectCap, 40},
		{"v", EffectMultiply, 13_333},
		{"v", EffectAdd, 7},
	}
	base := map[string]int64{"v": 11}
	want, err := ApplyEffects(base, effects)
	mustNil(t, err)
	perm := []int{0, 1, 2, 3, 4}
	var permute func(int)
	permute = func(k int) {
		if k == len(perm) {
			ordered := make([]Effect, len(perm))
			for i, p := range perm {
				ordered[i] = effects[p]
			}
			got, err := ApplyEffects(base, ordered)
			mustNil(t, err)
			if got["v"] != want["v"] {
				t.Errorf("order %v: got %d, want %d", perm, got["v"], want["v"])
			}
			return
		}
		for i := k; i < len(perm); i++ {
			perm[k], perm[i] = perm[i], perm[k]
			permute(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	permute(0)
	if want["v"] != 40 {
		t.Errorf("cap did not hold: %d", want["v"])
	}
}

func TestApplyEffectsRejects(t *testing.T) {
	tests := []struct {
		name string
		e    Effect
		want error
	}{
		{"unknown op", Effect{"v", "divide", 2}, ErrUnknownEffectOp},
		{"no target", Effect{"", EffectAdd, 1}, ErrInvalidEffect},
		{"negative multiply", Effect{"v", EffectMultiply, -1}, ErrInvalidEffect},
	}
	for _, tc := range tests {
		if _, err := ApplyEffects(nil, []Effect{tc.e}); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := ApplyEffects(map[string]int64{"v": math.MaxInt64}, []Effect{{"v", EffectAdd, 1}}); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflowing add: got %v", err)
	}
	if _, err := ApplyEffects(map[string]int64{"v": math.MaxInt64}, []Effect{{"v", EffectMultiply, 20_000}}); !errors.Is(err, ErrOverflow) {
		t.Errorf("overflowing multiply: got %v", err)
	}
	// An overflow a cap brings back in range is not an error: the value
	// that results fits.
	got, err := ApplyEffects(map[string]int64{"v": math.MaxInt64}, []Effect{{"v", EffectAdd, 1}, {"v", EffectCap, 100}})
	mustNil(t, err)
	if got["v"] != 100 {
		t.Errorf("capped: got %d", got["v"])
	}
}
