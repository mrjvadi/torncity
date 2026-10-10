//go:build integration

package tests

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The owner of a stall hires a keeper from the labour pool and dismisses him (ADR 0062): the lot shows the keeper block,
// the hire needs a free seat and is taken once, the seat is held while he is hired, and a dismissal frees it.
func TestAStallOwnerHiresAndDismissesAKeeper(t *testing.T) {
	e := newLotEnv(t)
	e.village.WithStallKeeper(1000)
	owner := e.owner
	id := e.house(owner, "market_stall", 500)
	t.Cleanup(func() {
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
