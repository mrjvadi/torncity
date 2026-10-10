//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// cutAll takes every tree and rock of an area off the table.
func (e *landEnv) cutAll(x0, y0, x1, y1 int) {
	e.t.Helper()
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, trees_cut, rocks_cut, regrow_anchor, updated_at) VALUES ($1::uuid, $2, $3, 99, 99, now(), now())
				ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET trees_cut = 99, rocks_cut = 99`, e.cityID, x, y); err != nil {
				e.t.Fatal(err)
			}
		}
	}
}

func (e *landEnv) landRow(p land.Pos) (cut, rocksCut, rockWork int64) {
	e.t.Helper()
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT trees_cut, rocks_cut, rock_work FROM settlement_land WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`,
		e.cityID, p.X, p.Y).Scan(&cut, &rocksCut, &rockWork); err != nil {
		e.t.Fatal(err)
	}
	return cut, rocksCut, rockWork
}

// A pit breaks a field rock in two shifts and then works its face when no rock is left in reach.
func TestAQuarryBreaksAFieldRockInTwoShiftsThenWorksItsFace(t *testing.T) {
	e := newLandEnv(t)
	worker := e.resident()
	pit := e.insertBuilding("small_pit", 7, 2)
	pitPos := land.Pos{X: 7, Y: 2}
	view := e.view()
	var rock land.Pos
	found := false
	for _, p := range view.Order() {
		if l := view.Lots[p]; l.Commons && !l.Occupied && l.Rocks > 0 && land.Dist(pitPos, p) <= 3 {
			rock, found = p, true
			break
		}
	}
	if !found {
		t.Skip("no field rock within three lots of the pit in this world")
	}
	if resp, err := rrc(e.village.ClearOrder(testCtx(t), e.as(e.head, "settlement.clear.order", "clear.order"), handlers.VillageClearRequest{X: itoa(int64(rock.X)), Y: itoa(int64(rock.Y)), What: "rocks"})); err != nil || resp.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("the head orders the rock cleared: %v", err)
	}
	for shift := 1; shift <= 2; shift++ {
		if got := e.work(worker, pit); got != "" {
			t.Fatalf("shift %d should start: %q", shift, got)
		}
		if kind, x, y := e.lastShiftLot(pit); kind != "rock" || (land.Pos{X: x, Y: y}) != rock {
			t.Fatalf("shift %d works the ordered rock: %q (%d,%d)", shift, kind, x, y)
		}
		_, cut, work := e.landRow(rock)
		if shift == 1 && (cut != 0 || work != 1) {
			t.Errorf("after the first shift the rock is half broken: rocks_cut %d rock_work %d", cut, work)
		}
		if shift == 2 && (cut != 1 || work != 0) {
			t.Errorf("after the second shift the rock is gone: rocks_cut %d rock_work %d", cut, work)
		}
		e.endShifts(pit)
	}

	// no rock left in reach: the pit works its face and touches no lot
	e.cutAll(2, -2, 12, 8)
	if got := e.work(worker, pit); got != "" {
		t.Fatalf("a pit with no rock in reach works its face: %q", got)
	}
	if kind, _, _ := e.lastShiftLot(pit); kind != "" {
		t.Errorf("a shift on the face touches no lot: %q", kind)
	}
	e.endShifts(pit)
}

