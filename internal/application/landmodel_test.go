package application

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

var (
	landWorldOnce sync.Once
	landWorld     *worldgen.World
	landWorldErr  error
)

func testLandWorld(t *testing.T) (*worldgen.World, string) {
	t.Helper()
	_, f, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(f)))
	landWorldOnce.Do(func() {
		pack, err := content.LoadWorldGen(filepath.Join(root, "configs", "content"))
		if err != nil {
			landWorldErr = err
			return
		}
		if err = pack.Validate(); err != nil {
			landWorldErr = err
			return
		}
		c, err := pack.ToContent()
		if err != nil {
			landWorldErr = err
			return
		}
		params := worldgen.DefaultParams()
		params.CellCount = 6000
		landWorld, landWorldErr = worldgen.Generate(20281010, params, c)
	})
	if landWorldErr != nil {
		t.Fatalf("building the test world: %v", landWorldErr)
	}
	return landWorld, root
}

func testLandDef(t *testing.T, root string) content.LandDef {
	t.Helper()
	pack, err := content.Load(filepath.Join(root, "configs", "content"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Land) != 1 {
		t.Fatalf("obstacles.yml should declare one land block: %d", len(pack.Land))
	}
	return pack.Land[0]
}

// aWoodedCell finds a cell whose lots are land and mostly forest.
func aWoodedCell(t *testing.T, w *worldgen.World) int32 {
	t.Helper()
	for i := range w.Cells {
		c := w.Cells[i]
		s := w.SampleFineLatLon(c.Point.LatDeg, c.Point.LonDeg)
		if s.IsOcean || s.IsLake || s.StreamKind != worldgen.StreamKindNone {
			continue
		}
		if code := w.BiomeCode(int32(i)); code == "temperate_forest" || code == "temperate_rainforest" || code == "boreal_forest" {
			return int32(i)
		}
	}
	t.Fatal("no wooded cell in the test world")
	return 0
}

func landRules() LandRules {
	return LandRules{RuleAt: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), GraceDays: 7, Ring: 3, RegrowEvery: 120 * time.Hour,
		SaplingFor: 24 * time.Hour, FellRadius: 4, QuarryRadius: 3, RockShifts: 2}
}

// The land of a new settlement: a ring of commons round the grid that is wooded, a founding grid that can be built on at
// once, and the same answer every time. A settlement founded before the rule keeps its founding grid clear.
func TestTheLandOfASettlement(t *testing.T) {
	w, root := testLandWorld(t)
	def := testLandDef(t, root)
	cell := aWoodedCell(t, w)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	fresh := FoundedSettlement{WorldCellID: cell, FoundedAt: time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)}
	old := FoundedSettlement{WorldCellID: cell, FoundedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}

	a := BuildLand(w, fresh, 5, nil, nil, def, landRules(), nil, nil, now)
	b := BuildLand(w, fresh, 5, nil, nil, def, landRules(), nil, nil, now)
	if a.Version != 1 || len(a.Lots) != 11*11 {
		t.Fatalf("a settlement founded after the rule has version 1 and 121 lots (grid and a ring of 3): version %d, %d lots", a.Version, len(a.Lots))
	}
	if a.Mark() == "" || a.Mark() != b.Mark() {
		t.Error("the land is a pure function: the same mark every time")
	}
	clear, ringTrees, ringLots := 0, 0, 0
	for p, l := range a.Lots {
		if l.Ring == 0 && !l.Obstructed {
			clear++
		}
		if l.Ring > 0 {
			ringLots++
			ringTrees += l.Trees
			if !l.Commons {
				t.Errorf("%v: a ring lot no road opened is commons", p)
			}
		}
	}
	if clear < 15 {
		t.Errorf("the founding grid must be mostly clear: %d of 25", clear)
	}
	if ringLots != 96 || ringTrees == 0 {
		t.Errorf("the ring has 96 lots and the forest has trees on them: %d lots, %d trees", ringLots, ringTrees)
	}
	if bps, gen, left := a.Forest(); bps != 10_000 || gen != left || gen == 0 {
		t.Errorf("an untouched forest is all there: %d bps, %d generated, %d left", bps, gen, left)
	}

	o := BuildLand(w, old, 5, nil, nil, def, landRules(), nil, nil, now)
	if o.Version != 0 {
		t.Fatalf("a settlement founded before the rule has version 0: %d", o.Version)
	}
	for p, l := range o.Lots {
		if l.Ring == 0 && l.Obstructed {
			t.Errorf("%v: the founding grid of an older settlement stays clear", p)
		}
	}
}

