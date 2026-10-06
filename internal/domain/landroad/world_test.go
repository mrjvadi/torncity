package landroad

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

var (
	worldOnce sync.Once
	worldVal  *worldgen.World
	worldErr  error
)

func testWorld(t *testing.T) *worldgen.World {
	t.Helper()
	worldOnce.Do(func() {
		_, f, _, _ := runtime.Caller(0)
		root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		pack, err := content.LoadWorldGen(filepath.Join(root, "configs", "content"))
		if err != nil {
			worldErr = err
			return
		}
		if err = pack.Validate(); err != nil {
			worldErr = err
			return
		}
		c, err := pack.ToContent()
		if err != nil {
			worldErr = err
			return
		}
		params := worldgen.DefaultParams()
		params.CellCount = 6000
		worldVal, worldErr = worldgen.Generate(20280928, params, c)
	})
	if worldErr != nil {
		t.Fatalf("building the test world: %v", worldErr)
	}
	return worldVal
}

// aLandCell finds a land cell well away from water, for a frame.
func aLandCell(t *testing.T, w *worldgen.World) int32 {
	t.Helper()
	for i := range w.Cells {
		c := w.Cells[i]
		s := w.SampleFineLatLon(c.Point.LatDeg, c.Point.LonDeg)
		if s.IsOcean || s.IsLake || s.StreamKind != worldgen.StreamKindNone {
			continue
		}
		ok := true
		for _, d := range [][2]int{{80, 0}, {-80, 0}, {0, 80}, {0, -80}} {
			f := settlement.FrameOfGrid(w, c.Point.LatDeg, c.Point.LonDeg, 0, 0, 0, 5)
			lat, lon := f.LatLon(d[0], d[1])
			if x := w.SampleFineLatLon(lat, lon); x.IsOcean || x.IsLake {
				ok = false
			}
		}
		if ok {
			return int32(i)
		}
	}
	t.Skip("no inland cell in the test world")
	return 0
}

func TestFrame_LatLonAndLotOfAreInverse(t *testing.T) {
	w := testWorld(t)
	cell := aLandCell(t, w)
	pt := w.Cells[cell].Point
	f := settlement.FrameOfGrid(w, pt.LatDeg, pt.LonDeg, 0, 0, 0, 5)
	for _, l := range []Lot{{0, 0}, {4, 4}, {-30, 12}, {250, -190}, {-1000, 1000}} {
		lat, lon := f.LatLon(l.X, l.Y)
		x, y := f.LotOf(lat, lon)
		if x != l.X || y != l.Y {
			t.Fatalf("%v -> (%v,%v) -> (%d,%d)", l, lat, lon, x, y)
		}
	}
}

func TestSampleRect_AgreesWithTheGridOnTheGrid(t *testing.T) {
	w := testWorld(t)
	cell := aLandCell(t, w)
	pt := w.Cells[cell].Point
	const side = 7
	cLat, cLon := settlement.GridCentreGrown(w, pt.LatDeg, pt.LonDeg, 0, 0, 0)
	grid := settlement.SampleGridDetail(w, cLat, cLon, side, cell)
	f := settlement.FrameOfGrid(w, pt.LatDeg, pt.LonDeg, 0, 0, 0, side)
	rect := settlement.SampleRect(w, f, 0, 0, side, side, cell)
	same := 0
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if rect[y][x].Buildable == grid.Lots[y][x].Buildable {
				same++
			}
		}
	}
	if same < side*side-1 {
		t.Fatalf("the rectangle sampler disagrees with the grid sampler on %d of %d lots", side*side-same, side*side)
	}
	// and a rectangle beyond the grid, west and south of it, is sampled at all
	out := settlement.SampleRect(w, f, -20, -20, 5, 5, cell)
	if len(out) != 5 || len(out[0]) != 5 {
		t.Fatalf("outer rectangle %dx%d", len(out), len(out[0]))
	}
}

func TestPlan_AcrossTheRealWorldReachesFarAndWestOfTheGrid(t *testing.T) {
	w := testWorld(t)
	cell := aLandCell(t, w)
	pt := w.Cells[cell].Point
	const side = 5
	f := settlement.FrameOfGrid(w, pt.LatDeg, pt.LonDeg, 0, 0, 0, side)
	ground := NewWorldGround(w, f, map[string]int{"temperate_forest": 16000})
	src := worldgen.NewChunkTileSource(w, 64)
	var done int
	for _, to := range []Lot{{-40, 10}, {60, -25}, {5, 90}, {-70, -70}} {
		req := Request{
			W: w, Src: src, Ground: ground,
			Class:   Class{Code: "path", MaxGradeBPS: 2500, BridgeMaxSpanM: 10, Fords: true},
			Planner: roads.DefaultParams(),
			From:    Lot{2, 2}, To: to,
			Blocked: func(l Lot) bool {
				return l.X >= 0 && l.X < side && l.Y >= 0 && l.Y < side && l != (Lot{2, 2}) && l.X == 3
			},
			MaxLots: 2000, StreamRun: 2, CorridorRing: 1,
		}
		path, route, err := Plan(req)
		if err != nil {
			t.Logf("to %v: %v (a lake, a river or the sea may lie between)", to, err)
			continue
		}
		done++
		last := path.Steps[len(path.Steps)-1].Lot
		if last != to {
			t.Fatalf("the road ends at %v, not %v", last, to)
		}
		if len(route.Tiles) == 0 || route.LengthM == 0 {
			t.Fatalf("the world router's line is empty: %+v", route)
		}
		// 4-connected, no repeats, nothing on the blocked column
		seen := map[Lot]bool{Lot{2, 2}: true}
		prev := Lot{2, 2}
		for _, s := range path.Steps {
			if abs(s.Lot.X-prev.X)+abs(s.Lot.Y-prev.Y) != 1 || seen[s.Lot] || req.Blocked(s.Lot) {
				t.Fatalf("a broken line at %v after %v", s.Lot, prev)
			}
			seen[s.Lot] = true
			prev = s.Lot
		}
		// the same request plans the same line again
		again, _, err := Plan(req)
		if err != nil || len(again.Steps) != len(path.Steps) {
			t.Fatalf("a second plan differs: %v", err)
		}
		for i := range again.Steps {
			if again.Steps[i].Lot != path.Steps[i].Lot {
				t.Fatalf("step %d differs", i)
			}
		}
	}
	if done == 0 {
		t.Fatal("not one of four roads could be planned over the test world")
	}
}
