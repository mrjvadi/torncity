//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

func keeperTerms() handlers.StallKeeperTerms {
	t := config.Defaults().Trade
	return handlers.StallKeeperTerms{ShareBPS: int64(t.StallKeeperShareBPS), ShareMinBPS: int64(t.StallKeeperShareMinBPS), ShareMaxBPS: int64(t.StallKeeperShareMaxBPS),
		Wage: int64(t.StallKeeperWage), WageMin: int64(t.StallKeeperWageMin), WageMax: int64(t.StallKeeperWageMax)}
}

// The owner of a stall hires a keeper from the labour pool and dismisses him (ADR 0062): the lot shows the keeper block,
// the hire needs a free seat and is taken once, the seat is held while he is hired, and a dismissal frees it.
func TestAStallOwnerHiresAndDismissesAKeeper(t *testing.T) {
	e := newLotEnv(t)
	e.village.WithStallKeeper(keeperTerms())
	clock, err := config.Defaults().GameClock()
	if err != nil {
		t.Fatal(err)
	}
	e.village.WithService(handlers.ServiceRules{Clock: clock})
	owner := e.owner
	id := e.house(owner, "market_stall", 500)
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM stall_keeper_wages WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM stall_keepers WHERE settlement_id = $1::uuid`, e.cityID)
	})
	// homes for the pool: the keeper is one of its people
	e.resident()
	e.resident()

	v, resp := e.manage(owner, handlers.VillageManageRequest{Building: id})
	if resp.Refusal != nil || v.Function.Code != "stall" || v.Keeper == nil {
		t.Fatalf("the stall shows its keeper block: %+v %v", v.Function, resp.Refusal)
	}
	if v.Keeper.Hired || v.Keeper.ShareBPS != 1000 {
		t.Fatalf("no keeper yet, the share on offer is 1000: %+v", v.Keeper)
	}
	if !v.Keeper.Can {
		t.Skipf("the settlement has no free person of the pool to hire (%s, %d free)", v.Keeper.Reason, v.Keeper.SeatsFree)
	}
	// the ask changes nothing; the confirm hires once
	ask, _ := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire})
	if ask.Stage != village.LotAsk || e.scalar(`SELECT count(*) FROM stall_keepers WHERE owner_id = $1::uuid AND ended_at IS NULL`, owner.ID) != 0 {
		t.Fatalf("a quote hires nobody: %+v", ask.Stage)
	}
	done := e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire})
	if done.Keeper == nil || !done.Keeper.Hired {
		t.Fatalf("the keeper is hired: %+v", done.Keeper)
	}
	if n := e.scalar(`SELECT count(*) FROM stall_keepers WHERE owner_id = $1::uuid AND ended_at IS NULL`, owner.ID); n != 1 {
		t.Fatalf("%d open hires, want 1", n)
	}
	// hiring a second one is refused
	req := handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire, Confirm: village.ResidenceConfirm}
	if _, r := e.manage(owner, req); r.Refusal == nil {
		t.Error("an owner with a keeper cannot hire another")
	}
	// the market line holds the seat
	if m := e.scalar(`SELECT count(*) FROM stall_keepers WHERE settlement_id = $1::uuid AND ended_at IS NULL`, e.cityID); m != 1 {
		t.Errorf("the settlement has %d open hires, want 1", m)
	}
	// dismissed
	gone := e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperEnd})
	if gone.Keeper == nil || gone.Keeper.Hired {
		t.Fatalf("the keeper is dismissed: %+v", gone.Keeper)
	}
	if n := e.scalar(`SELECT count(*) FROM stall_keepers WHERE owner_id = $1::uuid AND ended_at IS NULL`, owner.ID); n != 0 {
		t.Fatalf("%d open hires after the dismissal", n)
	}
}

// The owner can pay the keeper by the day instead (ADR 0062 addendum): the terms are held to the configured range, the day
// wage is charged once for each local day to the owner's cash, and an owner who cannot pay loses the keeper with a notice.
func TestAStallKeeperPaidByTheDay(t *testing.T) {
	e := newLotEnv(t)
	e.village.WithStallKeeper(keeperTerms())
	clock, err := config.Defaults().GameClock()
	if err != nil {
		t.Fatal(err)
	}
	e.village.WithService(handlers.ServiceRules{Clock: clock})
	owner := e.owner
	id := e.house(owner, "market_stall", 500)
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM stall_keeper_wages WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM stall_keepers WHERE settlement_id = $1::uuid`, e.cityID)
	})
	e.resident()
	e.resident()
	grantCash(t, e.pool, owner.ID, 1500)

	// pay outside the range is refused
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire, Code: "wage", Name: "50", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a day wage below the range is refused")
	}
	if _, r := e.manage(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire, Code: "share", Name: "9000", Confirm: village.ResidenceConfirm}); r.Refusal == nil {
		t.Error("a share above the range is refused")
	}
	done := e.confirmAct(owner, handlers.VillageManageRequest{Building: id, Action: village.LotActionKeeperHire, Code: "wage", Name: "700"})
	if done.Keeper == nil || !done.Keeper.Hired || done.Keeper.Pay != "wage" || done.Keeper.Wage != 700 || done.Keeper.ShareBPS != 0 {
		t.Fatalf("the keeper is hired by the day: %+v", done.Keeper)
	}

	settle := func() {
		t.Helper()
		if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
			s, err := tx.Settlements().ByID(ctx, e.cityID)
			if err != nil {
				return err
			}
			return e.village.SettleKeeperWages(ctx, tx, s, e.clock.Now(), e.meta)
		}); err != nil {
			t.Fatal(err)
		}
	}
	cash0 := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID)
	settle()
	settle() // a redelivery changes nothing
	if got := cash0 - cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID); got != 700 {
		t.Fatalf("the day wage was %d, want 700 once", got)
	}
	e.clock.Advance(26 * time.Hour)
	settle()
	if n := e.scalar(`SELECT count(*) FROM stall_keeper_wages WHERE settlement_id = $1::uuid`, e.cityID); n != 2 {
		t.Fatalf("%d wage days, want 2", n)
	}
	if v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id}); v.Keeper == nil || v.Keeper.CutTotal != 1400 || v.Keeper.SoldAway != 0 {
		t.Fatalf("the keeper block shows two day wages and no sales: %+v", v.Keeper)
	}
	// 100 is left: the keeper goes, and the lot says why
	e.clock.Advance(26 * time.Hour)
	settle()
	if n := e.scalar(`SELECT count(*) FROM stall_keepers WHERE owner_id = $1::uuid AND ended_at IS NULL`, owner.ID); n != 0 {
		t.Fatalf("a keeper nobody could pay stayed")
	}
	v, _ := e.manage(owner, handlers.VillageManageRequest{Building: id})
	if v.Keeper == nil || v.Keeper.Hired || v.Keeper.Left != "wage_unpaid" {
		t.Fatalf("the lot should say the keeper left unpaid: %+v", v.Keeper)
	}
	e.verify2b()
	vv, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	if sc := vv.ShopCheck; sc.KeeperDayLedger != sc.KeeperDayRows || sc.KeeperDayLedger != 1400 {
		t.Errorf("day wages %d in the ledger, %d in the rows, want 1400", sc.KeeperDayLedger, sc.KeeperDayRows)
	}
}
