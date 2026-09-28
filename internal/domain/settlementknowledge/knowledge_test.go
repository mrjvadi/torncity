package settlementknowledge

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

func vocab() Vocabulary {
	return Vocabulary{Skills: item.NewSet("leadership", "engineering")}
}

// A small, realistic tree: two branches of one capability, a linear item
// gated on a capability, and a two-generation leveled family — enough to
// exercise every rule ValidateTree checks.
func sampleTree() []Tech {
	return []Tech{
		{Code: "oral_tradition", Provides: []string{"literacy_spread"}, Cost: 0, Time: 0, ModeEligible: false},
		{Code: "basic_literacy", Requires: []string{"oral_tradition"}, Provides: []string{"literacy_spread", "record_keeping_gate"},
			Cost: 4000, Time: 48 * time.Hour, ModeEligible: true},
		{Code: "record_keeping", RequiresCapability: []string{"literacy_spread"}, Provides: []string{"record_keeping"},
			Cost: 6000, Time: 72 * time.Hour, ModeEligible: true},
		{Code: "canal_irrigation", TerrainTags: []string{"river_lot"}, TerrainMode: TerrainRequired,
			Provides: []string{"arable_farming"}, Cost: 3000, Time: 24 * time.Hour, ModeEligible: true},
		{Code: "shaft_irrigation", TerrainTags: []string{"desert"}, TerrainMode: TerrainRequired,
			Provides: []string{"arable_farming"}, Cost: 5000, Time: 48 * time.Hour, ModeEligible: true},
		{Code: "mounted_militia", TerrainTags: []string{"temperate_grassland"}, TerrainMode: TerrainPreferred,
			Provides: []string{"local_security"}, Cost: 2500, Time: 18 * time.Hour, ModeEligible: true},
		{Code: "carpentry", Family: "carpentry", Generation: 1, Cost: 1000, Time: 12 * time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "craft_workshop_output_bps", Op: item.EffectAdd, Value: 500}}},
		{Code: "carpentry_ii", Family: "carpentry", Generation: 2, Requires: []string{"carpentry"}, Cost: 3000, Time: 24 * time.Hour,
			ModeEligible: true, Effects: []item.Effect{{Target: "craft_workshop_output_bps", Op: item.EffectAdd, Value: 400}}},
	}
}

func TestValidateTreeAcceptsTheSampleTree(t *testing.T) {
	if err := ValidateTree(sampleTree(), vocab()); err != nil {
		t.Fatalf("a well-formed tree was refused: %v", err)
	}
}

func TestValidateTreeUnknownPrerequisite(t *testing.T) {
	techs := []Tech{{Code: "a", Requires: []string{"ghost"}, Cost: 1, Time: time.Hour, ModeEligible: true}}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrUnknownPrerequisite) {
		t.Fatalf("err = %v, want ErrUnknownPrerequisite", err)
	}
}

