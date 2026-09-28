package worldgen

import (
	"fmt"
	"math"
)

// GeneratorVersion identifies THIS algorithm. It is stored alongside every
// world's seed and is the thing a database row actually keys a generated
// planet by: (Seed, GeneratorVersion, a hash of Params/Content). Bumping it
// is mandatory whenever a change to this package would generate different
// cells for a seed that has already been used — a new noise octave, a
// different plate-boundary formula, a reordered climate pass, even a
// rounding tweak. An old, already-settled world keeps loading under the
// version it was made with; nothing here ever regenerates a live world
// differently underneath its players. See doc_test.go's golden hash test,
// which exists specifically to catch an accidental behaviour change that
// forgot to bump this.
const GeneratorVersion = 1

// Cell is one mesh cell's full generated state.
type Cell struct {
	Point         Point
	PlateID       int16
	Elevation     Elevation
	Temperature   Temp
	Precipitation Precip
	BiomeIdx      uint8
	IsOcean       bool
	IsLake        bool
	// RiverFlow is the flow accumulation (a cell count) if this cell carries
	// a river (>= Params.RiverFlowThreshold), else 0.
	RiverFlow int32
}

// World is one fully generated planet.
//
// SEED-FIRST: a World is a CACHE, not a record. Everything in it is a pure
// function of Seed, GeneratorVersion and Content (see the package doc and
// GeneratorVersion above); nothing here needs to be written to a database to
// exist again — Generate(seed, params, content) reproduces it byte for byte.
// What the database DOES need to store is the small amount of state that
// changes AFTER generation and that Generate has no way to know: how much of
// a Deposit remains, who owns it, what a player built on a cell. See the
// project report for the proposed schema built on that split.
type World struct {
	Seed             uint64
	GeneratorVersion int
	Params           Params
	Content          Content

	Cells    []Cell
	Plates   []Plate
	Deposits []Deposit

	Continents     []NamedRegion
	Seas           []NamedRegion
	MountainRanges []NamedRegion
	Rivers         []NamedRiver

	mesh  *Mesh
	index *nearestIndex
}

// Generate builds a whole planet from a seed, tuning parameters and authored
// content. It performs no I/O and, for a fixed GeneratorVersion, is a pure
// function: the same three inputs always produce an identical World (see
// doc_test.go's determinism test).
func Generate(seed uint64, params Params, content Content) (*World, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := content.Validate(); err != nil {
		return nil, err
	}

	root := NewRand(seed)
	mesh := buildMesh(params.CellCount, params.NeighborK)

	plates, plateOf := assignPlates(mesh, params, root)
	boundaries := computeBoundaries(mesh, plates, plateOf, params.BoundaryInfluenceSteps)

	rawElev, seaLevelRaw := computeElevation(mesh, plates, plateOf, boundaries, params, seed)
	elevation := make([]int32, mesh.Len())
	for c, v := range rawElev {
		elevation[c] = quantize(v - seaLevelRaw)
	}
	scrubSpeckleIslands(mesh, elevation)

	temp := computeTemperature(mesh, elevation, seed)
	precip := computeMoisture(mesh, elevation, params)

	hy := computeHydrology(mesh, elevation, params)
	flow := riverFlow(hy, params.RiverFlowThreshold)

	biome := classifyBiomes(elevation, temp, precip, hy, content)

	coast := coastalSteps(mesh, elevation)
	geo := computeGeology(geologyInput{
		Mesh:        mesh,
		Elevation:   elevation,
		Temp:        temp,
		Precip:      precip,
		Boundaries:  boundaries,
		MaxSteps:    params.BoundaryInfluenceSteps,
		CoastSteps:  coast,
		RiverFlow:   flow,
		IsEndorheic: hy.IsEndorheic,
	})

	deposits := placeResources(mesh, elevation, biome, content, geo, root.Sub("resources"))

	cells := make([]Cell, mesh.Len())
	for c := range cells {
		cells[c] = Cell{
			Point:         mesh.Points[c],
			PlateID:       int16(plateOf[c]),
			Elevation:     Elevation(elevation[c]),
			Temperature:   temp[c],
			Precipitation: precip[c],
			BiomeIdx:      biome[c],
			IsOcean:       elevation[c] <= 0,
			IsLake:        hy.IsLake[c],
			RiverFlow:     flow[c],
		}
	}

	nameRand := root.Sub("names")
	continents := nameTopRegions(nameRand, content, connectedComponents(mesh, func(c int32) bool { return elevation[c] > 0 }), 8, "names:continent")
	seas := nameTopRegions(nameRand, content, connectedComponents(mesh, func(c int32) bool { return elevation[c] <= 0 }), 8, "names:sea")
	mountainRanges := nameTopRegions(nameRand, content, connectedComponents(mesh, func(c int32) bool {
		return elevation[c] > 0 && geo[c][GeoOrogenicBelt] >= 500
	}), 10, "names:mountain")
	rivers := extractRivers(mesh, hy, flow, nameRand, content, 12)

	w := &World{
		Seed:             seed,
		GeneratorVersion: GeneratorVersion,
		Params:           params,
		Content:          content,
		Cells:            cells,
		Plates:           plates,
		Deposits:         deposits,
		Continents:       continents,
		Seas:             seas,
		MountainRanges:   mountainRanges,
		Rivers:           rivers,
		mesh:             mesh,
	}
	return w, nil
}

