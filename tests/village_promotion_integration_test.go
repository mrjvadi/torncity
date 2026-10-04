//go:build integration

// Integration test of the retired promotion ladder (owner scope rule 2026-10-02,
// ADR 0044): settlement.promote never changes a settlement's tier and
// settlement.promotion.view is only the development readout, which names what the
// settlement could research or build next. Old clients keep a true answer.
package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
)

func TestPromotionLadderIsRetired(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool

	meta, head := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	village := handlers.NewVillageHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil,
		staticContentSource{snap: loadTestContent(t)}, e.cache, postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now)
	tier := func() string {
		var s string
		if err := pool.Raw().QueryRow(ctx, `SELECT tier FROM cities WHERE id = $1::uuid`, cityID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := tier()
	as := func(command, action string) func() (*presentation.Response, error) {
		return func() (*presentation.Response, error) {
			m := asPlayer(meta, head)
			m.Command, m.Action = command, action
			m.IdempotencyKey = "it-" + randomToken(t, 16)
			switch command {
			case "settlement.promote":
				return village.Promote(ctx, m, handlers.VillagePromoteRequest{Confirm: vpres.VillagePromoteConfirm})
			default:
				return village.PromotionView(ctx, m)
			}
		}
	}
	for _, call := range []func() (*presentation.Response, error){as("settlement.promote", "promote"), as("settlement.promotion.view", "promotion.view")} {
		resp, err := call()
		if err != nil {
			t.Fatal(err)
		}
		if resp.Screen != vpres.ScreenVillageDevelopment {
			t.Errorf("an old promotion command answered %q, want the development readout", resp.Screen)
		}
		var v vpres.DevelopmentView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		for _, n := range v.Next {
			if n.Kind != "research" && n.Kind != "build" || n.Code == "" {
				t.Errorf("a next step %+v", n)
			}
		}
	}
	if got := tier(); got != before {
		t.Errorf("tier moved from %q to %q: the ladder is retired", before, got)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE payload->>'settlement_id' = $1 AND subject LIKE '%promoted%'`, cityID); n != 0 {
		t.Errorf("%d promoted events", n)
	}
}
