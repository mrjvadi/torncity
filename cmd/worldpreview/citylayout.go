package main

import (
	"math"
	"sort"
)

// This file is the DEMO's city layout: a small, pure function that turns a
// square window of terrain (already generated, already sampled — see
// export_city.go) into a road network and a set of building placements. It
// is pure so it can be unit-tested without a World at all (citylayout_test.go
// builds synthetic terrain), and deterministic so the same seed's terrain
// always lays out the same city — no randomness beyond what the terrain
// itself (and each lot's own position, see hashXY/floorsFor below) already
// encodes.
//
// This is demo-only scaffolding for --export-city, not the real settlement
// placement system ADR 0028 §6 describes (that one lives in a future phase,
// W5, validated server-side against a database of claims and queued
// builds). It borrows the ADR's vocabulary (lot, footprint, the building
// catalogue's codes) so the JSON it emits already looks like what a real
// settlement layout would eventually store, but it has no persistence, no
// construction queue and no legality checks beyond "not on water, not too
// steep".
//
// ZONED, NOT A FLAT CATALOGUE. An earlier version of this file placed a
// fixed count of each building type anywhere flat, then filled the rest
// with 2x2 housing — fine for a ~15-lot village, but a real city (the demo
// now exports roughly 48x48 lots, export_city.go's demoCitySize) reads as a
// city only if it actually looks like one: a taller, denser core that
// thins out toward the edge. layoutCity now zones by distance from the
// grid's own centre — downtown towers, a mid-rise ring, low-rise housing
// beyond that, parks/farms toward the very edge — with the zone radii
// themselves SCALED to size (downtownRadiusLots/midriseRadiusLots below)
// rather than hardcoded, so the same logic reads sensibly on both the real
// ~48-lot export and citylayout_test.go's smaller synthetic grids.

// cityTile is one lot's terrain, exactly enough for layout to decide roads
// and placements: whether it can be built on at all, and the handful of
// special conditions (coast, a deposit) a building type might want.
//
// WATER, IMPASSABLE, WIDERIVER — THREE LEVELS, ONE PER ROAD CLASS. At lot
// scale (~30m/lot, see export_city.go's lotMeters) a thin stream is only a
// few lots wide — any road bridges that, none needs to detour. A genuine
// river is wider: a LOCAL road still detours around it (that reads better
// at neighbourhood-street scale and keeps traceRoads simple), but an
// ARTERIAL or the elevated highway is allowed to bridge it. Only a lake or
// ocean is impassable to every road class outright.
//   - Water: any wet lot at all (stream, river, lake, ocean) — never
//     buildable, regardless of road class.
//   - Impassable: lake or ocean — never crossable by ANY road class.
//   - WideRiver: a river too wide for a LOCAL road to bridge — crossable by
//     an ARTERIAL or the elevated highway, not by a local road (see
//     passableForClass below).
type cityTile struct {
	Water        bool
	Impassable   bool
	WideRiver    bool
	Steep        bool // local elevation gradient too high to build on or road across
	Coast        bool // borders an ocean tile just outside this tile
	Deposit      bool // a resource deposit is exactly on this tile
	ResourceCode string
}

func (t cityTile) buildable() bool { return !t.Water && !t.Steep }

// pos is a local grid coordinate — layoutCity's own centre-out placement
// order and fillZone's zone-filtered fill passes both share this type
// (package-level, not function-local) so fillZone can take the same slice
// layoutCity builds without a conversion.
type pos struct{ x, y int }

// cityLot is one placed building: the same shape ADR 0028 §6.4 says the
// client renders from (type_code, lot_x, lot_y, rotation), plus Floors — a
// real storey count so the web renderer doesn't have to guess a building's
// height from its type code alone (see floorsFor).
type cityLot struct {
	Type   string
	X, Y   int
	W, H   int
	Rot    int
	Floors int
}

// roadTile is one road-network cell: its local grid coordinate and which
// CLASS of road runs through it. A cell that multiple classes would trace
// through keeps only its HIGHEST class (elevated highway over arterial over
// local) — see layoutCity's classAt map — so a cell never appears twice.
type roadTile struct {
	X, Y  int
	Class int // roadClassLocal / roadClassArterial / roadClassHighway
}

const (
	roadClassLocal    = 0
	roadClassArterial = 1
	roadClassHighway  = 2
)

// cityLayout is layoutCity's result: the road network (every cell tagged
// with its class), which of those road cells are bridges over a water
// crossing, and every placed building.
type cityLayout struct {
	Size    int
	Roads   []roadTile
	Bridges []roadTile // subset of Roads that cross a bridgeable water lot
	Lots    []cityLot
}