func TestValidateTreeCycle(t *testing.T) {
	techs := []Tech{
		{Code: "a", Requires: []string{"b"}, Cost: 1, Time: time.Hour, ModeEligible: true},
		{Code: "b", Requires: []string{"a"}, Cost: 1, Time: time.Hour, ModeEligible: true},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
}

func TestValidateTreeSelfRequireIsACycle(t *testing.T) {
	techs := []Tech{{Code: "a", Requires: []string{"a"}, Cost: 1, Time: time.Hour, ModeEligible: true}}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
}

// The branching mechanism's own rule: a RequiresCapability tag nothing
// Provides is refused at load, the same class of error as an unknown
// prerequisite (ADR 0031 section 5.2).
func TestValidateTreeUnreachableCapability(t *testing.T) {
	techs := []Tech{
		{Code: "state_school", RequiresCapability: []string{"a_capability_nothing_provides"},
			Cost: 1, Time: time.Hour, ModeEligible: true},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrUnreachableCapability) {
		t.Fatalf("err = %v, want ErrUnreachableCapability", err)
	}
}

// A capability provided by ANY one item in the tree satisfies a requirement
// naming it — the whole point of the branching mechanism.
func TestValidateTreeCapabilityProvidedByEitherBranchIsReachable(t *testing.T) {
	techs := []Tech{
		{Code: "canal_irrigation", TerrainTags: []string{"river_lot"}, TerrainMode: TerrainRequired,
			Provides: []string{"arable_farming"}, Cost: 1, Time: time.Hour, ModeEligible: true},
		{Code: "shaft_irrigation", TerrainTags: []string{"desert"}, TerrainMode: TerrainRequired,
			Provides: []string{"arable_farming"}, Cost: 1, Time: time.Hour, ModeEligible: true},
		{Code: "downstream", RequiresCapability: []string{"arable_farming"}, Cost: 1, Time: time.Hour, ModeEligible: true},
	}
	if err := ValidateTree(techs, vocab()); err != nil {
		t.Fatalf("a reachable capability was refused: %v", err)
	}
}

func TestValidateTreeTerrainModeConsistency(t *testing.T) {
	// Tags without a mode.
	err := ValidateTree([]Tech{{Code: "a", TerrainTags: []string{"desert"}, Cost: 1, Time: time.Hour, ModeEligible: true}}, vocab())
	if !errors.Is(err, ErrInvalidKnowledge) {
		t.Errorf("tags with no mode: err = %v, want ErrInvalidKnowledge", err)
	}
	// Mode without tags.
	err = ValidateTree([]Tech{{Code: "a", TerrainMode: TerrainRequired, Cost: 1, Time: time.Hour, ModeEligible: true}}, vocab())
	if !errors.Is(err, ErrInvalidKnowledge) {
		t.Errorf("mode with no tags: err = %v, want ErrInvalidKnowledge", err)
	}
	// An unknown mode string.
	err = ValidateTree([]Tech{{Code: "a", TerrainTags: []string{"desert"}, TerrainMode: "sometimes", Cost: 1, Time: time.Hour, ModeEligible: true}}, vocab())
	if !errors.Is(err, ErrInvalidKnowledge) {
		t.Errorf("bad mode: err = %v, want ErrInvalidKnowledge", err)
	}
}

// A founding-only grant (ModeEligible false) may declare zero cost and zero
// time; anything mode-eligible may not.
func TestValidateTreeFoundingGrantMayBeFree(t *testing.T) {
	techs := []Tech{{Code: "oral_tradition", Cost: 0, Time: 0, ModeEligible: false}}
	if err := ValidateTree(techs, vocab()); err != nil {
		t.Fatalf("a free founding grant was refused: %v", err)
	}
	techs = []Tech{{Code: "oral_tradition", Cost: 0, Time: 0, ModeEligible: true}}
	if err := ValidateTree(techs, vocab()); !errors.Is(err, ErrInvalidKnowledge) {
		t.Fatalf("a mode-eligible item with zero time was accepted: %v", err)
	}
}

// The Family/Generation rules are copied from technology.go verbatim; these
// exercise the same three failure shapes technology_test.go does, so a
// future edit to one package's copy that silently drifts from the other is
// caught by both suites independently.
func TestValidateTreeGenerationMustRequirePrevious(t *testing.T) {
	techs := []Tech{
		{Code: "carpentry", Family: "carpentry", Generation: 1, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 500}}},
		{Code: "carpentry_ii", Family: "carpentry", Generation: 2, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 300}}},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("err = %v, want ErrInvalidGeneration", err)
	}
}

func TestValidateTreeGenerationMustDiminish(t *testing.T) {
	techs := []Tech{
		{Code: "carpentry", Family: "carpentry", Generation: 1, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 300}}},
		{Code: "carpentry_ii", Family: "carpentry", Generation: 2, Requires: []string{"carpentry"}, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 500}}},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("err = %v, want ErrInvalidGeneration", err)
	}
}

func TestValidateTreeCumulativeCap(t *testing.T) {
	techs := []Tech{
		{Code: "g1", Family: "f", Generation: 1, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 5000}}},
		{Code: "g2", Family: "f", Generation: 2, Requires: []string{"g1"}, Cost: 1, Time: time.Hour, ModeEligible: true,
			Effects: []item.Effect{{Target: "x", Op: item.EffectAdd, Value: 4000}}},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("err = %v, want ErrInvalidGeneration (cumulative cap)", err)
	}
}

func TestValidateTreeDuplicateCode(t *testing.T) {
	techs := []Tech{
		{Code: "a", Cost: 1, Time: time.Hour, ModeEligible: true},
		{Code: "a", Cost: 1, Time: time.Hour, ModeEligible: true},
	}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrInvalidKnowledge) {
		t.Fatalf("err = %v, want ErrInvalidKnowledge", err)
	}
}

func TestValidateTreeUnknownSkill(t *testing.T) {
	techs := []Tech{{Code: "a", Cost: 1, Time: time.Hour, ModeEligible: true, Skill: "ghost_skill", Level: 1}}
	err := ValidateTree(techs, vocab())
	if !errors.Is(err, ErrInvalidKnowledge) {
		t.Fatalf("err = %v, want ErrInvalidKnowledge", err)
	}
}

// --- CanAcquire / Standing ---------------------------------------------

func treeOf(t *testing.T, techs []Tech) Tree {
	t.Helper()
	if err := ValidateTree(techs, vocab()); err != nil {
		t.Fatalf("test tree is invalid: %v", err)
	}
	out := make(Tree, len(techs))
	for _, tech := range techs {
		out[tech.Code] = tech
	}
	return out
}

