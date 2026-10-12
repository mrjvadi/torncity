//go:build integration

// A shift start that is refused must leave nothing behind (the refusal that committed: the inputs, the finish action and the meal
// of a refused NPC refill stayed with no shift). These tests prove the start is a savepoint, the verifier sees the leftovers, and
// the audited repair puts them right exactly once.
package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// cleanSettlementGoods removes what a test left in the append-only item journal and the kitchen of one settlement.
func cleanSettlementGoods(t *testing.T, l *laborEnv) {
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
}

func grantWarehouse(t *testing.T, l *laborEnv, item string, qty int64) {
	t.Helper()
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{
			ID: newUUID(t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
}

// Two NPC refills race for the last place of a workplace: one gets it, the other is refused, and the refused one leaves no meal,
// no input movement and no finish action behind.
func TestRefusedShiftStartLeavesNoTrace(t *testing.T) {
	l := newLaborEnv(t)
	l.alignToLocalMorning()
	cleanSettlementGoods(t, l)
	ctx := testCtx(t)
	camp := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, camp).Scan(&jobID); err != nil {
		t.Fatalf("no production job: %v", err)
	}
	grantWarehouse(t, l, "wheat", 20)
	// every place but one is taken by repair hands (they hold a place but are not the production crew), so that two refills can only share one
	def, ok := loadTestContent(t).SettlementBuildingDef("woodcutter_camp")
	if !ok || def.Workers < 2 {
		t.Fatalf("the camp should have at least two places: %+v", def.Workers)
	}
	for i := 0; i < def.Workers-1; i++ {
		if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_shifts (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed,
			game_action_id, started_at, finish_at, kind, worker_kind) VALUES ($1::uuid, $2::uuid, $3::uuid, NULL, 'working', 0, 0, '{}', '{}', $4::uuid, $5, $5::timestamptz + interval '1 hour', 'repair', 'npc')`,
			newUUID(t), l.cityID, camp, newUUID(t), l.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"}))
		}()
	}
	wg.Wait()
	// one more refill, in sequence: the place is taken, it is refused
	_, _ = rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"}))

	if got := l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND worker_kind = 'npc' AND kind = 'production' AND status = 'working'`, camp); got != 1 {
		t.Fatalf("exactly one NPC should hold the last place, %d do", got)
	}
	if got := l.scalar(`SELECT count(*) FROM settlement_meals WHERE settlement_id = $1::uuid AND shift_id NOT IN (SELECT id FROM settlement_shifts)`, l.cityID); got != 0 {
		t.Errorf("%d meal openings have no shift", got)
	}
	if got := l.scalar(`SELECT count(*) FROM item_movements WHERE reason = 'production_input' AND reference_type = 'settlement_shift'
		AND from_org = $1::uuid AND reference_id NOT IN (SELECT id FROM settlement_shifts)`, l.cityID); got != 0 {
		t.Errorf("%d production inputs have no shift", got)
	}
	if got := l.scalar(`SELECT count(*) FROM game_actions WHERE action_type = 'settlement_work' AND status = 'scheduled'
		AND reference_id NOT IN (SELECT id FROM settlement_shifts) AND reference_id IN (SELECT reference_id FROM game_actions WHERE reference_type = 'settlement_shift')`); got != 0 {
		t.Errorf("%d finish actions have no shift", got)
	}
	// the kitchen eats exactly what the shifts ate
	if got := l.scalar(`SELECT COALESCE(SUM(eaten_points), 0)::bigint FROM settlement_kitchen WHERE settlement_id = $1::uuid`, l.cityID) -
		l.scalar(`SELECT COALESCE(SUM(meal_points), 0)::bigint FROM settlement_shifts WHERE settlement_id = $1::uuid`, l.cityID); got != 0 {
		t.Errorf("the kitchen ate %d points more than the shifts did", got)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := v.VillageInvariants; i.WorkNodes && (i.MealsWithoutShift != 0 || i.InputMovesWithoutShift != 0 || i.ActionsWithoutShift != 0) {
		t.Errorf("the verifier sees leftovers of a refused start: meals %d, inputs %d, actions %d", i.MealsWithoutShift, i.InputMovesWithoutShift, i.ActionsWithoutShift)
	}
}

// The repair gives back what a refused start left, once, audited, and writes nothing in a dry run.
func TestRepairOrphanShifts(t *testing.T) {
	l := newLaborEnv(t)
	cleanSettlementGoods(t, l)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: l.pool, Language: testDefaultLanguage}
	actor := operator.Actor{Name: "tester", Reason: "repair test", At: time.Now()}
	ghost := newUUID(t)
	actionID := newUUID(t)
	grantWarehouse(t, l, "wheat", 10)
	// the leftovers of a refused start: 2 wheat moved for a shift that never was, a meal opening of 1 wheat (2 points) eaten by it,
	// and its finish action
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "wheat", Qty: 2, FromOrg: application.SettlementOrg(l.cityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemProductionInput, ReferenceType: application.SettlementShiftItemReference, ReferenceID: ghost, At: time.Now().UTC()}); err != nil {
			return err
		}
		mealID := newUUID(t)
		if err := tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "wheat", Qty: 1, FromOrg: application.SettlementOrg(l.cityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemMealEaten, ReferenceType: application.MealReference, ReferenceID: mealID, At: time.Now().UTC()}); err != nil {
			return err
		}
		if _, err := tx.SettlementTreasury().Pot(ctx, l.cityID); err != nil {
			return err
		}
		return tx.SettlementTreasury().Eat(ctx, l.cityID, ghost, 2, []application.MealOpening{{ID: mealID, Item: "wheat", Units: 1, PointsEach: 2}}, time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO game_actions (id, action_type, actor_type, reference_type, reference_id, payload, status, started_at, finish_at)
		VALUES ($1::uuid, 'settlement_work', 'system', 'settlement_shift', $2::uuid, '{}', 'scheduled', now(), now() + interval '1 hour')`, actionID, ghost); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = l.pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE id = $1::uuid`, actionID) })
	wheat := func() int64 {
		return l.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'wheat'`, l.cityID)
	}
	verify := func() postgres.VillageInvariants {
		v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		return v.VillageInvariants
	}
	if i := verify(); !i.WorkNodes || i.MealsWithoutShift != 1 || i.InputMovesWithoutShift != 1 || i.ActionsWithoutShift != 1 {
		t.Fatalf("the verifier should see one of each: %+v", i)
	}
	if wheat() != 7 {
		t.Fatalf("the warehouse holds %d wheat, want 7", wheat())
	}
	// a dry run writes nothing
	rep, err := ops.RepairOrphanShifts(ctx, actor, false)
	if err != nil || rep.Applied || len(rep.Refunds) != 1 || len(rep.Actions) != 1 || len(rep.Kitchens) != 1 || rep.Kitchens[0].GivenBack != 2 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	if wheat() != 7 || verify().InputMovesWithoutShift != 1 {
		t.Fatalf("a dry run changed something")
	}
	audits := func() int64 {
		return l.scalar(`SELECT count(*) FROM audit_logs WHERE action = 'settlement.repair_orphan_shifts' AND reason = 'repair test'`)
	}
	a0 := audits()
	if rep, err = ops.RepairOrphanShifts(ctx, actor, true); err != nil || !rep.Applied {
		t.Fatalf("apply: %+v %v", rep, err)
	}
	if i := verify(); i.MealsWithoutShift != 0 || i.InputMovesWithoutShift != 0 || i.ActionsWithoutShift != 0 || !i.WorkNodesOK() {
		t.Errorf("after the repair the verifier still sees leftovers: %+v", i)
	}
	if wheat() != 9 {
		t.Errorf("the 2 wheat of the input should be back (the meal's wheat stays in the pot): %d in the warehouse", wheat())
	}
	if got := l.scalar(`SELECT pot FROM settlement_kitchen WHERE settlement_id = $1::uuid`, l.cityID); got != 2 {
		t.Errorf("the pot should hold the 2 points again: %d", got)
	}
	var status string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT status FROM game_actions WHERE id = $1::uuid`, actionID).Scan(&status); err != nil || status != "cancelled" {
		t.Errorf("the finish action should be cancelled: %q %v", status, err)
	}
	if audits() != a0+1 {
		t.Errorf("the repair should write one audit row")
	}
	// a second run finds nothing and writes nothing
	rep, err = ops.RepairOrphanShifts(ctx, actor, true)
	if err != nil || len(rep.Refunds)+len(rep.Actions)+len(rep.Kitchens) != 0 || wheat() != 9 || audits() != a0+1 {
		t.Errorf("a second run changed something: %+v %v wheat %d", rep, err, wheat())
	}
}
