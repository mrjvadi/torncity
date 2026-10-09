//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// A settlement's own building (a lot on which the treasury built) is managed by the holder of public.build: the
// materials come from the settlement's stock, the wages from the treasury; its function and level do not change.
func TestThePublicBuildHolderOrdersOnTheSettlementsOwnBuilding(t *testing.T) {
	e := newLotEnv(t)
	head := e.head
	id := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'cottage', 60, 3, 'complete', now(), now())`, id, e.cityID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "timber", Qty: 30, ToOrg: application.SettlementOrg(e.cityID),
			ToHolding: application.HoldWarehouse, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	stock := func() int64 {
		return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'timber' AND holding = 'warehouse'`, e.cityID)
	}
	// a resident who holds no office cannot manage it
	if _, r := e.manage(e.owner, handlers.VillageManageRequest{Building: id}); r.Refusal == nil {
		t.Error("a resident managed the settlement's building")
	}
	v, r := e.manage(head, handlers.VillageManageRequest{Building: id})
	if r.Refusal != nil || !v.Public || !v.CanManage || v.Function.Code != "dwelling" {
		t.Fatalf("the head opens the building: %+v %+v", v.Function, r.Refusal)
	}
	if v.Upgrade != nil || len(v.Functions) != 0 {
		t.Error("a settlement's own building offers a level or a change of use")
	}
	s0, t0 := stock(), e.supportTreasuryOf()
	e.confirmAct(head, handlers.VillageManageRequest{Building: id, Action: village.LotActionAdd, Code: "storeroom", N: "1"})
	if s0-stock() != 2 {
		t.Errorf("the stock fell by %d, want 2", s0-stock())
	}
	e.build(head, id)
	if got := e.scalar(`SELECT count FROM building_modules WHERE building_ref_id = $1::uuid AND module_kind = 'storeroom'`, id); got != 2 {
		t.Errorf("storerooms %d, want 2", got)
	}
	// the wage is the treasury's: SUP, or the town's own money where it is chartered and the treasury holds the units
	if e.supportTreasuryOf() >= t0 && e.scalar(`SELECT count(*) FROM local_payments WHERE settlement_id = $1::uuid AND direction = 'pay'`, e.cityID) == 0 {
		t.Error("the treasury paid no wage")
	}
	t.Cleanup(func() { _, _ = e.pool.Raw().Exec(context.Background(), `DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`, e.cityID) })
	e.verifyLots()
}
