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
func loadTestContent(t *testing.T) *content.Snapshot {
	t.Helper()
	pack, err := content.Load("../configs/content")
	if err != nil {
		t.Fatalf("loading content: %v", err)
	}
	// The shipped world has one city; the integration database carries the
	// multi-city test world (main_integration_test.go), so the snapshot the
	// tests build must match it.
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
