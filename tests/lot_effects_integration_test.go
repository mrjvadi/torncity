//go:build integration

package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// What the modules of a lot DO (ADR 0045 B1, rule 1c): storerooms and shelves add room to the home store, a hearth
// makes a rest warm, a stall's shelves are counters of the owner's own on the village book; and two orders at the same
// moment on one building take the materials once.

// Storerooms beyond the level raise the home store by their spaces, once built; a shelf adds sixty.
func TestStoreroomsAndShelvesRaiseTheHomeStore(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	cfg := config.Defaults()
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-time.Hour), Scale: gameScale}
	inv := handlers.NewInventoryHandler(uow, workIDs{t}, nil, staticContentSource{snap: loadTestContent(t)}, postgres.NewCityRepository(e.pool),
		gametime.Scale(gameScale), crimeRules().Nerve, 10, time.Hour, e.clock.Now).WithCarry(cfg.CarryRules(), clock)
	capacity := func() int64 {
		t.Helper()
		m := e.as(owner, "inventory.show", "show")
		resp, err := rrcm(m)(inv.Show(testCtx(t), m, handlers.PageRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		var v plife.InventoryView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		if v.Home == nil {
			return 0
		}
		return v.Home.Capacity
	}
	id := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)
	if got := capacity(); got != 40 {
		t.Fatalf("a cottage keeps 40 spaces: %d", got)
	}
	e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "storeroom", N: "1"})
	if got := capacity(); got != 40 {
		t.Errorf("the storeroom counts before it is built: %d", got)
	}
	e.build(owner, id)
	if got := capacity(); got != 80 {
		t.Errorf("one more storeroom is 40 more spaces: %d", got)
	}
	e.verifyLots()
}

// A hearth in the house makes the rest warm: a unit of firewood from the home store buys a quarter more, and without it
// the rest works as before.
func TestAHearthMakesTheRestWarm(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	e.village.WithCitizenRules(handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000, TaxBPS: 200, TaxBPSMax: 500,
		TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000, MaxLotsPerPlayer: 3, PrivateShareMaxBPS: 6000,
		HomeRestCooldown: time.Hour, HomeRestHealth: 40, HomeRestHappiness: 20,
	})
	id := e.house(owner, "private_cottage", 800)
	// the lot must be opened once so that the house has its function and its hearth
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id}); r.Refusal != nil {
		t.Fatalf("opening the lot: %+v", r.Refusal)
	}
	rest := func() string {
		t.Helper()
		resp, err := rrc(e.village.HomeRest(testCtx(t), e.as(owner, "settlement.home.rest", "home.rest")))
		if err != nil {
			t.Fatal(err)
		}
		var v village.MineView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v.Notice
	}
	if n := rest(); n != "rested" {
		t.Errorf("without wood the rest is plain: %q", n)
	}
	e.clock.Advance(2 * time.Hour)
	e.give(owner, application.HoldHome, "firewood", 2)
	if n := rest(); n != "rested_warm" {
		t.Errorf("with wood and a hearth the rest is warm: %q", n)
	}
	if got := e.held(owner, application.HoldHome, "firewood"); got != 1 {
		t.Errorf("the hearth burnt %d firewood, want 1", 2-got)
	}
	e.verifyLots()
}

// A stall's shelves are counters of the owner's own: his orders sit on them, take no common stall and pay no listing fee.
func TestAStallsShelvesAreCountersOfTheOwnersOwn(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'barter_post', 72, 3, 'complete', now(), now())`, newUUID(t), e.cityID); err != nil {
		t.Fatal(err)
	}
	id := e.house(owner, "market_stall", 500)
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id}); r.Refusal != nil {
		t.Fatalf("opening the stall: %+v", r.Refusal)
	}
	var slots int64
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT COALESCE(SUM(m.count), 0) FROM building_modules m WHERE m.building_ref_id = $1::uuid AND m.module_kind = 'shelves'`, id).Scan(&slots); err != nil || slots != 1 {
		t.Fatalf("a stall comes with a shelf: %d %v", slots, err)
	}
	var own int64
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		own, err = tx.SettlementBuildings().OwnStallSlots(ctx, e.cityID, owner.ID)
		return err
	})
	if err != nil || own != 1 {
		t.Fatalf("the owner has %d counters of his own (%v), want 1", own, err)
	}
	// a stranger has none, and a stall under construction counts for nothing
	var other int64
	stranger := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, stranger.ID) })
	_ = postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		other, err = tx.SettlementBuildings().OwnStallSlots(ctx, e.cityID, stranger.ID)
		return err
	})
	if other != 0 {
		t.Errorf("a stranger has %d counters", other)
	}
}

// Two orders at the same moment on one building: one is taken, the materials leave once.
func TestTwoOrdersAtOnceTakeTheMaterialsOnce(t *testing.T) {
	e := newLotEnv(t)
	owner := e.owner
	id := e.house(owner, "private_cottage", 800)
	e.give(owner, application.HoldHome, "timber", 20)
	grantCash(t, e.pool, owner.ID, 5000)
	var wg sync.WaitGroup
	results := make([]*presentation.Response, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "storeroom", N: "1", Confirm: village.ResidenceConfirm}
			resp, err := e.village.ManageLot(testCtx(t), e.as(owner, "settlement.lot.manage", "lot.manage"), req)
			if err != nil {
				t.Errorf("order %d: %v", i, err)
				return
			}
			results[i] = resp
		}()
	}
	wg.Wait()
	if n := e.scalar(`SELECT count(*) FROM building_works WHERE building_id = $1::uuid AND status = 'open'`, id); n != 1 {
		t.Errorf("%d open orders, want 1", n)
	}
	if got := e.held(owner, application.HoldHome, "timber"); got != 18 {
		t.Errorf("the home store holds %d timber, want 18 (2 taken once)", got)
	}
	e.build(owner, id)
	e.verifyLots()
}
