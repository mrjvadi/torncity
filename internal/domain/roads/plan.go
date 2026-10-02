// Package roads is the road router of ADR 0042 section 6.2 (phase T0): an A*
// search over base-LOD world tiles that finds the cheapest line for a road
// class (or, with a rail class, for a railway: ADR 0045 section 8.5 reuses
// this router with a gentler slope limit).
//
// It is a PURE function of the terrain it reads through a Terrain, the class
// and the params: no state, no database, no clock. Costs are integers, ties
// break on (f, g, tile id), neighbours are visited in a fixed order, so every
// replica plans the same line for the same inputs (the discipline of
// settlementbuilding/roads.go, lifted from lots to tiles).
//
// Tobler's hiking function (walking speed falls exponentially with grade) is
// the real basis of the slope penalty: the planner prefers a longer, gentler
// line, as real road builders and least-cost-path studies do (ADR 0042 R29).
package roads

import (
	"container/heap"
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// Planner failures.
var (
	// ErrNoRoute means no line exists inside the search box (or the search
	// gave up after Params.MaxExpansions).
	ErrNoRoute = errors.New("roads: no route")
	// ErrInvalid means the request cannot be planned (no endpoints, a class
	// with no grade limit).
	ErrInvalid = errors.New("roads: invalid request")
)

// NoBridgeError is returned when a line would exist if the class could carry
// a longer span (ADR 0042 6.3: "no_bridge_for_class {span, class_max,
// next_class}"). The caller shows the refusal and names the next class.
type NoBridgeError struct {
	SpanM     int // the longest span the best line needs
	ClassMaxM int // what the class carries
}

func (e *NoBridgeError) Error() string {
	return fmt.Sprintf("roads: no bridge for class: the line needs %d m, the class carries %d m", e.SpanM, e.ClassMaxM)
}

// Class is one road (or rail) class's planner content (roads.yml, rail.yml).
type Class struct {
	Code string
	// MaxGradeBPS is the steepest grade the class builds (a 12 % grade is 1200).
	MaxGradeBPS int
	// BridgeMaxSpanM is the longest span the class carries (0: it fords
	// streams only and cannot cross a river tile at all).
	BridgeMaxSpanM int
	// Fords: a stream is forded (path); otherwise a culvert is laid (track and up).
	Fords bool
}

// Params are the world-level numbers of the planner (roads.yml `planner`).
type Params struct {
	// SearchMarginM: the search stays inside the bounding box of both ends plus this (5,000).
	SearchMarginM int
	// MaxExpansions: the planner gives up after this many (200,000).
	MaxExpansions int
	// BridgeCostRatio is how many times a metre of bridge costs a metre of
	// road (about 25, from the bridge-to-road ratio of ADR 0042 R32).
	BridgeCostRatio int
	// StreamCrossingSteps: a ford or culvert costs this many extra steps (3).
	StreamCrossingSteps int
	// MinTerrainBPS is the cheapest terrain multiplier in the world; it keeps
	// the heuristic admissible (10000).
	MinTerrainBPS int
}

// DefaultParams are the ADR 0042 draft values.
func DefaultParams() Params {
	return Params{SearchMarginM: 5000, MaxExpansions: 200000, BridgeCostRatio: 25, StreamCrossingSteps: 3, MinTerrainBPS: 10000}
}

// Cell is what the planner needs to know about one tile.
type Cell struct {
	ElevationM int
	// TerrainBPS is the clearing multiplier of the tile's biome (10000 =
	// grassland, 16000 = temperate forest, ...).
	TerrainBPS int
	Water      worldgen.WaterKind
	// Blocked: the tile is closed to this road (held land of another
	// settlement without transit, a zone's lots).
	Blocked bool
}

// Terrain is the world the planner reads.
type Terrain interface {
	Cell(t worldgen.TileID) (Cell, error)
	Neighbours8(t worldgen.TileID) [8]worldgen.TileID
	// StepM is the length of step k of Neighbours8 (305 straight, 431 diagonal).
	StepM(k int) int
	// Face edge length in tiles, for the search box.
	Grid() int32
}

// Crossing is a ford, a culvert or a bridge on the line.
type Crossing struct {
	Tile  worldgen.TileID
	Kind  string // "ford", "culvert" or "bridge"
	SpanM int
	Cost  int64
}

// Route is a planned line.
type Route struct {
	Tiles []worldgen.TileID // from a source to a target
	// Cost is in hundredths of a metre of flat grassland road.
	Cost         int64
	LengthM      int
	ClimbM       int
	MaxGradeBPS  int
	SwitchbackM  int // the extra length steep grades add (grade over the limit)
	Crossings    []Crossing
	Expansions   int
	LongestSpanM int
}

type node struct {
	tile worldgen.TileID
	f, g int64
}

type pq []node

func (h pq) Len() int { return len(h) }
func (h pq) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.f != b.f {
		return a.f < b.f
	}
	if a.g != b.g {
		return a.g < b.g
	}
	return a.tile < b.tile
}
func (h pq) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pq) Push(x any)   { *h = append(*h, x.(node)) }
func (h *pq) Pop() any     { o := *h; n := len(o); x := o[n-1]; *h = o[:n-1]; return x }

