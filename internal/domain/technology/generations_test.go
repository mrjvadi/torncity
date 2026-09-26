package technology

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// radarFamily is a three-generation family: radar_systems (gen 1, the
// component's gate), radar_systems_ii and radar_systems_iii, each boosting
// detection_range by a shrinking amount and each requiring the generation
// before it — the shape every leveled technology in content follows.
func radarFamily() []Tech {
	return []Tech{
		{Code: "radar_systems", Family: "radar_systems", Generation: 1,
			Cost: 100, Time: time.Hour, CompanyTypes: []string{"factory"}},
		{Code: "radar_systems_ii", Family: "radar_systems", Generation: 2, Requires: []string{"radar_systems"},
			Cost: 200, Time: time.Hour, CompanyTypes: []string{"factory"},
			Effects: []item.Effect{{Target: "detection_range", Op: item.EffectMultiply, Value: item.BPS + 1500}}},
		{Code: "radar_systems_iii", Family: "radar_systems", Generation: 3, Requires: []string{"radar_systems_ii"},
			Cost: 300, Time: time.Hour, CompanyTypes: []string{"factory"},
			Effects: []item.Effect{{Target: "detection_range", Op: item.EffectMultiply, Value: item.BPS + 800}}},
	}
}

func TestValidateTreeGenerations(t *testing.T) {
	if err := ValidateTree(radarFamily(), vocab); err != nil {
		t.Fatalf("the sample family: %v", err)
	}

	bad := []struct {
		name  string
		techs []Tech
		want  error
	}{
		{"generation without family", []Tech{
			{Code: "a", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"family without generation", []Tech{
			{Code: "a", Family: "f", Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"generation above max", []Tech{
			{Code: "a", Family: "f", Generation: MaxGeneration + 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"gap in generations", []Tech{
			{Code: "a", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "b", Family: "f", Generation: 3, Requires: []string{"a"}, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"duplicate generation", []Tech{
			{Code: "a", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "b", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"generation 2 does not require generation 1", []Tech{
			{Code: "a", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "b", Family: "f", Generation: 2, Time: time.Hour, CompanyTypes: []string{"factory"}},
		}, ErrInvalidGeneration},
		{"effect grows instead of diminishing", []Tech{
			{Code: "a", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"}},
			{Code: "b", Family: "f", Generation: 2, Requires: []string{"a"}, Time: time.Hour, CompanyTypes: []string{"factory"},
				Effects: []item.Effect{{Target: "range", Op: item.EffectMultiply, Value: item.BPS + 1000}}},
			{Code: "c", Family: "f", Generation: 3, Requires: []string{"b"}, Time: time.Hour, CompanyTypes: []string{"factory"},
				Effects: []item.Effect{{Target: "range", Op: item.EffectMultiply, Value: item.BPS + 1500}}},
		}, ErrInvalidGeneration},
		{"cumulative effect exceeds the family cap", []Tech{
			{Code: "a", Family: "f", Generation: 1, Time: time.Hour, CompanyTypes: []string{"factory"},
				Effects: []item.Effect{{Target: "range", Op: item.EffectMultiply, Value: item.BPS + 5000}}},
			{Code: "b", Family: "f", Generation: 2, Requires: []string{"a"}, Time: time.Hour, CompanyTypes: []string{"factory"},
				Effects: []item.Effect{{Target: "range", Op: item.EffectMultiply, Value: item.BPS + 4000}}},
		}, ErrInvalidGeneration},
	}
	for _, tt := range bad {
		if err := ValidateTree(tt.techs, vocab); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}
}

func radarComponents() item.Components {
	return item.Components{
		"fire_control_radar": {Code: "fire_control_radar", Category: "radar_set",
			RequiresTechnology: []string{"radar_systems"},
			Attributes:         map[string]int64{"detection_range": 110}},
	}
}

func radarDesign() item.Design {
	return item.Design{ID: "radar-1", Archetype: "radar", Origin: item.OriginAuthored,
		Fills: map[string]item.Fill{"set": {Component: "fire_control_radar", Quantity: 1}}}
}

func TestEffectsFor(t *testing.T) {
	tree := Tree{}
	for _, tc := range radarFamily() {
		tree[tc.Code] = tc
	}
	d := radarDesign()
	components := radarComponents()

	t.Run("company with only generation 1 gets no boost", func(t *testing.T) {
		s := Standing{Owned: item.NewSet("radar_systems")}
		effs := EffectsFor(d, components, tree, s)
		if len(effs) != 0 {
			t.Fatalf("effects = %v, want none", effs)
		}
	})

	t.Run("company with generation 3 gets both generations' effects", func(t *testing.T) {
		s := Standing{Owned: item.NewSet("radar_systems", "radar_systems_ii", "radar_systems_iii")}
		effs := EffectsFor(d, components, tree, s)
		if len(effs) != 2 {
			t.Fatalf("effects = %v, want 2", effs)
		}
		attrs, err := item.ApplyEffects(map[string]int64{"detection_range": 110}, effs)
		if err != nil {
			t.Fatalf("ApplyEffects: %v", err)
		}
		// 110 × 1.15 × 1.08 = 136 (truncated).
		if attrs["detection_range"] != 136 {
			t.Fatalf("detection_range = %d, want 136", attrs["detection_range"])
		}
	})

	t.Run("unrelated design gets nothing even from an owned family", func(t *testing.T) {
		s := Standing{Owned: item.NewSet("radar_systems", "radar_systems_ii", "radar_systems_iii")}
		other := item.Design{ID: "other", Archetype: "phone", Fills: map[string]item.Fill{
			"cpu": {Component: "cpu_x", Quantity: 1},
		}}
		otherComponents := item.Components{"cpu_x": {Code: "cpu_x", Category: "processor"}}
		if effs := EffectsFor(other, otherComponents, tree, s); len(effs) != 0 {
			t.Fatalf("effects = %v, want none: no gating technology in this design's fills", effs)
		}
	})
}

func TestLevelOf(t *testing.T) {
	tree := Tree{}
	for _, tc := range radarFamily() {
		tree[tc.Code] = tc
	}
	s := Standing{Owned: item.NewSet("radar_systems", "radar_systems_ii")}
	if got := s.LevelOf(tree, "radar_systems"); got != 2 {
		t.Fatalf("LevelOf = %d, want 2", got)
	}
	if got := s.LevelOf(tree, "unknown_family"); got != 0 {
		t.Fatalf("LevelOf of unknown family = %d, want 0", got)
	}
}

// A gate's prerequisites count: a design whose part is gated by a
// technology built on another family's process is made better by that
// process's generations, though the process gates no part itself.
func TestEffectsForCountsPrerequisites(t *testing.T) {
	tree := Tree{
		"process": {Code: "process", Family: "process", Generation: 1},
		"process_ii": {Code: "process_ii", Family: "process", Generation: 2, Requires: []string{"process"},
			Effects: []item.Effect{{Target: "quality", Op: item.EffectMultiply, Value: 11000}}},
		"chips": {Code: "chips", Requires: []string{"process"}},
	}
	d := item.Design{ID: "phone", Archetype: "phone", Fills: map[string]item.Fill{"board": {Component: "chipset", Quantity: 1}}}
	components := item.Components{"chipset": {Code: "chipset", Category: "circuit", RequiresTechnology: []string{"chips"}}}

	if effs := EffectsFor(d, components, tree, Standing{Owned: item.NewSet("process", "chips")}); len(effs) != 0 {
		t.Fatalf("generation 1 of the process: effects = %v, want none", effs)
	}
	effs := EffectsFor(d, components, tree, Standing{Owned: item.NewSet("process", "process_ii", "chips")})
	if len(effs) != 1 || effs[0].Value != 11000 {
		t.Fatalf("generation 2 of the process: effects = %v, want its one effect", effs)
	}
}