// footprintSpec is one building type's shape and how many the layout tries
// to place, in priority order.
type footprintSpec struct {
	kind      string
	w, h      int
	count     int  // how many to try placing
	needCoast bool // port: only tried on a coast tile
	needMine  bool // mine: only tried near a deposit tile
}

// civicSpecs are the one-of-a-kind civic/infrastructure buildings placed
// FIRST, closest to the grid's own centre, on the best ground — unchanged
// in spirit from the original flat catalogue, just trimmed to what is
// still a fixed single (or coast/deposit-gated) count rather than something
// the zoned fill passes below handle instead.
var civicSpecs = []footprintSpec{
	{kind: "civic_hall", w: 2, h: 2, count: 1},
	{kind: "market", w: 3, h: 3, count: 1},
	{kind: "bank", w: 3, h: 3, count: 1},
	{kind: "school", w: 3, h: 3, count: 1},
	{kind: "clinic", w: 3, h: 3, count: 1},
	{kind: "police", w: 2, h: 2, count: 1},
	{kind: "port", w: 2, h: 2, count: 1, needCoast: true},
	{kind: "mine", w: 2, h: 2, count: 1, needMine: true},
}

// downtownSpecs/midriseSpecs are the alternating footprints the downtown
// and mid-rise fill passes pick between (hashXY-driven, not random — see
// fillZone) for visual variety within each ring, matching the coordinator's
// spec: downtown towers on tight 2x2/3x3 footprints, mid-rise on 2x2/3x2.
var (
	downtownSpecs = []footprintSpec{
		{kind: "tower_office", w: 3, h: 3},
		{kind: "tower_residential", w: 2, h: 2},
	}
	midriseSpecs = []footprintSpec{
		{kind: "midrise", w: 2, h: 2},
		{kind: "midrise", w: 3, h: 2},
	}
	housingSpec = footprintSpec{kind: "housing", w: 2, h: 2}
	parkSpec    = footprintSpec{kind: "park", w: 2, h: 2}
	farmSpec    = footprintSpec{kind: "farm", w: 2, h: 2}
)

// roadSpacing/arterialSpacing are how many lots apart each road grid's own
// lines fall — two independent Manhattan grids overlaid on the same
// window, the kind of local-street-vs-arterial hierarchy a real city plan
// has: roadSpacing (local streets, unchanged from the original single-tier
// design) close enough together that every block a building lands in is
// road-adjacent (ADR 0028 §6.2's rule); arterialSpacing coarser, a wider
// through-street every few blocks that (unlike a local street) is allowed
// to bridge a genuine river rather than detour around it (see
// passableForClass).
const (
	roadSpacing = 4
	// arterialSpacing is 2*roadSpacing (8, within the coordinator's own
	// "every ~6-8 lots" range) rather than an independent literal number:
	// lineIndices computes both grids' lines from the SAME centre anchor,
	// so a spacing that is an exact multiple of roadSpacing makes every
	// arterial line land exactly ON one of the local grid's own lines
	// (arterial "promotes" a subset of local streets to a wider
	// through-road) instead of an unaligned second grid interleaved
	// between them. That keeps combined road coverage close to the local
	// grid's own density; an independently-spaced arterial grid measured
	// in practice (see this file's git history) covered a genuinely
	// excessive ~61% of the whole window between the two overlaid grids,
	// leaving too little contiguous space for even the civic buildings
	// alone, let alone downtown towers beside them.
	arterialSpacing = roadSpacing * 2
)

// downtownRadiusLots/midriseRadiusLots/edgeBandLots/parkCount/farmCount are
// all SCALED to size rather than fixed, so the same zoning logic produces a
// sensible skyline on both the real ~48-lot export (giving exactly the
// coordinator's own "inner ~6 lots" / "~6-16 lots" numbers) and
// citylayout_test.go's smaller synthetic grids (still three distinct
// rings, just narrower ones), instead of the outer ring or the park/farm
// counts silently vanishing on a small test grid.
func downtownRadiusLots(size int) int {
	// size/6 (≈8 for 48, close to the coordinator's own "inner ~6 lots"):
	// the 6 single-count civic buildings (civic_hall/market/bank/school/
	// clinic/police, ~3x3 each) already cluster in the exact centre (they
	// place FIRST, nearest-to-centre-first, same as before), and the real
	// road grid (traceLineSet, a genuine multi-line Manhattan grid, unlike
	// the single-cross line this file used to trace) eats a real share of
	// any given radius too — this still leaves fillZone's largest-first
	// pass (see its own doc comment) genuine room for several towers
	// alongside the civic cluster, without shrinking the mid-rise ring
	// past midriseRadiusLots down to nothing.
	r := size / 5
	if r < 3 {
		r = 3
	}
	return r
}

