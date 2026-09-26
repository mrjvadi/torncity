//go:build integration

// Integration test for an operator's override of a player's company-count
// cap (migrations/0032_player_limits, `admin player limit`,
// internal/operator.Ops.SetCompanyLimit): through the real founding
// handler, a player with no override is refused at the config cap; granted
// --unlimited, they found past it; --clear restores the cap.
package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/operator"
)

func TestPlayerLimitOverridesTheCompanyCap(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := companyRegistry(t, pool)
	ctx := testCtx(t)

	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}

	owner := insertPlayer(t, pool)
	// player_limits carries a foreign key to players; clear it before
	// insertPlayer's own cleanup deletes the player (t.Cleanup runs LIFO,
	// so registering this after insertPlayer runs it first).
	t.Cleanup(func() {
		if _, err := pool.Raw().Exec(testCtx(t), `DELETE FROM player_limits WHERE player_id = $1::uuid`, owner.ID); err != nil {
			t.Errorf("cleanup: clearing player_limits: %v", err)
		}
	})
	t.Cleanup(func() { purgeLedgerFor(t, pool, owner.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, owner.ID) })

	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
		  WHERE id = $1::uuid`, owner.ID, city.ID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, owner.ID, 100_000)

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	const configDefault = 2
	rules := handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: configDefault, NameMin: 3, NameMax: 24,
		FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5, PriceStepBPS: 1000, Limits: limits}
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, rules, time.Hour, func() time.Time { return time.Now().UTC() })

	found := func(name string) *handlers.CompanyRequest {
		return &handlers.CompanyRequest{Type: "grocery", Method: "cash", Name: name}
	}
	meta := func() envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = owner.TelegramUserID
		m.Command = "company.found"
		m.Language = "en"
		return m
	}
	foundOK := func(t *testing.T, name string) {
		t.Helper()
		resp, err := companies.Found(ctx, meta(), *found(name))
		if err != nil {
			t.Fatalf("Found(%q): %v", name, err)
		}
		if resp != nil && strings.Contains(resp.Text, "company.refused.") {
			t.Fatalf("Found(%q) was refused: %s", name, resp.Text)
		}
	}
	foundRefusedAtLimit := func(t *testing.T, name string) {
		t.Helper()
		resp, err := companies.Found(ctx, meta(), *found(name))
		if err != nil || resp == nil || !strings.Contains(resp.Text, "company.refused.limit") {
			t.Fatalf("Found(%q) = %v, %v; want the limit refusal", name, resp, err)
		}
	}

	// No override: the third company hits the config default.
	foundOK(t, "Limit Test Alpha")
	foundOK(t, "Limit Test Beta")
	foundRefusedAtLimit(t, "Limit Test Gamma")

	ops := operator.Ops{Pool: pool}
	actor := operator.Actor{Name: "integration-test", Reason: "TestPlayerLimitOverridesTheCompanyCap"}

	// --unlimited lifts the cap: the third company now founds.
	granted, err := ops.SetCompanyLimit(ctx, owner.PublicCode, nil, true, actor)
	if err != nil {
		t.Fatalf("SetCompanyLimit(unlimited): %v", err)
	}
	if !granted.Unlimited || granted.Cleared || granted.MaxCompanies != nil {
		t.Fatalf("granted = %+v, want unlimited only", granted)
	}
	foundOK(t, "Limit Test Gamma")

	owned, err := postgres.NewCompanyRepository(pool).OwnedCount(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owned != 3 {
		t.Fatalf("owned companies = %d, want 3", owned)
	}

	// --clear restores the config default: a fourth is refused again, since
	// the player already owns more than the cap.
	cleared, err := ops.SetCompanyLimit(ctx, owner.PublicCode, nil, false, actor)
	if err != nil {
		t.Fatalf("SetCompanyLimit(clear): %v", err)
	}
	if !cleared.Cleared || cleared.Unlimited || cleared.MaxCompanies != nil {
		t.Fatalf("cleared = %+v, want Cleared only", cleared)
	}
	foundRefusedAtLimit(t, "Limit Test Delta")

	// A numeric override is exact and audited, too.
	five := 5
	grantedFive, err := ops.SetCompanyLimit(ctx, owner.PublicCode, &five, false, actor)
	if err != nil {
		t.Fatalf("SetCompanyLimit(5): %v", err)
	}
	if grantedFive.Unlimited || grantedFive.Cleared || grantedFive.MaxCompanies == nil || *grantedFive.MaxCompanies != 5 {
		t.Fatalf("grantedFive = %+v, want a cap of 5", grantedFive)
	}
	foundOK(t, "Limit Test Delta")

	if n := countRows(t, pool, `SELECT count(*) FROM audit_logs WHERE action LIKE 'player.limit.%' AND new_value->>'player' = $1`,
		owner.ID); n < 3 {
		t.Fatalf("audit rows for the player's limit = %d, want at least 3", n)
	}

	// An unknown player code is refused before anything is touched.
	if _, err := ops.SetCompanyLimit(ctx, "ZZZZZZ", nil, true, actor); err == nil {
		t.Fatal("SetCompanyLimit for an unknown player code = nil error, want a refusal")
	}
}
