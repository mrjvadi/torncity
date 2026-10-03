package settlement

import (
	"math"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// The land frame (ADR 0044, "a road opens the land it reaches").
//
// A settlement's lots are counted from the south-west corner of its first
// grid: lot (0,0) never moves, x grows east and y north. The first grid is
// only the first block of land. Lots beyond it, and lots west and south of it
// (negative coordinates), are addressed the same way; their ground is a pure
// function of the world and the frame below, exactly as the grid's own lots
// are (SampleGridDetail), so any replica reconstructs the same terrain for the
// same lot.

// LotFrame places lot (x, y) on the planet.
type LotFrame struct {
	// OriginLat/OriginLon is the centre of lot (0,0).
	OriginLat, OriginLon float64
	// LotM is the side of a lot in metres; RadiusM the planet's radius.
	LotM, RadiusM float64
}

// FrameOfGrid is the frame of a settlement whose first grid has side gridLots,
// found at the world cell's point, slid by shift and grown by growth.
func FrameOfGrid(w *worldgen.World, cellLat, cellLon float64, shiftX, shiftY, growth, gridLots int) LotFrame {
	cLat, cLon := GridCentreGrown(w, cellLat, cellLon, shiftX, shiftY, growth)
	lotM := LotMeters(w)
	oLat, oLon := lotLatLon(w, cLat, cLon, lotM, 0, 0, gridLots)
	return LotFrame{OriginLat: oLat, OriginLon: oLon, LotM: lotM, RadiusM: w.Params.PlanetRadiusKm * 1000}
}

// LatLon is the centre of lot (x, y), a flat projection round the origin (the
// same approximation lotLatLon makes: unmeasurable at this extent).
func (f LotFrame) LatLon(x, y int) (lat, lon float64) {
	latRad := f.OriginLat * math.Pi / 180
	dLat := (float64(y) * f.LotM / f.RadiusM) * 180 / math.Pi
	dLon := (float64(x) * f.LotM / (f.RadiusM * math.Cos(latRad))) * 180 / math.Pi
	return f.OriginLat + dLat, f.OriginLon + dLon
}

// LotOf is the lot a point lies in (the inverse of LatLon).
func (f LotFrame) LotOf(lat, lon float64) (x, y int) {
	latRad := f.OriginLat * math.Pi / 180
	northM := (lat - f.OriginLat) * math.Pi / 180 * f.RadiusM
	eastM := (lon - f.OriginLon) * math.Pi / 180 * f.RadiusM * math.Cos(latRad)
	return int(math.Round(eastM / f.LotM)), int(math.Round(northM / f.LotM))
}

// SampleRect samples the terrain of the lots [x0, x0+wd) x [y0, y0+ht) of the
// frame, with the same rules as SampleGridDetail (buildable, coastal, river,
// sloped, ore). A one-lot margin is read so the neighbour-based tags of the
// outermost lots are right. Out[y][x] is indexed from the rectangle's corner.
func SampleRect(w *worldgen.World, f LotFrame, x0, y0, wd, ht int, cellID int32) [][]LotTerrain {
	if wd < 1 || ht < 1 {
		return nil
	}
	hasOre := false
	for _, d := range w.Deposits {
		if d.CellID == cellID && oreResourceCodes[d.ResourceCode] {
			hasOre = true
			break
		}
	}
	sw, sh := wd+2, ht+2
	samples := make([][]worldgen.FineSample, sh)
	for j := 0; j < sh; j++ {
		samples[j] = make([]worldgen.FineSample, sw)
		for i := 0; i < sw; i++ {
			lat, lon := f.LatLon(x0+i-1, y0+j-1)
			samples[j][i] = w.SampleFineLatLon(lat, lon)
		}
	}
	nb := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	out := make([][]LotTerrain, ht)
	for y := 0; y < ht; y++ {
		out[y] = make([]LotTerrain, wd)
		for x := 0; x < wd; x++ {
			i, j := x+1, y+1
			s := samples[j][i]
			var maxDiff float64
			coastal := false
			for _, d := range nb {
				n := samples[j+d[1]][i+d[0]]
				if n.IsOcean {
					coastal = true
				}
				diff := math.Abs(s.ElevationM - n.ElevationM)
				if diff > maxDiff {
					maxDiff = diff
				}
			}
			lot := LotTerrain{Buildable: buildableFine(s), ElevationM: s.ElevationM, SlopeM: maxDiff, Ocean: s.IsOcean, Lake: s.IsLake}
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
			if coastal {
				lot.Tags = append(lot.Tags, "coastal_lot")
			}
			if maxDiff > slopeThresholdM {
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