const costScale = 100 // cost units per metre of flat grassland road

// Plan finds the cheapest line from any of from to any of to for a class.
// Both ends are inclusive. If the best line needs a bridge the class cannot
// carry, the error is a *NoBridgeError (when a line exists at all with a
// longer bridge), else ErrNoRoute.
func Plan(t Terrain, from, to []worldgen.TileID, c Class, p Params) (Route, error) {
	if len(from) == 0 || len(to) == 0 || c.MaxGradeBPS <= 0 {
		return Route{}, ErrInvalid
	}
	r, err := plan(t, from, to, c, p, false)
	if errors.Is(err, ErrNoRoute) && c.BridgeMaxSpanM >= 0 {
		// would a longer bridge make a line? then say so, as ADR 0042 6.3 asks
		if r2, err2 := plan(t, from, to, c, p, true); err2 == nil && r2.LongestSpanM > c.BridgeMaxSpanM {
			return Route{}, &NoBridgeError{SpanM: r2.LongestSpanM, ClassMaxM: c.BridgeMaxSpanM}
		}
	}
	return r, err
}

func plan(t Terrain, from, to []worldgen.TileID, c Class, p Params, anySpan bool) (Route, error) {
	targets := map[worldgen.TileID]bool{}
	for _, x := range to {
		targets[x] = true
	}
	minX, minY, maxX, maxY, face, oneFace := box(t, from, to, p.SearchMarginM)
	inBox := func(id worldgen.TileID) bool {
		f, x, y := id.Parts()
		if !oneFace || f != face {
			return true // off the endpoints' face: bounded by MaxExpansions only
		}
		return x >= minX && x <= maxX && y >= minY && y <= maxY
	}
	stepM := t.StepM(0)
	diagM := t.StepM(1)
	h := func(id worldgen.TileID) int64 {
		best := int64(-1)
		f, x, y := id.Parts()
		for _, tg := range to { // the caller's order: the heuristic must not depend on map order
			tf, tx, ty := tg.Parts()
			if tf != f {
				return 0 // across a face edge: no cheap bound, plain Dijkstra
			}
			dx, dy := int64(abs32(x-tx)), int64(abs32(y-ty))
			lo, hi := dx, dy
			if lo > hi {
				lo, hi = hi, lo
			}
			d := (hi-lo)*int64(stepM) + lo*int64(diagM)
			if best < 0 || d < best {
				best = d
			}
		}
		if best < 0 {
			return 0
		}
		return best * int64(p.MinTerrainBPS) * costScale / 10000
	}

	g := map[worldgen.TileID]int64{}
	from2 := map[worldgen.TileID]worldgen.TileID{}
	var open pq
	sources := append([]worldgen.TileID(nil), from...)
	for _, s := range sources {
		cell, err := t.Cell(s)
		if err != nil {
			return Route{}, err
		}
		if cell.Blocked || cell.Water >= worldgen.WaterLake {
			continue
		}
		if _, ok := g[s]; !ok {
			g[s] = 0
			heap.Push(&open, node{tile: s, f: h(s), g: 0})
		}
	}
	closed := map[worldgen.TileID]bool{}
	expansions := 0
	for open.Len() > 0 {
		cur := heap.Pop(&open).(node)
		if closed[cur.tile] || cur.g != g[cur.tile] {
			continue
		}
		closed[cur.tile] = true
		if targets[cur.tile] {
			return build(t, cur.tile, from2, cur.g, c, p, expansions)
		}
		expansions++
		if expansions > p.MaxExpansions {
			return Route{}, ErrNoRoute
		}
		here, err := t.Cell(cur.tile)
		if err != nil {
			return Route{}, err
		}
		for k, nb := range t.Neighbours8(cur.tile) {
			if closed[nb] || !inBox(nb) {
				continue
			}
			cell, err := t.Cell(nb)
			if err != nil {
				return Route{}, err
			}
			cost, _, ok := stepCost(cell, here, t.StepM(k), c, p, anySpan)
			if !ok {
				continue
			}
			ng := cur.g + cost
			if old, seen := g[nb]; seen && old <= ng {
				continue
			}
			g[nb] = ng
			from2[nb] = cur.tile
			heap.Push(&open, node{tile: nb, f: ng + h(nb), g: ng})
		}
	}
	return Route{}, ErrNoRoute
}