// Trees come back by timestamps alone: one tree per 120 hours on a wooded lot of the commons.
func TestTreesGrowBackOnTheCommonsByTheClock(t *testing.T) {
	e := newLandEnv(t)
	view := e.view()
	var lot land.Pos
	var base int
	for _, p := range view.Order() {
		if l := view.Lots[p]; l.Commons && !l.Occupied && l.Trees >= 2 && !l.Ground.Water {
			lot, base = p, l.Trees
			break
		}
	}
	if base == 0 {
		t.Skip("no commons lot with two trees in this world")
	}
	for _, c := range []struct {
		ago  time.Duration
		want int
	}{{70 * time.Hour, 0}, {150 * time.Hour, 1}, {250 * time.Hour, 2}} {
		if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, trees_cut, regrow_anchor, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, now())
			ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET trees_cut = $4, regrow_anchor = $5`, e.cityID, lot.X, lot.Y, base, time.Now().UTC().Add(-c.ago)); err != nil {
			t.Fatal(err)
		}
		if got := e.view().Lots[lot].Trees; got != c.want {
			t.Errorf("%s after the cut %d of %d trees stand again, want %d", c.ago, got, base, c.want)
		}
	}
}

// A crew clears a citizen's lot at his order: he pays the wage of the shift to the treasury and the goods go to his home store.
func TestACitizenLotIsClearedAtHisOrderAndHePaysForIt(t *testing.T) {
	e := newLandEnv(t)
	owner := e.resident()
	worker := e.resident()
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, owner.ID) })
	grantCash(t, e.pool, owner.ID, 5000)

	// his workplace gives him a home store to receive the wood
	house := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'carpentry_workshop_own', 9, 9, 'complete', now(), now())`, house, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 2000, now())`, house, e.cityID, owner.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_lots WHERE settlement_id = $1::uuid`, e.cityID)
	})

	// a lot of the grid with trees on it, in reach of the camp, becomes his
	view := e.view()
	camp := land.Pos{X: 6, Y: 2}
	var lot land.Pos
	found := false
	for _, p := range view.Order() {
		if l := view.Lots[p]; l.Ring == 0 && !l.Occupied && l.Trees > 0 && land.Dist(camp, p) <= 4 {
			lot, found = p, true
			break
		}
	}
	if !found {
		t.Skip("no wooded lot of the grid in reach of the camp in this world")
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_lots (id, settlement_id, lot_x, lot_y, tenure, owner_id, price, ledger_transaction_id, acquired_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 'freehold', $5::uuid, 100, $6::uuid, now())`, newUUID(t), e.cityID, lot.X, lot.Y, owner.ID, newUUID(t)); err != nil {
		t.Fatal(err)
	}

	// a resident who is not the owner cannot order it; the owner can
	if resp, err := rrc(e.village.ClearOrder(testCtx(t), e.as(worker, "settlement.clear.order", "clear.order"), handlers.VillageClearRequest{X: itoa(int64(lot.X)), Y: itoa(int64(lot.Y))})); err != nil || resp.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a stranger cannot order a citizen's lot cleared: %v %q", err, resp.Screen)
	}
	if resp, err := rrc(e.village.ClearOrder(testCtx(t), e.as(owner, "settlement.clear.order", "clear.order"), handlers.VillageClearRequest{X: itoa(int64(lot.X)), Y: itoa(int64(lot.Y)), What: "trees"})); err != nil || resp.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("the owner orders his lot cleared: %v", err)
	}

	cash0 := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID)
	if got := e.work(worker, e.camp); got != "" {
		t.Fatalf("the shift should start: %q", got)
	}
	if kind, x, y := e.lastShiftLot(e.camp); kind != "tree" || (land.Pos{X: x, Y: y}) != lot {
		t.Fatalf("the crew works his lot: %q (%d,%d), want %v", kind, x, y, lot)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND land_owner = $2::uuid`, e.camp, owner.ID); n != 1 {
		t.Errorf("the shift names the owner of the lot: %d", n)
	}
	cash1 := cashBalance(t, e.pool, application.AccountPlayerCash, owner.ID)
	if cash1 >= cash0 {
		t.Errorf("the owner pays the crew: cash %d -> %d", cash0, cash1)
	}
	if n := e.scalar(`SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'clearing_fee' AND amount > 0`); n != cash0-cash1 {
		t.Errorf("the treasury got %d, the owner paid %d", n, cash0-cash1)
	}
	e.endShifts(e.camp)
	if n := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks WHERE player_id = $1::uuid AND holding = 'home'`, owner.ID); n <= 0 {
		t.Errorf("the wood goes to the owner's home store: %d units", n)
	}

	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := v.VillageInvariants; i.ClearingLedger == 0 || i.ClearingLedger != i.ClearingRows || i.LandShiftsWithoutLot != 0 {
		t.Errorf("the clearing fee is in the books once: %+v", i)
	}
}
