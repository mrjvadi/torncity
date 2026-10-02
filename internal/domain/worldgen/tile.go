package worldgen

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// Tile primitives (ADR 0042 section 9.3, phase T0): addressing and queries at
// the level of one base-LOD tile (about 305 m on Earth's radius).
//
// Everything here is a PURE FUNCTION of the world, its seed and its
// GeneratorVersion: it adds no state and changes no existing output, so the
// frozen live world is untouched. Tiles are read through a TileSource (a chunk
// cache in production, a synthetic grid in tests).
//
// Integer arithmetic. Elevations are the chunk's int16; the only floating
// point is the cube-sphere mapping that chunk addressing already uses (see
// chunk_address.go's VISUAL/QUERY note). Everything downstream of a TileID
// (slopes, searches, ties) is exact.

const tileCoordBits = 28

// TileID names one base-LOD tile: face (3 bits) | gx (28 bits) | gy (28 bits),
// gx and gy being the tile's global index along the face's edge. It is an
// int64 so it can be a map key, a database key and a sort key; the order of
// ids is the deterministic tie-break of every search in this package.
type TileID int64

// MakeTileID packs a tile address. gx and gy must be in [0, 2^28).
func MakeTileID(face int8, gx, gy int32) TileID {
	return TileID(int64(face)<<(2*tileCoordBits) | int64(gx)<<tileCoordBits | int64(gy))
}

// Parts unpacks a TileID.
func (t TileID) Parts() (face int8, gx, gy int32) {
	const mask = 1<<tileCoordBits - 1
	return int8(int64(t) >> (2 * tileCoordBits)), int32(int64(t) >> tileCoordBits & mask), int32(int64(t) & mask)
}

// TileGrid is the number of tiles along one cube-face edge at base LOD.
func (w *World) TileGrid() int32 {
	return chunksPerEdge(w.Params.ChunkBaseLOD) * int32(w.Params.ChunkTileEdge)
}

// TileLengthM is a tile's edge length in whole metres (305 on Earth's radius).
func (w *World) TileLengthM() int {
	return int(math.Round(w.Params.PlanetRadiusKm * 1000 * (math.Pi / 2) / float64(w.TileGrid())))
}

// TileDiagonalM is the diagonal step between two tiles in whole metres (431).
func (w *World) TileDiagonalM() int {
	return int(math.Round(float64(w.TileLengthM()) * math.Sqrt2))
}

func (w *World) tileFlatUV(face int8, gx, gy int32) (u, v float64) {
	n := float64(w.TileGrid())
	return -1 + 2*(float64(gx)+0.5)/n, -1 + 2*(float64(gy)+0.5)/n
}

func (w *World) tileAtFlat(face int8, u, v float64) TileID {
	n := w.TileGrid()
	idx := func(f float64) int32 {
		i := int32(math.Floor((clamp(f, -1, 1) + 1) / 2 * float64(n)))
		if i >= n {
			i = n - 1
		}
		if i < 0 {
			i = 0
		}
		return i
	}
	return MakeTileID(face, idx(u), idx(v))
}

// TileOfLatLon returns the tile containing a point.
func (w *World) TileOfLatLon(latDeg, lonDeg float64) TileID {
	la, lo := latDeg*math.Pi/180, lonDeg*math.Pi/180
	face, u, v := directionToFace(math.Cos(la)*math.Cos(lo), math.Cos(la)*math.Sin(lo), math.Sin(la))
	return w.tileAtFlat(face, u, v)
}

// TileCentre returns a tile's centre as latitude and longitude in degrees.
func (w *World) TileCentre(t TileID) (latDeg, lonDeg float64) {
	face, gx, gy := t.Parts()
	u, v := w.tileFlatUV(face, gx, gy)
	x, y, z := normalize3(faceDirection(face, u, v))
	return math.Asin(clamp(z, -1, 1)) * 180 / math.Pi, math.Atan2(y, x) * 180 / math.Pi
}

// TileChunk returns the base chunk holding a tile and the tile's local index.
func (w *World) TileChunk(t TileID) (addr ChunkAddr, i, j int) {
	face, gx, gy := t.Parts()
	e := int32(w.Params.ChunkTileEdge)
	return ChunkAddr{Face: face, LOD: w.Params.ChunkBaseLOD, X: gx / e, Y: gy / e}, int(gx % e), int(gy % e)
}

