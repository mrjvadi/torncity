package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is the DEMO export path (--export-city): picks one city site
// deterministically from an already-generated World, exports the 3x3-chunk
// neighbourhood of terrain around it as compact JSON, and lays a city out on
// real ground as the web client's --export-city demo consumes it. See the
// project report for why this is demo-only scaffolding, not ADR 0028 §6's
// real placement system.
//
// TWO RESOLUTIONS, ON PURPOSE. A base terrain TILE (worldgen.Params.
// TileMeters, ~305m) and a settlement LOT (lotMeters below, ~30.5m — a
// tenth of a tile) are different things: a tile is the finest terrain the
// world generator itself resolves; a lot is the finest unit a building
// actually occupies (ADR 0028 §4/§6). An earlier version of this export
// conflated the two — it laid the city out directly on tiles, which made
// every building ~305m wide and a 15x15 city ~4.5km across. This version
// exports BOTH grids, at their own native resolution, so the web client can
// render real tile-scale terrain as the far backdrop (coarseGrid below,
// unchanged in shape from before, just documented and renamed for clarity)
// and a real lot-scale settlement in the middle of it (fineGrid,
// worldgen.SampleFineLatLon's new sampling layer — see internal/domain/
// worldgen/fine.go).
//
// CUBE-SPHERE PLACEMENT, NOT EQUIRECTANGULAR. Converting a fine lot
// coordinate to lat/lon here reuses the SAME cube-sphere projection
// chunk_address.go's TileFlatUVFrac/FaceDirection already use for a tile,
// evaluated at a fractional, possibly-ghost (see TileFlatUVFrac's own doc)
// tile coordinate local to the city's own centre chunk — not a flat
// equirectangular metres-per-degree approximation. This is more accurate
// over the fine grid's few-km extent (no seam mismatch against the coarse
// grid it sits inside) and was no more code than the simpler approximation
// would have been, since the exact machinery already existed. It shares the
// package's usual VISUAL/QUERY caveat (sin/cos, tan/atan): fine outside a
// few tiles of a genuine cube-face edge or corner, which this demo's export
// window never approaches for an ordinary city site.

// citySize is ADR 0028 §4's own city-TIER lot grid (village 5x5, town 9x9,
// city 15x15 LOTS, ~457m) — kept at its documented value even though the
// DEMO export below no longer uses it directly for its own window size.
// citySize stays here as the ADR reference point; nothing in this demo
// path is a real settlement, so nothing outside cmd/worldpreview depends
// on this constant's value.
const citySize = 15

// demoCitySize is the actual --export-city window size, in LOTS: bigger
// than ADR 0028's own city tier on purpose. The web client is building a
// realistic procedural city (real streets, extruded buildings with real
// floor heights, downtown towers, an elevated highway) — a 15x15
// (~457m) window reads as a village on that renderer, not a city. ~48x48
// (~1.46km) is a genuine small-city footprint while still comfortably
// fitting inside flatSearchTiles' own flat-neighbourhood search (below)
// and keeping the export's byte cost in the low hundreds of KB (see
// runExportCity's logged size).
const demoCitySize = 48

// lotsPerTile is how many settlement lots make up one base terrain tile
// along an edge — the ADR 0028 default (~30.5m lots inside a ~305m tile).
// lotMeters is computed FROM Params.TileMeters, never hardcoded, so it
// tracks PlanetRadiusKm/ChunkBaseLOD/ChunkTileEdge if those ever change.
const lotsPerTile = 10

// flatSearchTiles is bestCityWindow's own search-window size, in base TILES
// (~305m each) — deliberately decoupled from demoCitySize (a LOT count,
// above): this only picks a generously flat, buildable neighbourhood
// (scoreCitySite already required at least this much of a flat run to
// consider the site at all) for the real lot-scale city core to sit in the
// MIDDLE of, with room to spare on every side. 20 tiles (~6.1km) generously
// exceeds demoCitySize+2*margin's own extent (~3.9km, see fineMarginMeters
// below) so the fine grid's margin never spills past the verified-flat
// window into unchecked ground.
const flatSearchTiles = 20

// fineMarginMeters is how far past the city's own demoCitySize x
// demoCitySize lot core the fine grid extends on every side, so the web client has real
// lot-resolution terrain to render right up to (and a little past) the
// player's view of the city, instead of a hard fine/coarse seam sitting
// inside it. ~1.2km keeps the fine grid's own byte cost (see runExportCity's
// logged size) comfortably under a few hundred KB even packed generously;
// see this file's byte-budget notes on Elevation encoding below.
const fineMarginMeters = 1200.0

// fineSteepDeltaM is the lot-scale unbuildable-slope threshold: an
// elevation change of this many units (loosely metre-scaled — see
// worldgen.Elevation's own doc comment in params.go) between two ADJACENT
// lots (~lotMeters apart) reads as too steep to build on or road across.
// Modelled on an unbuildable ~26% grade over one lot: 0.26 * lotMeters
// (~30.5m at defaults) ~= 8. The OLD tile-scale steepElevationDelta (below,
// still used for the coarse grid's own flat-window search) is meaningless
// here: a 220-unit jump was calibrated for two points ~305m apart, ten
// times further apart than two adjacent lots, so the same number would
// almost never trigger at lot scale even on real hillsides.
const fineSteepDeltaM = 8.0

