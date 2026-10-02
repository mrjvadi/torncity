package roads

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// grid is a synthetic terrain on cube face PZ: tiles (x, y) with x, y in
// [0, size). Anything outside is blocked.
type grid struct {
	size int32
	cell func(x, y int32) Cell
}

func id(x, y int32) worldgen.TileID { return worldgen.MakeTileID(worldgen.FacePZ, x, y) }

func (g grid) Cell(t worldgen.TileID) (Cell, error) {
	_, x, y := t.Parts()
	if x < 0 || y < 0 || x >= g.size || y >= g.size {
		return Cell{Blocked: true}, nil
	}
	return g.cell(x, y), nil
}

func (g grid) Neighbours8(t worldgen.TileID) [8]worldgen.TileID {
	_, x, y := t.Parts()
	d := [8][2]int32{{0, 1}, {1, 1}, {1, 0}, {1, -1}, {0, -1}, {-1, -1}, {-1, 0}, {-1, 1}}
	var out [8]worldgen.TileID
	for k, v := range d {
		nx, ny := x+v[0], y+v[1]
		if nx < 0 || ny < 0 { // keep ids packable: clamp onto the edge, which is blocked by Cell
			nx, ny = g.size, g.size
		}
		out[k] = id(nx, ny)
	}
	return out
}

func (g grid) StepM(k int) int {
	if k%2 == 1 {
		return 431
	}
	return 305
}

func (g grid) Grid() int32 { return g.size }

var plain = func(x, y int32) Cell { return Cell{TerrainBPS: 10000} }

var path = Class{Code: "path", MaxGradeBPS: 2500, BridgeMaxSpanM: 10, Fords: true}
var track = Class{Code: "track", MaxGradeBPS: 1200, BridgeMaxSpanM: 60}

func TestPlan_StraightLineOnAPlain(t *testing.T) {
	g := grid{size: 30, cell: plain}
	r, err := Plan(g, []worldgen.TileID{id(2, 5)}, []worldgen.TileID{id(12, 5)}, path, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tiles) != 11 || r.LengthM != 10*305 {
		t.Fatalf("tiles %d length %d", len(r.Tiles), r.LengthM)
	}
	if r.Cost != 10*305*costScale {
		t.Fatalf("cost %d", r.Cost)
	}
}

func TestPlan_AvoidsForestAndPrefersGrass(t *testing.T) {
	// a forest block (16000) across the direct line: the planner skirts it
	g := grid{size: 40, cell: func(x, y int32) Cell {
		if x >= 8 && x <= 12 && y >= 4 && y <= 6 {
			return Cell{TerrainBPS: 16000}
		}
		return Cell{TerrainBPS: 10000}
	}}
	r, err := Plan(g, []worldgen.TileID{id(2, 5)}, []worldgen.TileID{id(18, 5)}, path, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, tile := range r.Tiles {
		_, x, y := tile.Parts()
		if x >= 8 && x <= 12 && y >= 4 && y <= 6 {
			t.Fatalf("the line goes through the forest at %d,%d", x, y)
		}
	}
}

func TestPlan_SlopePrefersTheGentlerLine(t *testing.T) {
	// a wall of steep ground with a gentle pass at y = 20
	g := grid{size: 40, cell: func(x, y int32) Cell {
		h := 0
		if x >= 10 && x <= 12 && y != 20 {
			h = 400 // 400 m over 3 tiles: far too steep for a track
		}
		return Cell{TerrainBPS: 10000, ElevationM: h}
	}}
	r, err := Plan(g, []worldgen.TileID{id(5, 15)}, []worldgen.TileID{id(18, 15)}, track, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	through := false
	for _, tile := range r.Tiles {
		_, x, y := tile.Parts()
		if x == 11 && y == 20 {
			through = true
		}
	}
	if !through {
		t.Fatal("the line should take the pass")
	}
	if r.MaxGradeBPS > 3*track.MaxGradeBPS {
		t.Fatalf("grade %d", r.MaxGradeBPS)
	}
}

func TestPlan_SlopeMultiplier(t *testing.T) {
	for _, c := range []struct {
		dh, want int
		ok       bool
	}{{0, 10000, true}, {18, 10000 + 2000*(18*10000/305)/1200, true}, {37, 10000 * (37 * 10000 / 305) / 1200, true}, {200, 0, false}} {
		got, ok := slopeBPS(c.dh, 305, 1200)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("dh %d: %d %v, want %d %v", c.dh, got, ok, c.want, c.ok)
		}
	}
}

func TestPlan_RiverNeedsABridgeTheClassCarries(t *testing.T) {
	// a 40 m river across the whole box except far north where it is a stream
	g := grid{size: 30, cell: func(x, y int32) Cell {
		if x == 10 {
			return Cell{TerrainBPS: 10000, Water: worldgen.WaterRiver}
		}
		return Cell{TerrainBPS: 10000}
	}}
	from, to := []worldgen.TileID{id(5, 10)}, []worldgen.TileID{id(15, 10)}
	_, err := Plan(g, from, to, path, DefaultParams()) // path carries 10 m
	var nb *NoBridgeError
	if !errors.As(err, &nb) || nb.SpanM != 40 || nb.ClassMaxM != 10 {
		t.Fatalf("path over a 40 m river: %v", err)
	}
	r, err := Plan(g, from, to, track, DefaultParams()) // track carries 60 m
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Crossings) != 1 || r.Crossings[0].Kind != "bridge" || r.Crossings[0].SpanM != 40 {
		t.Fatalf("crossings %+v", r.Crossings)
	}
	if want := int64(40 * 25 * costScale); r.Crossings[0].Cost != want {
		t.Fatalf("bridge cost %d want %d", r.Crossings[0].Cost, want)
	}
}

