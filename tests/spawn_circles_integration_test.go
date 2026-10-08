//go:build integration

package tests

import (
	"github.com/mrjvadi/torncity/internal/settlementcfg"
	"math"
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/config"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Spawn circles (ADR 0028 section 3.2, amendment 2026-10-09): new settlements are founded close together, one circle
// at a time. The test planet has 4000 cells (about 360 km apart), so the circles here are scaled to it.

func testCircleRules() wsettle.CircleParams {
	return wsettle.CircleParams{RadiusKm: 900, Capacity: 3, FillBandKm: 300, MaxAdvance: 200}
}

// foundSome founds n villages one after another, each from its own group, and returns their cells.
func (e *foundingEnv) foundSome(t *testing.T, n int) []int32 {
	t.Helper()
	var cells []int32
	for i := 0; i < n; i++ {
		meta, _ := e.group(t)
		resp := foundVillage(t, e.pool, e.h, meta)
		if resp == nil {
			t.Fatal("no answer")
		}
		var cell int32
		if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT world_cell_id FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cell); err != nil {
			t.Fatalf("founding %d left no village: %v", i, err)
		}
		cells = append(cells, cell)
	}
	return cells
}

func (e *foundingEnv) world(t *testing.T) (*worldgen.World, string) {
	t.Helper()
	row, w, err := e.cache.Active(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	return w, row.ID
}

func km(w *worldgen.World, a, b int32) float64 {
	pa, pb := w.Cells[a].Point, w.Cells[b].Point
	return math.Acos(math.Max(-1, math.Min(1, pa.X*pb.X+pa.Y*pb.Y+pa.Z*pb.Z))) * w.Params.PlanetRadiusKm
}

type circleRow struct {
	idx            int
	lat, lon, rad  float64
	capacity, took int
	reason         string
}

func (e *foundingEnv) circles(t *testing.T, worldID string) []circleRow {
	t.Helper()
	rows, err := e.pool.Raw().Query(testCtx(t), `SELECT idx, lat_deg, lon_deg, radius_km, capacity, taken, COALESCE(close_reason, '')
		FROM spawn_circles WHERE world_id = $1::uuid ORDER BY idx`, worldID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []circleRow
	for rows.Next() {
		var c circleRow
		if err := rows.Scan(&c.idx, &c.lat, &c.lon, &c.rad, &c.capacity, &c.took, &c.reason); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// Foundings fill the first circle, then the next opens beside it; spacing holds and nothing stands in the sea.
func TestFoundingsFillACircleThenTheNextOpens(t *testing.T) {
	e := newFoundingEnv(t)
	e.h.WithSpawnCircles(testCircleRules())
	cells := e.foundSome(t, 7)
	w, worldID := e.world(t)
	for i, a := range cells {
		if w.Cells[a].IsOcean || w.Cells[a].IsLake {
			t.Errorf("village %d stands on water", i)
		}
		for _, b := range cells[i+1:] {
			if a == b {
				t.Fatalf("two villages on one cell")
			}
			if d := km(w, a, b); d < 30 {
				t.Errorf("two villages %.1f km apart, minimum 30", d)
			}
		}
	}
	cs := e.circles(t, worldID)
	if len(cs) < 2 {
		t.Fatalf("7 foundings of capacity 3 opened %d circle(s)", len(cs))
	}
	if cs[0].took != 3 || cs[0].reason != wsettle.CircleFull && cs[0].reason != wsettle.CircleNoRoom {
		t.Errorf("the first circle: %+v, want it closed with 3 taken", cs[0])
	}
	total := 0
	for _, c := range cs {
		if c.took > c.capacity {
			t.Errorf("circle %d took %d over capacity %d", c.idx, c.took, c.capacity)
		}
		total += c.took
	}
	if total < 7 {
		t.Errorf("the circles count %d foundings, 7 were made", total)
	}
	// the newest villages stand beside the first ones, not across the planet: within a few circle radii of the first
	for _, c := range cells[1:] {
		if d := km(w, cells[0], c); d > 8*testCircleRules().RadiusKm {
			t.Errorf("a village stands %.0f km from the first, circles of %.0f km should keep them together", d, testCircleRules().RadiusKm)
		}
	}
	// the cursor follows the circle in use
	var cur int
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT current_idx FROM spawn_circle_cursor WHERE world_id = $1::uuid`, worldID).Scan(&cur); err != nil {
		t.Fatal(err)
	}
	if cur != cs[len(cs)-1].idx {
		t.Errorf("the cursor is at circle %d, the last opened is %d", cur, cs[len(cs)-1].idx)
	}
}

// The stored circles follow the spiral: circle k's centre is the spiral's k-th centre counted from circle 0, so the
// order they open in never depends on timing or on which replica placed the founding.
func TestStoredCirclesFollowTheSpiral(t *testing.T) {
	e := newFoundingEnv(t)
	e.h.WithSpawnCircles(testCircleRules())
	e.foundSome(t, 7)
	w, worldID := e.world(t)
	cs := e.circles(t, worldID)
	if len(cs) < 2 || cs[0].idx != 0 {
		t.Fatalf("circles stored: %+v", cs)
	}
	for _, c := range cs[1:] {
		lat, lon := wsettle.CircleCentre(cs[0].lat, cs[0].lon, c.idx, c.rad, w.Params.PlanetRadiusKm)
		if math.Abs(lat-c.lat) > 1e-6 || math.Abs(lon-c.lon) > 1e-6 {
			t.Errorf("circle %d is stored at %v,%v but the spiral says %v,%v", c.idx, c.lat, c.lon, lat, lon)
		}
	}
}

// Foundings at the same moment never take the same cell or the same slot: the cursor row serialises them.
func TestConcurrentFoundingsNeverShareACellOrOverfillACircle(t *testing.T) {
	e := newFoundingEnv(t)
	e.h.WithSpawnCircles(testCircleRules())
	const n = 6
	var wg sync.WaitGroup
	chats := make([]int64, n)
	metas := make([]envelope.Metadata, n)
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		meta, _ := e.group(t)
		metas[i], chats[i] = meta, meta.TelegramChatID
		if _, err := e.h.Found(testCtx(t), meta); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			meta := metas[i]
			req := validFoundingRequest(t, openDraftID(t, e.pool, chats[i]))
			if _, err := e.h.Submit(testCtx(t), clientMeta(meta, "settlement.found.submit", "found.submit"), req); err != nil {
				errs <- err.Error()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Errorf("a founding failed: %s", msg)
	}
	w, worldID := e.world(t)
	var cells []int32
	rows, err := e.pool.Raw().Query(testCtx(t), `SELECT world_cell_id FROM cities WHERE world_id = $1::uuid AND origin = 'founded'`, worldID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c int32
		_ = rows.Scan(&c)
		cells = append(cells, c)
	}
	rows.Close()
	if len(cells) != n {
		t.Fatalf("%d villages founded of %d", len(cells), n)
	}
	for i, a := range cells {
		for _, b := range cells[i+1:] {
			if d := km(w, a, b); a == b || d < 30 {
				t.Errorf("two villages %.1f km apart", d)
			}
		}
	}
	total := 0
	for _, c := range e.circles(t, worldID) {
		if c.took > c.capacity {
			t.Errorf("circle %d over capacity: %d of %d", c.idx, c.took, c.capacity)
		}
		total += c.took
	}
	if total != n {
		t.Errorf("circles count %d foundings, %d happened", total, n)
	}
}

// A service boots the handler with the rules built from config. The shipped defaults must place a founding.
func TestSpawnCirclesWorkAsTheServiceBuildsThemFromConfig(t *testing.T) {
	e := newFoundingEnv(t)
	cfg := config.Defaults()
	rules := settlementcfg.SpawnCircle(cfg.Settlement)
	if !rules.Valid() {
		t.Fatalf("the shipped defaults are not usable: %+v", rules)
	}
	e.h.WithSpawnCircles(rules)
	cells := e.foundSome(t, 2)
	w, worldID := e.world(t)
	if cells[0] == cells[1] || km(w, cells[0], cells[1]) < 30 {
		t.Errorf("the two villages are on %d and %d", cells[0], cells[1])
	}
	cs := e.circles(t, worldID)
	if len(cs) == 0 {
		t.Fatal("no circle was stored")
	}
	if cs[0].rad != cfg.Settlement.SpawnCircleRadiusKm || cs[0].capacity != cfg.Settlement.SpawnCircleCapacity {
		t.Errorf("the circle is %+v, the config says radius %v capacity %d", cs[0], cfg.Settlement.SpawnCircleRadiusKm, cfg.Settlement.SpawnCircleCapacity)
	}
}