// Offset returns the tile dx, dy tiles away, crossing cube-face edges: a tile
// past the face edge is mapped through the cube-sphere to whichever face its
// centre falls on, the same construction ChunkAddr.Neighbor uses. At a cube
// corner (where three faces meet) a diagonal offset resolves deterministically
// to one of the corner's tiles.
func (w *World) Offset(t TileID, dx, dy int32) TileID {
	face, gx, gy := t.Parts()
	n := w.TileGrid()
	nx, ny := gx+dx, gy+dy
	if nx >= 0 && nx < n && ny >= 0 && ny < n {
		return MakeTileID(face, nx, ny)
	}
	u, v := w.tileFlatUV(face, nx, ny)
	f, fu, fv := directionToFace(faceDirection(face, u, v))
	return w.tileAtFlat(f, fu, fv)
}

// Neighbours8 returns a tile's 8 surrounding tiles in the fixed order
// N, NE, E, SE, S, SW, W, NW (the order ChunkAddr.Neighbors8 uses).
func (w *World) Neighbours8(t TileID) [8]TileID {
	return [8]TileID{
		w.Offset(t, 0, 1), w.Offset(t, 1, 1), w.Offset(t, 1, 0), w.Offset(t, 1, -1),
		w.Offset(t, 0, -1), w.Offset(t, -1, -1), w.Offset(t, -1, 0), w.Offset(t, -1, 1),
	}
}

// stepLengthM is the distance to a neighbour in the Neighbours8 order.
func (w *World) stepLengthM(k int) int {
	if k%2 == 1 {
		return w.TileDiagonalM()
	}
	return w.TileLengthM()
}

// TileSource reads base tiles.
type TileSource interface {
	TileAt(t TileID) (ChunkTile, error)
}

// ChunkTileSource reads tiles through a chunk cache. It is safe for concurrent
// use (ChunkCache itself is not).
type ChunkTileSource struct {
	W     *World
	mu    sync.Mutex
	cache *ChunkCache
}

// NewChunkTileSource builds a source over w holding up to limit chunks.
func NewChunkTileSource(w *World, limit int) *ChunkTileSource {
	return &ChunkTileSource{W: w, cache: NewChunkCache(w, limit)}
}

