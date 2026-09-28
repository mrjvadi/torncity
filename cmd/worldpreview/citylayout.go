package main

import "sort"

// This file is the DEMO's city layout: a small, pure function that turns a
// square window of terrain (already generated, already sampled — see
// export_city.go) into a road grid and a set of building placements. It is
// pure so it can be unit-tested without a World at all (citylayout_test.go
// builds synthetic terrain), and deterministic so the same seed's terrain
// always lays out the same city — no randomness beyond what the terrain
// itself already encodes.
//
// This is demo-only scaffolding for --export-city, not the real settlement
// placement system ADR 0028 §6 describes (that one lives in a future phase,
// W5, validated server-side against a database of claims and queued
// builds). It borrows the ADR's vocabulary (lot, footprint, the building
// catalogue's codes) so the JSON it emits already looks like what a real
// settlement layout would eventually store, but it has no persistence, no
// construction queue and no legality checks beyond "not on water, not too
// steep".

// cityTile is one lot's terrain, exactly enough for layout to decide roads
// and placements: whether it can be built on at all, and the handful of
// special conditions (coast, a deposit) a building type might want.
type cityTile struct {
	Water        bool // river, stream, lake or ocean tile: never buildable, never a road
	Steep        bool // local elevation gradient too high to build on
	Coast        bool // borders an ocean tile just outside this tile
	Deposit      bool // a resource deposit is exactly on this tile
	ResourceCode string
}

func (t cityTile) buildable() bool { return !t.Water && !t.Steep }

// cityLot is one placed building: the same shape ADR 0028 §6.4 says the
// client renders from (type_code, lot_x, lot_y, rotation) — a footprint's
// top-left corner, its size, and a rotation in degrees (always a multiple
// of 90 here; the demo never needs a finer angle).
type cityLot struct {
	Type string
	X, Y int
	W, H int
	Rot  int
}

// cityLayout is layoutCity's result: the road tiles (local grid
// coordinates) and every placed building.
type cityLayout struct {
	Size  int
	Roads [][2]int
	Lots  []cityLot
}

// footprintSpec is one building type's shape and how many the layout tries
// to place, in priority order — civic infrastructure first (so it always
// lands near the centre, on the best ground), then the rest, then housing
// fills whatever is left. Mirrors docs/adr/0028-world-and-settlements.md §7's
// catalogue sketch, trimmed to what a demo can usefully show on a 15x15
// grid and to the models the client actually has art for.
type footprintSpec struct {
	kind      string
	w, h      int
	count     int  // how many to try placing; 0 = fill remaining space (housing only)
	edgeOnly  bool // farm: only tried on tiles touching the grid's own border
	needCoast bool // port: only tried on a coast tile
	needMine  bool // mine/well: only tried near a deposit tile
}

var citySpecs = []footprintSpec{
	{kind: "civic_hall", w: 2, h: 2, count: 1},
	{kind: "market", w: 3, h: 3, count: 1},
	{kind: "bank", w: 3, h: 3, count: 1},
	{kind: "school", w: 3, h: 3, count: 1},
	{kind: "clinic", w: 3, h: 3, count: 1},
	{kind: "police", w: 2, h: 2, count: 1},
	{kind: "park", w: 2, h: 2, count: 3},
	{kind: "port", w: 2, h: 2, count: 1, needCoast: true},
	{kind: "mine", w: 2, h: 2, count: 1, needMine: true},
	{kind: "farm", w: 2, h: 2, count: 4, edgeOnly: true},
	{kind: "housing", w: 2, h: 2, count: 0},
}

// roadSpacing is how many lots apart the road grid's lines fall — a simple
// Manhattan grid of the kind SimCity's own zoned-block layouts use (§0 of
// the ADR's research notes), close enough together on a 15-lot city that
// every block a building lands in is road-adjacent, ADR 0028 §6.2's rule.
const roadSpacing = 4