// stepCost is ADR 0042 6.2's step cost: length x terrain x slope + crossing.
// ok is false for an impassable step.
func stepCost(to, from Cell, lengthM int, c Class, p Params, anySpan bool) (cost int64, cr *Crossing, ok bool) {
	if to.Blocked || to.Water >= worldgen.WaterLake {
		return 0, nil, false
	}
	slope, ok := slopeBPS(abs(to.ElevationM-from.ElevationM), lengthM, c.MaxGradeBPS)
	if !ok {
		return 0, nil, false
	}
	terrain := to.TerrainBPS
	if terrain <= 0 {
		terrain = 10000
	}
	cost = int64(lengthM) * int64(terrain) * int64(slope) * costScale / 100_000_000
	switch to.Water {
	case worldgen.WaterStream:
		extra := int64(p.StreamCrossingSteps) * int64(lengthM) * costScale
		kind := "culvert"
		if c.Fords {
			kind = "ford"
		}
		return cost + extra, &Crossing{Kind: kind, Cost: extra}, true
	case worldgen.WaterRiver, worldgen.WaterGreatRiver:
		span := to.Water.WaterWidthM()
		if !anySpan && span > c.BridgeMaxSpanM {
			return 0, nil, false
		}
		extra := int64(span) * int64(p.BridgeCostRatio) * costScale
		return cost + extra, &Crossing{Kind: "bridge", SpanM: span, Cost: extra}, true
	}
	return cost, nil, true
}

// slopeBPS is the slope multiplier of ADR 0042 6.2 for a rise of dh over a run
// of length: with g the grade in bps and m the class limit, g <= m costs
// 10000 + 2000 g / m (cut and fill), m < g <= 3m costs 10000 g / m
// (switchbacks lengthen the road), and above 3m the step is impassable.
func slopeBPS(dh, lengthM, maxBPS int) (int, bool) {
	g := dh * 10000 / lengthM
	switch {
	case g <= maxBPS:
		return 10000 + 2000*g/maxBPS, true
	case g <= 3*maxBPS:
		return 10000 * g / maxBPS, true
	}
	return 0, false
}

func build(t Terrain, end worldgen.TileID, from2 map[worldgen.TileID]worldgen.TileID, cost int64, c Class, p Params, expansions int) (Route, error) {
	var tiles []worldgen.TileID
	for cur, ok := end, true; ok; cur, ok = from2[cur] {
		tiles = append(tiles, cur)
	}
	for i, j := 0, len(tiles)-1; i < j; i, j = i+1, j-1 {
		tiles[i], tiles[j] = tiles[j], tiles[i]
	}
	r := Route{Tiles: tiles, Cost: cost, Expansions: expansions}
	prev, err := t.Cell(tiles[0])
	if err != nil {
		return Route{}, err
	}
	for i := 1; i < len(tiles); i++ {
		cell, err := t.Cell(tiles[i])
		if err != nil {
			return Route{}, err
		}
		length := stepLength(t, tiles[i-1], tiles[i])
		dh := cell.ElevationM - prev.ElevationM
		if dh > 0 {
			r.ClimbM += dh
		}
		g := abs(dh) * 10000 / length
		if g > r.MaxGradeBPS {
			r.MaxGradeBPS = g
		}
		r.LengthM += length
		if g > c.MaxGradeBPS {
			r.SwitchbackM += length * (g - c.MaxGradeBPS) / c.MaxGradeBPS
		}
		_, cr, _ := stepCost(cell, prev, length, c, p, true)
		if cr != nil {
			cr.Tile = tiles[i]
			r.Crossings = append(r.Crossings, *cr)
			if cr.SpanM > r.LongestSpanM {
				r.LongestSpanM = cr.SpanM
			}
		}
		prev = cell
	}
	return r, nil
}

// stepLength is the nominal length between two neighbouring tiles.
func stepLength(t Terrain, a, b worldgen.TileID) int {
	for k, n := range t.Neighbours8(a) {
		if n == b {
			return t.StepM(k)
		}
	}
	return t.StepM(0)
}

func box(t Terrain, from, to []worldgen.TileID, marginM int) (minX, minY, maxX, maxY int32, face int8, oneFace bool) {
	all := append(append([]worldgen.TileID(nil), from...), to...)
	face, _, _ = all[0].Parts()
	oneFace = true
	first := true
	for _, id := range all {
		f, x, y := id.Parts()
		if f != face {
			oneFace = false
		}
		if first || x < minX {
			minX = x
		}
		if first || x > maxX {
			maxX = x
		}
		if first || y < minY {
			minY = y
		}
		if first || y > maxY {
			maxY = y
		}
		first = false
	}
	margin := int32(marginM / t.StepM(0))
	return minX - margin, minY - margin, maxX + margin, maxY + margin, face, oneFace
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func abs32(a int32) int32 {
	if a < 0 {
		return -a
	}
	return a
}