// CellCount is the number of cells in the mesh.
func (w *World) CellCount() int { return len(w.Cells) }

// Neighbors returns the cell ids adjacent to cell c.
func (w *World) Neighbors(c int32) []int32 { return w.mesh.Neighbors(int(c)) }

// BiomeCode returns the content code of the biome a cell is classified as.
func (w *World) BiomeCode(cellID int32) string {
	return w.Content.Biomes[w.Cells[cellID].BiomeIdx].Code
}

// NearestCell returns the id of the cell closest to the given latitude and
// longitude in degrees.
//
// VISUAL/QUERY ONLY: converting a degree coordinate to a 3D point uses
// sin/cos, so two different runtimes could in principle disagree about
// which of two nearly-equidistant cells is "closer" at a boundary. Nothing
// in Generate calls this; it exists for looking a point up in an
// already-built World (the preview renderer's pixel-to-cell mapping today,
// perhaps a settlement-placement query later).
func (w *World) NearestCell(latDeg, lonDeg float64) int32 {
	if w.index == nil {
		w.index = newNearestIndex(w.mesh.Points)
	}
	latRad := latDeg * math.Pi / 180
	lonRad := lonDeg * math.Pi / 180
	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Cos(latRad) * math.Sin(lonRad)
	z := math.Sin(latRad)
	return w.index.Nearest(x, y, z)
}

// NearestCells returns up to k cell ids closest to the given latitude and
// longitude, nearest first, with their squared chord distances.
//
// VISUAL/QUERY ONLY, same caveat as NearestCell. It exists for the preview
// renderer's inverse-distance-weighted shading, so a relief map looks like
// smooth terrain instead of one flat-shaded polygon per cell.
func (w *World) NearestCells(latDeg, lonDeg float64, k int) ([]int32, []float64) {
	if w.index == nil {
		w.index = newNearestIndex(w.mesh.Points)
	}
	latRad := latDeg * math.Pi / 180
	lonRad := lonDeg * math.Pi / 180
	x := math.Cos(latRad) * math.Cos(lonRad)
	y := math.Cos(latRad) * math.Sin(lonRad)
	z := math.Sin(latRad)
	return w.index.NearestK(x, y, z, k)
}

