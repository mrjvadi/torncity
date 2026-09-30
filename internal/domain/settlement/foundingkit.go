package settlement

import (
	"math"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file is ADR 0028 section 3.1/7's free founding kit: laying "road x1 +
// civic_hall x1" on a brand-new village's own local lot grid, deterministically
// from the chosen cell's terrain — never a second RNG, never authored by
// hand. It reuses worldgen.SampleFineLatLon exactly as it is (fine.go),
// the same lot-resolution sampling cmd/worldpreview's demo city layout reads
// terrain from, so a founded village's first two buildings sit on the same
// terrain the client will one day render.

// lotsPerTile mirrors cmd/worldpreview/export_city.go's own constant: ADR
// 0028's ~30m lot is a tenth of the world generator's own ~305m base tile.
// Kept in step with that file's own comment rather than re-derived, since
// both exist to express the same ADR 0028 section 6.1 ratio.
const lotsPerTile = 10

// BuildingPlacement is one founding-kit building's placement on the
// settlement's own local grid: 0-based lot coordinates, the grid's own
// footprint-legality check already applied (ADR 0028 section 6.2).
type BuildingPlacement struct {
	TypeCode   string
	LotX, LotY int
}

// FoundingKitBuildings is the settlement.founding_kit content this ADR names
// (section 7): one road, one civic hall. Kept here rather than in
// configs/content because nothing about placing exactly these two differs
// per world — the day a second founding kit exists, this becomes a
// parameter the content loader supplies instead.
var FoundingKitBuildings = []struct {
	TypeCode string
	W, H     int
}{
	{TypeCode: "civic_hall", W: 2, H: 2},
	{TypeCode: "road", W: 1, H: 1},
}

// buildableFine reports whether one fine sample is legal ground for a
// founding-kit building: not open water, not a lake, and not sitting in a
// stream or river channel — the same "unbuildable" shape ADR 0028 section
// 6.1 describes for a settlement lot, at the coarsest of its rules (the
// full slope-band/coastline legality is section 6's own later phase, W5).
func buildableFine(s worldgen.FineSample) bool {
	return !s.IsOcean && !s.IsLake && s.StreamKind == worldgen.StreamKindNone
}

// PlaceFoundingKit lays out FoundingKitBuildings on a gridLots x gridLots
// grid centred on (centerLat, centerLon) — the settlement's chosen cell
// (FindSpawn's Candidate). It is a pure function of the world and the
// centre: the same world and centre always place the same buildings on the
// same lots.
//
// Each building type is placed at the first buildable, unoccupied footprint
// found by a deterministic row-major scan of the grid — simple on purpose:
// the real construction-queue placement system (ADR 0028 section 6, phase
// W5) is where a player chooses a lot; this only has to seed a brand-new
// village with something legal to stand on. A civic_hall that cannot fit
// anywhere on the grid (every lot underwater — implausible after FindSpawn's
// own eligibility check, but not impossible at a grid's edge) is simply
// omitted rather than forced onto illegal ground; the caller may treat an
// empty result as a reason to retry FindSpawn's next candidate.
func PlaceFoundingKit(w *worldgen.World, centerLat, centerLon float64, gridLots int) []BuildingPlacement {
	if gridLots < 2 {
		gridLots = 2
	}
	lotMeters := w.Params.TileMeters() / lotsPerTile
	buildableCache := map[[2]int]bool{}
	buildableAt := func(x, y int) bool {
		key := [2]int{x, y}
		if v, ok := buildableCache[key]; ok {
			return v
		}
		lat, lon := lotLatLon(w, centerLat, centerLon, lotMeters, x, y, gridLots)
		v := buildableFine(w.SampleFineLatLon(lat, lon))
		buildableCache[key] = v
		return v
	}
	return placeKit(gridLots, buildableAt)
}

// placeKit is PlaceFoundingKit over any per-lot buildable test, so the site
// search can run the very same placement on a pre-sampled window.
//
// The civic hall takes the buildable footprint nearest the grid's centre
// (row-major among equals), so the village's heart is where its middle is; the
// road then goes next to it.
func placeKit(gridLots int, buildableAt func(x, y int) bool) []BuildingPlacement {
	occupied := make([][]bool, gridLots)
	for i := range occupied {
		occupied[i] = make([]bool, gridLots)
	}
	fits := func(x, y, width, height int) bool {
		if x < 0 || y < 0 || x+width > gridLots || y+height > gridLots {
			return false
		}
		for j := y; j < y+height; j++ {
			for i := x; i < x+width; i++ {
				if occupied[j][i] || !buildableAt(i, j) {
					return false
				}
			}
		}
		return true
	}
	place := func(x, y, width, height int) {
		for j := y; j < y+height; j++ {
			for i := x; i < x+width; i++ {
				occupied[j][i] = true
			}
		}
	}
	// scan is the free footprint whose centre is nearest the grid's centre
	// (squared distance in half lots, exact integers), row-major among equals.
	scan := func(width, height int) (int, int, bool) {
		bx, by, best := 0, 0, -1
		for y := 0; y <= gridLots-height; y++ {
			for x := 0; x <= gridLots-width; x++ {
				if !fits(x, y, width, height) {
					continue
				}
				dx := 2*x + width - gridLots
				dy := 2*y + height - gridLots
				if d := dx*dx + dy*dy; best < 0 || d < best {
					bx, by, best = x, y, d
				}
			}
		}
		return bx, by, best >= 0
	}

	var out []BuildingPlacement
	var hallX, hallY, hallW, hallH int
	haveHall := false
	for _, b := range FoundingKitBuildings {
		if b.TypeCode == "road" && haveHall {
			// Prefer a road lot directly adjacent to the civic hall's own
			// footprint (ADR 0028 section 6.2's road-adjacency rule), before
			// falling back to the grid's first free lot.
			if x, y, ok := firstAdjacentFree(hallX, hallY, hallW, hallH, gridLots, fits); ok {
				place(x, y, 1, 1)
				out = append(out, BuildingPlacement{TypeCode: b.TypeCode, LotX: x, LotY: y})
				continue
			}
		}
		x, y, ok := scan(b.W, b.H)
		if !ok {
			continue
		}
		place(x, y, b.W, b.H)
		out = append(out, BuildingPlacement{TypeCode: b.TypeCode, LotX: x, LotY: y})
		if b.TypeCode == "civic_hall" {
			hallX, hallY, hallW, hallH, haveHall = x, y, b.W, b.H, true
		}
	}
	return out
}

// firstAdjacentFree returns the first free, buildable 1x1 lot directly
// touching a placed footprint's perimeter, row-major (top edge, then bottom,
// then left, then right) for determinism.
func firstAdjacentFree(fx, fy, fw, fh, gridLots int, fits func(x, y, w, h int) bool) (int, int, bool) {
	var ring [][2]int
	for x := fx; x < fx+fw; x++ {
		ring = append(ring, [2]int{x, fy - 1}, [2]int{x, fy + fh})
	}
	for y := fy; y < fy+fh; y++ {
		ring = append(ring, [2]int{fx - 1, y}, [2]int{fx + fw, y})
	}
	for _, p := range ring {
		if p[0] < 0 || p[1] < 0 || p[0] >= gridLots || p[1] >= gridLots {
			continue
		}
		if fits(p[0], p[1], 1, 1) {
			return p[0], p[1], true
		}
	}
	return 0, 0, false
}

// lotLatLon converts a lot-grid coordinate to lat/lon by a flat metres-per-
// degree approximation around the grid's own centre. A village's grid spans
// a few hundred metres at most (5 lots x ~30m, ADR 0028 section 4) — several
// orders of magnitude below the distance at which a flat approximation's
// error would matter (worldgen's own VISUAL/QUERY caveat is about sin/cos
// disagreeing between runtimes at the level of a bit, not about a flat
// projection's curvature error, which is unmeasurable at this extent).
func lotLatLon(w *worldgen.World, centerLat, centerLon, lotMeters float64, x, y, gridLots int) (lat, lon float64) {
	mid := float64(gridLots-1) / 2
	dyM := (float64(y) - mid) * lotMeters
	dxM := (float64(x) - mid) * lotMeters

	radiusM := w.Params.PlanetRadiusKm * 1000
	latRad := centerLat * math.Pi / 180
	dLat := (dyM / radiusM) * 180 / math.Pi
	dLon := (dxM / (radiusM * math.Cos(latRad))) * 180 / math.Pi
	return centerLat + dLat, centerLon + dLon
}
