//go:build integration

package tests

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/content/testworld"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// testWorldMarker is written into the reason of the load and looked for in
// the active version's notes. Bump the revision whenever
// internal/content/testworld changes, so an integration database that
// already carries an older test world is loaded again.
const testWorldMarker = "integration test world r2"

// TestMain makes sure the integration database holds the multi-city TEST
// world before any test runs.
//
// The shipped content has one city, Support (docs/adr/0032-support-merge.md).
// The tests of travel, borders, sanctions, war and regional careers need
// places that are apart, so they run against the shipped pack extended by
// internal/content/testworld: the seven cities Support replaced, their
// routes and their regional restrictions. Loading that pack is idempotent
// (the loader matches cities on code and never duplicates a seat), and it is
// skipped when the database already carries this revision of the world. This touches the
// integration database only, never a real one: the whole file is behind the
// integration build tag and INTEGRATION_DSN.
func TestMain(m *testing.M) {
	if dsn := os.Getenv(envDSN); dsn != "" {
		if err := ensureTestWorld(dsn); err != nil {
			fmt.Fprintf(os.Stderr, "tests: loading the multi-city test world: %v\n", err)
			os.Exit(1)
		}
	}
	// INTEGRATION_GROWTH=shadow runs the whole suite with the capability dual
	// read on (ADR 0044 phase G1): every gate computes the capability answer
	// beside the tier's and meters the disagreements; the tier stays the
	// answer, so every test must pass exactly as with the flag off.
	suiteGrowth = os.Getenv("INTEGRATION_GROWTH")
	if dsn := os.Getenv(envDSN); dsn != "" && suiteGrowth != "" {
		ctx := context.Background()
		pool, err := postgres.New(ctx, dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tests: growth pool: %v\n", err)
			os.Exit(1)
		}
		suiteGrowthPool = pool
		restoreSuiteGrowth()
	}
	code := m.Run()
	if g := suiteGate; g != nil && suiteGrowthPool != nil {
		if err := g.Flush(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "tests: growth flush: %v\n", err)
		}
		if rows, err := postgres.NewGrowthRepository(suiteGrowthPool).List(context.Background(), 20); err == nil {
			fmt.Fprintf(os.Stderr, "tests: growth shadow metered %d distinct disagreements (top 20 listed)\n", len(rows))
			for _, r := range rows {
				fmt.Fprintf(os.Stderr, "  seen %d  %-14s %s/%s tier=%v capabilities=%v missing=%s\n", r.Count, r.Site, r.Kind, r.Code, r.TierAnswer, r.CapabilityAnswer, r.Missing)
			}
		}
	}
	os.Exit(code)
}

func ensureTestWorld(dsn string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := postgres.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	// A database without the content tables at all (migrations not applied
	// yet) has nothing to load into; the tests skip themselves there.
	var have bool
	if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('public.cities') IS NOT NULL`).Scan(&have); err != nil || !have {
		return nil
	}
	var loaded bool
	if err := pool.Raw().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM content_versions WHERE status = 'active' AND notes LIKE '%' || $1 || '%')`,
		testWorldMarker).Scan(&loaded); err != nil {
		return err
	}
	if loaded {
		return nil
	}

	pack, err := content.Load("../configs/content")
	if err != nil {
		return err
	}
	pack = testworld.Extend(pack)
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("the test world does not validate: %w", err)
	}
	_, err = postgres.NewContentStore(pool).Apply(ctx, pack, postgres.ApplyRequest{
		Actor:  "integration-tests",
		Reason: testWorldMarker + ": the shipped content plus the legacy cities (docs/adr/0032)",
	})
	return err
}
