package worldgen

import "math"

// This file is the two-level generation chunk.go's package doc promises:
// the coarse 40k-cell mesh (world.go's Generate) stays the one expensive,
// whole-planet computation — plates, climate, rivers, deposits all need the
// whole mesh to make sense — and a Chunk is a CHEAP, LOCAL sample of that
// already-computed field, interpolated across the mesh's nearby cells and
// refined with local FastNoiseLite detail no 40k-cell mesh could represent
// (a single hill, a stream's meander, a coastline's actual wrinkle).
//
// EXACT vs VISUAL, same split the package doc draws for the coarse mesh. A
// tile's elevation, biome and river/stream flag are computed from this
// package's own noise/RNG and portable float ops (+,-,*,/,floor,sqrt) once
// the tile's 3D sample point is known — reproducible given that point.
// FINDING that point (ChunkAddr.TileUnitSpherePoint) goes through the same
// trig chunk_address.go already documents as visual/query-only, which is
// why chunk.go inherits that file's caveat rather than introducing a new
// one: a tile's exact identity (which chunk, which local index) is already
// portable; only its precise sub-metre position could, in principle, shift
// by a bit between math libraries. See the project report's open
// questions.

// idwK is how many nearby coarse cells a tile blends between. 4 is enough
// to smooth the visible facet lines a single nearest-cell lookup would show
// at this much finer a sampling resolution, without paying for a much
// larger blend radius that would wash out real coarse-scale structure
// (coastlines, mountain ranges) a chunk is supposed to still track.
const idwK = 4

// ChunkTile is one tile's full generated state, compact by design: a whole
// base chunk (32x32 by default) is meant to move over the wire and sit in
// an LRU cache across many replicas (see chunk_cache.go, chunk_codec.go).
type ChunkTile struct {
	// Elevation mirrors the coarse Cell.Elevation's units, narrowed to
	// int16: the coarse field alone never approaches int16's +-32767
	// range (elevation.go quantizes to roughly +-10000), and the local
	// detail noise added on top (ChunkDetailAmplitude) is tuned to stay
	// well inside that same headroom — see chunk_test.go's range check.
	Elevation int16
	// Biome is an index into the SAME Content.Biomes table the coarse
	// World uses — a tile never needs its own biome table.
	Biome uint8
	// Flags is a small bitset: tileFlagOcean, tileFlagStream, tileFlagLake.
	Flags uint8
	// DepositTile is 0 if this tile carries no exact deposit placement,
	// else 1+the index into the owning Chunk's Deposits slice.
	DepositTile uint8
}

const (
	tileFlagOcean = 1 << iota
	tileFlagStream
	tileFlagLake
)

func (t ChunkTile) IsOcean() bool  { return t.Flags&tileFlagOcean != 0 }
func (t ChunkTile) IsStream() bool { return t.Flags&tileFlagStream != 0 }
func (t ChunkTile) IsLake() bool   { return t.Flags&tileFlagLake != 0 }

// ChunkDeposit is one coarse-mesh Deposit's exact placement inside this
// base chunk: which tiles it occupies. The deposit's economic data (grade,
// reserve) stays on the coarse Deposit — a chunk only adds WHERE, at tile
// resolution, that already-decided deposit actually sits.
type ChunkDeposit struct {
	DepositID    string
	ResourceCode string
	TileX, TileY uint8
}

// Chunk is one chunk's full generated tile grid: a base-LOD chunk is
// gameplay resolution (deposits, streams, hills); a coarser chunk (LOD <
// ChunkBaseLOD) is the cheap, zoomed-out read described in the project
// report — the same coarse fields, no local detail, no deposits.
//
// SEED-FIRST, same rule as World: nothing here is ever "the truth" that
// must be stored. Generate(seed,...).GenerateChunk(addr) reproduces a Chunk
// byte-for-byte from (seed, GeneratorVersion, Params/Content, addr) alone;
// see chunk_golden_test.go. What a database DOES need — placements, claims,
// deposit depletion — is a per-chunk DELTA on top, keyed by ChunkAddr (see
// the project report's proposed schema).
type Chunk struct {
	Addr             ChunkAddr
	Seed             uint64
	GeneratorVersion int
	TileEdge         int
	Tiles            []ChunkTile // row-major: Tiles[j*TileEdge+i]
	Deposits         []ChunkDeposit
}

// TileAt returns tile (i,j)'s data, i,j in [0,TileEdge).
func (c *Chunk) TileAt(i, j int) ChunkTile { return c.Tiles[j*c.TileEdge+i] }

// coarseSample is one tile's inverse-distance-weighted blend of the coarse
// mesh's fields at a point, plus the single nearest cell's discrete fields
// (biome, ocean/lake — blending a biome INDEX numerically would be
// nonsensical, so those come from the single closest coarse cell instead,
// the same convention biome.go's own classification already treats as the
// right answer for "not exactly on a sample point").
type coarseSample struct {
	elevation float64
	riverFlow float64
	nearest   int32 // nearest coarse cell id, for biome/ocean/lake
}