// Fingerprint hashes every gameplay-relevant output — per-cell plate,
// elevation, climate, biome and river-flow class; every deposit; every
// named feature's size and cell membership — into a single value that
// changes if and only if a change to this package would change what a
// player actually experiences for a given seed. It intentionally leaves out
// nothing that affects gameplay and intentionally leaves out exact 3D cell
// POSITIONS (Point), which are the one field this package documents as not
// proven bit-identical across languages (geometry.go) — the fingerprint is a
// regression guard for THIS Go implementation across changes to this
// package, not a promise about a hypothetical reimplementation.
func (w *World) Fingerprint() uint64 {
	var h uint64 = 0xF00DCAFEF00DCAFE
	mix := func(v uint64) {
		h = splitmix64Finalize(h ^ v)
	}
	mixInt := func(v int64) { mix(uint64(v)) }

	for _, c := range w.Cells {
		mixInt(int64(c.PlateID))
		mixInt(int64(c.Elevation))
		mixInt(int64(c.Temperature))
		mixInt(int64(c.Precipitation))
		mixInt(int64(c.BiomeIdx))
		mixInt(int64(c.RiverFlow))
	}
	for _, d := range w.Deposits {
		mix(fnv1a64(d.ID))
		mix(fnv1a64(d.ResourceCode))
		mixInt(int64(d.CellID))
		mixInt(d.Reserve)
		mixInt(int64(d.GradePermille))
	}
	for _, group := range [][]NamedRegion{w.Continents, w.Seas, w.MountainRanges} {
		for _, reg := range group {
			mix(fnv1a64(reg.Name.Latin))
			mixInt(int64(reg.CellCount))
		}
	}
	for _, riv := range w.Rivers {
		mix(fnv1a64(riv.Name.Latin))
		mixInt(int64(len(riv.Cells)))
	}
	return h
}

// Summary is the small, JSON-friendly report the preview CLI prints: land
// percentage, biome mix, resource counts, longest rivers.
type Summary struct {
	CellCount        int                `json:"cell_count"`
	LandPercent      float64            `json:"land_percent"`
	BiomePercent     map[string]float64 `json:"biome_percent"`
	ResourceDeposits map[string]int     `json:"resource_deposits"`
	Continents       []string           `json:"continents"`
	Seas             []string           `json:"seas"`
	MountainRanges   []string           `json:"mountain_ranges"`
	Rivers           []RiverSummary     `json:"rivers"`
	GenerationTimeMS int64              `json:"generation_time_ms,omitempty"`
	Fingerprint      string             `json:"fingerprint"`
}

// RiverSummary is one named river's length, for the JSON report.
type RiverSummary struct {
	Name   string `json:"name"`
	NameFa string `json:"name_fa"`
	Cells  int    `json:"cells"`
}

// BuildSummary computes the report described in the project brief.
func (w *World) BuildSummary() Summary {
	land := 0
	biomeCount := make(map[string]int)
	for _, c := range w.Cells {
		if !c.IsOcean {
			land++
		}
		biomeCount[w.Content.Biomes[c.BiomeIdx].Code]++
	}

	biomePercent := make(map[string]float64, len(biomeCount))
	for code, n := range biomeCount {
		biomePercent[code] = 100 * float64(n) / float64(len(w.Cells))
	}

	deposits := make(map[string]int)
	for _, d := range w.Deposits {
		deposits[d.ResourceCode]++
	}

	names := func(regs []NamedRegion) []string {
		out := make([]string, len(regs))
		for i, r := range regs {
			out[i] = fmt.Sprintf("%s (%s)", r.Name.Latin, r.Name.Persian)
		}
		return out
	}

	rivers := make([]RiverSummary, len(w.Rivers))
	for i, r := range w.Rivers {
		rivers[i] = RiverSummary{Name: r.Name.Latin, NameFa: r.Name.Persian, Cells: len(r.Cells)}
	}

	return Summary{
		CellCount:        len(w.Cells),
		LandPercent:      100 * float64(land) / float64(len(w.Cells)),
		BiomePercent:     biomePercent,
		ResourceDeposits: deposits,
		Continents:       names(w.Continents),
		Seas:             names(w.Seas),
		MountainRanges:   names(w.MountainRanges),
		Rivers:           rivers,
		Fingerprint:      fmt.Sprintf("%016x", w.Fingerprint()),
	}
}
