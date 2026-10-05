//go:build integration

// Journeys take their real time now (game.travel_time_scale 1): a traveller is on the
// road, so what needs them in the village is refused until they arrive.
package tests

import (
	"context"
	stderrors "errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

func TestATravellerCannotActInTheVillage(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now)
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID, supportID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE code = 'support'`).Scan(&supportID); err != nil {
		t.Fatal(err)
	}
	donate := func() error {
		m := asPlayer(meta, founder)
		m.Command = "settlement.stock.donate"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		_, err := village.StockDonate(ctx, m, handlers.VillageStockMoveRequest{Item: "timber", Qty: "1"})
		return err
	}
	if err := donate(); stderrors.Is(err, application.ErrAlreadyTravelling) {
		t.Fatalf("a player at home was refused as a traveller: %v", err)
	}
	// put the founder on a six-hour journey to the neutral city
	actionID, travelID := newUUID(t), newUUID(t)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.GameActions().Schedule(ctx, application.GameAction{ID: actionID, ActionType: "travel", ActorType: "player", ActorID: founder.ID,
			ReferenceType: "travels", ReferenceID: travelID, StartedAt: e.clock.Now(), FinishAt: e.clock.Now().Add(6 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5::uuid, 'in_transit', $6, $7)`,
		travelID, founder.ID, cityID, supportID, actionID, e.clock.Now(), e.clock.Now().Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM travels WHERE id = $1::uuid`, travelID)
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE id = $1::uuid`, actionID)
	})
	if err := donate(); !stderrors.Is(err, application.ErrAlreadyTravelling) {
		t.Errorf("a traveller gave to the village stock: %v", err)
	}
	// the journey is the scheduler's: nothing here moved it
	var status string
	if err := pool.Raw().QueryRow(ctx, `SELECT status FROM travels WHERE id = $1::uuid`, travelID).Scan(&status); err != nil || status != "in_transit" {
		t.Errorf("the journey changed: %q %v", status, err)
	}
}
