//go:build integration

package tests

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/content/testworld"
)

// staticContentSource is the handlers.ContentSource every K2/W5 handler
// needs (Current() *content.Snapshot), for a test that has no live content
// registry (internal/infrastructure/postgres.watchContent, cmd/game's own)
// to hot-reload from — the shipped content, loaded once, is enough for an
// integration test that only exercises the transactional wiring.
type staticContentSource struct{ snap *content.Snapshot }

func (s staticContentSource) Current() *content.Snapshot { return s.snap }

// loadTestContent loads and validates the shipped content
// (configs/content/) and builds a snapshot from it, the identical pipeline
// `admin content load` runs, for a test that needs a real
// handlers.ContentSource rather than a fake.
// landBoost makes loadTestContent fill the land with trees and rocks (a test of the land model sets it before it builds its
// environment and clears it in a cleanup).
var landBoost bool

func loadTestContent(t *testing.T) *content.Snapshot {
	t.Helper()
	return loadTestContentOpen(t)
}

// loadTestContentOpen is loadTestContent with the named buildings offered although they wait for their mechanic
// (waits_for, ADR 0063): a fixture for the tests of placement rules that need a building of a given shape.
func loadTestContentOpen(t *testing.T, open ...string) *content.Snapshot {
	t.Helper()
	pack, err := content.Load("../configs/content")
	if err != nil {
		t.Fatalf("loading content: %v", err)
	}
	// The shipped world has one city; the integration database carries the
	// multi-city test world (main_integration_test.go), so the snapshot the
	// tests build must match it.
	for i := range pack.SettlementBuildings {
		for _, code := range open {
			if pack.SettlementBuildings[i].Code == code {
				pack.SettlementBuildings[i].WaitsFor = ""
			}
		}
	}
	if landBoost && len(pack.Land) > 0 {
		// every lot of every biome carries all it can, so a test finds trees and rocks wherever the test world puts its village
		for i := range pack.Land[0].Biomes {
			pack.Land[0].Biomes[i].TreeBPS, pack.Land[0].Biomes[i].RockBPS = 10_000, 5_000
		}
	}
	pack = testworld.Extend(pack)
	if err := pack.Validate(); err != nil {
		t.Fatalf("validating content: %v", err)
	}
	snap, err := content.BuildSnapshot(1, pack)
	if err != nil {
		t.Fatalf("building snapshot: %v", err)
	}
	return snap
}
