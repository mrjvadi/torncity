package content

import (
	"strings"
	"testing"
)

// growthLintPack is a complete pack held to the G0 rule (ADR 0044): it declares
// the grandfather list, so a tag gated only by a stage is refused.
func growthLintPack(legacy ...string) *Pack {
	p := availPack()
	p.GrowthLint = true
	p.LegacyStageOnly = legacy
	return p
}

var growthBaseLegacy = []string{"building/road", "building/civic_hall", "knowledge/oral_tradition"}

// openBase marks the skill and component tags tagBase makes as open.
func openBase(p *Pack) {
	for i := range p.Availability {
		if a := &p.Availability[i]; a.Kind == "skill" || a.Kind == "component" {
			a.Growth = &AvailabilityGrowth{Open: true}
		}
	}
}

func TestGrowthLintRefusesANewTagGatedOnlyByAStage(t *testing.T) {
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageVillage, Requires: &AvailabilityNeeds{}})
	openBase(p)
	got := availProblems(p)
	if !strings.Contains(got, "course/bread_school: gated only by the stage") {
		t.Fatalf("a stage-only new course was accepted: %q", got)
	}
	if strings.Contains(got, "skill/cooking: gated only") {
		t.Fatalf("an open tag was refused: %q", got)
	}
}

func TestGrowthLintAcceptsGatesAndOpen(t *testing.T) {
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p)
	openBase(p)
	// no stage at all, gated by what the settlement has
	p.Availability = append(p.Availability, AvailabilityDef{Kind: "course", Code: "bread_school",
		Requires: &AvailabilityNeeds{Buildings: []AvailabilityBuilding{{Code: "road"}}}, Elsewhere: []AvailabilityElsewhere{{Where: "support"}}})
	if got := availProblems(p); got != "" {
		t.Fatalf("gated and open tags were refused: %v", got)
	}
}

func TestGrowthLintRefusesNoStageAndNoGate(t *testing.T) {
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Requires: &AvailabilityNeeds{}})
	openBase(p)
	if got := availProblems(p); !strings.Contains(got, "no stage and no real gate") {
		t.Fatalf("a tag with neither stage nor gate was accepted: %q", got)
	}
}

// TestGrowthLintOnlyFoundingRowsMayDropTheirStageWithoutAGate: dropping the stage
// of a row with an empty requires opens it to everyone, so only an
// open-from-founding row (Appendix A class A) may do it; a bare open row, a
// deferral and an empty requires may not.
func TestGrowthLintOnlyFoundingRowsMayDropTheirStageWithoutAGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		growth *AvailabilityGrowth
		ok     bool
	}{
		{"founding open row", &AvailabilityGrowth{Open: true, Founding: true}, true},
		{"bare open row", &AvailabilityGrowth{Open: true}, false},
		{"deferred row", &AvailabilityGrowth{Deferred: GrowthDeferredCharter}, false},
		{"no growth block", nil, false},
		{"real gate", &AvailabilityGrowth{Requires: &AvailabilityNeeds{Buildings: []AvailabilityBuilding{{Code: "road"}}}}, true},
	} {
		p := growthLintPack(growthBaseLegacy...)
		tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Requires: &AvailabilityNeeds{}, Growth: tc.growth,
			Elsewhere: []AvailabilityElsewhere{{Where: "support"}}})
		openBase(p)
		got := availProblems(p)
		if tc.ok && got != "" {
			t.Errorf("%s: refused: %v", tc.name, got)
		}
		if !tc.ok && !strings.Contains(got, "no stage and no real gate") {
			t.Errorf("%s: a stage-less row with nothing gating it was accepted: %q", tc.name, got)
		}
	}
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport, Requires: &AvailabilityNeeds{},
		Growth: &AvailabilityGrowth{Founding: true, Deferred: GrowthDeferredCharter}})
	openBase(p)
	if got := availProblems(p); !strings.Contains(got, "growth.founding only goes with growth.open") {
		t.Fatalf("founding without open was accepted: %q", got)
	}
}

func TestGrowthLintLegacyListOnlyShrinks(t *testing.T) {
	p := growthLintPack(append([]string{"course/bread_school", "course/ghost"}, growthBaseLegacy...)...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageVillage,
		Requires: &AvailabilityNeeds{Buildings: []AvailabilityBuilding{{Code: "road"}}}, Elsewhere: []AvailabilityElsewhere{{Where: "support"}}})
	openBase(p)
	got := availProblems(p)
	if !strings.Contains(got, "course/bread_school is gated now") || !strings.Contains(got, "course/ghost is not a tagged entry") {
		t.Fatalf("a stale legacy entry was accepted: %q", got)
	}
}

func TestGrowthLintChecksTheShapeOfAGrowthBlock(t *testing.T) {
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport,
		Requires: &AvailabilityNeeds{}, Growth: &AvailabilityGrowth{Open: true, Deferred: GrowthDeferredCharter}})
	openBase(p)
	if got := availProblems(p); !strings.Contains(got, "exactly one of open, requires, deferred") {
		t.Fatalf("a growth block with two parts was accepted: %q", got)
	}
	p = growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport,
		Requires: &AvailabilityNeeds{}, Growth: &AvailabilityGrowth{Deferred: "someday"}})
	openBase(p)
	if got := availProblems(p); !strings.Contains(got, "growth.deferred") {
		t.Fatalf("an unknown deferral was accepted: %q", got)
	}
}

func TestGrowthRequiresMustResolveAndBeReachable(t *testing.T) {
	p := growthLintPack(growthBaseLegacy...)
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport, Requires: &AvailabilityNeeds{},
		Growth: &AvailabilityGrowth{Requires: &AvailabilityNeeds{Buildings: []AvailabilityBuilding{{Code: "no_such_building"}}}}})
	openBase(p)
	if got := availProblems(p); !strings.Contains(got, "building \"no_such_building\" does not exist") {
		t.Fatalf("an unresolved growth gate was accepted: %q", got)
	}
}

// TestShippedLegacyStageOnlyOnlyShrinks pins the grandfather list: new content
// can never be added to it, so it holds at most what G0 started with.
func TestShippedLegacyStageOnlyOnlyShrinks(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	const g0 = 4 // bank, barracks, port, airport at ADR 0044 phase G0
	if !pack.GrowthLint {
		t.Fatal("availability.yml must declare legacy_stage_only so the growth lint is on")
	}
	// Appendix A class A is 123 rows; only open-from-founding ones may carry growth.founding.
	founding := 0
	for _, a := range pack.Availability {
		if a.Growth != nil && a.Growth.Founding {
			founding++
		}
	}
	if founding > 123 {
		t.Fatalf("%d rows are open from founding; ADR 0044 Appendix A class A has 123", founding)
	}
	if len(pack.LegacyStageOnly) > g0 {
		t.Fatalf("legacy_stage_only grew to %d entries (G0 started with %d): gate the new content instead", len(pack.LegacyStageOnly), g0)
	}
}