// TileAt implements TileSource.
func (s *ChunkTileSource) TileAt(t TileID) (ChunkTile, error) {
	addr, i, j := s.W.TileChunk(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.cache.Get(addr)
	if err != nil {
		return ChunkTile{}, err
	}
	return c.TileAt(i, j), nil
}

// TileSlopeBPS is the largest grade from a tile to any of its 8 neighbours in
// basis points of rise over run (a 12 % grade is 1200): |dh| x 10000 / run,
// with run the 305 m straight or 431 m diagonal step. Elevation is the
// chunk's metre-scaled int16.
func (w *World) TileSlopeBPS(src TileSource, t TileID) (int, error) {
	here, err := src.TileAt(t)
	if err != nil {
		return 0, err
	}
	best := 0
	for k, n := range w.Neighbours8(t) {
		nt, err := src.TileAt(n)
		if err != nil {
			return 0, err
		}
		d := int(here.Elevation) - int(nt.Elevation)
		if d < 0 {
			d = -d
		}
		if g := d * 10000 / w.stepLengthM(k); g > best {
			best = g
		}
	}
	return best, nil
}

// WaterKind classifies the water a tile carries.
type WaterKind uint8

// The kinds, narrowest channel first. Ocean and Lake are bodies, the rest
// are channels a road crosses (ADR 0042 section 6.3).
const (
	WaterNone WaterKind = iota
	WaterStream
	WaterRiver
	WaterGreatRiver
	WaterLake
	WaterOcean
)

// riverFlowMultiple and greatRiverFlowMultiple are ADR 0042 section 6.3's
// thresholds: a river is a coarse flow of 4 x the world's river threshold, a
// great river 16 x. Fixed by the ADR, as fineRiverFlowMultiple is.
const (
	riverFlowMultiple      = 4
	greatRiverFlowMultiple = 16
)

// WaterWidthM is the span a crossing of this kind needs, per ADR 0042 section
// 6.3: stream up to 10 m, river 40 m, great river 200 m.
func (k WaterKind) WaterWidthM() int {
	switch k {
	case WaterStream:
		return 10
	case WaterRiver:
		return 40
	case WaterGreatRiver:
		return 200
	}
	return 0
}

// TileWaterKind reads a tile's water: ocean and lake from the tile flags; a
// channel from the stream flag, widened by the nearest coarse cell's river
// flow against the world's threshold (stream, then river at 4x, then great
// river at 16x).
func (w *World) TileWaterKind(src TileSource, t TileID) (WaterKind, error) {
	ct, err := src.TileAt(t)
	if err != nil {
		return WaterNone, err
	}
	return w.waterKindOf(ct, t), nil
}

func (w *World) waterKindOf(ct ChunkTile, t TileID) WaterKind {
	switch {
	case ct.IsOcean():
		return WaterOcean
	case ct.IsLake():
		return WaterLake
	}
	flow := int32(0)
	if thr := int32(w.Params.RiverFlowThreshold); thr > 0 {
		la, lo := w.TileCentre(t)
		flow = w.Cells[w.NearestCell(la, lo)].RiverFlow
		switch {
		case ct.IsStream() && flow >= greatRiverFlowMultiple*thr:
			return WaterGreatRiver
		case ct.IsStream() && flow >= riverFlowMultiple*thr:
			return WaterRiver
		}
	}
	if ct.IsStream() {
		return WaterStream
	}
	return WaterNone
}

// WaterHit is a nearest-water search result.
type WaterHit struct {
	Tile     TileID
	Kind     WaterKind
	Distance int // rings (Chebyshev steps) from the start
}

// NearestWater is a bounded breadth-first search for the nearest tile of the
// wanted kind within r rings. Rings are expanded in the fixed Neighbours8
// order and ties go to the lowest TileID, so every replica agrees. ok is false
// when nothing is in reach.
func (w *World) NearestWater(src TileSource, from TileID, r int, want WaterKind) (hit WaterHit, ok bool, err error) {
	seen := map[TileID]bool{from: true}
	frontier := []TileID{from}
	for d := 0; d <= r; d++ {
		var found []TileID
		for _, t := range frontier {
			k, err := w.TileWaterKind(src, t)
			if err != nil {
				return WaterHit{}, false, err
			}
			if k == want {
				found = append(found, t)
			}
		}
		if len(found) > 0 {
			sort.Slice(found, func(i, j int) bool { return found[i] < found[j] })
			return WaterHit{Tile: found[0], Kind: want, Distance: d}, true, nil
		}
		var next []TileID
		for _, t := range frontier {
			for _, n := range w.Neighbours8(t) {
				if !seen[n] {
					seen[n] = true
					next = append(next, n)
				}
			}
		}
		frontier = next
	}
	return WaterHit{}, false, nil
}

// Peak is a local maximum with its topographic prominence.
type Peak struct {
	Tile       TileID
	Elevation  int
	Prominence int
}

// PeaksNear returns the local maxima within r rings of a tile with their
// prominence inside that window: the height above the lowest saddle that
// still leads to higher ground (the standard definition; the highest peak of
// the window is measured down to the window's lowest tile). Peaks are sorted
// by prominence, then elevation, then TileID, all descending but the id.
//
// The algorithm is the usual sweep: tiles are visited from high to low and
// joined to already-visited neighbours with a union-find; when two basins
// meet at a saddle, the lower peak's prominence is its height minus the
// saddle's. Equal elevations are ordered by TileID so a plateau has exactly
// one peak.
func (w *World) PeaksNear(src TileSource, from TileID, r int) ([]Peak, error) {
	elev := map[TileID]int{}
	ring := map[TileID]int{from: 0}
	frontier := []TileID{from}
	for d := 0; d <= r; d++ {
		var next []TileID
		for _, t := range frontier {
			ct, err := src.TileAt(t)
			if err != nil {
				return nil, err
			}
			elev[t] = int(ct.Elevation)
			if d == r {
				continue
			}
			for _, n := range w.Neighbours8(t) {
				if _, ok := ring[n]; !ok {
					ring[n] = d + 1
					next = append(next, n)
				}
			}
		}
		frontier = next
	}

	order := make([]TileID, 0, len(elev))
	for t := range elev {
		order = append(order, t)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if elev[a] != elev[b] {
			return elev[a] > elev[b]
		}
		return a < b
	})

	parent := map[TileID]TileID{}
	top := map[TileID]TileID{} // root -> its highest tile (the peak)
	var find func(TileID) TileID
	find = func(x TileID) TileID {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	prom := map[TileID]int{}
	isPeak := map[TileID]bool{}
	minElev := elev[order[len(order)-1]]
	for _, t := range order {
		parent[t] = t
		top[t] = t
		isPeak[t] = true
		for _, n := range w.Neighbours8(t) {
			if _, done := parent[n]; !done {
				continue
			}
			isPeak[t] = false
			a, b := find(t), find(n)
			if a == b {
				continue
			}
			pa, pb := top[a], top[b]
			low, high := pa, pb
			if elev[pa] > elev[pb] || (elev[pa] == elev[pb] && pa < pb) {
				low, high = pb, pa
			}
			// the lower basin's peak is a real peak, measured to this saddle
			if isPeak[low] {
				prom[low] = elev[low] - elev[t]
			}
			root, other := a, b
			if find(high) == b {
				root, other = b, a
			}
			parent[other] = root
			top[root] = high
		}
	}
	root := find(order[0])
	prom[top[root]] = elev[top[root]] - minElev

	out := make([]Peak, 0, len(prom))
	for t, p := range prom {
		if p <= 0 {
			continue // a flat plateau's tie-break tile is not a peak
		}
		out = append(out, Peak{Tile: t, Elevation: elev[t], Prominence: p})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Prominence != b.Prominence {
			return a.Prominence > b.Prominence
		}
		if a.Elevation != b.Elevation {
			return a.Elevation > b.Elevation
		}
		return a.Tile < b.Tile
	})
	return out, nil
}

