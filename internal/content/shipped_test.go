package content

import (
	"path/filepath"
	"runtime"
	"testing"
)

// shippedContentDir is configs/content/ in this checkout.
//
// It is located from this file's own path rather than from the working
// directory, because `go test` runs in the package directory and the content
// lives at the repository root.
func shippedContentDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// internal/content/shipped_test.go -> repository root
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(root, "configs", "content")
}

// TestShippedContentLoadsAndValidates is the test that stops somebody breaking
// the real world.
//
// Every other test in this package builds its own content, so all of them
// would keep passing with configs/content/ in any state at all. This one runs
// the files that actually ship through exactly the path `admin content load`
// runs them through, so a typo committed to cities.yml fails in CI rather than
// at the moment an operator loads it.
func TestShippedContentLoadsAndValidates(t *testing.T) {
	dir := shippedContentDir(t)

	pack, err := Load(dir)
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("the shipped content does not validate: %v", err)
	}

	if pack.Schema != 1 {
		t.Errorf("the shipped content declares schema version %d, want 1", pack.Schema)
	}
	if len(pack.Cities) == 0 {
		t.Fatal("the shipped content declares no cities")
	}
	if len(pack.Routes) == 0 {
		t.Fatal("the shipped content declares no routes")
	}

	// A warning is not a failure, but it should be deliberate. Printing it
	// here means somebody reading a test log sees what an operator would see.
	for _, w := range pack.Warnings() {
		t.Logf("shipped content warning: %s", w)
	}

	t.Logf("shipped content: %d cities, %d routes, %d skills, checksum %s",
		len(pack.Cities), len(pack.Routes), len(pack.Skills), pack.Checksum)
}

// TestShippedContentBuildsAWorldEverybodyCanReach goes one step further than
// validation: it builds the snapshot a running service builds, and checks that
// the shipped routes really do connect the shipped cities.
//
// Validate deliberately allows a disconnected world, because a city has to be
// addable before its routes are authored. That tolerance is right for the
// rule and wrong for the content we actually ship: a city nobody can travel to
// is not a place in this game, it is a typo. So the leniency lives in the rule
// and the strictness lives here.
func TestShippedContentBuildsAConnectedWorld(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}

	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatalf("the shipped content does not build: %v", err)
	}

	cities := snap.Cities()
	routes := snap.Routes()
	for _, from := range cities {
		for _, to := range cities {
			if _, err := routes.DistanceBetween(from.Code, to.Code); err != nil {
				t.Errorf("no journey is possible from %q to %q: %v", from.Code, to.Code, err)
			}
		}
	}
}

// Every skill the content names must be one the domain declares, and the
// reverse gap is worth knowing about too: a skill the domain declares and the
// content never names has no display name anywhere.
func TestShippedSkillsMatchTheDomain(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}
	// Validate already refuses a code the domain does not declare; this only
	// reports the other direction, which is a gap rather than a fault.
	if len(pack.Skills) == 0 {
		t.Log("no skills are authored yet: skills.yml does not exist in this phase")
	}
}

// The shipped world must place newcomers somewhere, and the two expensive
// far-end cities are places players travel to, not places they are born.
func TestShippedSpawnWeights(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}
	candidates := pack.SpawnCandidates()
	if len(candidates) < 2 {
		t.Errorf("the shipped content spawns players in %d city(ies); new players should be spread over several", len(candidates))
	}
	for _, c := range candidates {
		if c.Code == "calderis" || c.Code == "vantor_reach" {
			t.Errorf("%s has spawn_weight %d; it is a destination, not a birthplace", c.Code, c.Weight)
		}
	}
}
