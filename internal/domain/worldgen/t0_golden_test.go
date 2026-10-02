package worldgen_test

import (
	"hash/fnv"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/territory"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// The T0 golden tests of ADR 0042 section 12: coverage and the road router on
// the real generated terrain of seed 42 (the fast fixture world). A change to
// worldgen, coverage or the planner that moves any output must be deliberate:
// bump worldgen.GeneratorVersion for a worldgen change and capture fresh
// values, never edit these to make a failing test pass.

func t0World(t *testing.T) (*worldgen.World, *worldgen.ChunkTileSource) {
	t.Helper()
	w, err := worldgen.SmallWorldForTests(42)
	if err != nil {
		t.Fatal(err)
	}
	return w, worldgen.NewChunkTileSource(w, 64)
}

var trackClass = roads.Class{Code: "track", MaxGradeBPS: 1200, BridgeMaxSpanM: 60}

// landRoadEnds scans the world on a fixed lattice for the first land tile that
// a track can reach another land tile 12 east and 7 north of it, so the golden
// runs on real dry ground whatever the fixture's seas are.
func landRoadEnds(t *testing.T, w *worldgen.World, src *worldgen.ChunkTileSource) (from, to worldgen.TileID) {
	t.Helper()
	terrain := roads.WorldTerrain{W: w, Src: src, DefaultBPS: 10000}
	for lat := -50.0; lat <= 50; lat += 7 {
		for lon := -170.0; lon <= 170; lon += 11 {
			f := w.TileOfLatLon(lat, lon)
			g := w.Offset(w.Offset(f, 12, 7), 0, 0)
			if _, err := roads.Plan(terrain, []worldgen.TileID{f}, []worldgen.TileID{g}, trackClass, roads.DefaultParams()); err == nil {
				return f, g
			}
		}
	}
	t.Fatal("no land line found on the fixture world")
	return
}

func TestT0Golden_CoverageOnSeed42(t *testing.T) {
	if worldgen.GeneratorVersion != 1 {
		t.Skip("capture fresh golden values for the new GeneratorVersion")
	}
	w, src := t0World(t)
	obs, _ := landRoadEnds(t, w, src)
	k := territory.Kind{Bins: 64, ObserverHeightM: 12, ClaimBaseM: 1500, ClaimCapM: 5000, SightM: 8000, StepM: w.TileLengthM(),
		TargetHeightM: 2, RefractionNum: 7, RefractionDen: 6}
	cov, err := territory.Compute(territory.WorldSampler{W: w, Src: src, Observer: obs}, k, territory.DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	h := fnv.New64a()
	for i := range cov.Claim {
		h.Write([]byte{byte(cov.Claim[i]), byte(cov.Claim[i] >> 8), byte(cov.Sight[i]), byte(cov.Sight[i] >> 8)})
	}
	t.Logf("cap %d prominence %d", cov.CapM, cov.ProminenceM)
	if got := h.Sum64(); got != goldenCoverage {
		t.Fatalf("coverage golden 0x%x", got)
	}
	again, _ := territory.Compute(territory.WorldSampler{W: w, Src: src, Observer: obs}, k, territory.DefaultParams())
	for i := range cov.Claim {
		if cov.Claim[i] != again.Claim[i] || cov.Sight[i] != again.Sight[i] {
			t.Fatal("coverage is not deterministic")
		}
	}
}

func TestT0Golden_RoadOnSeed42(t *testing.T) {
	if worldgen.GeneratorVersion != 1 {
		t.Skip("capture fresh golden values for the new GeneratorVersion")
	}
	w, src := t0World(t)
	terrain := roads.WorldTerrain{W: w, Src: src, DefaultBPS: 10000,
		TerrainBPS: map[string]int{"temperate_forest": 16000, "desert": 12000, "tundra": 14000, "tropical_rainforest": 22000}}
	from, to := landRoadEnds(t, w, src)
	r, err := roads.Plan(terrain, []worldgen.TileID{from}, []worldgen.TileID{to}, trackClass, roads.DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	h := fnv.New64a()
	for _, id := range r.Tiles {
		h.Write([]byte(id.String()))
	}
	t.Logf("tiles %d length %d m cost %d climb %d", len(r.Tiles), r.LengthM, r.Cost, r.ClimbM)
	if got := h.Sum64(); got != goldenRoad {
		t.Fatalf("road golden 0x%x", got)
	}
}

const (
	goldenCoverage = uint64(0x165bb6dce1bdb7f1)
	goldenRoad     = uint64(0x50a361e5bec24d4)
)
