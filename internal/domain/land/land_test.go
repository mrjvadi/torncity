package land

import (
	"testing"
	"time"
)

func testParams() Params {
	return Params{GenVersion: 1, MaxTreesGrid: 3, MaxTreesRing: 6, MaxRocks: 2, CoreDensityBPS: 0, OuterFactorBPS: 5000,
		SlopeRockBonusBPS: 1500, CoreClearShareBPS: 6000, CoreClearBlock: 4,
		Biomes: map[string]Density{"temperate_forest": {6500, 800}, "desert": {20, 1200}}}
}

func gridGrounds(side, ring int, biome string) map[Pos]Ground {
	g := map[Pos]Ground{}
	for y := -ring; y < side+ring; y++ {
		for x := -ring; x < side+ring; x++ {
			g[Pos{x, y}] = Ground{Biome: biome}
		}
	}
	return g
}

// The same seed, cell and lot always give the same answer, and a different seed gives a different land.
func TestObstaclesAreAPureFunction(t *testing.T) {
	pr := testParams()
	a := Generate(77, 12, pr, 5, gridGrounds(5, 3, "temperate_forest"))
	b := Generate(77, 12, pr, 5, gridGrounds(5, 3, "temperate_forest"))
	c := Generate(78, 12, pr, 5, gridGrounds(5, 3, "temperate_forest"))
	same, differ := true, false
	for p, o := range a {
		if b[p] != o {
			same = false
		}
		if c[p] != o {
			differ = true
		}
	}
	if !same {
		t.Error("the same seed must give the same land")
	}
	if !differ {
		t.Error("another seed must give another land")
	}
}

// Nothing exceeds its maximum, water carries nothing, the ring carries more trees than the grid.
func TestObstaclesStayWithinTheirMaximums(t *testing.T) {
	pr := testParams()
	grounds := gridGrounds(5, 3, "temperate_forest")
	grounds[Pos{-2, 0}] = Ground{Biome: "temperate_forest", Water: true}
	out := Generate(5, 3, pr, 5, grounds)
	var gridTrees, ringTrees, gridLots, ringLots int
	for p, o := range out {
		if o.Rocks > pr.MaxRocks {
			t.Errorf("%v: %d rocks", p, o.Rocks)
		}
		if Ring(p, 5) > 0 {
			if o.Trees > pr.MaxTreesRing {
				t.Errorf("%v: %d trees on the ring", p, o.Trees)
			}
			ringTrees += o.Trees
			ringLots++
		} else {
			if o.Trees > pr.MaxTreesGrid {
				t.Errorf("%v: %d trees in the grid", p, o.Trees)
			}
			gridTrees += o.Trees
			gridLots++
		}
	}
	if o := out[Pos{-2, 0}]; o.Trees != 0 || o.Rocks != 0 {
		t.Errorf("water carries nothing: %+v", o)
	}
	if float64(ringTrees)/float64(ringLots) <= float64(gridTrees)/float64(gridLots) {
		t.Errorf("the ring should be wooded more than the grid: %d/%d against %d/%d", ringTrees, ringLots, gridTrees, gridLots)
	}
}

// A new group can build at once: whatever the seed, at least 60 percent of the founding 5x5 is clear and a clear 4x4 exists.
func TestTheFoundingGridIsAlwaysBuildable(t *testing.T) {
	pr := testParams()
	pr.CoreDensityBPS = 9000 // the worst case: the generator itself would fill the core
	for seed := uint64(0); seed < 200; seed++ {
		out := Generate(seed, int32(seed%9), pr, 5, gridGrounds(5, 3, "temperate_forest"))
		clear := 0
		for y := 0; y < 5; y++ {
			for x := 0; x < 5; x++ {
				if o := out[Pos{x, y}]; o.Trees == 0 && o.Rocks == 0 {
					clear++
				}
			}
		}
		if clear*10_000 < 25*pr.CoreClearShareBPS {
			t.Fatalf("seed %d: only %d of 25 lots clear", seed, clear)
		}
		block := false
		for y := 0; y+4 <= 5 && !block; y++ {
			for x := 0; x+4 <= 5 && !block; x++ {
				ok := true
				for dy := 0; dy < 4 && ok; dy++ {
					for dx := 0; dx < 4; dx++ {
						if o := out[Pos{x + dx, y + dy}]; o.Trees != 0 || o.Rocks != 0 {
							ok = false
							break
						}
					}
				}
				block = ok
			}
		}
		if !block {
			t.Fatalf("seed %d: no clear 4x4 block", seed)
		}
	}
}

func TestRingIsTheDistanceOutsideTheGrid(t *testing.T) {
	cases := map[Pos]int{{0, 0}: 0, {4, 4}: 0, {-1, 2}: 1, {2, 5}: 1, {5, 5}: 1, {-3, 0}: 3, {7, 2}: 3}
	for p, want := range cases {
		if got := Ring(p, 5); got != want {
			t.Errorf("%v: ring %d, want %d", p, got, want)
		}
	}
}

// What is cut stays cut; a sapling is a tree once it is grown; a wooded lot regains a tree after the regrowth time.
func TestTreesFollowTheDeltas(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	anchor := now.Add(-5 * time.Hour)
	l := Lot{Generated: Obstacles{Trees: 4}, Delta: Delta{TreesCut: 3, RegrowAnchor: &anchor}}
	if got := l.Trees(now, Dynamics{}, 6, true); got != 1 {
		t.Errorf("4 generated less 3 cut and no regrowth: %d, want 1", got)
	}
	if got := l.Trees(now, Dynamics{RegrowEvery: 2 * time.Hour}, 6, true); got != 3 {
		t.Errorf("two regrown in five hours: %d, want 3", got)
	}
	if got := l.Trees(now, Dynamics{RegrowEvery: 2 * time.Hour}, 6, false); got != 1 {
		t.Errorf("no wooded neighbour, no regrowth: %d, want 1", got)
	}
	l.Saplings = []Sapling{{PlantedAt: now.Add(-30 * time.Hour), ReadyAt: now.Add(-6 * time.Hour)}, {PlantedAt: now.Add(-2 * time.Hour), ReadyAt: now.Add(22 * time.Hour)}}
	if got := l.Trees(now, Dynamics{}, 6, true); got != 2 {
		t.Errorf("one sapling grown: %d, want 2", got)
	}
	if g := l.Growing(now); len(g) != 1 || g[0] <= 0 || g[0] >= 1 {
		t.Errorf("one sapling still growing: %v", g)
	}
	if l.Stumps() != 3 {
		t.Errorf("stumps are capped at three: %d", l.Stumps())
	}
}

// The crews take the lots in one order everywhere: orders, then near, then wooded, then by position.
func TestCrewsTakeLotsInOneOrder(t *testing.T) {
	ts := []Target{{Pos: Pos{3, 3}, Dist: 1, Wood: 2}, {Pos: Pos{0, 1}, Dist: 2, Ordered: true}, {Pos: Pos{1, 1}, Dist: 1, Wood: 5}, {Pos: Pos{0, 2}, Dist: 1, Wood: 5}}
	ByReach(ts)
	want := []Pos{{0, 1}, {1, 1}, {0, 2}, {3, 3}}
	// ordered first; then dist 1 and wood 5 by (y, x): (1,1) before (0,2); then the less wooded
	for i, p := range want {
		if ts[i].Pos != p {
			t.Fatalf("order %v, want %v", ts, want)
		}
	}
}
