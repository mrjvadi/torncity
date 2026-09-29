package settlement

import "github.com/mrjvadi/torncity/internal/domain/worldgen"

// This file is grid.go's sampling with the numbers a client draws terrain
// from kept, and the two facts about a tier the grid depends on. SampleGrid
// is derived from SampleGridDetail, so what a placement is validated
// against and what a client is shown can never differ.

// LotTerrain is one lot's terrain with the numbers a client draws it from.
type LotTerrain struct {
	Buildable bool
	Tags      []string
	// ElevationM is the lot's height in the generator's elevation unit
	// (loosely metres, worldgen.FineSample.ElevationM).
	ElevationM float64
	// SlopeM is the largest height difference to a side neighbour, in the
	// same unit; a lot is tagged sloped_lot above SlopeThresholdM.
	SlopeM float64
	// Biome is the biome's content code, empty when unknown.
	Biome string
	Ocean bool
	Lake  bool
	// Stream is "", "stream" (a brook) or "river".
	Stream string
}

// GridDetail is a settlement's sampled grid with its geometry.
type GridDetail struct {
	Lots      [][]LotTerrain // Lots[y][x]
	LotMeters float64
	// OriginLat/OriginLon is the centre of lot (0,0); x grows east and y
	// north.
	OriginLat, OriginLon float64
}

// LotsPerTile is how many lots fit along one edge of a base world tile.
const LotsPerTile = lotsPerTile

// LotMeters is the side of a lot on w, in metres.
func LotMeters(w *worldgen.World) float64 { return w.Params.TileMeters() / lotsPerTile }

// SlopeThresholdM is how much a lot's height may differ from a side
// neighbour before the lot is tagged sloped_lot.
func SlopeThresholdM() float64 { return slopeThresholdM }

// SampleGridDetail samples every lot of a gridLots x gridLots grid centred
// on (centerLat, centerLon), see SampleGrid.
func SampleGridDetail(w *worldgen.World, centerLat, centerLon float64, gridLots int, cellID int32) GridDetail {
	if gridLots < 1 {
		gridLots = 1
	}
	lotMeters := w.Params.TileMeters() / lotsPerTile

	hasOre := false
	for _, d := range w.Deposits {
		if d.CellID == cellID && oreResourceCodes[d.ResourceCode] {
			hasOre = true
			break
		}
	}

	samples := make([][]worldgen.FineSample, gridLots)
	for y := 0; y < gridLots; y++ {
		samples[y] = make([]worldgen.FineSample, gridLots)
		for x := 0; x < gridLots; x++ {
			lat, lon := lotLatLon(w, centerLat, centerLon, lotMeters, x, y, gridLots)
			samples[y][x] = w.SampleFineLatLon(lat, lon)
		}
	}

	out := make([][]LotTerrain, gridLots)
	for y := 0; y < gridLots; y++ {
		out[y] = make([]LotTerrain, gridLots)
		for x := 0; x < gridLots; x++ {
			s := samples[y][x]
			lot := LotTerrain{Buildable: buildableFine(s), ElevationM: s.ElevationM,
				SlopeM: maxDrop(samples, x, y, gridLots), Ocean: s.IsOcean, Lake: s.IsLake}
			if code := biomeCodeOf(w, s.Biome); code != "" {
				lot.Tags = append(lot.Tags, code)
				lot.Biome = code
			}
			switch s.StreamKind {
			case worldgen.StreamKindStream:
				lot.Stream = "stream"
			case worldgen.StreamKindRiver:
				lot.Stream = "river"
			}
			if s.StreamKind != worldgen.StreamKindNone {
				lot.Tags = append(lot.Tags, "river_lot")
			}
			if isCoastal(samples, x, y, gridLots) {
				lot.Tags = append(lot.Tags, "coastal_lot")
			}
			if isSloped(samples, x, y, gridLots) {
				lot.Tags = append(lot.Tags, "sloped_lot")
			}
			if hasOre {
				lot.Tags = append(lot.Tags, "ore_deposit")
			}
			out[y][x] = lot
		}
	}
	oLat, oLon := lotLatLon(w, centerLat, centerLon, lotMeters, 0, 0, gridLots)
	return GridDetail{Lots: out, LotMeters: lotMeters, OriginLat: oLat, OriginLon: oLon}
}

// maxDrop is the largest height difference between (x,y) and a side
// neighbour.
func maxDrop(samples [][]worldgen.FineSample, x, y, gridLots int) float64 {
	here := samples[y][x].ElevationM
	var m float64
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= gridLots || ny >= gridLots {
			continue
		}
		diff := here - samples[ny][nx].ElevationM
		if diff < 0 {
			diff = -diff
		}
		if diff > m {
			m = diff
		}
	}
	return m
}

// Grid size by tier (docs/adr/0028-world-and-settlements.md section 4): a
// village's is configured (settlement.village_grid_lots), a town's and a
// city's are fixed.
const (
	townGridLots = 9
	cityGridLots = 15
)

// GridLotsForTier is the side, in lots, of a settlement's grid.
func GridLotsForTier(tier string, villageLots int) int {
	switch tier {
	case "town":
		return townGridLots
	case "city":
		return cityGridLots
	}
	return villageLots
}

// HeadOffice is the code of the office at the top of a settlement of the
// tier (docs/adr/0028 section 4).
func HeadOffice(tier string) string {
	switch tier {
	case "town":
		return "town_head"
	case "city":
		return "mayor"
	}
	return "village_head"
}