// RegionKind says which named-feature list a region came from.
type RegionKind uint8

// The kinds, matching World's lists.
const (
	RegionContinent RegionKind = iota
	RegionSea
	RegionMountainRange
	RegionRiver
)

// RegionRef points into one of World's named-feature lists.
type RegionRef struct {
	Kind  RegionKind
	Index int
}

// RegionIndex maps a coarse cell to the named features that contain it. It is
// built once per world and read-only afterwards.
type RegionIndex struct {
	w      *World
	byCell map[int32][]RegionRef
}

// NewRegionIndex builds the cell to region index (a pass over every named
// feature's cells).
func NewRegionIndex(w *World) *RegionIndex {
	ri := &RegionIndex{w: w, byCell: map[int32][]RegionRef{}}
	add := func(kind RegionKind, cells []int32, i int) {
		for _, c := range cells {
			ri.byCell[c] = append(ri.byCell[c], RegionRef{Kind: kind, Index: i})
		}
	}
	for i, r := range w.Continents {
		add(RegionContinent, r.Cells, i)
	}
	for i, r := range w.Seas {
		add(RegionSea, r.Cells, i)
	}
	for i, r := range w.MountainRanges {
		add(RegionMountainRange, r.Cells, i)
	}
	for i, r := range w.Rivers {
		add(RegionRiver, r.Cells, i)
	}
	return ri
}

// NamedPlace is what a tile is "in": its nearest coarse cell and the named
// features that cell belongs to, with their names in both languages.
type NamedPlace struct {
	Cell    int32
	Regions []RegionRef
	Names   []Name
}

// NamedAt answers "what is here" for a tile.
func (ri *RegionIndex) NamedAt(t TileID) NamedPlace {
	la, lo := ri.w.TileCentre(t)
	cell := ri.w.NearestCell(la, lo)
	refs := ri.byCell[cell]
	p := NamedPlace{Cell: cell, Regions: refs}
	for _, r := range refs {
		p.Names = append(p.Names, ri.name(r))
	}
	return p
}

func (ri *RegionIndex) name(r RegionRef) Name {
	switch r.Kind {
	case RegionContinent:
		return ri.w.Continents[r.Index].Name
	case RegionSea:
		return ri.w.Seas[r.Index].Name
	case RegionMountainRange:
		return ri.w.MountainRanges[r.Index].Name
	default:
		return ri.w.Rivers[r.Index].Name
	}
}

func (t TileID) String() string {
	f, x, y := t.Parts()
	return fmt.Sprintf("%d:%d:%d", f, x, y)
}
