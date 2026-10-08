package settlement

import (
	"errors"
	"math"
	"sort"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// Spawn circles (docs/adr/0028 section 3.2, amendment 2026-10-09). New settlements are founded close together: the
// game keeps one current circle (a centre and a radius on the sphere), fills it with foundings, and when it is full,
// or no eligible cell is left in it, opens the next circle beside the filled ones. Circle centres follow a hex
// lattice spiralling outward from the first circle, so the settled area grows as one connected region and
// neighbours meet. Everything here is pure and deterministic: the same world, the same settlements and the same
// circle state give the same spot. The circle state itself is stored (migration 0132) and locked by the caller.

// ErrCircleParams means the circle parameters cannot place anything.
var ErrCircleParams = errors.New("settlement: spawn circle parameters are not usable")

// CircleParams are the circle tunables (config settlement.spawn_circle_*).
type CircleParams struct {
	// RadiusKm is the circle's radius on the sphere.
	RadiusKm float64
	// Capacity is how many settlements a circle takes before the next one opens.
	Capacity int
	// FillBandKm quantises the distance from the circle's centre: cells in the same band are ranked by their land
	// score (and only then by the threat), so a circle fills from its middle outward while terrain still decides
	// between nearly equal places.
	FillBandKm float64
	// MaxAdvance bounds how many circles one founding may open or skip before giving up.
	MaxAdvance int
}

// Valid reports whether the parameters can place anything.
func (c CircleParams) Valid() bool {
	return c.RadiusKm > 0 && c.Capacity > 0 && c.FillBandKm > 0 && c.MaxAdvance > 0
}

// Circle is one circle's stored state.
type Circle struct {
	Index    int
	LatDeg   float64
	LonDeg   float64
	RadiusKm float64
	Capacity int
	// Count is how many settlements the circle has taken: those already standing inside it when it opened plus
	// the foundings placed in it since.
	Count int
}

// Close reasons of a circle.
const (
	CircleFull   = "full"
	CircleNoRoom = "no_room"
)

// CircleClose is a circle the plan closed.
type CircleClose struct {
	Index  int
	Reason string
}

// SpawnPlan is what PlanSpawn decided: the chosen cell, the circle it belongs to, and the circles the search closed
// and opened on the way (to be written by the caller in the same transaction).
type SpawnPlan struct {
	Cand   Candidate
	Circle int
	Closed []CircleClose
	Opened []Circle
}

// hexDirs are the six axial directions of a hex lattice.
var hexDirs = [6][2]int{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}

// HexOffset is the axial hex coordinate of circle k in the spiral: k = 0 is the first circle, ring r holds 6r
// circles, each ring walked once around from the same corner. It is the only place the spiral's order is defined.
func HexOffset(k int) (q, r int) {
	if k <= 0 {
		return 0, 0
	}
	ring := 1
	for 3*ring*(ring+1) < k {
		ring++
	}
	p := k - 1 - 3*ring*(ring-1) // 0 .. 6*ring-1
	q, r = -ring, ring           // hexDirs[4] * ring
	for side := 0; side < 6; side++ {
		for step := 0; step < ring; step++ {
			if p == 0 {
				return q, r
			}
			p--
			q += hexDirs[side][0]
			r += hexDirs[side][1]
		}
	}
	return q, r
}

// CircleSpacingKm is the distance between neighbouring circle centres: circles of radius R laid on a hex lattice
// this far apart cover the plane without a gap.
func CircleSpacingKm(radiusKm float64) float64 { return math.Sqrt(3) * radiusKm }

// CircleCentre is the centre of circle k, given the first circle's centre: the hex offset, in kilometres on the
// tangent plane at the first centre, carried over the sphere along the great circle (azimuthal equidistant).
func CircleCentre(firstLat, firstLon float64, k int, radiusKm, planetKm float64) (lat, lon float64) {
	if k <= 0 {
		return firstLat, firstLon
	}
	q, r := HexOffset(k)
	s := CircleSpacingKm(radiusKm)
	x := s * (float64(q) + float64(r)/2) // east
	y := s * (math.Sqrt(3) / 2 * float64(r))
	dist := math.Hypot(x, y) / planetKm
	bearing := math.Atan2(x, y)
	la1, lo1 := firstLat*math.Pi/180, firstLon*math.Pi/180
	la2 := math.Asin(clamp(math.Sin(la1)*math.Cos(dist)+math.Cos(la1)*math.Sin(dist)*math.Cos(bearing), -1, 1))
	lo2 := lo1 + math.Atan2(math.Sin(bearing)*math.Sin(dist)*math.Cos(la1), math.Cos(dist)-math.Sin(la1)*math.Sin(la2))
	lat, lon = la2*180/math.Pi, math.Mod(lo2*180/math.Pi+540, 360)-180
	return lat, lon
}

// unit is a point on the unit sphere.
func unitOf(latDeg, lonDeg float64) (x, y, z float64) {
	la, lo := latDeg*math.Pi/180, lonDeg*math.Pi/180
	return math.Cos(la) * math.Cos(lo), math.Cos(la) * math.Sin(lo), math.Sin(la)
}

// cellToPointKm is the great-circle distance between a cell's centre and a point.
func cellToPointKm(w *worldgen.World, id int32, latDeg, lonDeg float64) float64 {
	p := w.Cells[id].Point
	x, y, z := unitOf(latDeg, lonDeg)
	return math.Acos(clamp(p.X*x+p.Y*y+p.Z*z, -1, 1)) * w.Params.PlanetRadiusKm
}

// ChooseFirstCentre picks the first circle's centre from the settlements that already stand: the one with the most
// settlements within two radii of it, the lowest cell id among equals. ok is false when none stands.
func ChooseFirstCentre(w *worldgen.World, existing []ExistingSettlement, radiusKm float64) (lat, lon float64, ok bool) {
	if len(existing) == 0 {
		return 0, 0, false
	}
	best, bestN := int32(-1), -1
	for _, a := range existing {
		n := 0
		for _, b := range existing {
			if greatCircleKm(w, a.CellID, b.CellID) <= 2*radiusKm {
				n++
			}
		}
		if n > bestN || (n == bestN && a.CellID < best) {
			best, bestN = a.CellID, n
		}
	}
	c := w.Cells[best].Point
	return c.LatDeg, c.LonDeg, true
}

// openCircle builds circle k with its count of the settlements already inside it.
func openCircle(w *worldgen.World, k int, lat, lon float64, existing []ExistingSettlement, cp CircleParams) Circle {
	c := Circle{Index: k, LatDeg: lat, LonDeg: lon, RadiusKm: cp.RadiusKm, Capacity: cp.Capacity}
	for _, e := range existing {
		if cellToPointKm(w, e.CellID, lat, lon) <= cp.RadiusKm {
			c.Count++
		}
	}
	return c
}

// PlanSpawn places one founding. cur is the current circle (nil before the first); first is circle 0 (nil before
// the first). The chosen cell is habitable, unclaimed and at least Params.MinSpawnDistanceKm from every settlement,
// inside the current circle; a circle that is full, or holds no eligible cell, is closed and the next opens.
func PlanSpawn(w *worldgen.World, existing []ExistingSettlement, cur, first *Circle, cp CircleParams, p Params) (SpawnPlan, error) {
	var plan SpawnPlan
	if !cp.Valid() {
		return plan, ErrCircleParams
	}
	var c Circle
	if cur == nil {
		lat, lon, ok := ChooseFirstCentre(w, existing, cp.RadiusKm)
		if !ok {
			lat, lon = LatticePointBand(1, p.MaxAbsLatitudeDeg)
		}
		c = openCircle(w, 0, lat, lon, existing, cp)
		plan.Opened = append(plan.Opened, c)
		first = &plan.Opened[0]
	} else {
		c = *cur
		if first == nil {
			first = cur
		}
	}
	deposits := depositsByCell(w)
	rules := newBiomeRules(w, p)
	for adv := 0; adv <= cp.MaxAdvance; adv++ {
		reason := CircleFull
		if c.Count < c.Capacity {
			if cand, ok := searchCircle(w, c, existing, deposits, rules, p, cp); ok {
				cand.LatticeIndex = int64(c.Index)
				plan.Cand, plan.Circle = cand, c.Index
				return plan, nil
			}
			reason = CircleNoRoom
		}
		plan.Closed = append(plan.Closed, CircleClose{Index: c.Index, Reason: reason})
		lat, lon := CircleCentre(first.LatDeg, first.LonDeg, c.Index+1, cp.RadiusKm, w.Params.PlanetRadiusKm)
		c = openCircle(w, c.Index+1, lat, lon, existing, cp)
		plan.Opened = append(plan.Opened, c)
	}
	return plan, ErrNoEligibleSpot
}

type ranked struct {
	cand   Candidate
	band   int
	base   float64
	threat float64
}

// searchCircle finds the best eligible cell inside one circle: ranked by distance band from the centre, then by the
// land score, then by the threat (a tie-breaker only, so a newcomer is never pushed away from a strong neighbour
// beyond the minimum spacing), then by cell id.
func searchCircle(w *worldgen.World, c Circle, existing []ExistingSettlement, deposits map[int32][]worldgen.Deposit,
	rules biomeRules, p Params, cp CircleParams,
) (Candidate, bool) {
	start := w.NearestCell(c.LatDeg, c.LonDeg)
	visited := map[int32]bool{start: true}
	queue := []int32{start}
	var found []ranked
	for n := 0; len(queue) > 0 && n < p.SearchMaxCells; n++ {
		id := queue[0]
		queue = queue[1:]
		d := cellToPointKm(w, id, c.LatDeg, c.LonDeg)
		if d <= c.RadiusKm && eligible(w, id, existing, rules, p.MinSpawnDistanceKm) {
			if rep, ok := FitSite(w, id, p.Site); ok {
				base, threat := scoreParts(w, id, existing, deposits, p.ThreatRadiusKm)
				base -= rules.penalty[w.Cells[id].BiomeIdx]
				cell := w.Cells[id]
				found = append(found, ranked{
					cand: Candidate{CellID: id, LatDeg: cell.Point.LatDeg, LonDeg: cell.Point.LonDeg, Score: base - threat,
						ShiftX: rep.ShiftX, ShiftY: rep.ShiftY, BuildableLots: rep.BuildableLots, TotalLots: rep.TotalLots},
					band: int(d / cp.FillBandKm), base: base, threat: threat,
				})
			}
		}
		for _, nb := range w.Neighbors(id) {
			if !visited[nb] && cellToPointKm(w, nb, c.LatDeg, c.LonDeg) <= c.RadiusKm {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	if len(found) == 0 {
		return Candidate{}, false
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.band != b.band {
			return a.band < b.band
		}
		if ba, bb := math.Floor(a.base), math.Floor(b.base); ba != bb {
			return ba > bb
		}
		if a.threat != b.threat {
			return a.threat < b.threat
		}
		return a.cand.CellID < b.cand.CellID
	})
	return found[0].cand, true
}
