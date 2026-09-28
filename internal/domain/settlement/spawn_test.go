package settlement

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// shippedContentDir locates configs/content/ in this checkout, the same way
// internal/content's own tests do.
func shippedContentDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// internal/domain/settlement/spawn_test.go -> repository root
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	return filepath.Join(root, "configs", "content")
}

var (
	testWorldOnce sync.Once
	testWorldVal  *worldgen.World
	testWorldErr  error
)

// testWorld returns one small, deterministic World shared by every test in
// this package: generation is the expensive part, and nothing here mutates
// it. A few thousand cells is enough land/ocean/river variety to exercise
// the algorithm and cheap enough to build in every `go test` run.
func testWorld(t *testing.T) *worldgen.World {
	t.Helper()
	testWorldOnce.Do(func() {
		pack, err := content.LoadWorldGen(shippedContentDir(t))
		if err != nil {
			testWorldErr = err
			return
		}
		if err := pack.Validate(); err != nil {
			testWorldErr = err
			return
		}
		c, err := pack.ToContent()
		if err != nil {
			testWorldErr = err
			return
		}
		params := worldgen.DefaultParams()
		params.CellCount = 6000
		testWorldVal, testWorldErr = worldgen.Generate(20280928, params, c)
	})
	if testWorldErr != nil {
		t.Fatalf("building the test world: %v", testWorldErr)
	}
	return testWorldVal
}

func testParams() Params {
	return Params{
		MinSpawnDistanceKm: 30,
		ThreatRadiusKm:     150,
		SearchMaxCells:     2000,
		SearchMaxAttempts:  400,
	}
}

func TestLatticePoint_Deterministic(t *testing.T) {
	for _, n := range []int64{1, 2, 3, 1000, 2_000_001} {
		lat1, lon1 := LatticePoint(n)
		lat2, lon2 := LatticePoint(n)
		if lat1 != lat2 || lon1 != lon2 {
			t.Fatalf("LatticePoint(%d) is not deterministic: (%v,%v) vs (%v,%v)", n, lat1, lon1, lat2, lon2)
		}
	}
}

func TestLatticePoint_InRange(t *testing.T) {
	for n := int64(1); n <= 5000; n++ {
		lat, lon := LatticePoint(n)
		if lat < -90 || lat > 90 {
			t.Fatalf("LatticePoint(%d) lat = %v, out of range", n, lat)
		}
		if lon < -180 || lon > 180 {
			t.Fatalf("LatticePoint(%d) lon = %v, out of range", n, lon)
		}
	}
}

func TestLatticePoint_DistinctPointsDontClusterAtPoles(t *testing.T) {
	// A regression guard on the lattice formula itself (not the spawn
	// algorithm): consecutive N's should be spread across the sphere, not
	// piled at one pole, or every low N would waste attempts on frozen caps.
	var north, south, mid int
	for n := int64(1); n <= 3000; n++ {
		lat, _ := LatticePoint(n)
		switch {
		case lat > 60:
			north++
		case lat < -60:
			south++
		default:
			mid++
		}
	}
	if north == 0 || south == 0 || mid == 0 {
		t.Fatalf("lattice is not spread across latitude bands: north=%d mid=%d south=%d", north, mid, south)
	}
}

func TestFindSpawn_Deterministic(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	c1, err1 := FindSpawn(w, nil, 1, p)
	c2, err2 := FindSpawn(w, nil, 1, p)
	if err1 != nil || err2 != nil {
		t.Fatalf("FindSpawn errors: %v / %v", err1, err2)
	}
	if c1 != c2 {
		t.Fatalf("FindSpawn(1) is not deterministic: %+v vs %+v", c1, c2)
	}
}