// depositSearchMarginLots widens anyCornerDeposit's own building-footprint
// search (citylayout.go) by this many lots on every side, so a mine/well
// still finds a coarse-mesh deposit's (tile-resolution) footprint from a
// few lots away rather than needing a building to land exactly inside it.
const depositSearchMarginLots = 3

// riverFlowMultiple is how much higher a coarse cell's RiverFlow must be
// than Params.RiverFlowThreshold before findCitySite/scoreCitySite treat it
// as a genuine RIVER site (not just "a stream somewhere in the chunk") —
// the SAME cutoff internal/domain/worldgen/fine.go's own
// fineRiverFlowMultiple uses for StreamKindRiver, duplicated here rather
// than exported (it is a small, self-contained tuning constant, not worth
// widening that package's API for) — kept in sync by hand; if fine.go's
// own constant is ever retuned, this should move with it.
const riverFlowMultiple = 4

// exportChunkTiles is the export window: 3x3 base chunks around the chosen
// site (the task's own spec), giving the client a full chunk of margin on
// every side of the city to render as surrounding countryside.
const exportChunkSpan = 3

// cityExport is the whole --export-city JSON document.
type cityExport struct {
	Seed             uint64  `json:"seed"`
	GeneratorVersion int     `json:"generatorVersion"`
	TileMeters       float64 `json:"tileMeters"`
	// LotMeters is Params.TileMeters()/lotsPerTile — a settlement lot's real
	// width/height in metres (~30.5m at DefaultParams), the unit every
	// fineGrid/city coordinate below is expressed in.
	LotMeters     float64 `json:"lotMeters"`
	ChunkTileEdge int     `json:"chunkTileEdge"`
	LotsPerTile   int     `json:"lotsPerTile"`

	CenterChunk chunkAddrJSON `json:"centerChunk"`
	LatDeg      float64       `json:"latDeg"`
	LonDeg      float64       `json:"lonDeg"`

	// CoarseGrid is the FAR-LOD backdrop: base terrain TILE resolution
	// (~305m/cell, worldgen.Params.TileMeters), the same 3x3-chunk window
	// this export has always covered. The web client renders this as the
	// countryside the fine-resolution city sits inside, and blends the two
	// at the fine grid's own edge.
	CoarseGrid coarseGridJSON `json:"coarseGrid"`

	// FineGrid is the NEAR-LOD settlement window: LOT resolution
	// (~30.5m/cell, LotMeters), sampled with worldgen.SampleFineLatLon
	// rather than read off CoarseGrid, so a stream reads as a few lots wide
	// instead of a whole tile and the city's own buildings sit at their
	// real ~30m scale.
	FineGrid fineGridJSON `json:"fineGrid"`

	BiomeLegend    []biomeLegendEntry    `json:"biomeLegend"`
	ResourceLegend []resourceLegendEntry `json:"resourceLegend"`
	// Deposits is the coarse-mesh deposit list, in CoarseGrid's own TILE
	// coordinate space — unchanged from before. FineGrid's per-lot Deposit
	// flag (used for the city's mine/well placement) is derived FROM this
	// list, not exported separately: cityTileFromFine (below) converts
	// each entry's tile coordinate into its lot-coordinate footprint.
	Deposits []depositJSON `json:"deposits"`

	City citySummaryJSON `json:"city"`

	World worldSummaryJSON `json:"world"`
}

type chunkAddrJSON struct {
	Face int8  `json:"face"`
	LOD  int8  `json:"lod"`
	X    int32 `json:"x"`
	Y    int32 `json:"y"`
}

type biomeLegendEntry struct {
	Code     string `json:"code"`
	ColorHex string `json:"colorHex"`
}

type resourceLegendEntry struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	ColorHex string `json:"colorHex"`
}

type depositJSON struct {
	X            int    `json:"x"`
	Y            int    `json:"y"`
	ResourceCode string `json:"resource"`
}

// coarseGridJSON is TILE-resolution terrain (~305m/cell), row-major, origin
// at the export window's own north-west corner.
//
// BYTE BUDGET. Elevation is packed as little-endian int16 PAIRS into a
// []byte (2 bytes/tile) rather than a raw JSON number array: encoding/json
// marshals a []byte as a base64 STRING automatically, the same free
// compaction Biome/Flags already used before this export had a fine grid at
// all (a few thousand small integers as comma-separated JSON text costs
// roughly 4-5 bytes each; base64 of 2 raw bytes costs roughly 2.7). Decode
// on the client: atob() -> Uint8Array -> a DataView reading int16LE pairs
// (or equivalently a Int16Array over an even-byte-aligned copy, respecting
// platform endianness — DataView.getInt16(i*2, true) is the simplest
// portable choice). Biome/Flags keep their original encoding: one byte
// each, already free via the same []byte-is-base64 trick.
type coarseGridJSON struct {
	W int `json:"w"`
	H int `json:"h"`
	// Elevation is W*H little-endian int16 values (see packInt16LE), base64
	// via encoding/json's []byte handling.
	Elevation []byte `json:"elevation"`
	Biome     []byte `json:"biome"`
	Flags     []byte `json:"flags"`
}

