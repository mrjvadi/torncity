package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is the DEMO export path (--export-city): picks one city site
// deterministically from an already-generated World, exports the 3x3-chunk
// neighbourhood of terrain around it as compact JSON, and lays a 15x15 city
// out on the centre chunk's flattest window (citylayout.go). It exists so
// the web client can render one real settlement sitting on real generated
// terrain, in a browser, with no server and no database — see the project
// report for why this is demo-only scaffolding, not ADR 0028 §6's real
// placement system.

// citySize is the city tier's own grid (ADR 0028 §4's table: village 5x5,
// town 9x9, city 15x15) — the demo always shows the biggest tier, since
// that is what best shows off a settlement on real terrain.
const citySize = 15

// exportChunkTiles is the export window: 3x3 base chunks around the chosen
// site (the task's own spec), giving the client a full chunk of margin on
// every side of the city to render as surrounding countryside.
const exportChunkSpan = 3

// cityExport is the whole --export-city JSON document.
type cityExport struct {
	Seed             uint64  `json:"seed"`
	GeneratorVersion int     `json:"generatorVersion"`
	TileMeters       float64 `json:"tileMeters"`
	ChunkTileEdge    int     `json:"chunkTileEdge"`

	CenterChunk chunkAddrJSON `json:"centerChunk"`
	LatDeg      float64       `json:"latDeg"`
	LonDeg      float64       `json:"lonDeg"`

	Grid struct {
		W int `json:"w"`
		H int `json:"h"`
	} `json:"grid"`

	// Elevation/Biome/Flags are row-major, W*H long, origin at the export
	// window's own north-west corner — the same flat-array shape chunk.go's
	// own Chunk.Tiles uses, just stitched across the 3x3 window so the
	// client draws one seamless mesh instead of 9 independently-lit ones.
	// Biome/Flags are Go []byte, which encoding/json marshals as a base64
	// string, not a JSON array — free compaction (9216 bytes become a
	// ~12KB string instead of ~30KB of comma-separated small integers) the
	// client undoes with one atob()+Uint8Array, not a format to hand-roll.
	Elevation []int16 `json:"elevation"`
	Biome     []uint8 `json:"biome"`
	Flags     []uint8 `json:"flags"`

	BiomeLegend    []biomeLegendEntry    `json:"biomeLegend"`
	ResourceLegend []resourceLegendEntry `json:"resourceLegend"`
	Deposits       []depositJSON         `json:"deposits"`

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

type citySummaryJSON struct {
	OriginX int             `json:"originX"` // top-left of the city's local grid, in the export's own tile coordinates
	OriginY int             `json:"originY"`
	Size    int             `json:"size"`
	Roads   [][2]int        `json:"roads"` // local (0..size-1) coordinates
	Lots    []cityLotJSON   `json:"lots"`
	Counts  map[string]int  `json:"counts"`
}

type cityLotJSON struct {
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Rot  int    `json:"rot"`
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
	fmt.Printf("export-city: city lots by type: %v\n", doc.City.Counts)
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

			cand, ok := scoreCitySite(w, addr)
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
// flat, buildable window (a real 15x15 city has to fit on it), rewards a
// stream running through it, and adds smaller bonuses for a coastline, a
// lake or real elevation range nearby — the "character" the task asks for
// without letting it crowd out the flat-and-buildable requirement that
// actually matters for laying the city itself out.
func scoreCitySite(w *worldgen.World, addr worldgen.ChunkAddr) (citySite, bool) {
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
	if buildableRun < citySize {
		return citySite{}, false // can't fit a 15-wide city window anywhere in this chunk
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
// terrain grid, finds the flattest 15x15 window inside the centre chunk,
// lays the city out on it, and assembles the full JSON document.
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

	elevation := make([]int16, gridW*gridH)
	biome := make([]uint8, gridW*gridH)
	flags := make([]uint8, gridW*gridH)
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
					elevation[gi] = t.Elevation
					biome[gi] = t.Biome
					flags[gi] = tileFlagsJSON(t)
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

	// The city's own window is somewhere inside the CENTRE chunk (cx=cy=1),
	// so its city-tile terrain lookup always indexes into that one chunk's
	// worth of the already-stitched grid — chosen by scanning every valid
	// top-left offset for the one with the fewest unbuildable tiles
	// (deterministic: ties keep the first — smallest y, then x — found).
	centerOx, centerOy := 1*edge, 1*edge
	winX, winY := bestCityWindow(elevation, flags, gridW, centerOx, centerOy, edge)
	originX, originY := centerOx+winX, centerOy+winY

	cityTiles := make([]cityTile, citySize*citySize)
	for ly := 0; ly < citySize; ly++ {
		for lx := 0; lx < citySize; lx++ {
			gx, gy := originX+lx, originY+ly
			cityTiles[ly*citySize+lx] = cityTileFromGrid(elevation, flags, gridW, gridH, gx, gy, deposits)
		}
	}
	layout := layoutCity(citySize, cityTiles)

	counts := make(map[string]int)
	lotsJSON := make([]cityLotJSON, 0, len(layout.Lots))
	for _, l := range layout.Lots {
		counts[l.Type]++
		lotsJSON = append(lotsJSON, cityLotJSON{Type: l.Type, X: l.X, Y: l.Y, W: l.W, H: l.H, Rot: l.Rot})
	}

	doc := &cityExport{
		Seed:             w.Seed,
		GeneratorVersion: w.GeneratorVersion,
		TileMeters:       w.Params.TileMeters(),
		ChunkTileEdge:    edge,
		CenterChunk: chunkAddrJSON{
			Face: site.addr.Face, LOD: site.addr.LOD, X: site.addr.X, Y: site.addr.Y,
		},
		LatDeg: site.latDeg,
		LonDeg: site.lonDeg,
		Elevation: elevation,
		Biome:     biome,
		Flags:     flags,
		Deposits:  deposits,
		City: citySummaryJSON{
			OriginX: originX,
			OriginY: originY,
			Size:    citySize,
			Roads:   layout.Roads,
			Lots:    lotsJSON,
			Counts:  counts,
		},
		World: worldSummaryFor(w, site),
	}
	doc.Grid.W, doc.Grid.H = gridW, gridH

	for _, b := range w.Content.Biomes {
		doc.BiomeLegend = append(doc.BiomeLegend, biomeLegendEntry{Code: b.Code, ColorHex: b.ColorHex})
	}
	for _, r := range w.Content.Resources {
		doc.ResourceLegend = append(doc.ResourceLegend, resourceLegendEntry{Code: r.Code, Name: r.Name, ColorHex: r.ColorHex})
	}

	return doc, nil
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

// bestCityWindow scans every valid citySize x citySize top-left offset
// inside the one base chunk at (chunkOx,chunkOy) in the stitched grid and
// returns the offset (local to that chunk) with the fewest water/steep
// tiles — a simple, deterministic "flattest, most buildable window" search,
// good enough for a 32x32 chunk (at most (32-15+1)^2 = 324 windows).
func bestCityWindow(elevation []int16, flags []uint8, gridW, chunkOx, chunkOy, chunkEdge int) (int, int) {
	bestX, bestY := 0, 0
	bestBad := -1
	maxOff := chunkEdge - citySize
	for oy := 0; oy <= maxOff; oy++ {
		for ox := 0; ox <= maxOff; ox++ {
			bad := 0
			for ly := 0; ly < citySize; ly++ {
				for lx := 0; lx < citySize; lx++ {
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

// steepElevationDelta is the local elevation-gradient threshold (Elevation
// units between two adjacent tiles) above which a tile counts as too steep
// to build on — ADR 0028 §6.1's "a steep-slope... lot is unbuildable",
// given a concrete number for the demo. Chosen generously (a real slope
// system would grade this continuously): only a genuinely sharp local jump
// disqualifies a tile, so a gently rolling site is not rejected wholesale.
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

// cityTileFromGrid converts one stitched-grid tile into layoutCity's own
// cityTile shape: water/steep from the terrain flags/elevation already
// computed for the window search, coast from a direct 4-neighbour check
// (an ocean tile just outside this one counts, so a shoreline city block
// still gets a port), and a deposit flag from the exported deposit list
// (small enough — a few dozen entries across the whole 3x3 window — that a
// linear scan per tile is simpler than indexing it, and this only runs
// citySize^2 = 225 times).
func cityTileFromGrid(elevation []int16, flags []uint8, gridW, gridH, gx, gy int, deposits []depositJSON) cityTile {
	gi := gy*gridW + gx
	f := flags[gi]
	water := f&(flagOcean|flagStream|flagLake) != 0
	steep := isSteepAt(elevation, gridW, gx, gy)

	coast := false
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := gx+d[0], gy+d[1]
		if nx < 0 || ny < 0 || nx >= gridW || ny >= gridH {
			continue
		}
		if flags[ny*gridW+nx]&flagOcean != 0 {
			coast = true
			break
		}
	}

	tile := cityTile{Water: water, Steep: steep, Coast: coast}
	for _, d := range deposits {
		if d.X == gx && d.Y == gy {
			tile.Deposit = true
			tile.ResourceCode = d.ResourceCode
			break
		}
	}
	return tile
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
