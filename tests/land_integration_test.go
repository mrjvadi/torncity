//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Trees and rocks as land (ADR 0065): the woodcutter fells a real tree of the commons within reach, stops when there is none (after
// the grace), takes an ordered lot first; the forester plants a sapling; a lot with trees or rocks cannot be built on; the books agree.

type landEnv struct {
	*laborEnv
	camp, lodge string
	rules       application.LandRules
}

func newLandEnv(t *testing.T) *landEnv {
	t.Helper()
	landBoost = true
	t.Cleanup(func() { landBoost = false })
	l := newLaborEnv(t)
	rules := application.LandRules{RuleAt: time.Now().UTC().Add(-48 * time.Hour), GraceDays: 0, Ring: 3, RegrowEvery: 120 * time.Hour,
		SaplingFor: 24 * time.Hour, FellRadius: 4, QuarryRadius: 3, RockShifts: 2}
	l.village.WithLand(rules)
	e := &landEnv{laborEnv: l, rules: rules}
	t.Cleanup(func() {
		ctx := testCtx(t)
		_, _ = l.pool.Raw().Exec(ctx, `DELETE FROM settlement_land WHERE settlement_id = $1::uuid`, l.cityID)
		_, _ = l.pool.Raw().Exec(ctx, `DELETE FROM settlement_saplings WHERE settlement_id = $1::uuid`, l.cityID)
		_, _ = l.pool.Raw().Exec(ctx, `DELETE FROM game_actions WHERE reference_type = 'settlement_shift'`)
	})
	e.camp = e.insertBuilding("woodcutter_camp", 6, 2)
	e.lodge = e.insertBuilding("forester_lodge", 6, 3)
	return e
}

// insertBuilding puts a finished building of a type on a lot (the commons ring lot beyond the grid, so the camps stand by the woods).
func (e *landEnv) insertBuilding(code string, x, y int) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'complete', now(), now())`, id, e.cityID, code, x, y); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// view is the land as the server computes it now.
func (e *landEnv) view() *application.LandView {
	e.t.Helper()
	ctx := testCtx(e.t)
	_, w, err := e.cache.Active(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	var out *application.LandView
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		s, err := tx.Settlements().ByID(ctx, e.cityID)
		if err != nil {
			return err
		}
		def, _ := loadTestContent(e.t).Land()
		deltas, err := tx.Land().Rows(ctx, e.cityID)
		if err != nil {
			return err
		}
		saplings, err := tx.Land().Saplings(ctx, e.cityID)
		if err != nil {
			return err
		}
		blds, err := tx.SettlementBuildings().List(ctx, e.cityID)
		if err != nil {
			return err
		}
		occupied := map[land.Pos]bool{}
		for _, b := range blds {
			occupied[land.Pos{X: b.LotX, Y: b.LotY}] = true
		}
		out = application.BuildLand(w, s, 5, nil, occupied, def, e.rules, deltas, saplings, time.Now().UTC())
		return nil
	}); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *landEnv) work(who *application.Player, building string) (kind string) {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(who, "settlement.work", "work"), handlers.VillageWorkRequest{ID: building}))
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.Screen == screens.ScreenVillageRefusal {
		return refusalKind(e.t, resp.View)
	}
	return ""
}

func (e *landEnv) endShifts(building string) {
	e.t.Helper()
	e.clock.Advance(3 * time.Hour)
	for _, s := range e.workingShifts(building) {
		e.end(s)
	}
}

func (e *landEnv) lastShiftLot(building string) (kind string, x, y int) {
	e.t.Helper()
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT land_kind, COALESCE(land_x, 0), COALESCE(land_y, 0) FROM settlement_shifts WHERE building_id = $1::uuid ORDER BY started_at DESC LIMIT 1`,
		building).Scan(&kind, &x, &y); err != nil {
		e.t.Fatal(err)
	}
	return kind, x, y
}