// fineGridJSON is LOT-resolution terrain (~30.5m/cell, LotMeters), row-major,
// sampled with worldgen.SampleFineLatLon.
//
// PLACEMENT. OriginTileX/OriginTileY locate the fine grid's own north-west
// LOT inside CoarseGrid's own TILE coordinate space (fractional: a lot is a
// tenth of a tile, so this is almost never a whole number) — the client
// places the fine mesh against the coarse one using the SAME coordinate
// space both grids already share, rather than a second, independent
// lat/lon placement that could drift out of registration with the coarse
// grid by a pixel at the seam.
//
// STREAM/RIVER GEOMETRY. WaterKind's fine-resolution raster IS the
// channel's geometry (a mask a few lots wide, per fine.go's own narrow
// isoline band) — no separate vector polyline is exported; the client
// traces/meshes the WaterKind==1/2 cells into a ribbon directly.
type fineGridJSON struct {
	OriginTileX float64 `json:"originTileX"`
	OriginTileY float64 `json:"originTileY"`
	W           int     `json:"w"` // lot counts
	H           int     `json:"h"`
	// Elevation is W*H little-endian int16 values, same encoding as
	// coarseGridJSON.Elevation (see its doc comment) — worldgen.FineSample.
	// ElevationM is an unclamped float64; export rounds and clamps to
	// int16 range the same way chunk.go's own tile export already does.
	Elevation []byte `json:"elevation"`
	Biome     []byte `json:"biome"`
	// WaterKind is one byte/lot: 0 none, 1 stream, 2 river, 3 lake, 4
	// ocean — a strict superset of worldgen.StreamKind (which only knows
	// stream/river; lake/ocean come from FineSample.IsLake/IsOcean).
	WaterKind []byte `json:"waterKind"`
}

type citySummaryJSON struct {
	// OriginX/OriginY are the city's demoCitySize x demoCitySize lot core's
	// top-left corner, in FineGrid's own LOCAL lot-index space (0..W-1,
	// 0..H-1) — NOT CoarseGrid's tile space (contrast the top-level
	// Deposits list, still tile-space). Roads/Lots/Bridges below are all in
	// the city's OWN local (0..size-1) coordinates; add OriginX/OriginY to
	// place them inside FineGrid.
	OriginX int        `json:"originX"`
	OriginY int        `json:"originY"`
	Size    int        `json:"size"` // lots per side (== demoCitySize)
	Roads   []roadJSON `json:"roads"`
	// Bridges is a SELF-CONTAINED subset of Roads (same {x,y,class} shape,
	// not just coordinates) crossing a bridgeable water lot: a thin stream
	// under a local road, or a stream OR river under an arterial/the
	// elevated highway (citylayout.go's WideRiver/passableForClass).
	Bridges []roadJSON     `json:"bridges"`
	Lots    []cityLotJSON  `json:"lots"`
	Counts  map[string]int `json:"counts"`
}

// roadJSON is one road-network cell: its local grid coordinate (same space
// as citySummaryJSON.OriginX/Y) and which class of road runs through it —
// 0 local (the original ~4-lot-spaced street grid), 1 arterial (a coarser,
// ~7-lot-spaced grid that MAY bridge a genuine river, not just a thin
// stream), 2 elevated highway (a single roughly-straight line ignoring
// terrain entirely — see citylayout.go's elevatedHighwayPath; the web
// client places it on pillars at a fixed height above the highest terrain
// along its path).
type roadJSON struct {
	X     int `json:"x"`
	Y     int `json:"y"`
	Class int `json:"class"`
}

type cityLotJSON struct {
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Rot  int    `json:"rot"`
	// Floors is the lot's real storey count (citylayout.go's floorsFor),
	// deterministic from its type and position — so the web renderer
	// extrudes each building to its own real height (~3.2m/floor) instead
	// of guessing from the type code alone. 0 means no vertical structure
	// at all (park, farm).
	Floors int `json:"floors"`
}

type worldSummaryJSON struct {
	Continent string `json:"continent,omitempty"`
	River     string `json:"river,omitempty"`
	Sea       string `json:"sea,omitempty"`
}

// runExportCity is the --export-city entry point: pick a site, export its
// terrain and a city layout, write the JSON.
func runExportCity(w *worldgen.World, outPath string) error {
	site, ok := findCitySite(w)
	if !ok {
		return fmt.Errorf("export-city: could not find a suitable city site (flat land near water)")
	}
	fmt.Printf("export-city: site = %+v (lat=%.3f lon=%.3f) score=%.1f reasons=%s\n",
		site.addr, site.latDeg, site.lonDeg, site.score, site.reasons)

	doc, err := buildCityExport(w, site)
	if err != nil {
		return fmt.Errorf("building export: %w", err)
	}

	buf, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshalling export: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	fmt.Printf("export-city: wrote %s (%d bytes)\n", outPath, len(buf))
	fmt.Printf("export-city: coarseGrid %dx%d tiles, fineGrid %dx%d lots (lotMeters=%.2f)\n",
		doc.CoarseGrid.W, doc.CoarseGrid.H, doc.FineGrid.W, doc.FineGrid.H, doc.LotMeters)
	fmt.Printf("export-city: city lots by type: %v (bridges=%d)\n", doc.City.Counts, len(doc.City.Bridges))
	return nil
}

