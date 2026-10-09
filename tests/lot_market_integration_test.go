//go:build integration

package tests

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// The owner of a stall sells on the village book from his own counter (ADR 0045 B1: the shelves of a stall): no
// listing fee and no common stall used; a second order, beyond his counters, takes a common stall and pays the fee as
// everyone does.
func TestAnOwnCounterTakesNoListingFee(t *testing.T) {
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
		WithVillageBook(handlers.VillageMarketRules{StallsPost: 6, StallsHall: 20, StallsPerPlayerPost: 3, StallsPerPlayerHall: 6})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	post, stall := newUUID(t), newUUID(t)
	for _, q := range []struct {
		id, code string
		x        int
	}{{post, "barter_post", 70}, {stall, "market_stall", 73}} {
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, 3, 'complete', now(), now())`, q.id, cityID, q.code, q.x); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 500, now())`, stall, cityID, founder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO building_functions (building_ref_kind, building_ref_id, settlement_id, function_code, since)
		VALUES ('settlement_building', $1::uuid, $2::uuid, 'stall', now())`, stall, cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO building_modules (building_ref_kind, building_ref_id, module_kind, count)
		VALUES ('settlement_building', $1::uuid, 'shelves', 1)`, stall); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM game_actions WHERE reference_type = 'market_orders' AND reference_id IN (SELECT id FROM market_orders WHERE city_id = $1::uuid)`,
			`DELETE FROM market_listing_fees WHERE city_id = $1::uuid`,
			`DELETE FROM market_orders WHERE city_id = $1::uuid`,
			`DELETE FROM building_modules WHERE building_ref_id IN (SELECT building_ref_id FROM building_functions WHERE settlement_id = $1::uuid)`,
			`DELETE FROM building_functions WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`,
		} {
			_, _ = pool.Raw().Exec(c, stmt, cityID)
		}
	})
	grant(t, pool, application.AccountPlayerCash, founder.ID, 10_000)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: 40, To: founder.ID,
			ToHolding: application.HoldCarried, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	order := func() {
		t.Helper()
		m := asPlayer(meta, founder)
		m.Command, m.Language = "market.order", "en"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		if _, err := rrcm(m)(market.Order(ctx, m, handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "10", Price: "100", Method: "cash", Nonce: randomToken(t, 8)})); err != nil {
			t.Fatal(err)
		}
	}
	treasury0 := treasuryOf(t, pool, cityID)
	order() // on his own counter: no fee
	if got := treasuryOf(t, pool, cityID) - treasury0; got != 0 {
		t.Errorf("an order on his own counter paid %d in listing fee", got)
	}
	order() // beyond his counters: a common stall and the fee (1 percent of 1000)
	if got := treasuryOf(t, pool, cityID) - treasury0; got != 10 {
		t.Errorf("the second order paid %d in listing fee, want 10", got)
	}
}