func midriseRadiusLots(size int) int {
	r := size / 3
	if dr := downtownRadiusLots(size); r <= dr {
		r = dr + 1
	}
	return r
}

func edgeBandLots(size int) int {
	r := size / 16
	if r < 2 {
		r = 2
	}
	return r
}

func parkCount(size int) int {
	n := size / 8
	if n < 3 {
		n = 3
	}
	return n
}

func farmCount(size int) int {
	n := size / 6
	if n < 4 {
		n = 4
	}
	return n
}

// layoutCity lays roads then buildings over a size x size window of
// terrain, tiles in row-major order (tiles[y*size+x]). Pure function of its
// inputs: same tiles, same result, always — no internal randomness (see
// hashXY: every position-dependent choice, footprint alternation and
// storey count alike, is a deterministic hash of the lot's own coordinate,
// never math/rand), so a city's layout is exactly as reproducible as the
// terrain it sits on (the seed-first rule, ADR 0028 §2, extended to
// placement).
func layoutCity(size int, tiles []cityTile) cityLayout {
	at := func(x, y int) int { return y*size + x }

	// --- Roads: local, then arterial, then the elevated highway, each
	// cell keeping only its HIGHEST class.
	localRoads := traceLineSet(size, tiles, lineIndices(size, roadSpacing), roadClassLocal)
	arterialRoads := traceLineSet(size, tiles, lineIndices(size, arterialSpacing), roadClassArterial)
	highway := elevatedHighwayPath(size)

	classAt := make([]int, size*size)
	for i := range classAt {
		classAt[i] = -1
	}
	setClass := func(x, y, class int) {
		idx := at(x, y)
		if classAt[idx] < class {
			classAt[idx] = class
		}
	}
	for _, r := range localRoads {
		setClass(r.X, r.Y, r.Class)
	}
	for _, r := range arterialRoads {
		setClass(r.X, r.Y, r.Class)
	}
	for _, r := range highway {
		setClass(r.X, r.Y, r.Class)
	}

	var roads []roadTile
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if c := classAt[at(x, y)]; c >= 0 {
				roads = append(roads, roadTile{X: x, Y: y, Class: c})
			}
		}
	}

	occupied := make([]bool, size*size)
	for _, r := range roads {
		occupied[at(r.X, r.Y)] = true
	}
	// A road tile is never buildable even where the terrain underneath
	// would otherwise allow it — the road itself occupies the lot (ADR
	// 0028 §7's own `road` catalogue entry, 1x1, near-free).
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
		lots = append(lots, cityLot{Type: spec.kind, X: x, Y: y, W: spec.w, H: spec.h, Rot: 0, Floors: floorsFor(spec.kind, x, y)})
		return true
	}

	// order ranks every top-left position by distance to the grid's own
	// centre — civic buildings and downtown towers want the middle,
	// everything else radiates out from it. Computed once, reused by every
	// pass below.
	cx, cy := float64(size-1)/2, float64(size-1)/2
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
	distOf := func(x, y int) float64 { return math.Sqrt(sqDist(float64(x)-cx, float64(y)-cy)) }

	downtownR := float64(downtownRadiusLots(size))
	midriseR := float64(midriseRadiusLots(size))
	edgeBand := edgeBandLots(size)
	nearEdge := func(x, y int) bool {
		d := x
		if y < d {
			d = y
		}
		if size-1-x < d {
			d = size - 1 - x
		}
		if size-1-y < d {
			d = size - 1 - y
		}
		return d <= edgeBand
	}
	inDowntown := func(x, y int) bool { return distOf(x, y) <= downtownR }
	inMidrise := func(x, y int) bool { d := distOf(x, y); return d > downtownR && d <= midriseR }
	inOuter := func(x, y int) bool { return distOf(x, y) > midriseR }

	// 1. Civic/infrastructure specials: nearest-to-centre first, same
	// mechanism as before.
	for _, spec := range civicSpecs {
		placed := 0
		for _, p := range order {
			if placed >= spec.count {
				break
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

	// 2. Parks: scattered through the mid-rise ring and beyond (not
	// competing with downtown's own tower footprint), count scaled to size.
	{
		placed := 0
		want := parkCount(size)
		for _, p := range order {
			if placed >= want {
				break
			}
			if inDowntown(p.x, p.y) {
				continue
			}
			if place(parkSpec, p.x, p.y) {
				placed++
			}
		}
	}

	// 3. Farms: the very edge band only, count scaled to size.
	{
		placed := 0
		want := farmCount(size)
		for _, p := range order {
			if placed >= want {
				break
			}
			if !nearEdge(p.x, p.y) {
				continue
			}
			if place(farmSpec, p.x, p.y) {
				placed++
			}
		}
	}

	// 4. Downtown: tight tower footprints, alternated by position so the
	// core reads as varied rather than one repeated block.
	fillZone(place, order, inDowntown, downtownSpecs)

	// 5. Mid-rise ring.
	fillZone(place, order, inMidrise, midriseSpecs)

	// 6. Outer ring: low-rise housing.
	fillZone(place, order, inOuter, []footprintSpec{housingSpec})

	// 7. Catch-all: whatever buildable space is still left anywhere
	// (zone-boundary gaps a 2x2/3x2/3x3 footprint didn't fit into) — the
	// same safety net the original single-tier version used, so the city
	// never has large unexplained holes.
	for _, p := range order {
		place(housingSpec, p.x, p.y)
	}

	var bridges []roadTile
	for _, r := range roads {
		if tiles[at(r.X, r.Y)].Water {
			bridges = append(bridges, r)
		}
	}

	return cityLayout{Size: size, Roads: roads, Bridges: bridges, Lots: lots}
}

// fillZone places EVERY spec in specs across every position in order that
// satisfies inZone, LARGEST FOOTPRINT AREA FIRST: a dedicated centre-out
// pass per spec, not a single per-position pick between them. This is what
// actually guarantees both shapes show up wherever a zone has room for
// both — picking per-position (an earlier version of this function did,
// via hashXY) systematically let the smaller footprint win almost
// everywhere instead, since a spot a larger footprint could use is ALWAYS
// also valid for a smaller one, but not the reverse: whichever spec a
// given position happened to try first would often just claim it, denying
// the larger spec's own later attempt at the same spot before it ever got
// a chance to run there. Running every position for the larger spec FIRST,
// then only offering the smaller spec whatever is still free, removes that
// race entirely. The variety still reads as genuine: a contiguous block
// exactly one size fits, not the other, is common enough (see
// downtownSpecs/midriseSpecs' own footprints) that both shapes still end
// up genuinely mixed through the zone, just no longer at odds over the
// exact same spot.
func fillZone(place func(spec footprintSpec, x, y int) bool, order []pos, inZone func(x, y int) bool, specs []footprintSpec) {
	sorted := append([]footprintSpec(nil), specs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].w*sorted[i].h > sorted[j].w*sorted[j].h })
	for _, spec := range sorted {
		for _, p := range order {
			if !inZone(p.x, p.y) {
				continue
			}
			place(spec, p.x, p.y)
		}
	}
}