func TestCanAcquireAlreadyOwned(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.NewSet("oral_tradition")}
	err := CanAcquire(tree["oral_tradition"], tree, s, true)
	if !errors.Is(err, ErrAlreadyOwned) {
		t.Fatalf("err = %v, want ErrAlreadyOwned", err)
	}
}

func TestCanAcquireNotModeEligible(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.Set{}}
	err := CanAcquire(tree["oral_tradition"], tree, s, true)
	if !errors.Is(err, ErrNotModeEligible) {
		t.Fatalf("err = %v, want ErrNotModeEligible", err)
	}
}

func TestCanAcquireTerrainRequired(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.Set{}, TerrainTags: []string{"desert"}}
	if err := CanAcquire(tree["canal_irrigation"], tree, s, true); !errors.Is(err, ErrTerrainRequired) {
		t.Fatalf("canal in a desert: err = %v, want ErrTerrainRequired", err)
	}
	if err := CanAcquire(tree["shaft_irrigation"], tree, s, true); err != nil {
		t.Fatalf("shaft in a desert should be acquirable: %v", err)
	}
}

func TestCanAcquireMissingPrerequisite(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.Set{}}
	err := CanAcquire(tree["basic_literacy"], tree, s, true)
	if !errors.Is(err, ErrPrerequisiteMissing) {
		t.Fatalf("err = %v, want ErrPrerequisiteMissing", err)
	}
}

// record_keeping requires the CAPABILITY literacy_spread, satisfied by
// EITHER oral_tradition or basic_literacy — holding either is enough.
func TestCanAcquireCapabilitySatisfiedByEitherHeldItem(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.NewSet("oral_tradition")}
	if err := CanAcquire(tree["record_keeping"], tree, s, true); err != nil {
		t.Fatalf("oral_tradition alone should satisfy literacy_spread: %v", err)
	}
}

func TestCanAcquireSkillTooLow(t *testing.T) {
	techs := append(sampleTree(), Tech{Code: "commander", Requires: []string{"record_keeping"},
		Skill: "leadership", Level: 3, Cost: 1000, Time: time.Hour, ModeEligible: true})
	tree := treeOf(t, techs)
	s := Standing{Owned: item.NewSet("oral_tradition", "basic_literacy", "record_keeping"),
		SkillLevel: func(string) int { return 1 }}
	err := CanAcquire(tree["commander"], tree, s, true)
	if !errors.Is(err, ErrSkillTooLow) {
		t.Fatalf("err = %v, want ErrSkillTooLow", err)
	}
	s.SkillLevel = func(string) int { return 5 }
	if err := CanAcquire(tree["commander"], tree, s, true); err != nil {
		t.Fatalf("a sufficient skill should pass: %v", err)
	}
}

func TestCanAcquireBusyOnlyBlocksResearch(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.NewSet("oral_tradition"), Researching: true}
	if err := CanAcquire(tree["record_keeping"], tree, s, true); !errors.Is(err, ErrBusy) {
		t.Fatalf("research while busy: err = %v, want ErrBusy", err)
	}
	if err := CanAcquire(tree["record_keeping"], tree, s, false); err != nil {
		t.Fatalf("a purchase should not be blocked by a running research: %v", err)
	}
}

// Rival branches: a settlement holding canal_irrigation may still acquire
// shaft_irrigation later (ADR 0031 section 10 point 1 — no first-branch
// lock). Nothing in CanAcquire refuses it; only ErrAlreadyOwned would, and
// the two are different codes.
func TestRivalBranchesMayBothBeAcquired(t *testing.T) {
	tree := treeOf(t, sampleTree())
	s := Standing{Owned: item.NewSet("canal_irrigation"), TerrainTags: []string{"desert"}}
	if err := CanAcquire(tree["shaft_irrigation"], tree, s, true); err != nil {
		t.Fatalf("a rival branch should still be acquirable: %v", err)
	}
}

func TestDiscountedOnlyForPreferredTerrainMatch(t *testing.T) {
	tree := treeOf(t, sampleTree())
	inGrassland := Standing{TerrainTags: []string{"temperate_grassland"}}
	if !inGrassland.Discounted(tree["mounted_militia"]) {
		t.Error("a preferred-terrain match should be discounted")
	}
	elsewhere := Standing{TerrainTags: []string{"desert"}}
	if elsewhere.Discounted(tree["mounted_militia"]) {
		t.Error("no match should not be discounted")
	}
	// TerrainRequired is eligibility, never a "discount".
	desertMatch := Standing{TerrainTags: []string{"desert"}}
	if desertMatch.Discounted(tree["shaft_irrigation"]) {
		t.Error("a required match is eligibility, not a discount")
	}
}
