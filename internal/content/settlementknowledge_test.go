package content

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
)

// The shipped catalogue's own shape: how many items, split founding-only
// (mode_eligible false) versus offered for research/purchase, and grouped by
// the capability they Provide — a quick regression signal if a future edit
// silently drops a branch.
func TestShippedSettlementKnowledgeCatalogueShape(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	defs := pack.SettlementKnowledge
	if len(defs) < 30 {
		t.Fatalf("only %d settlement knowledge items shipped, want at least 30", len(defs))
	}

	var foundingOnly, acquirable int
	byCapability := map[string]int{}
	for _, d := range defs {
		if d.IsModeEligible() {
			acquirable++
		} else {
			foundingOnly++
		}
		provides := d.Provides
		if len(provides) == 0 {
			provides = []string{d.Code}
		}
		for _, p := range provides {
			byCapability[p]++
		}
	}
	if foundingOnly != 4 {
		t.Errorf("%d founding-only items, want the 4 universal baselines (ADR 0031 section 4.3)", foundingOnly)
	}
	for _, want := range []string{"arable_farming", "pastoral_husbandry", "aquatic_harvest",
		"local_security", "skilled_labor", "market_access"} {
		if byCapability[want] < 1 {
			t.Errorf("capability %q has no provider", want)
		}
	}
	t.Logf("settlement knowledge: %d items (%d founding-only, %d acquirable), capabilities: %v",
		len(defs), foundingOnly, acquirable, byCapability)
}

// A settlement founded in a desert cell would hold shaft_irrigation, not
// canal_irrigation — both branches must exist and both must validate as
// terrain-required, mutually exclusive by terrain, coexistable by ownership
// (ADR 0031 section 10 point 1).
func TestShippedSettlementKnowledgeRivalBranchesAreTerrainGated(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tree := make(settlementknowledge.Tree, len(pack.SettlementKnowledge))
	for _, d := range pack.SettlementKnowledge {
		tree[d.Code] = d.Tech()
	}
	canal, shaft := tree["canal_irrigation"], tree["shaft_irrigation"]
	if canal.TerrainMode != settlementknowledge.TerrainRequired || shaft.TerrainMode != settlementknowledge.TerrainRequired {
		t.Fatal("canal_irrigation and shaft_irrigation must both be terrain_mode: required")
	}
	s := settlementknowledge.Standing{Owned: map[string]struct{}{"canal_irrigation": {}}, TerrainTags: []string{"desert"}}
	if err := settlementknowledge.CanAcquire(shaft, tree, s, true); err != nil {
		t.Errorf("a settlement holding canal_irrigation should still be able to acquire shaft_irrigation in a desert: %v", err)
	}
}

// The four founding-only grants must be exactly the ones ADR 0031 section
// 4.3 names, and must be free.
func TestShippedFoundingGrantsAreTheUniversalFour(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]bool{"oral_tradition": true, "communal_watch": true, "kin_apprenticeship": true, "barter_ring": true}
	got := map[string]bool{}
	for _, d := range pack.SettlementKnowledge {
		if !d.IsModeEligible() {
			got[d.Code] = true
			if d.Cost != 0 {
				t.Errorf("founding-only item %q has a nonzero cost %d", d.Code, d.Cost)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("founding-only items = %v, want %v", got, want)
	}
	for code := range want {
		if !got[code] {
			t.Errorf("%q is not a founding-only item", code)
		}
	}
}

// A knowledge item naming an unknown skill is refused at load, exactly like
// technology's own equivalent check.
func TestSettlementKnowledgeUnknownSkillRefused(t *testing.T) {
	p := &Pack{
		Skills: []SkillDef{{Code: "leadership", Name: "Leadership"}},
		SettlementKnowledge: []SettlementKnowledgeDef{
			{Code: "x", Name: "X", Cost: 1, Time: "1h", Skill: "ghost", ModeEligible: boolPtr(true)},
		},
	}
	err := p.Validate()
	if !errors.Is(err, ErrInvalidSettlementKnowledgeContent) {
		t.Fatalf("err = %v, want ErrInvalidSettlementKnowledgeContent", err)
	}
}

func boolPtr(b bool) *bool { return &b }