// hashXY is a small, pure, portable hash (FNV-1a) of a lot's own position
// plus a tag string — the ONLY source of position-dependent variety this
// file uses (footprint alternation in fillZone, storey counts in
// floorsFor). Never math/rand: the same (size,tiles) input to layoutCity
// must always produce the identical layout, and a lot's floor count must
// be reproducible from its own coordinate alone, not from placement order.
func hashXY(x, y int, tag string) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	h := uint64(offset64)
	mix := func(v uint64) {
		h ^= v
		h *= prime64
	}
	for i := 0; i < len(tag); i++ {
		mix(uint64(tag[i]))
	}
	mix(uint64(uint32(x)))
	mix(uint64(uint32(y)))
	return h
}

// floorsFor derives a lot's building height deterministically from its own
// type and position (hashXY) — no RNG, so the same terrain always produces
// the same skyline. Ranges are picked to read as a real city profile:
// downtown towers dominate, mid-rise fills the ring around it, low-rise
// housing stays modest, and anything with no vertical structure at all
// (park, farm) is 0. Civic/infrastructure buildings (civic_hall, market,
// bank, school, clinic, police, port, mine) get a small, generic range —
// "whatever reads sensibly" for a single, one-off building each.
func floorsFor(kind string, x, y int) int {
	h := hashXY(x, y, "floors:"+kind)
	switch kind {
	case "tower_office", "tower_residential":
		return 15 + int(h%26) // 15..40
	case "midrise":
		return 5 + int(h%8) // 5..12
	case "housing":
		return 2 + int(h%3) // 2..4
	case "park", "farm":
		return 0 // no vertical structure
	default:
		return 2 + int(h%5) // civic_hall/market/bank/school/clinic/police/port/mine: 2..6
	}
}