// layoutCity lays roads then buildings over a size x size window of
// terrain, tiles in row-major order (tiles[y*size+x]). Pure function of its
// inputs: same tiles, same result, always — no internal randomness, so a
// city's layout is exactly as reproducible as the terrain it sits on (the
// seed-first rule, ADR 0028 §2, extended to placement).
func layoutCity(size int, tiles []cityTile) cityLayout {
	roadLines := roadLineIndices(size)
	roadRow, roadCol := traceRoads(size, tiles, roadLines)

	occupied := make([]bool, size*size)
	at := func(x, y int) int { return y*size + x }
	for x := 0; x < size; x++ {
		occupied[at(x, roadRow[x])] = true
	}
	for y := 0; y < size; y++ {
		occupied[at(roadCol[y], y)] = true
	}
	// A road tile is never buildable even where a traced row and a tile's
	// own terrain would otherwise allow it — the road itself occupies the
	// lot (ADR 0028 §7's own `road` catalogue entry, 1x1, near-free).
	buildable := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		return !occupied[at(x, y)] && tiles[at(x, y)].buildable()
	}

	var lots []cityLot
	place := func(spec footprintSpec, x, y int) bool {
		if x+spec.w > size || y+spec.h > size {
			return false
		}
		for dy := 0; dy < spec.h; dy++ {
			for dx := 0; dx < spec.w; dx++ {
				if !buildable(x+dx, y+dy) {
					return false
				}
			}
		}
		for dy := 0; dy < spec.h; dy++ {
			for dx := 0; dx < spec.w; dx++ {
				occupied[at(x+dx, y+dy)] = true
			}
		}
		lots = append(lots, cityLot{Type: spec.kind, X: x, Y: y, W: spec.w, H: spec.h, Rot: 0})
		return true
	}

	// candidateOrder ranks every top-left position by distance to the
	// grid's centre (civic buildings want the middle) — computed once,
	// reused for every spec so "closest to centre, first free spot" is the
	// one placement rule every building type shares.
	cx, cy := float64(size-1)/2, float64(size-1)/2
	type pos struct{ x, y int }
	var order []pos
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			order = append(order, pos{x, y})
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		di := sqDist(float64(order[i].x)-cx, float64(order[i].y)-cy)
		dj := sqDist(float64(order[j].x)-cx, float64(order[j].y)-cy)
		return di < dj
	})

	isEdge := func(x, y int) bool { return x == 0 || y == 0 || x == size-1 || y == size-1 }

	for _, spec := range citySpecs {
		if spec.count == 0 {
			continue // housing: filled last, below
		}
		placed := 0
		for _, p := range order {
			if placed >= spec.count {
				break
			}
			if spec.edgeOnly && !isEdge(p.x, p.y) {
				continue
			}
			if spec.needCoast && !anyCornerCoast(tiles, size, p.x, p.y, spec.w, spec.h) {
				continue
			}
			if spec.needMine && !anyCornerDeposit(tiles, size, p.x, p.y, spec.w, spec.h) {
				continue
			}
			if place(spec, p.x, p.y) {
				placed++
			}
		}
	}

	// Housing fills whatever buildable space is left, in the same
	// centre-out order, so a demo city reads as dense near its civic core
	// and thins toward the edge — the same "packed along the roads" shape
	// the existing client engine's own filler already goes for.
	housingSpec := footprintSpec{kind: "housing", w: 2, h: 2}
	for _, p := range order {
		place(housingSpec, p.x, p.y)
	}

	var roads [][2]int
	seen := make(map[[2]int]bool)
	for x := 0; x < size; x++ {
		key := [2]int{x, roadRow[x]}
		if !seen[key] {
			seen[key] = true
			roads = append(roads, key)
		}
	}
	for y := 0; y < size; y++ {
		key := [2]int{roadCol[y], y}
		if !seen[key] {
			seen[key] = true
			roads = append(roads, key)
		}
	}
	sort.Slice(roads, func(i, j int) bool {
		if roads[i][1] != roads[j][1] {
			return roads[i][1] < roads[j][1]
		}
		return roads[i][0] < roads[j][0]
	})

	return cityLayout{Size: size, Roads: roads, Lots: lots}
}

// roadLineIndices picks roadSpacing-apart grid lines inside [0,size), always
// including at least one line, spaced from the centre outward so a small
// grid still gets a sensible cross through its middle.
func roadLineIndices(size int) []int {
	var lines []int
	mid := size / 2
	lines = append(lines, mid)
	for off := roadSpacing; mid-off >= 0 || mid+off < size; off += roadSpacing {
		if mid-off >= 0 {
			lines = append(lines, mid-off)
		}
		if mid+off < size {
			lines = append(lines, mid+off)
		}
	}
	sort.Ints(lines)
	return lines
}

// traceRoads walks each nominal road line across the grid, jogging it
// sideways (within a small search radius) wherever the terrain under it is
// water or too steep to grade a road across — ADR 0028 §6.2's roads are
// "near-free", not free: a straight line the terrain won't allow simply
// is not built there, the same way a real road bends around a stream. Ties
// (the line's own row/column, then the nearest neighbour) resolve toward
// the original line, so a road only ever jogs as far as it has to.
func traceRoads(size int, tiles []cityTile, hLines []int) (rowAt, colAt []int) {
	at := func(x, y int) int { return y*size + x }
	ok := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < size && y < size && !tiles[at(x, y)].Water && !tiles[at(x, y)].Steep
	}

	// One shared nominal line serves both axes on a square grid (the same
	// spacing looks right either way), reused here as each axis's own line
	// set for clarity.
	vLines := hLines

	rowAt = make([]int, size)
	for x := 0; x < size; x++ {
		nominal := nearestLine(hLines, size/2)
		rowAt[x] = jog(size, nominal, func(y int) bool { return ok(x, y) })
	}
	colAt = make([]int, size)
	for y := 0; y < size; y++ {
		nominal := nearestLine(vLines, size/2)
		colAt[y] = jog(size, nominal, func(x int) bool { return ok(x, y) })
	}
	return rowAt, colAt
}

func nearestLine(lines []int, want int) int {
	best := lines[0]
	for _, l := range lines {
		if abs(l-want) < abs(best-want) {
			best = l
		}
	}
	return best
}

// jog returns the nearest index to nominal (within size) for which test
// passes, searching outward alternately (0, +1, -1, +2, -2, ...) so a tie
// always prefers staying on the original line.
func jog(size, nominal int, test func(int) bool) int {
	if test(nominal) {
		return nominal
	}
	for d := 1; d < size; d++ {
		if nominal+d < size && test(nominal+d) {
			return nominal + d
		}
		if nominal-d >= 0 && test(nominal-d) {
			return nominal - d
		}
	}
	return nominal // no clear tile anywhere on this line: leave it, best effort
}

func anyCornerCoast(tiles []cityTile, size, x, y, w, h int) bool {
	if x+w > size || y+h > size {
		return false
	}
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if tiles[(y+dy)*size+(x+dx)].Coast {
				return true
			}
		}
	}
	return false
}

func anyCornerDeposit(tiles []cityTile, size, x, y, w, h int) bool {
	if x+w > size || y+h > size {
		return false
	}
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if tiles[(y+dy)*size+(x+dx)].Deposit {
				return true
			}
		}
	}
	return false
}

func sqDist(dx, dy float64) float64 { return dx*dx + dy*dy }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
