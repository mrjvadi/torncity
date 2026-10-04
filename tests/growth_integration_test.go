//go:build integration

// Integration test of the capability dual read (docs/adr/0044 phase G1): with
// growth.capabilities off nothing is computed and nothing is listed; in shadow
// mode the capability answer is computed beside the tier's, a disagreement is
// metered and flushed to growth_disagreements (additively, one row per
// settlement, gate and entry), the tier stays authoritative, and the readout is
// listed and answered.
package tests

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// The suite-wide state of the dual read (TestMain): INTEGRATION_GROWTH names the
// mode ("shadow") the whole run uses; empty leaves it off.
var (
	suiteGrowth     string
	suiteGrowthPool *postgres.Pool
	suiteGate       *handlers.GrowthGate
)

// restoreSuiteGrowth puts the process-wide gate back to what the run asked for.
func restoreSuiteGrowth() {
	if suiteGrowth == "" || suiteGrowthPool == nil {
		handlers.ConfigureGrowth(handlers.GrowthConfig{Mode: handlers.GrowthModeOff}, nil, nil, nil)
		suiteGate = nil
		return
	}
	repo := postgres.NewGrowthRepository(suiteGrowthPool)
	suiteGate = handlers.ConfigureGrowth(handlers.GrowthConfig{Mode: suiteGrowth, CacheTTL: time.Second, RuinedBPS: 10000, FlushInterval: time.Hour},
		repo, repo, nil)
}

func TestGrowthDualRead(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool

	meta, head := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	snap := loadTestContent(t)
	village := handlers.NewVillageHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil,
		staticContentSource{snap: snap}, e.cache, postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now)
	gate := handlers.NewServiceGate(postgres.NewCityRepository(pool), "support")
	repo := postgres.NewGrowthRepository(pool)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(ctx, `DELETE FROM growth_disagreements WHERE settlement_id = $1::uuid`, cityID)
		restoreSuiteGrowth()
	})

	as := func(p *application.Player, command, action string) envelope.Metadata {
		m := clientMeta(asPlayer(meta, p), command, action)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	overview := func() map[string]any {
		r, err := village.Overview(ctx, as(head, "settlement.overview", "overview"))
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		return viewOf(t, r)
	}
	disagreements := func() int {
		return e.count(t, `SELECT coalesce(sum(seen_count), 0)::int FROM growth_disagreements WHERE settlement_id = $1::uuid`, cityID)
	}

	// 1. Off: the service gate computes nothing and answers by the tier; the readout is always on offer
	// (the promotion ladder is retired).
	handlers.ConfigureGrowth(handlers.GrowthConfig{Mode: handlers.GrowthModeOff}, nil, nil, nil)
	if overview()["development"] != true {
		t.Fatal("off: the overview does not list the readout")
	}
	if r, err := village.DevelopmentView(ctx, as(head, "settlement.development.view", "development.view")); err != nil {
		t.Fatalf("off: %v", err)
	} else if strings.Contains(string(r.View), "not_available") {
		t.Errorf("off: the readout was refused: %s", r.View)
	}
	if un, err := gate.CheckEntry(ctx, snap, cityID, "company_type", "courier"); err != nil || un == nil {
		t.Fatalf("off: a fresh village is not offered a town's courier business: %v %v", un, err)
	}
	if n := disagreements(); n != 0 {
		t.Fatalf("off: %d disagreements metered", n)
	}

	// 2. Shadow: the tier stays the answer, the capabilities are metered.
	g := handlers.ConfigureGrowth(handlers.GrowthConfig{Mode: handlers.GrowthModeShadow, CacheTTL: time.Second, RuinedBPS: 10000, FlushInterval: time.Hour},
		repo, repo, nil)
	if overview()["development"] != true {
		t.Fatal("shadow: the overview does not list the readout")
	}
	// A courier business: the tier (stage town) refuses a village; the capabilities (a road and a market L1,
	// both in the founding kit) accept. The gate must still answer by the tier.
	un, err := gate.CheckEntry(ctx, snap, cityID, "company_type", "courier")
	if err != nil || un == nil {
		t.Fatalf("shadow: the tier answer must stay authoritative: %v %v", un, err)
	}
	if p := g.Pending(); len(p) != 1 || p[0].Site != "service_gate" || p[0].Code != "courier" || p[0].TierAnswer || !p[0].CapabilityAnswer {
		t.Fatalf("shadow: pending = %+v", p)
	}
	// the same entry again is a count, not a row
	if _, err := gate.CheckEntry(ctx, snap, cityID, "company_type", "courier"); err != nil {
		t.Fatal(err)
	}
	// a bank is a city thing by tier AND needs the bank building by capability: they agree, nothing is metered
	if un, err := gate.CheckEntry(ctx, snap, cityID, "finance_service", "bank"); err != nil || un == nil {
		t.Fatalf("shadow: bank in a fresh village: %v %v", un, err)
	}
	if err := g.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if n := disagreements(); n != 2 {
		t.Fatalf("shadow: %d disagreements stored, want 2 (one row, seen twice)", n)
	}
	// flushing again after more sightings adds to the same row
	if _, err := gate.CheckEntry(ctx, snap, cityID, "company_type", "courier"); err != nil {
		t.Fatal(err)
	}
	// (the gate caches capabilities for the ttl; the tier answer is unchanged, so the same disagreement is seen)
	if err := g.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM growth_disagreements WHERE settlement_id = $1::uuid`, cityID); n != 1 {
		t.Fatalf("shadow: %d rows, want 1", n)
	}
	if n := disagreements(); n != 3 {
		t.Fatalf("shadow: seen_count = %d, want 3", n)
	}
	rows, err := repo.List(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.SettlementID == cityID && r.Kind == "company_type" && r.Code == "courier" && !r.TierAnswer && r.CapabilityAnswer {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report does not list the courier disagreement: %+v", rows)
	}

	// 3. The readout answers with its view and no act.
	r, err := village.DevelopmentView(ctx, as(head, "settlement.development.view", "development.view"))
	if err != nil {
		t.Fatalf("development view: %v", err)
	}
	var v struct {
		Village    string `json:"village"`
		Dimensions []struct {
			Code     string `json:"code"`
			Load     int64  `json:"load"`
			Capacity int64  `json:"capacity"`
		} `json:"dimensions"`
		Next []map[string]any `json:"next"`
	}
	if err := json.Unmarshal(r.View, &v); err != nil || v.Village == "" || len(v.Dimensions) != 3 || v.Dimensions[0].Code != "people" || v.Dimensions[0].Load != 1 {
		t.Fatalf("development view: %v %s", err, r.View)
	}
	if len(v.Next) == 0 {
		t.Error("a young village has goals ahead")
	}
	if strings.Contains(string(r.View), "promote") {
		t.Errorf("the readout carries an act: %s", r.View)
	}

	// 4. A settlement's building list is unchanged by the flag: the same menu with it on and off.
	menuOn, err := village.BuildMenu(ctx, as(head, "settlement.build", "build"))
	if err != nil {
		t.Fatalf("build menu: %v", err)
	}
	handlers.ConfigureGrowth(handlers.GrowthConfig{Mode: handlers.GrowthModeOff}, nil, nil, nil)
	menuOff, err := village.BuildMenu(ctx, as(head, "settlement.build", "build"))
	if err != nil {
		t.Fatalf("build menu: %v", err)
	}
	if string(menuOn.View) != string(menuOff.View) {
		t.Errorf("shadow mode changed the build menu:\n on: %s\noff: %s", menuOn.View, menuOff.View)
	}
}