// chunkCandidateK is how many coarse cells sampleCoarseAmong is handed as
// its candidate pool per chunk. It only needs to safely cover idwK's worth
// of TRUE nearest neighbours for every tile in the chunk, not just its
// centre — see coarseCandidates' doc for why one pool safely serves the
// whole chunk.
const chunkCandidateK = 3 * idwK

// coarseCandidates returns the chunkCandidateK coarse cells nearest to
// addr's centre — the shared candidate pool every tile in this chunk blends
// among (sampleCoarseAmong), instead of each of a chunk's TileEdge^2 tiles
// running its own full nearest-neighbour search.
//
// WHY ONE SEARCH PER CHUNK IS ENOUGH. The coarse mesh's average cell
// spacing at the default 40,000 cells is on the order of tens of
// kilometres (the sphere's surface area divided by cell count); a base
// chunk spans under 10km (Params.TileMeters x ChunkTileEdge). A chunk's
// footprint is therefore small compared to one coarse cell, so the set of
// coarse cells near ANY tile in the chunk is, to a very good
// approximation, the same set near the chunk's centre — chunkCandidateK
// (3x the actual blend width) is the safety margin for a tile sitting
// near a chunk's corner, farthest from that centre. This is what turns
// chunk generation from TileEdge^2 (1024, by default) expensive
// bucket-grid searches into exactly one, which is the difference between
// this package's "a few ms per chunk" target (see the project report's
// timing numbers) and the ~15ms a naive per-tile search actually measured
// at during development.
func (w *World) coarseCandidates(addr ChunkAddr) []int32 {
	cx, cy, cz := addr.UnitSpherePoint()
	latDeg := math.Asin(clamp(cz, -1, 1)) * 180 / math.Pi
	lonDeg := math.Atan2(cy, cx) * 180 / math.Pi
	ids, _ := w.NearestCells(latDeg, lonDeg, chunkCandidateK)
	return ids
}

// sampleCoarseAmong blends the coarse fields at (x,y,z) via inverse-distance
// weighting over its idwK nearest cells FROM a pre-fetched candidate pool
// (coarseCandidates) — no bucket-grid search, just a distance to each
// candidate and a partial sort, both O(chunkCandidateK) and cheap. This is
// the same inverse-distance-weighting technique the preview renderer uses
// (cmd/worldpreview/render.go) to turn a faceted 40k-cell mesh into a
// smooth-looking field, reused here for the same reason: a chunk samples at
// a resolution far finer than one coarse cell, so a naive nearest-cell-only
// read would show hard facets at chunk scale.
func (w *World) sampleCoarseAmong(candidates []int32, ranked []candDist, x, y, z float64) coarseSample {
	if len(candidates) == 0 {
		return coarseSample{}
	}
	for i, id := range candidates {
		p := w.Cells[id].Point
		dx, dy, dz := x-p.X, y-p.Y, z-p.Z
		ranked[i] = candDist{id, dx*dx + dy*dy + dz*dz}
	}
	keep := idwK
	if keep > len(ranked) {
		keep = len(ranked)
	}
	sortCandidates(ranked, keep)

	var wSum, elevSum, flowSum float64
	for k := 0; k < keep; k++ {
		d2 := ranked[k].d2
		// Inverse-distance weight, epsilon-guarded so a sample that lands
		// exactly on a coarse cell's own point doesn't divide by zero.
		wt := 1 / (d2 + 1e-12)
		wSum += wt
		elevSum += wt * float64(w.Cells[ranked[k].idx].Elevation)
		flowSum += wt * float64(w.Cells[ranked[k].idx].RiverFlow)
	}
	return coarseSample{
		elevation: elevSum / wSum,
		riverFlow: flowSum / wSum,
		nearest:   ranked[0].idx,
	}
}