// citySite is one candidate location, scored by findCitySite.
type citySite struct {
	addr    worldgen.ChunkAddr
	latDeg  float64
	lonDeg  float64
	score   float64
	reasons string
}

// findCitySite searches candidate base chunks along every named river (the
// same "walk every river's cells" approach zoom.go's findGoodChunkAnchor
// uses, reused here rather than reinvented) and scores each one by actually
// generating it and its two coastline/lake neighbours, not by guessing from
// the coarse mesh: a good city site is mostly flat, sits near a stream, and
// gets bonus character from a coast or a lake nearby or real hills in
// view — never a hard requirement (a river with no nearby coast is still a
// perfectly good site), only a preference among the candidates the walk
// turns up. Deterministic for a fixed seed: the same World always walks the
// same rivers in the same order and keeps the same best candidate.
func findCitySite(w *worldgen.World) (citySite, bool) {
	baseLOD := w.Params.ChunkBaseLOD
	tried := make(map[worldgen.ChunkAddr]bool)

	var best citySite
	haveBest := false

	const maxCandidates = 300
	tries := 0

	for _, riv := range w.Rivers {
		for _, cellID := range riv.Cells {
			if tries >= maxCandidates {
				break
			}
			cell := w.Cells[cellID]
			if cell.Elevation <= 0 {
				continue // at/below sea level: a river's mouth, not a buildable site
			}
			addr := worldgen.ChunkOfLatLon(baseLOD, cell.Point.LatDeg, cell.Point.LonDeg)
			if tried[addr] {
				continue
			}
			tried[addr] = true
			tries++

			// This candidate is anchored to a SPECIFIC river cell (cell,
			// above) — cheaply and precisely known already, no re-derivation
			// needed: is it a genuine high-flow river cell (RiverFlow far
			// past RiverFlowThreshold), not just an ordinary flow-eligible
			// stream cell? See scoreCitySite's own doc for how this drives
			// the "prefer a site a real river actually crosses" scoring.
			isHighFlowRiver := cell.RiverFlow >= int32(w.Params.RiverFlowThreshold)*riverFlowMultiple

			cand, ok := scoreCitySite(w, addr, isHighFlowRiver)
			if !ok {
				continue
			}
			if !haveBest || cand.score > best.score {
				best, haveBest = cand, true
			}
		}
		if tries >= maxCandidates {
			break
		}
	}
	return best, haveBest
}

// scoreCitySite generates addr's chunk (and peeks at its 8 neighbours for
// coast/lake bonuses) and scores it as a city site: heavily rewards a large
// flat, buildable window (flatSearchTiles' own neighbourhood has to fit on
// it, generously more than the real lot-scale city needs), STRONGLY prefers
// a site anchored to a genuine high-flow RIVER cell (isHighFlowRiver, from
// findCitySite's own cheap per-candidate check — not just "a stream
// somewhere in this chunk", the old, much weaker bonus this still also
// gives), and adds smaller bonuses for a coastline, a lake or real
// elevation range nearby — the "character" the task asks for without
// letting any of it crowd out the flat-and-buildable requirement that
// actually matters for laying the city itself out.
//
// APPROXIMATION. isHighFlowRiver is about the ONE cell findCitySite
// anchored this candidate to, not a check that the river specifically
// crosses the eventual ~demoCitySize window (bestCityWindow runs later, and
// could in principle land the window's centre away from this exact cell) —
// but since a chunk is only picked as a candidate at all when it contains
// both this river cell AND a flatSearchTiles-sized flat run, and rivers
// tend to run through the flattest part of their own chunk (floodplains),
// the two are highly correlated in practice. A precise "does the river
// cross the final window" check would need fine-resolution sampling per
// candidate (up to 300 of them), which is unnecessary cost for a scoring
// heuristic that was already approximate before this change.
func scoreCitySite(w *worldgen.World, addr worldgen.ChunkAddr, isHighFlowRiver bool) (citySite, bool) {
	c, err := w.GenerateChunk(addr)
	if err != nil {
		return citySite{}, false
	}
	edge := c.TileEdge

	hasStream := false
	minE, maxE := int16(1<<15-1), int16(-1<<15)
	buildableRun := 0 // best (approx) contiguous flat/buildable run found by a coarse scan
	for j := 0; j < edge; j++ {
		run := 0
		for i := 0; i < edge; i++ {
			t := c.TileAt(i, j)
			if t.Elevation < minE {
				minE = t.Elevation
			}
			if t.Elevation > maxE {
				maxE = t.Elevation
			}
			if t.IsStream() {
				hasStream = true
			}
			if !t.IsOcean() && !t.IsLake() && !t.IsStream() {
				run++
				if run > buildableRun {
					buildableRun = run
				}
			} else {
				run = 0
			}
		}
	}
	if buildableRun < flatSearchTiles {
		return citySite{}, false // can't fit a flatSearchTiles-wide window anywhere in this chunk
	}
	elevRange := int(maxE - minE)

	nearCoast, nearLake := false, false
	for _, n := range addr.Neighbors8() {
		nc, err := w.GenerateChunk(n)
		if err != nil {
			continue
		}
		for _, t := range nc.Tiles {
			if t.IsOcean() {
				nearCoast = true
			}
			if t.IsLake() {
				nearLake = true
			}
		}
		if nearCoast && nearLake {
			break
		}
	}
	for _, t := range c.Tiles {
		if t.IsOcean() {
			nearCoast = true
		}
		if t.IsLake() {
			nearLake = true
		}
	}

	score := float64(buildableRun) * 10
	reasons := "flat-land"
	if hasStream {
		score += 500
		reasons += "+stream"
	}
	if isHighFlowRiver {
		// Dominates every other bonus below: a real river crossing the city
		// (bridged by an arterial or the elevated highway — see
		// citylayout.go) is what the coordinator specifically asked this
		// export to prefer, not merely "some stream exists in the chunk".
		score += 3000
		reasons += "+river"
	}
	if nearCoast {
		score += 150
		reasons += "+coast"
	}
	if nearLake {
		score += 150
		reasons += "+lake"
	}
	// Character bonus for visible relief, but capped and gentle: past a
	// modest range it stops helping (a city site that is ALL hillside is a
	// worse pick, not a better one, however scenic).
	characterBonus := float64(elevRange)
	if characterBonus > 400 {
		characterBonus = 400
	}
	score += characterBonus
	if elevRange > 60 {
		reasons += "+hills"
	}

	latDeg, lonDeg := addr.LatLon()
	return citySite{addr: addr, latDeg: latDeg, lonDeg: lonDeg, score: score, reasons: reasons}, true
}

