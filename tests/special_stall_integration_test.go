//go:build integration

package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// A stall sells only with a stall right where the settlement requires one (docs/adr/0077 7.4): the sell is refused, in the player's own words, without
// the licence; with it the order takes its stall as before.
func TestAStallSellsOnlyWithAStallRightWhereTheTownRequiresOne(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	market := handlers.NewMarketHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, postgres.NewCityRepository(pool),
		postgres.NewPolicyReader(pool, nil), gametime.Scale(1),
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, e.clock.Now).
		WithHome("support").
		WithVillageBook(handlers.VillageMarketRules{StallsPost: 6, StallsHall: 20, StallsPerPlayerPost: 3, StallsPerPlayerHall: 6}).
		WithLicenceMarket(e.clock.Now().Add(-48 * time.Hour))
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`DELETE FROM game_actions WHERE reference_type = 'market_orders' AND reference_id IN (SELECT id FROM market_orders WHERE city_id = $1::uuid)`,
			`DELETE FROM market_listing_fees WHERE city_id = $1::uuid`,
			`DELETE FROM market_orders WHERE city_id = $1::uuid`,
			`TRUNCATE licence_events, licences, licence_rules CASCADE`,
		} {
			args := []any{}
			if strings.Contains(stmt, "$1") {
				args = append(args, cityID)
			}
			if _, err := pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	grant(t, pool, application.AccountPlayerCash, founder.ID, 10_000)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: 20, To: founder.ID, ToHolding: application.HoldCarried,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	sell := func() string {
		t.Helper()
		m := asPlayer(meta, founder)
		m.Command, m.Language = "market.order", "en"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		r, err := rrcm(m)(market.Order(ctx, m, handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "2", Price: "100", Method: "cash", Nonce: randomToken(t, 8)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	if got := sell(); strings.Contains(got, "مجوز غرفه") {
		t.Fatalf("with no requirement a stall sells freely: %s", got)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO licence_rules (settlement_id, kind, mode, fee, reserve, term_days, transferable, set_by, set_at, effective_at) VALUES ($1::uuid, 'stall_right', 'fixed', 100, 0, 30, true, $2::uuid, now() - interval '2 days', now() - interval '1 day')`, cityID, founder.ID); err != nil {
		t.Fatal(err)
	}
	if got := sell(); !strings.Contains(got, "مجوز غرفه") {
		t.Fatalf("once the town requires a stall right a seller without one is refused: %s", got)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO licences (id, kind, issuer_id, holder_kind, holder_id, target_ref, via, paid, transferable, status, term_start, term_end, created_at)
		VALUES ($1::uuid, 'stall_right', $2::uuid, 'player', $3::uuid, '', 'fixed', 100, true, 'active', now(), now() + interval '30 days', now())`, newUUID(t), cityID, founder.ID); err != nil {
		t.Fatal(err)
	}
	if got := sell(); strings.Contains(got, "مجوز غرفه") {
		t.Fatalf("with a stall right the order goes ahead: %s", got)
	}
}