// GenerateChunk builds one chunk. It depends only on w (itself a pure
// function of seed/params/content — see Generate's own doc) and addr: two
// calls with the same seed, version, params/content and addr always
// produce byte-identical tiles (chunk_golden_test.go).
func (w *World) GenerateChunk(addr ChunkAddr) (*Chunk, error) {
	baseLOD := w.Params.ChunkBaseLOD
	if !addr.Valid(baseLOD) {
		return nil, errInvalidParam("chunk_addr", "face/lod/x/y out of range for this world's ChunkBaseLOD")
	}
	edge := w.Params.ChunkTileEdge
	isBase := addr.LOD == baseLOD

	candidates := w.coarseCandidates(addr)
	ranked := make([]candDist, len(candidates))

	tiles := make([]ChunkTile, edge*edge)
	for j := 0; j < edge; j++ {
		for i := 0; i < edge; i++ {
			x, y, z := addr.TileUnitSpherePoint(edge, i, j)
			cs := w.sampleCoarseAmong(candidates, ranked, x, y, z)

			elev := cs.elevation
			var stream bool
			if isBase {
				detail := w.chunkDetailNoise.Sample3(x, y, z)
				elev += detail * float64(w.Params.ChunkDetailAmplitude)

				// A small stream: the coarse-interpolated flow accumulation
				// already says "water drains through here"; the local
				// stream noise picks a thin, wandering subset of those
				// tiles instead of flooding the whole drainage corridor,
				// so a river reads as a meandering line at tile scale
				// rather than a wide, straight smear the coarse cell's own
				// resolution would otherwise imply.
				if cs.riverFlow >= float64(w.Params.RiverFlowThreshold) {
					wiggle := w.chunkStreamNoise.Sample3(x*3, y*3, z*3)
					stream = wiggle > -0.15 && wiggle < 0.15
				}
			}

			e16 := clampInt16(quantize(elev))
			nearest := w.Cells[cs.nearest]

			var flags uint8
			isOcean := e16 <= 0
			if isOcean {
				flags |= tileFlagOcean
			}
			if nearest.IsLake && !isOcean {
				flags |= tileFlagLake
			}
			if stream && !isOcean {
				flags |= tileFlagStream
			}

			tiles[j*edge+i] = ChunkTile{
				Elevation: e16,
				Biome:     nearest.BiomeIdx,
				Flags:     flags,
			}
		}
	}

	c := &Chunk{
		Addr:             addr,
		Seed:             w.Seed,
		GeneratorVersion: w.GeneratorVersion,
		TileEdge:         edge,
		Tiles:            tiles,
	}

	if isBase {
		c.Deposits = w.placeDepositTiles(addr, c)
	}
	return c, nil
}

// placeDepositTiles scatters each coarse Deposit whose coarse cell falls in
// this base chunk onto a deterministic handful of exact tiles.
//
// EXACT: the tile positions are drawn from this package's own SplitMix64
// stream (rng.go), keyed by the deposit's own stable ID and this chunk's
// address, never by iteration order over a map or slice whose order could
// differ between an equivalent-but-differently-built World.
func (w *World) placeDepositTiles(addr ChunkAddr, c *Chunk) []ChunkDeposit {
	edge := w.Params.ChunkTileEdge
	perDeposit := w.Params.ChunkDepositTilesPerDeposit
	var out []ChunkDeposit

	for _, d := range w.Deposits {
		cell := w.Cells[d.CellID]
		depositChunk := ChunkOfLatLon(addr.LOD, cell.Point.LatDeg, cell.Point.LonDeg)
		if depositChunk != addr {
			continue
		}

		rr := w.chunkRand(addr, "deposit:"+d.ID)
		placed := 0
		attempts := 0
		seen := make(map[[2]uint8]bool, perDeposit)
		for placed < perDeposit && attempts < perDeposit*8 {
			attempts++
			ti := uint8(rr.IntN(edge))
			tj := uint8(rr.IntN(edge))
			key := [2]uint8{ti, tj}
			if seen[key] {
				continue
			}
			seen[key] = true

			idx := int(tj)*edge + int(ti)
			if c.Tiles[idx].IsOcean() && !w.resourceAllowsOcean(d.ResourceCode) {
				continue
			}
			c.Tiles[idx].DepositTile = uint8(len(out) + 1)
			out = append(out, ChunkDeposit{
				DepositID:    d.ID,
				ResourceCode: d.ResourceCode,
				TileX:        ti,
				TileY:        tj,
			})
			placed++
		}
	}
	return out
}

func (w *World) resourceAllowsOcean(resourceCode string) bool {
	for _, r := range w.Content.Resources {
		if r.Code == resourceCode {
			return r.AllowOcean
		}
	}
	return false
}

// chunkRand derives a chunk-and-tag-scoped RNG stream the same way rng.go's
// Rand.Sub derives every other named stream in this package, keyed off the
// world seed plus the chunk's own address so two different chunks (or the
// same chunk regenerated) never share a stream by accident.
func (w *World) chunkRand(addr ChunkAddr, tag string) *Rand {
	root := NewRand(w.Seed)
	return root.Sub("chunk").Sub(addrTag(addr)).Sub(tag)
}

func addrTag(a ChunkAddr) string {
	// A short, stable, human-readable tag — fine to be a bit verbose since
	// it is only ever hashed (fnv1a64), never displayed.
	buf := make([]byte, 0, 32)
	buf = appendInt(buf, int64(a.Face))
	buf = append(buf, ':')
	buf = appendInt(buf, int64(a.LOD))
	buf = append(buf, ':')
	buf = appendInt(buf, int64(a.X))
	buf = append(buf, ':')
	buf = appendInt(buf, int64(a.Y))
	return string(buf)
}

func appendInt(buf []byte, v int64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	if v < 0 {
		buf = append(buf, '-')
		v = -v
	}
	start := len(buf)
	for v > 0 {
		buf = append(buf, byte('0'+v%10))
		v /= 10
	}
	// reverse the digits just appended
	for l, r := start, len(buf)-1; l < r; l, r = l+1, r-1 {
		buf[l], buf[r] = buf[r], buf[l]
	}
	return buf
}

func clampInt16(v int32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
