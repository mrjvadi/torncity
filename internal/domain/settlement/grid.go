package settlement

import "github.com/mrjvadi/torncity/internal/domain/worldgen"

// This file is ADR 0028 section 6.1's terrain sampling generalised beyond
// the founding kit: every lot of a settlement's own grid, not just the two
// the founding transaction places. Phase W5 (village construction) needs
// this for CanPlace's own terrain and occupancy checks — internal/domain/
// settlementbuilding.Lot is what a caller reports per lot; this file is
// where that report comes from.
//
// oreResourceCodes/coastalRadiusLots are this package's own pragmatic v1
// scope, not a claim of completeness: precise slope banding (for
// terrace_irrigation-gated content) is left for a later pass, and the
// smithing/mine "ore-bearing deposit" gate reads only the one metal
// resource world.yml ships today (iron). Both are cheap to widen — one
// more resource code, one more geometry term — without moving anything
// that already depends on this function's own shape.

// oreResourceCodes are world.yml resource codes settlementbuilding's own
// "ore_deposit" terrain tag (smithing, small_pit, mine) is granted for.
var oreResourceCodes = map[string]bool{"iron": true}

// slopeThresholdM is how much a lot's elevation may differ from its
// immediate neighbour before the grid marks it sloped_lot — the same rough
// instinct spawn.go's own scoreCell slopeWeight already applies at cell
// scale, here at lot scale instead. A first-draft number (ADR 0031 section
// 9's own precedent for a tunable this ADR did not fix).
const slopeThresholdM = 15.0

// GridLot is one lot's own terrain, as
// internal/domain/settlementbuilding.Lot needs it.
type GridLot struct {
	Buildable bool
	// Tags: a biome code (world.yml), "river_lot", "coastal_lot",
	// "sloped_lot", "ore_deposit" — whichever apply to this lot.
	Tags []string
}

// SampleGrid computes every lot's own terrain for a gridLots x gridLots
// grid centred on (centerLat, centerLon) — FindSpawn's own chosen cell
// centre, which is exactly what a settlement's world_cell_id regenerates
// (w.Cells[cellID].Point), so a caller with only the stored cell id can
// reconstruct the identical grid PlaceFoundingKit itself placed on. It is a
// pure function of the world and the centre, like every other function in
// this package: the same world and centre always produce the same grid.
func SampleGrid(w *worldgen.World, centerLat, centerLon float64, gridLots int, cellID int32) [][]GridLot {
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

	out := make([][]GridLot, gridLots)
	for y := 0; y < gridLots; y++ {
		out[y] = make([]GridLot, gridLots)
		for x := 0; x < gridLots; x++ {
			s := samples[y][x]
			lot := GridLot{Buildable: buildableFine(s)}
			if code := biomeCodeOf(w, s.Biome); code != "" {
				lot.Tags = append(lot.Tags, code)
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
	return out
}

// biomeCodeOf converts a fine sample's raw biome index to its content code,
// exactly worldgen.World.BiomeCode's own lookup, once removed from a cell.
func biomeCodeOf(w *worldgen.World, idx uint8) string {
	if int(idx) >= len(w.Content.Biomes) {
		return ""
	}
	return w.Content.Biomes[idx].Code
}

// isCoastal reports whether any of (x,y)'s four lot-grid neighbours is open
// water — the lot-scale analogue of scoreCell's own "immediate neighbourhood
// is mostly open water" check, here a boolean rather than a share.
func isCoastal(samples [][]worldgen.FineSample, x, y, gridLots int) bool {
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= gridLots || ny >= gridLots {
			continue
		}
		if samples[ny][nx].IsOcean {
			return true
		}
	}
	return false
}

// isSloped reports whether (x,y) differs from any neighbour's elevation by
// more than slopeThresholdM.
func isSloped(samples [][]worldgen.FineSample, x, y, gridLots int) bool {
	here := samples[y][x].ElevationM
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= gridLots || ny >= gridLots {
			continue
		}
		diff := here - samples[ny][nx].ElevationM
		if diff < 0 {
			diff = -diff
		}
		if diff > slopeThresholdM {
			return true
		}
	}
	return false
}
