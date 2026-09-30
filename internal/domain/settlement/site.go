package settlement

import (
	"math"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// This file decides whether a village may stand at a candidate cell, judged
// on the lot grid it would really get - not on the coarse world cell alone.
// A cell that is land can still hold a lake, a river or a coast at lot
// resolution, and a village whose grid is mostly water has nowhere to build.
//
// There is one sampler: SampleGridDetail (griddetail.go) is what the
// placement rules and the client's layout read, and EvaluateSite is built on
// it, so the number the site search checks is the number a player will see.
// The search only PRE-SCREENS with a shared window of samples (cheaper than
// resampling every slid grid) and then confirms the winner with the exact
// sampler, so a pre-screen can never accept a site the exact one rejects.

// SiteRules are the bounds a village's site must meet (config.Settlement).
type SiteRules struct {
	// GridLots is the village grid's side; 0 switches the site check off.
	GridLots int
	// MinBuildableShareBps is the least share of the grid's lots (basis
	// points, 10000 = all) that must be buildable.
	MinBuildableShareBps int
	// MaxShiftLots is how far, in whole lots, the grid may slide from the
	// cell centre in each axis to find a good placement.
	MaxShiftLots int
}

// Enabled reports whether the site check is on.
func (r SiteRules) Enabled() bool { return r.GridLots > 0 }

// SiteReport is what a grid placed at a cell (and a shift) looks like.
type SiteReport struct {
	ShiftX, ShiftY  int
	BuildableLots   int
	TotalLots       int
	CentreBuildable bool
	// Kit is where the founding kit lands on this grid; a full kit has one
	// entry per FoundingKitBuildings.
	Kit []BuildingPlacement
}

// ShareBps is the buildable share of the grid, in basis points.
func (s SiteReport) ShareBps() int {
	if s.TotalLots == 0 {
		return 0
	}
	return s.BuildableLots * 10000 / s.TotalLots
}

// KitComplete reports that every founding-kit building found ground.
func (s SiteReport) KitComplete() bool { return len(s.Kit) == len(FoundingKitBuildings) }

// Meets reports whether the grid is good enough to found a village on: the
// centre lot buildable, the whole kit placed, and at least the configured
// share of lots buildable.
func (s SiteReport) Meets(r SiteRules) bool {
	return s.CentreBuildable && s.KitComplete() && s.BuildableLots*10000 >= r.MinBuildableShareBps*s.TotalLots
}

// GridCentre is the centre of the grid slid by (shiftX, shiftY) whole lots
// from a cell's centre (x grows east, y north), the point PlaceFoundingKit,
// SampleGridDetail and SampleGrid are all given.
func GridCentre(w *worldgen.World, cellLat, cellLon float64, shiftX, shiftY int) (lat, lon float64) {
	if shiftX == 0 && shiftY == 0 {
		return cellLat, cellLon
	}
	lotMeters := w.Params.TileMeters() / lotsPerTile
	radiusM := w.Params.PlanetRadiusKm * 1000
	latRad := cellLat * math.Pi / 180
	dLat := (float64(shiftY) * lotMeters / radiusM) * 180 / math.Pi
	dLon := (float64(shiftX) * lotMeters / (radiusM * math.Cos(latRad))) * 180 / math.Pi
	return cellLat + dLat, cellLon + dLon
}

// EvaluateSite samples the grid a village at cellID would get, slid by the
// shift, with the exact sampler the placement rules use.
func EvaluateSite(w *worldgen.World, cellID int32, shiftX, shiftY, gridLots int) SiteReport {
	pt := w.Cells[cellID].Point
	lat, lon := GridCentre(w, pt.LatDeg, pt.LonDeg, shiftX, shiftY)
	d := SampleGridDetail(w, lat, lon, gridLots, cellID)
	rep := SiteReport{ShiftX: shiftX, ShiftY: shiftY, TotalLots: gridLots * gridLots}
	for y := range d.Lots {
		for x := range d.Lots[y] {
			if d.Lots[y][x].Buildable {
				rep.BuildableLots++
			}
		}
	}
	c := gridLots / 2
	rep.CentreBuildable = d.Lots[c][c].Buildable
	rep.Kit = PlaceFoundingKit(w, lat, lon, gridLots)
	return rep
}

// shiftOrder lists every shift within max lots of the centre, nearest ring
// first and row-major within a ring: the same order for the same max, always.
func shiftOrder(max int) [][2]int {
	out := [][2]int{{0, 0}}
	for r := 1; r <= max; r++ {
		for sy := -r; sy <= r; sy++ {
			for sx := -r; sx <= r; sx++ {
				if abs(sx) == r || abs(sy) == r {
					out = append(out, [2]int{sx, sy})
				}
			}
		}
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// FitSite looks for a placement of the village grid at cellID that meets the
// rules, sliding the grid a bounded number of lots around the cell centre.
// It returns the exact report of the slide with the most buildable lots that
// meets them (nearest the centre among equals), or false when none does. Pure and deterministic.
func FitSite(w *worldgen.World, cellID int32, r SiteRules) (SiteReport, bool) {
	if !r.Enabled() {
		return SiteReport{}, true
	}
	g := r.GridLots
	pt := w.Cells[cellID].Point
	lotMeters := w.Params.TileMeters() / lotsPerTile

	// One lazily sampled window of lots around the cell centre, shared by
	// every slide: lot (X,Y) of a slide (sx,sy) is window lot (X+sx, Y+sy).
	memo := map[[2]int]bool{}
	window := func(x, y int) bool {
		k := [2]int{x, y}
		if v, ok := memo[k]; ok {
			return v
		}
		lat, lon := lotLatLon(w, pt.LatDeg, pt.LonDeg, lotMeters, x, y, g)
		v := buildableFine(w.SampleFineLatLon(lat, lon))
		memo[k] = v
		return v
	}
	c := g / 2
	type slide struct{ sx, sy, n int }
	var ok []slide
	for _, sh := range shiftOrder(r.MaxShiftLots) {
		sx, sy := sh[0], sh[1]
		if !window(c+sx, c+sy) {
			continue
		}
		n := 0
		for y := 0; y < g; y++ {
			for x := 0; x < g; x++ {
				if window(x+sx, y+sy) {
					n++
				}
			}
		}
		if n*10000 < r.MinBuildableShareBps*g*g {
			continue
		}
		if len(placeKit(g, func(x, y int) bool { return window(x+sx, y+sy) })) != len(FoundingKitBuildings) {
			continue
		}
		ok = append(ok, slide{sx, sy, n})
		if n == g*g {
			// Every lot dry: nothing later can beat it, and it is the
			// nearest of its kind, so stop sampling the rest of the window.
			break
		}
	}
	// The most buildable slide wins; among equals the one nearest the cell
	// centre (the order above), so the result never depends on map order.
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].n > ok[j].n })
	for _, s := range ok {
		// The pre-screen passed: confirm with the exact sampler.
		if rep := EvaluateSite(w, cellID, s.sx, s.sy, g); rep.Meets(r) {
			return rep, true
		}
	}
	return SiteReport{}, false
}