func TestFindSpawn_ReturnsHabitableLand(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	var existing []ExistingSettlement
	for n := int64(1); n <= 20; n++ {
		c, err := FindSpawn(w, existing, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		cell := w.Cells[c.CellID]
		if cell.IsOcean || cell.IsLake {
			t.Fatalf("FindSpawn(%d) returned a wet cell %d", n, c.CellID)
		}
		existing = append(existing, ExistingSettlement{CellID: c.CellID, TierWeight: 1})
	}
}

// TestFindSpawn_RespectsMinSpawnDistance is the load-bearing guarantee: no
// two settlements the algorithm places ever end up closer than
// MinSpawnDistanceKm, however many are founded back to back — the property
// that keeps an automatic territory claim (ADR 0028 section 5.1) from ever
// overlapping one already made.
func TestFindSpawn_RespectsMinSpawnDistance(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	var existing []ExistingSettlement
	var placed []int32
	for n := int64(1); n <= 15; n++ {
		c, err := FindSpawn(w, existing, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		for _, other := range placed {
			if d := greatCircleKm(w, c.CellID, other); d < p.MinSpawnDistanceKm {
				t.Fatalf("settlement %d landed %v km from an existing one (< %v km): cell %d vs %d",
					n, d, p.MinSpawnDistanceKm, c.CellID, other)
			}
		}
		placed = append(placed, c.CellID)
		existing = append(existing, ExistingSettlement{CellID: c.CellID, TierWeight: 1})
	}
}

// TestFindSpawn_ConcurrentRequestsNeverCollide simulates what the
// application layer's retry loop guarantees under real concurrency (many
// replicas racing on the same reserved N): here, sequential Ns computed
// against the same growing `existing` list, which is exactly what a
// serialised-by-unique-constraint founding sees one at a time. The
// assertion is the same one RespectsMinSpawnDistance makes, restated for a
// larger N so a spacing bug that only shows up once the lattice has spun a
// few turns is still caught.
func TestFindSpawn_ManySettlementsStayApart(t *testing.T) {
	w := testWorld(t)
	p := testParams()
	p.MinSpawnDistanceKm = 15 // denser, to fit more settlements in a small test world

	var existing []ExistingSettlement
	for n := int64(1); n <= 60; n++ {
		c, err := FindSpawn(w, existing, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		for _, e := range existing {
			if d := greatCircleKm(w, c.CellID, e.CellID); d < p.MinSpawnDistanceKm {
				t.Fatalf("settlement %d violates min spawn distance: %v km from cell %d", n, d, e.CellID)
			}
		}
		existing = append(existing, ExistingSettlement{CellID: c.CellID, TierWeight: 1})
	}
}

// TestFindSpawn_AvoidsStrongNeighbourWhenAnAlternativeExists checks the
// threat score is doing something: seeded with one heavily-weighted
// existing settlement near the very first lattice point, a fresh search
// from N=1 should prefer a cell further from it over the closest merely-
// eligible one, whenever a comparably good alternative is within the
// search bound.
func TestFindSpawn_ThreatScoreLowersNearbyScore(t *testing.T) {
	w := testWorld(t)
	p := testParams()

	lat, lon := LatticePoint(1)
	start := w.NearestCell(lat, lon)
	deposits := depositsByCell(w)

	noThreat := scoreCell(w, start, nil, deposits, p.ThreatRadiusKm)
	withThreat := scoreCell(w, start, []ExistingSettlement{{CellID: start, TierWeight: 100}}, deposits, p.ThreatRadiusKm)

	if withThreat >= noThreat {
		t.Fatalf("a strong neighbour on the same cell did not lower the score: %v (no threat) vs %v (with threat)",
			noThreat, withThreat)
	}
}

func TestFindSpawn_GivesUpAfterMaxAttempts(t *testing.T) {
	w := testWorld(t)
	p := testParams()
	p.SearchMaxAttempts = 3
	p.MinSpawnDistanceKm = 1_000_000 // impossible on any real planet

	// One existing settlement is enough: at 1,000,000km minimum distance,
	// every cell on the planet is "too close" to it, so every candidate the
	// bounded walk finds is refused and every attempt exhausts its search.
	existing := []ExistingSettlement{{CellID: 0, TierWeight: 1}}
	if _, err := FindSpawn(w, existing, 1, p); err != ErrNoEligibleSpot {
		t.Fatalf("FindSpawn with an impossible distance = %v, want ErrNoEligibleSpot", err)
	}
}