// lineIndices picks spacing-apart grid lines inside [0,size), always
// including at least one line, spaced from the centre outward so a small
// grid still gets a sensible cross through its middle. Shared by both the
// local (roadSpacing) and arterial (arterialSpacing) road grids.
func lineIndices(size, spacing int) []int {
	var lines []int
	mid := size / 2
	lines = append(lines, mid)
	for off := spacing; mid-off >= 0 || mid+off < size; off += spacing {
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

// passableForClass reports whether road class `class` may run through t at
// all (before jogging is even considered): never through Impassable
// (lake/ocean) or Steep ground, regardless of class; a WideRiver blocks
// only roadClassLocal — an arterial or the elevated highway may cross it
// (and does, via a bridge — see layoutCity's Bridges).
func passableForClass(t cityTile, class int) bool {
	if t.Impassable || t.Steep {
		return false
	}
	if t.WideRiver && class == roadClassLocal {
		return false
	}
	return true
}

// traceLineSet traces a REAL Manhattan grid for one road class: EVERY line
// in lines becomes both a horizontal street (nominal row = that line,
// jogged in y per column) and a vertical street (nominal column = that
// line, jogged in x per row) — not just the single line nearest the grid's
// centre. Jogging happens sideways (within a small search radius) wherever
// passableForClass says this class can't run through the terrain there —
// ADR 0028 §6.2's roads are "near-free", not free: a straight line the
// terrain won't allow simply is not built there, the same way a real road
// bends around a lake. A tile that IS passable for this class but is still
// Water (a thin stream for a local road; a stream OR a river for an
// arterial/highway) does NOT force a jog — the road crosses it straight and
// that crossing is reported back as a bridge (layoutCity, above). Ties (the
// line's own row/column, then the nearest neighbour) resolve toward the
// original line, so a road only ever jogs as far as it has to.
//
// THE ELEVATED HIGHWAY DOES NOT USE THIS FUNCTION. It is not traced at
// all — see elevatedHighwayPath: on pillars, it ignores terrain entirely.
func traceLineSet(size int, tiles []cityTile, lines []int, class int) []roadTile {
	ok := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < size && y < size && passableForClass(tiles[y*size+x], class)
	}

	var out []roadTile
	for _, l := range lines {
		for x := 0; x < size; x++ {
			y := jog(size, l, func(y int) bool { return ok(x, y) })
			out = append(out, roadTile{X: x, Y: y, Class: class})
		}
		for y := 0; y < size; y++ {
			x := jog(size, l, func(x int) bool { return ok(x, y) })
			out = append(out, roadTile{X: x, Y: y, Class: class})
		}
	}
	return out
}

// elevatedHighwayPath is a single, roughly-straight, full-width line
// through the grid, tagged roadClassHighway — deliberately NOT terrain-
// aware (see traceRoadClass's own doc comment): an elevated highway sits on
// pillars, so the web client places it at a fixed height above the highest
// terrain along its path rather than Go needing to avoid water, steep
// ground or anything else here. A horizontal line three-quarters of the way
// down the grid — "beside" the downtown core rather than straight through
// its own centre, still comfortably inside the window either way.
func elevatedHighwayPath(size int) []roadTile {
	row := size - size/4
	if row >= size {
		row = size - 1
	}
	if row < 0 {
		row = 0
	}
	path := make([]roadTile, size)
	for x := 0; x < size; x++ {
		path[x] = roadTile{X: x, Y: row, Class: roadClassHighway}
	}
	return path
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

// anyCornerDeposit reports whether a Deposit-flagged lot exists within
// spec's own w x h footprint at (x,y), widened by depositSearchMarginLots
// (export_city.go) on every side. A coarse-mesh deposit's exact lot
// footprint (export_city.go's cityTileFromFine) is itself only
// lotsPerTile lots wide, so a mine/well built a few lots off to the side
// should still count as "near" it rather than needing to land exactly
// inside the footprint.
func anyCornerDeposit(tiles []cityTile, size, x, y, w, h int) bool {
	if x+w > size || y+h > size {
		return false
	}
	x0, y0 := x-depositSearchMarginLots, y-depositSearchMarginLots
	x1, y1 := x+w+depositSearchMarginLots, y+h+depositSearchMarginLots
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > size {
		x1 = size
	}
	if y1 > size {
		y1 = size
	}
	for dy := y0; dy < y1; dy++ {
		for dx := x0; dx < x1; dx++ {
			if tiles[dy*size+dx].Deposit {
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