func TestTheWoodcutterFellsRealTreesAndStopsWithoutThem(t *testing.T) {
	e := newLandEnv(t)
	worker := e.resident()

	// a shift fells one tree of the commons within four lots of the camp
	if got := e.work(worker, e.camp); got != "" {
		t.Fatalf("the shift should start: refused as %q", got)
	}
	kind, x, y := e.lastShiftLot(e.camp)
	if kind != "tree" || land.Dist(land.Pos{X: 6, Y: 2}, land.Pos{X: x, Y: y}) > 4 || land.Ring(land.Pos{X: x, Y: y}, 5) == 0 {
		t.Fatalf("a tree of the commons in reach: %q at (%d,%d)", kind, x, y)
	}
	if n := e.scalar(`SELECT trees_cut FROM settlement_land WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`, e.cityID, x, y); n != 1 {
		t.Fatalf("one tree is cut on the lot: %d", n)
	}
	e.endShifts(e.camp)

	// the forest thins: with every tree in reach cut there is nothing to fell
	for yy := -4; yy <= 8; yy++ {
		for xx := -4; xx <= 10; xx++ {
			if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, trees_cut, regrow_anchor, updated_at) VALUES ($1::uuid, $2, $3, 99, now(), now())
				ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET trees_cut = 99`, e.cityID, xx, yy); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := e.work(worker, e.camp); got != "no_trees_in_reach" {
		t.Errorf("no tree in reach: refused as %q, want no_trees_in_reach", got)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'working'`, e.camp); n != 0 {
		t.Errorf("a refused shift left %d running", n)
	}

	// a camp that stood before the model keeps cutting through the grace, with no lot touched
	graced := e.rules
	graced.GraceDays = 30
	e.village.WithLand(graced)
	if got := e.work(worker, e.camp); got != "" {
		t.Fatalf("in the grace the camp cuts as it did: refused as %q", got)
	}
	if kind, _, _ := e.lastShiftLot(e.camp); kind != "" {
		t.Errorf("a shift in the grace touches no lot: %q", kind)
	}
	e.village.WithLand(e.rules)
	e.endShifts(e.camp)

	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := v.VillageInvariants; i.LandShiftsWithoutLot != 0 || i.ClearingLedger != i.ClearingRows {
		t.Errorf("the books do not agree: %+v", i)
	}
}

func TestAnOrderedLotIsFelledFirstAndTheForesterPlants(t *testing.T) {
	e := newLandEnv(t)
	worker := e.resident()
	view := e.view()
	camp := land.Pos{X: 6, Y: 2}

	// the lot farthest in reach with trees on it is ordered cleared: the next shift takes it before the near ones
	var far land.Pos
	for _, p := range view.Order() {
		l := view.Lots[p]
		if l.Commons && !l.Occupied && l.Trees > 0 && land.Dist(camp, p) == 4 && land.Dist(land.Pos{X: 6, Y: 3}, p) <= 4 {
			far = p
			break
		}
	}
	if far == (land.Pos{}) {
		t.Skip("no wooded lot four lots from the camp in this world")
	}
	resp, err := rrc(e.village.ClearOrder(testCtx(t), e.as(e.head, "settlement.clear.order", "clear.order"), handlers.VillageClearRequest{X: itoa(int64(far.X)), Y: itoa(int64(far.Y)), What: "trees"}))
	if err != nil || resp.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("the head orders the clearing: %v %+v", err, resp)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_land WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND clear_trees`, e.cityID, far.X, far.Y); n != 1 {
		t.Fatal("the order is stored on the lot")
	}
	if got := e.work(worker, e.camp); got != "" {
		t.Fatalf("the shift should start: %q", got)
	}
	if kind, x, y := e.lastShiftLot(e.camp); kind != "tree" || (land.Pos{X: x, Y: y}) != far {
		t.Errorf("the ordered lot comes first: worked (%d,%d), ordered %v", x, y, far)
	}
	e.endShifts(e.camp)

	// a stranger to the office cannot order a clearing of the commons
	if resp, err := rrc(e.village.ClearOrder(testCtx(t), e.as(worker, "settlement.clear.order", "clear.order"), handlers.VillageClearRequest{X: itoa(int64(far.X)), Y: itoa(int64(far.Y))})); err != nil || resp.Screen != screens.ScreenVillageRefusal {
		t.Errorf("a resident without land.clear is refused: %v %q", err, resp.Screen)
	}

	// the forester plants on a cut lot of the commons in reach
	if got := e.work(worker, e.lodge); got != "" {
		t.Fatalf("the forester's shift should start: %q", got)
	}
	if kind, _, _ := e.lastShiftLot(e.lodge); kind != "sapling" {
		t.Errorf("the forester plants: %q", kind)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_saplings WHERE settlement_id = $1::uuid`, e.cityID); n != 1 {
		t.Errorf("%d saplings planted, want 1", n)
	}
	e.endShifts(e.lodge)
}

func TestALotWithTreesOrRocksCannotBeBuiltOn(t *testing.T) {
	e := newLandEnv(t)
	view := e.view()
	var lot land.Pos
	found := false
	for _, p := range view.Order() {
		if l := view.Lots[p]; l.Ring == 0 && l.Obstructed && !l.Occupied {
			lot, found = p, true
			break
		}
	}
	if !found {
		t.Skip("the founding grid of this world is clear")
	}
	resp, err := rrc(e.village.Place(testCtx(t), e.as(e.head, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: "road", Lot: screens.LotToken(lot.X, lot.Y, false), Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatal(err)
	}
	if got := refusalKind(t, resp.View); got != "obstructed" {
		t.Fatalf("a building on a lot with trees is refused as obstructed, not %q", got)
	}
	if n := e.scalar(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`, e.cityID, lot.X, lot.Y); n != 0 {
		t.Errorf("a refused building left %d rows", n)
	}
}