// What is cut stays cut and a sapling grows: the deltas change the land and its mark.
func TestTheLandFollowsWhatWasDoneToIt(t *testing.T) {
	w, root := testLandWorld(t)
	def := testLandDef(t, root)
	cell := aWoodedCell(t, w)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	s := FoundedSettlement{WorldCellID: cell, FoundedAt: time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)}
	base := BuildLand(w, s, 5, nil, nil, def, landRules(), nil, nil, now)
	var target land.Pos
	for _, p := range base.Order() {
		if l := base.Lots[p]; l.Ring > 0 && l.Trees >= 2 {
			target = p
			break
		}
	}
	if base.Lots[target] == nil {
		t.Skip("no ring lot with two trees in this world")
	}
	before := base.Lots[target].Trees
	anchor := now
	cut := BuildLand(w, s, 5, nil, nil, def, landRules(), []land.Delta{{Pos: target, TreesCut: 2, RegrowAnchor: &anchor}}, nil, now)
	if got := cut.Lots[target].Trees; got != before-2 {
		t.Errorf("two trees cut leave %d, want %d", got, before-2)
	}
	if cut.Lots[target].Stumps != 2 {
		t.Errorf("two stumps stand: %d", cut.Lots[target].Stumps)
	}
	if cut.Mark() == base.Mark() {
		t.Error("a felled tree changes the mark of the land")
	}
	planted := now.Add(-2 * time.Hour)
	young := BuildLand(w, s, 5, nil, nil, def, landRules(), []land.Delta{{Pos: target, TreesCut: 2, RegrowAnchor: &anchor}},
		[]land.Sapling{{Pos: target, PlantedAt: planted, ReadyAt: planted.Add(24 * time.Hour)}}, now)
	if len(young.Lots[target].Growing) != 1 {
		t.Error("a sapling planted two hours ago is still growing")
	}
	grown := BuildLand(w, s, 5, nil, nil, def, landRules(), []land.Delta{{Pos: target, TreesCut: 2, RegrowAnchor: &anchor}},
		[]land.Sapling{{Pos: target, PlantedAt: planted.Add(-30 * time.Hour), ReadyAt: planted.Add(-6 * time.Hour)}}, now)
	if got := grown.Lots[target].Trees; got != before-1 {
		t.Errorf("a grown sapling is a tree: %d, want %d", got, before-1)
	}
	if bps, _, _ := cut.Forest(); bps >= 10_000 {
		t.Errorf("a cut forest is less than whole: %d", bps)
	}
}

// The lots a herd grazes do not regrow trees (docs/adr/0067); the same lots left alone do.
func TestGrazedLotsDoNotRegrowTrees(t *testing.T) {
	w, root := testLandWorld(t)
	def := testLandDef(t, root)
	cell := aWoodedCell(t, w)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s := FoundedSettlement{WorldCellID: cell, FoundedAt: time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)}
	base := BuildLand(w, s, 5, nil, nil, def, landRules(), nil, nil, now)
	var target land.Pos
	for _, p := range base.Order() {
		if l := base.Lots[p]; l.Ring > 0 && l.Trees >= 2 {
			target = p
			break
		}
	}
	if base.Lots[target] == nil {
		t.Skip("no ring lot with two trees in this world")
	}
	anchor := now.Add(-300 * time.Hour) // two and a half regrowth periods ago
	cut := []land.Delta{{Pos: target, TreesCut: 2, RegrowAnchor: &anchor}}
	free := BuildLand(w, s, 5, nil, nil, def, landRules(), cut, nil, now)
	grazed := BuildLandWith(w, s, 5, nil, nil, GrazedLots([]LandSite{{X: target.X, Y: target.Y, W: 1, H: 1}}, 1), def, landRules(), cut, nil, now)
	if free.Lots[target].Trees != base.Lots[target].Trees {
		t.Errorf("left alone the lot grew its trees back: %d of %d", free.Lots[target].Trees, base.Lots[target].Trees)
	}
	if got := grazed.Lots[target].Trees; got != base.Lots[target].Trees-2 {
		t.Errorf("a grazed lot grows no tree back: %d, want %d", got, base.Lots[target].Trees-2)
	}
}
