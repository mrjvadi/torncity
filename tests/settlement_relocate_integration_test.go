//go:build integration

// Integration test of `admin settlement relocate` (docs/adr/0028 section 3.2):
// a village founded on a grid that is mostly water is moved to dry ground only
// while it holds nothing but its founding kit, in one transaction that also
// writes the audit row and the event that makes clients refetch the layout.
package tests

import (
	"errors"
	"strings"
	"testing"
	"time"

	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

func TestSettlementRelocate(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	groupMeta, _ := e.group(t)
	foundVillage(t, e.pool, e.h, groupMeta)

	var id string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, groupMeta.TelegramChatID).Scan(&id); err != nil {
		t.Fatalf("reading the founded village: %v", err)
	}
	_, w, err := e.cache.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := wsettle.SiteRules{GridLots: 5, MinBuildableShareBps: 10000, MaxShiftLots: 3}
	params := wsettle.Params{MinSpawnDistanceKm: 30, ThreatRadiusKm: 150, SearchMaxCells: 2000, SearchMaxAttempts: 50,
		ExcludedBiomes: []string{"polar_ice"}, MaxAbsLatitudeDeg: 70, Site: rules}

	// Put the village where the old rule could have: a land cell whose
	// untouched grid is mostly water, with the kit laid on it as it was.
	var wet int32 = -1
	for i := range w.Cells {
		c := w.Cells[i]
		if c.IsOcean || c.IsLake {
			continue
		}
		if rep := wsettle.EvaluateSite(w, int32(i), 0, 0, 5); rep.KitComplete() && rep.BuildableLots < rep.TotalLots {
			wet = int32(i)
			break
		}
	}
	if wet < 0 {
		t.Skip("this planet has no land cell whose grid is mostly water")
	}
	pt := w.Cells[wet].Point
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE cities SET world_cell_id = $2, grid_shift_x = 0, grid_shift_y = 0 WHERE id = $1::uuid`, id, wet); err != nil {
		t.Fatal(err)
	}
	// Two-step move: the lot index is unique per lot.
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET lot_x = lot_x + 100 WHERE settlement_id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	for _, b := range wsettle.PlaceFoundingKit(w, pt.LatDeg, pt.LonDeg, 5) {
		if _, err := e.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET lot_x = $3, lot_y = $4 WHERE settlement_id = $1::uuid AND type_code = $2`,
			id, b.TypeCode, b.LotX, b.LotY); err != nil {
			t.Fatal(err)
		}
	}
	var kitIDs string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT string_agg(id::text, ',' ORDER BY id) FROM settlement_buildings WHERE settlement_id = $1::uuid`, id).Scan(&kitIDs); err != nil {
		t.Fatal(err)
	}

	ops := postgres.NewSettlementOps(e.pool)
	relocate := func() (postgres.RelocationDone, error) {
		return ops.Relocate(ctx, postgres.Relocation{
			SettlementID: id, Actor: "integration-test", Reason: "test relocation", At: time.Now().UTC(),
			KitTypes: postgres.FoundingKitTypes(), Plan: postgres.RelocationPlan(w, params),
		})
	}
	cellNow := func() (cell, sx, sy int) {
		if err := e.pool.Raw().QueryRow(ctx, `SELECT world_cell_id, grid_shift_x, grid_shift_y FROM cities WHERE id = $1::uuid`, id).Scan(&cell, &sx, &sy); err != nil {
			t.Fatal(err)
		}
		return
	}

	// 1. A player's building: refused, and nothing moved.
	var buildingID string
	if err := e.pool.Raw().QueryRow(ctx,
		`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		 SELECT gen_random_uuid(), $1::uuid, 'watch_hut', g.x, gy.y, 'complete', now(), now()
		   FROM generate_series(0, 4) AS g(x) CROSS JOIN LATERAL (SELECT generate_series(0, 4) AS y) AS gy(y)
		  WHERE NOT EXISTS (SELECT 1 FROM settlement_buildings b WHERE b.settlement_id = $1::uuid AND b.lot_x = g.x AND b.lot_y = gy.y)
		  ORDER BY g.x, gy.y LIMIT 1
		 RETURNING id::text`, id).Scan(&buildingID); err != nil {
		t.Fatalf("placing a player's building: %v", err)
	}
	_, err = relocate()
	var refused postgres.ErrRelocateRefused
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "beyond the founding kit") || !strings.Contains(refused.Reason, "watch_hut") {
		t.Fatalf("a village with a player's building was not refused clearly: %v", err)
	}
	if c, _, _ := cellNow(); int32(c) != wet {
		t.Errorf("a refused relocation moved the village to cell %d", c)
	}

	// 2. Construction in progress: refused too.
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET status = 'building', completed_at = NULL, finish_at = now() + interval '1 hour' WHERE id = $1::uuid`, buildingID); err != nil {
		t.Fatal(err)
	}
	if _, err = relocate(); !errors.As(err, &refused) || !strings.Contains(refused.Reason, "construction is in progress") {
		t.Fatalf("construction in progress was not refused clearly: %v", err)
	}

	// 3. The building is cancelled: only the kit remains, and the relocation goes through.
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET status = 'cancelled', cancelled_at = now(), completed_at = NULL WHERE id = $1::uuid`, buildingID); err != nil {
		t.Fatal(err)
	}
	done, err := relocate()
	if err != nil {
		t.Fatalf("relocating a village that holds only its kit: %v", err)
	}
	cell, sx, sy := cellNow()
	if int32(cell) != done.After.CellID || sx != done.After.ShiftX || sy != done.After.ShiftY {
		t.Errorf("the village is at cell %d slide (%d,%d), the result says %+v", cell, sx, sy, done.After)
	}
	if sx == 0 && sy == 0 && int32(cell) == wet {
		t.Errorf("the village did not move")
	}

	// The layout after the move: every kit building stands on buildable lots
	// of the grid the readers now sample, and the grid meets the rules.
	rep := wsettle.EvaluateSite(w, int32(cell), sx, sy, 5)
	if !rep.Meets(rules) {
		t.Errorf("the relocated grid does not meet the rules: %+v", rep)
	}
	ncell := w.Cells[cell].Point
	lat, lon := wsettle.GridCentre(w, ncell.LatDeg, ncell.LonDeg, sx, sy)
	grid := wsettle.SampleGridDetail(w, lat, lon, 5, int32(cell))
	rows, err := e.pool.Raw().Query(ctx, `SELECT type_code, lot_x, lot_y FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'complete'`, id)
	if err != nil {
		t.Fatal(err)
	}
	standing := 0
	for rows.Next() {
		var code string
		var x, y int
		if err := rows.Scan(&code, &x, &y); err != nil {
			t.Fatal(err)
		}
		standing++
		fw, fh := 1, 1
		for _, k := range wsettle.FoundingKitBuildings {
			if k.TypeCode == code {
				fw, fh = k.W, k.H
			}
		}
		for yy := y; yy < y+fh; yy++ {
			for xx := x; xx < x+fw; xx++ {
				if !grid.Lots[yy][xx].Buildable {
					t.Errorf("%s stands on the unbuildable lot (%d,%d) after the relocation", code, xx, yy)
				}
			}
		}
	}
	rows.Close()
	if standing != len(wsettle.FoundingKitBuildings) {
		t.Errorf("%d kit buildings stand after the move, want %d", standing, len(wsettle.FoundingKitBuildings))
	}
	var after string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT string_agg(id::text, ',' ORDER BY id) FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'complete'`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	for _, k := range strings.Split(after, ",") {
		if !strings.Contains(kitIDs, k) {
			t.Errorf("the kit's rows were recreated, not moved (%s)", k)
		}
	}

	// The audit row and the event for clients.
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'settlement.relocate' AND target_id = $1::uuid AND actor = 'integration-test' AND reason = 'test relocation'`, id); n != 1 {
		t.Errorf("%d audit rows for the relocation, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox WHERE subject LIKE 'game.event.settlement.relocated.%' AND payload->>'settlement_id' = $1`, id); n != 1 {
		t.Errorf("%d settlement.relocated events, want 1", n)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM audit_logs WHERE action = 'settlement.relocate' AND target_id = $1::uuid`, id)
	})

	// 4. Relocating a village that already meets the rules keeps it put (idempotent).
	again, err := relocate()
	if err != nil {
		t.Fatalf("second relocation: %v", err)
	}
	if again.After.CellID != done.After.CellID || again.After.ShiftX != done.After.ShiftX || again.After.ShiftY != done.After.ShiftY {
		t.Errorf("a valid village moved again: %+v -> %+v", done.After, again.After)
	}
}