func TestPlan_StreamIsFordedOrCulverted(t *testing.T) {
	g := grid{size: 30, cell: func(x, y int32) Cell {
		if x == 10 {
			return Cell{TerrainBPS: 10000, Water: worldgen.WaterStream}
		}
		return Cell{TerrainBPS: 10000}
	}}
	from, to := []worldgen.TileID{id(5, 10)}, []worldgen.TileID{id(15, 10)}
	rp, _ := Plan(g, from, to, path, DefaultParams())
	rt, _ := Plan(g, from, to, track, DefaultParams())
	if rp.Crossings[0].Kind != "ford" || rt.Crossings[0].Kind != "culvert" {
		t.Fatalf("%+v %+v", rp.Crossings, rt.Crossings)
	}
}

func TestPlan_NoRouteAndInvalid(t *testing.T) {
	g := grid{size: 30, cell: func(x, y int32) Cell {
		if x == 10 {
			return Cell{Blocked: true}
		}
		return Cell{TerrainBPS: 10000}
	}}
	if _, err := Plan(g, []worldgen.TileID{id(5, 10)}, []worldgen.TileID{id(15, 10)}, path, DefaultParams()); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("walled: %v", err)
	}
	if _, err := Plan(g, nil, nil, path, DefaultParams()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	p := DefaultParams()
	p.MaxExpansions = 3
	open := grid{size: 30, cell: plain}
	if _, err := Plan(open, []worldgen.TileID{id(1, 1)}, []worldgen.TileID{id(25, 25)}, path, p); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("expansion cap: %v", err)
	}
}

func TestPlan_DeterministicTies(t *testing.T) {
	g := grid{size: 30, cell: plain}
	a, _ := Plan(g, []worldgen.TileID{id(2, 2), id(2, 4)}, []worldgen.TileID{id(12, 12), id(14, 12)}, path, DefaultParams())
	for i := 0; i < 5; i++ {
		b, _ := Plan(g, []worldgen.TileID{id(2, 2), id(2, 4)}, []worldgen.TileID{id(12, 12), id(14, 12)}, path, DefaultParams())
		if len(a.Tiles) != len(b.Tiles) {
			t.Fatal("length differs")
		}
		for k := range a.Tiles {
			if a.Tiles[k] != b.Tiles[k] {
				t.Fatalf("tile %d differs", k)
			}
		}
	}
}