// buildCityExport stitches the 3x3 chunks around site.addr into one flat
// COARSE terrain grid, finds the flattest flatSearchTiles-wide window
// inside the centre chunk, samples a FINE lot-resolution grid centred on
// that window (with fineMarginMeters of margin), slices the city's own
// citySize x citySize lot core straight out of it, lays the city out, and
// assembles the full JSON document.
func buildCityExport(w *worldgen.World, site citySite) (*cityExport, error) {
	edge := w.Params.ChunkTileEdge
	span := exportChunkSpan
	gridW, gridH := span*edge, edge*span

	addrs := make([]worldgen.ChunkAddr, span*span) // row-major: addrs[cy*span+cx]
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			cx, cy := dx+1, dy+1
			var a worldgen.ChunkAddr
			if dx == 0 && dy == 0 {
				a = site.addr
			} else {
				a = site.addr.Neighbor(int32(dx), int32(dy))
			}
			addrs[cy*span+cx] = a
		}
	}

	coarseElev := make([]int16, gridW*gridH)
	coarseBiome := make([]uint8, gridW*gridH)
	coarseFlags := make([]uint8, gridW*gridH)
	var deposits []depositJSON

	for cy := 0; cy < span; cy++ {
		for cx := 0; cx < span; cx++ {
			addr := addrs[cy*span+cx]
			c, err := w.GenerateChunk(addr)
			if err != nil {
				return nil, fmt.Errorf("generating chunk %+v: %w", addr, err)
			}
			ox, oy := cx*edge, cy*edge
			for j := 0; j < edge; j++ {
				for i := 0; i < edge; i++ {
					t := c.TileAt(i, j)
					gx, gy := ox+i, oy+j
					gi := gy*gridW + gx
					coarseElev[gi] = t.Elevation
					coarseBiome[gi] = t.Biome
					coarseFlags[gi] = tileFlagsJSON(t)
				}
			}
			for _, d := range c.Deposits {
				deposits = append(deposits, depositJSON{
					X:            ox + int(d.TileX),
					Y:            oy + int(d.TileY),
					ResourceCode: d.ResourceCode,
				})
			}
		}
	}

	// bestCityWindow's flatSearchTiles-wide window is somewhere inside the
	// CENTRE chunk (cx=cy=1): a generously-sized, verified-flat ~4.5km
	// neighbourhood for the real lot-scale city to sit in the middle of.
	centerOx, centerOy := 1*edge, 1*edge
	winX, winY := bestCityWindow(coarseElev, coarseFlags, gridW, centerOx, centerOy, edge, flatSearchTiles)

	// Fine lot geometry: centre the demoCitySize x demoCitySize core in the
	// flat window just found, then extend fineMarginMeters of margin on
	// every side. All expressed as fractional TILE coordinates in the
	// coarse grid's own coordinate space, per fineGridJSON's own doc
	// comment.
	lotMeters := w.Params.TileMeters() / lotsPerTile
	marginLots := int(math.Ceil(fineMarginMeters / lotMeters))
	fineW := demoCitySize + 2*marginLots
	fineH := demoCitySize + 2*marginLots

	coreOriginTileX := float64(centerOx+winX) + float64(flatSearchTiles)/2 - float64(demoCitySize)/(2*float64(lotsPerTile))
	coreOriginTileY := float64(centerOy+winY) + float64(flatSearchTiles)/2 - float64(demoCitySize)/(2*float64(lotsPerTile))
	fineOriginTileX := coreOriginTileX - float64(marginLots)/float64(lotsPerTile)
	fineOriginTileY := coreOriginTileY - float64(marginLots)/float64(lotsPerTile)

	fineSamples := make([]worldgen.FineSample, fineW*fineH)
	for fy := 0; fy < fineH; fy++ {
		for fx := 0; fx < fineW; fx++ {
			tileX := fineOriginTileX + float64(fx)/float64(lotsPerTile)
			tileY := fineOriginTileY + float64(fy)/float64(lotsPerTile)
			// Local to the CENTRE chunk's own address: iFrac/jFrac may be
			// negative or >= edge (a ghost tile position, chunk_address.go's
			// own term) when the fine window's margin spills into a
			// neighbouring chunk — TileFlatUVFrac is documented to tolerate
			// exactly that.
			iFrac := tileX - float64(centerOx)
			jFrac := tileY - float64(centerOy)
			lat, lon := latLonOfLocalTile(site.addr, edge, iFrac, jFrac)
			fineSamples[fy*fineW+fx] = w.SampleFineLatLon(lat, lon)
		}
	}

	fineElevBytes := make([]int16, fineW*fineH)
	fineBiome := make([]byte, fineW*fineH)
	fineWater := make([]byte, fineW*fineH)
	for i, fs := range fineSamples {
		fineElevBytes[i] = clampElevInt16(fs.ElevationM)
		fineBiome[i] = fs.Biome
		fineWater[i] = waterKindByte(fs)
	}

	// City core: slice directly out of the fine grid just built (no
	// re-sampling).
	coreX0, coreY0 := marginLots, marginLots
	cityTiles := make([]cityTile, demoCitySize*demoCitySize)
	for ly := 0; ly < demoCitySize; ly++ {
		for lx := 0; lx < demoCitySize; lx++ {
			cityTiles[ly*demoCitySize+lx] = cityTileFromFine(fineSamples, fineW, fineH, coreX0+lx, coreY0+ly, deposits, fineOriginTileX, fineOriginTileY)
		}
	}
	layout := layoutCity(demoCitySize, cityTiles)

	counts := make(map[string]int)
	lotsJSON := make([]cityLotJSON, 0, len(layout.Lots))
	for _, l := range layout.Lots {
		counts[l.Type]++
		lotsJSON = append(lotsJSON, cityLotJSON{Type: l.Type, X: l.X, Y: l.Y, W: l.W, H: l.H, Rot: l.Rot, Floors: l.Floors})
	}
	roadsJSON := make([]roadJSON, len(layout.Roads))
	for i, r := range layout.Roads {
		roadsJSON[i] = roadJSON{X: r.X, Y: r.Y, Class: r.Class}
	}
	bridgesJSON := make([]roadJSON, len(layout.Bridges))
	for i, b := range layout.Bridges {
		bridgesJSON[i] = roadJSON{X: b.X, Y: b.Y, Class: b.Class}
	}

	doc := &cityExport{
		Seed:             w.Seed,
		GeneratorVersion: w.GeneratorVersion,
		TileMeters:       w.Params.TileMeters(),
		LotMeters:        lotMeters,
		ChunkTileEdge:    edge,
		LotsPerTile:      lotsPerTile,
		CenterChunk: chunkAddrJSON{
			Face: site.addr.Face, LOD: site.addr.LOD, X: site.addr.X, Y: site.addr.Y,
		},
		LatDeg: site.latDeg,
		LonDeg: site.lonDeg,
		CoarseGrid: coarseGridJSON{
			W:         gridW,
			H:         gridH,
			Elevation: packInt16LE(coarseElev),
			Biome:     coarseBiome,
			Flags:     coarseFlags,
		},
		FineGrid: fineGridJSON{
			OriginTileX: fineOriginTileX,
			OriginTileY: fineOriginTileY,
			W:           fineW,
			H:           fineH,
			Elevation:   packInt16LE(fineElevBytes),
			Biome:       fineBiome,
			WaterKind:   fineWater,
		},
		Deposits: deposits,
		City: citySummaryJSON{
			OriginX: coreX0,
			OriginY: coreY0,
			Size:    demoCitySize,
			Roads:   roadsJSON,
			Bridges: bridgesJSON,
			Lots:    lotsJSON,
			Counts:  counts,
		},
		World: worldSummaryFor(w, site),
	}

	for _, b := range w.Content.Biomes {
		doc.BiomeLegend = append(doc.BiomeLegend, biomeLegendEntry{Code: b.Code, ColorHex: b.ColorHex})
	}
	for _, r := range w.Content.Resources {
		doc.ResourceLegend = append(doc.ResourceLegend, resourceLegendEntry{Code: r.Code, Name: r.Name, ColorHex: r.ColorHex})
	}

	return doc, nil
}

// latLonOfLocalTile converts a fractional tile coordinate local to addr
// (chunk_address.go's TileFlatUVFrac convention: i=0/j=0 is addr's own
// first tile, negative or >=tileEdge is a ghost tile past its edge) into
// latitude/longitude in degrees, via the same cube-sphere projection
// TileUnitSpherePoint uses internally for an integer tile — just exposed
// here through the package's exported FaceDirection since this file lives
// outside internal/domain/worldgen. VISUAL/QUERY ONLY, same caveat every
// lat/lon conversion in that package already documents (sin/cos, tan/atan).
func latLonOfLocalTile(addr worldgen.ChunkAddr, tileEdge int, iFrac, jFrac float64) (latDeg, lonDeg float64) {
	u, v := addr.TileFlatUVFrac(tileEdge, iFrac, jFrac)
	x, y, z := worldgen.FaceDirection(addr.Face, u, v)
	n := math.Sqrt(x*x + y*y + z*z)
	x, y, z = x/n, y/n, z/n
	latDeg = math.Asin(clampF(z, -1, 1)) * 180 / math.Pi
	lonDeg = math.Atan2(y, x) * 180 / math.Pi
	return
}

// clampF is zoom.go's own helper, reused here rather than redeclared.

// packInt16LE encodes vals as little-endian int16 pairs — see
// coarseGridJSON.Elevation's doc comment for why (encoding/json base64s a
// []byte for free, far cheaper than a raw JSON number array).
func packInt16LE(vals []int16) []byte {
	buf := make([]byte, len(vals)*2)
	for i, v := range vals {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

// clampElevInt16 rounds and clamps a fine sample's float64 elevation into
// int16 range, the same clamping chunk.go's own tile export already applies
// (worldgen keeps that logic unexported, so it is repeated here rather than
// exposed): elevation never approaches this range in practice (see
// worldgen.Elevation's own doc), this is only a defensive bound so a
// pathological input can never corrupt the packed byte layout.
func clampElevInt16(v float64) int16 {
	r := math.Round(v)
	switch {
	case r > math.MaxInt16:
		return math.MaxInt16
	case r < math.MinInt16:
		return math.MinInt16
	default:
		return int16(r)
	}
}

// waterKindByte maps a FineSample onto fineGridJSON.WaterKind's 5-value
// encoding (0 none/1 stream/2 river/3 lake/4 ocean) — a strict superset of
// worldgen.StreamKind, since a fine sample's ocean/lake state lives on
// separate FineSample fields, not folded into StreamKind itself.
func waterKindByte(fs worldgen.FineSample) byte {
	switch {
	case fs.IsOcean:
		return 4
	case fs.IsLake:
		return 3
	case fs.StreamKind == worldgen.StreamKindRiver:
		return 2
	case fs.StreamKind == worldgen.StreamKindStream:
		return 1
	default:
		return 0
	}
}

func tileFlagsJSON(t worldgen.ChunkTile) uint8 {
	var f uint8
	if t.IsOcean() {
		f |= 1
	}
	if t.IsStream() {
		f |= 2
	}
	if t.IsLake() {
		f |= 4
	}
	return f
}

const (
	flagOcean  = 1
	flagStream = 2
	flagLake   = 4
)

// bestCityWindow scans every valid winSize x winSize top-left offset inside
// the one base chunk at (chunkOx,chunkOy) in the stitched COARSE grid and
// returns the offset (local to that chunk) with the fewest water/steep
// TILES — a simple, deterministic "flattest, most buildable window" search
// at TILE resolution, good enough for a 32x32 chunk (at most (32-15+1)^2 =
// 324 windows at the default winSize=flatSearchTiles=15). This only finds a
// good NEIGHBOURHOOD for the real lot-scale city (buildCityExport centres
// the much smaller lot core inside whatever window this returns); it does
// not itself decide the city's own footprint.
func bestCityWindow(elevation []int16, flags []uint8, gridW, chunkOx, chunkOy, chunkEdge, winSize int) (int, int) {
	bestX, bestY := 0, 0
	bestBad := -1
	maxOff := chunkEdge - winSize
	for oy := 0; oy <= maxOff; oy++ {
		for ox := 0; ox <= maxOff; ox++ {
			bad := 0
			for ly := 0; ly < winSize; ly++ {
				for lx := 0; lx < winSize; lx++ {
					gx, gy := chunkOx+ox+lx, chunkOy+oy+ly
					gi := gy*gridW + gx
					if flags[gi]&(flagOcean|flagStream|flagLake) != 0 {
						bad++
						continue
					}
					if isSteepAt(elevation, gridW, gx, gy) {
						bad++
					}
				}
			}
			if bestBad < 0 || bad < bestBad {
				bestBad = bad
				bestX, bestY = ox, oy
			}
		}
	}
	return bestX, bestY
}

// steepElevationDelta is the TILE-scale (~305m) elevation-gradient
// threshold bestCityWindow's flat-neighbourhood search uses — ADR 0028
// §6.1's "a steep-slope... lot is unbuildable", given a concrete number.
// This is NOT what the city's own buildings/roads are checked against
// (that is fineSteepDeltaM, above, calibrated for ~30m lot spacing
// instead): this constant only screens the coarse ~4.5km neighbourhood
// search for gross hillside, generously, so a genuinely flat area is
// preferred without rejecting a site for texture-scale bumps.
const steepElevationDelta = 220

func isSteepAt(elevation []int16, gridW, x, y int) bool {
	e := int(elevation[y*gridW+x])
	worst := 0
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= gridW {
			continue
		}
		ni := ny*gridW + nx
		if ni < 0 || ni >= len(elevation) {
			continue
		}
		diff := int(elevation[ni]) - e
		if diff < 0 {
			diff = -diff
		}
		if diff > worst {
			worst = diff
		}
	}
	return worst > steepElevationDelta
}

// cityTileFromFine converts one FINE-grid lot into layoutCity's own
// cityTile shape (see fine.go's own doc comment for FineSample and this
// file's package doc for WHY this is fine-, not coarse-, resolution):
// water/impassable/biome-driven state straight from the fine sample,
// fineSteepDeltaM-scale steepness from the fine grid's own 4-neighbour
// elevation gradient, coast from a fine-grid 4-neighbour ocean check, and a
// deposit flag derived from the (tile-resolution, unchanged) coarse
// Deposits list: each deposit's tile coordinate expands to its
// lotsPerTile x lotsPerTile lot footprint in the SAME fine-grid coordinate
// space (via fineOriginTileX/Y), and a fine lot inside that footprint is
// flagged. citylayout.go's anyCornerDeposit separately widens its own
// building-footprint search by depositSearchMarginLots, so a mine/well can
// still find a footprint a few lots away rather than needing to land
// exactly inside it.
func cityTileFromFine(samples []worldgen.FineSample, fineW, fineH, fx, fy int, deposits []depositJSON, fineOriginTileX, fineOriginTileY float64) cityTile {
	fs := samples[fy*fineW+fx]
	water := fs.IsOcean || fs.IsLake || fs.StreamKind != worldgen.StreamKindNone
	// Impassable to EVERY road class: a lake or ocean only.
	impassable := fs.IsOcean || fs.IsLake
	// WideRiver: blocks a LOCAL road (still detours around it) but not an
	// arterial or the elevated highway (citylayout.go's passableForClass) —
	// treating worldgen.StreamKindRiver as this export's "too wide for a
	// local road" case is a deliberate simplification over actually
	// measuring a river's local lot-width. An ordinary StreamKindStream is
	// bridgeable by EVERY road class, local included.
	wideRiver := fs.StreamKind == worldgen.StreamKindRiver
	steep := fineSteepAt(samples, fineW, fineH, fx, fy)

	coast := false
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := fx+d[0], fy+d[1]
		if nx < 0 || ny < 0 || nx >= fineW || ny >= fineH {
			continue
		}
		if samples[ny*fineW+nx].IsOcean {
			coast = true
			break
		}
	}

	tile := cityTile{Water: water, Impassable: impassable, WideRiver: wideRiver, Steep: steep, Coast: coast}
	for _, d := range deposits {
		lx0 := int(math.Round((float64(d.X) - fineOriginTileX) * float64(lotsPerTile)))
		ly0 := int(math.Round((float64(d.Y) - fineOriginTileY) * float64(lotsPerTile)))
		if fx >= lx0 && fx < lx0+lotsPerTile && fy >= ly0 && fy < ly0+lotsPerTile {
			tile.Deposit = true
			tile.ResourceCode = d.ResourceCode
			break
		}
	}
	return tile
}

// fineSteepAt is isSteepAt's lot-scale counterpart: the same 4-neighbour
// max-gradient check, over worldgen.FineSample.ElevationM (float64,
// ~lotMeters apart) against fineSteepDeltaM instead of over int16 tile
// elevation (~TileMeters apart) against steepElevationDelta.
func fineSteepAt(samples []worldgen.FineSample, fineW, fineH, x, y int) bool {
	e := samples[y*fineW+x].ElevationM
	worst := 0.0
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= fineW || ny >= fineH {
			continue
		}
		diff := samples[ny*fineW+nx].ElevationM - e
		if diff < 0 {
			diff = -diff
		}
		if diff > worst {
			worst = diff
		}
	}
	return worst > fineSteepDeltaM
}

// worldSummaryFor names the site's nearest river (already known — it is
// what the site search walked) and, if the site's own coarse cell falls
// inside a named continent region, that continent's name too — a small
// amount of "where in the world is this" context for the web demo's label
// overlay, computed once here rather than asking the client to re-derive it
// from the raw cell graph.
func worldSummaryFor(w *worldgen.World, site citySite) worldSummaryJSON {
	var out worldSummaryJSON

	cellID := w.NearestCell(site.latDeg, site.lonDeg)
	for _, reg := range w.Continents {
		for _, c := range reg.Cells {
			if c == cellID {
				out.Continent = reg.Name.Persian
				break
			}
		}
		if out.Continent != "" {
			break
		}
	}

	bestDist := -1.0
	for _, riv := range w.Rivers {
		for _, c := range riv.Cells {
			p := w.Cells[c].Point
			dx, dy := p.LatDeg-site.latDeg, p.LonDeg-site.lonDeg
			d := dx*dx + dy*dy
			if bestDist < 0 || d < bestDist {
				bestDist = d
				out.River = riv.Name.Persian
			}
		}
	}
	return out
}
