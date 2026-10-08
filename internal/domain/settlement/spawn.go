// Package settlement is the pure, deterministic part of docs/adr/0028-world-
// and-settlements.md: where the game places a newly founded village on the
// generated planet (section 3.2), and where its two-building founding kit
// lands on its own lot grid (section 6, section 3.1's "free founding kit").
//
// It performs no I/O. Everything here is a function of a *worldgen.World
// (itself seed-first, internal/domain/worldgen's own rule), the settlements
// that already exist, and a settlement index N: the same seed, the same
// existing settlements and the same N always produce the same spot, the same
// way internal/domain/war/roll.go's dice always resolve the same battle for
// the same seed and coordinates. Nothing here decides who may found, spends
// money or writes a row; the application layer does that around this.
package settlement

import (
	"errors"
	"math"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// ErrNoEligibleSpot means Params.SearchMaxAttempts lattice points in a row
// each ran out of Params.SearchMaxCells without finding one eligible cell —
// implausible on a planet with any habitable land left, and the caller's
// signal to stop rather than loop forever.
var ErrNoEligibleSpot = errors.New("settlement: no eligible spawn spot found within the search bounds")

// Params are the spawn algorithm's tunable bounds (config.Settlement),
// carried here as plain values so this package need not import
// internal/config (internal/domain stays free of it, the same boundary
// worldgen.Params itself draws).
type Params struct {
	// MinSpawnDistanceKm is the least great-circle distance a candidate must
	// keep from every existing settlement's cell (ADR 0028 section 3.2 step
	// 2): more than twice a village's automatic territory radius, so an
	// automatic claim can never overlap one already made.
	MinSpawnDistanceKm float64
	// ThreatRadiusKm is how far a stronger neighbour's weight still steers a
	// new spawn away (section 3.2 step 3): a preference, not a wall.
	ThreatRadiusKm float64
	// SearchMaxCells bounds one lattice point's outward walk (section 3.2
	// step 4): a bounded local search, never a global one.
	SearchMaxCells int
	// SearchMaxAttempts bounds how many lattice points, in total, one
	// founding tries before giving up.
	SearchMaxAttempts int
	// ExcludedBiomes are the biome codes a village can never stand on (ADR
	// 0028 section 3.2 step 2's habitability rule): only truly uninhabitable
	// land belongs here (polar ice). Harsh but livable biomes are not
	// banned; they cost score through BiomePenalties.
	ExcludedBiomes []string
	// MaxAbsLatitudeDeg caps |latitude| of any spawn (0 = no cap). The
	// lattice itself is squeezed into this band (LatticePointBand), so no
	// attempt is wasted on the frozen caps.
	MaxAbsLatitudeDeg float64
	// BiomePenalties is the score subtracted for standing on a harsh but
	// livable biome (desert, tundra, ...), by biome code: the "terrain
	// penalty" of section 3.2 step 3 in the same units as the other terms.
	BiomePenalties map[string]float64
	// Site is the lot-grid rule a cell must also meet: the grid the village
	// would really get must be mostly buildable (site.go). Zero value = off.
	Site SiteRules
}

// biomeRules is Params' biome lists resolved against one world's biome
// table, once per FindSpawn, so the per-cell checks are array lookups.
type biomeRules struct {
	excluded []bool
	penalty  []float64
	maxLat   float64
}

func newBiomeRules(w *worldgen.World, p Params) biomeRules {
	r := biomeRules{
		excluded: make([]bool, len(w.Content.Biomes)),
		penalty:  make([]float64, len(w.Content.Biomes)),
		maxLat:   p.MaxAbsLatitudeDeg,
	}
	for i, b := range w.Content.Biomes {
		for _, code := range p.ExcludedBiomes {
			if code == b.Code {
				r.excluded[i] = true
			}
		}
		r.penalty[i] = p.BiomePenalties[b.Code]
	}
	return r
}

// ExistingSettlement is one already-founded settlement, as FindSpawn needs
// it: its cell and its weight in the threat score. The caller derives
// TierWeight from the settlement's tier (village/town/city/…) — this
// package does not know what a tier is, only that some settlements weigh
// more than others.
type ExistingSettlement struct {
	CellID     int32
	TierWeight float64
}

// Candidate is one scored, eligible cell FindSpawn chose.
type Candidate struct {
	// LatticeIndex is the N (ADR 0028 section 3.2 step 1) whose lattice
	// point's search found this cell — the caller's own next N to try if
	// this candidate is later refused for an unrelated reason (a UNIQUE
	// violation racing another replica; see the application layer).
	LatticeIndex int64
	CellID       int32
	LatDeg       float64
	LonDeg       float64
	// Score is the value the candidate won on: resourceScore + freshwater
	// bonus − terrainPenalty − threatScore. Exposed for logging/tests, not
	// gameplay.
	Score float64
	// ShiftX/ShiftY slide the village grid from the cell centre, in whole
	// lots (site.go); BuildableLots of TotalLots is the chosen grid's
	// buildable count. All zero when Params.Site is off.
	ShiftX, ShiftY           int
	BuildableLots, TotalLots int
}

// r2Alpha1/r2Alpha2 are 1/phi and 1/phi^2 — the golden ratio's own 2D
// low-discrepancy generalisation, the "R2 sequence" (Roberts, "The
// Unreasonable Effectiveness of Quasirandom Sequences", 2018). A classical
// Fibonacci sphere lattice (the ADR's own citation) needs its TOTAL point
// count M fixed in advance to be even: point i's latitude band is 1-(2i+1)/M,
// so a prefix i=1..N with N << M — exactly this package's situation, since
// nobody knows in advance how many settlements a planet will ever hold —
// only ever covers a thin sliver near one pole. The R2 construction has no
// such total: every prefix of it, however short, is already
// low-discrepancy (near-uniformly spread) over the sphere by area, which is
// the property "the Nth settlement lands somewhere sensible, whatever N
// turns out to be" actually needs. It keeps the ADR's own golden-ratio
// spirit (the classical 1D low-discrepancy sequence step is exactly
// 1/phi); this is that idea's direct extension to two dimensions instead of
// a fixed-total lattice pressed into an unbounded role it was not built
// for.
const (
	r2Alpha1 = 0.6180339887498949 // 1/phi
	r2Alpha2 = 0.3819660112501051 // 1/phi^2 = 1 - 1/phi
)

// LatticePoint returns the Nth point (N >= 1) of a fixed, deterministic
// low-discrepancy sequence over the sphere (ADR 0028 section 3.2 step 1). It
// is itself seed-first: never stored, always recomputed from N.
//
// z is drawn low-discrepancy-uniform over [-1,1] and theta low-discrepancy-
// uniform over [0,2*pi): together, by the standard equal-area
// cylindrical-projection construction, that is a near-uniform-by-area
// distribution over the sphere's surface — the same z/theta shape
// fibonacciSphere (internal/domain/worldgen/geometry.go) uses for its own,
// differently-purposed, fixed-count mesh, here driven by R2 instead of a
// linear index so a SHORT prefix is already well spread.
func LatticePoint(n int64) (latDeg, lonDeg float64) {
	return LatticePointBand(n, 90)
}

// LatticePointBand is LatticePoint squeezed into the latitude band
// [-maxAbsLatDeg, +maxAbsLatDeg] (a value outside (0,90) means the whole
// sphere): z is drawn over [-sin(band), sin(band)] instead of [-1,1], which
// keeps the near-uniform-by-area property inside the band. No lattice point
// ever lands on a polar cap the spawn rules exclude, so early villages are
// spread over the habitable latitudes instead of wasting attempts on ice.
// The Nth point is still a pure function of N and the band.
func LatticePointBand(n int64, maxAbsLatDeg float64) (latDeg, lonDeg float64) {
	if n < 1 {
		n = 1
	}
	zMax := 1.0
	if maxAbsLatDeg > 0 && maxAbsLatDeg < 90 {
		zMax = math.Sin(maxAbsLatDeg * math.Pi / 180)
	}
	i := float64(n)
	// The 0.5 offset keeps N=1 off the exact pole/prime-meridian corner,
	// which is not wrong, just a visually uninteresting first point.
	u := frac(0.5 + i*r2Alpha1)
	v := frac(0.5 + i*r2Alpha2)
	z := (2*v - 1) * zMax
	theta := 2 * math.Pi * u

	latDeg = math.Asin(clamp(z, -1, 1)) * 180 / math.Pi
	lonDeg = math.Mod(theta*180/math.Pi, 360)
	if lonDeg > 180 {
		lonDeg -= 360
	}
	if lonDeg < -180 {
		lonDeg += 360
	}
	return latDeg, lonDeg
}

// frac is the fractional part of x, for x >= 0.
func frac(x float64) float64 { return x - math.Floor(x) }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// FindSpawn runs ADR 0028 section 3.2 in full: it walks the lattice starting
// at startN, and for each point runs the bounded local search (step 2-4)
// for the best eligible cell nearby. It returns the first lattice point that
// has one, or ErrNoEligibleSpot after Params.SearchMaxAttempts points in a
// row have none.
//
// existing is every settlement already on the planet (any tier), for the
// spacing check and the threat score; it is read only, never mutated.
func FindSpawn(w *worldgen.World, existing []ExistingSettlement, startN int64, p Params) (Candidate, error) {
	if startN < 1 {
		startN = 1
	}
	deposits := depositsByCell(w)
	rules := newBiomeRules(w, p)

	for attempt := 0; attempt < p.SearchMaxAttempts; attempt++ {
		n := startN + int64(attempt)
		lat, lon := LatticePointBand(n, p.MaxAbsLatitudeDeg)
		start := w.NearestCell(lat, lon)

		if c, ok := searchAround(w, start, existing, deposits, rules, p); ok {
			c.LatticeIndex = n
			return c, nil
		}
	}
	return Candidate{}, ErrNoEligibleSpot
}

// depositsByCell indexes w.Deposits by cell, once, so the bounded walk below
// can look a cell's deposits up in O(1) instead of rescanning every deposit
// on the planet for every cell it visits.
func depositsByCell(w *worldgen.World) map[int32][]worldgen.Deposit {
	m := make(map[int32][]worldgen.Deposit, len(w.Deposits))
	for _, d := range w.Deposits {
		m[d.CellID] = append(m[d.CellID], d)
	}
	return m
}

// searchAround is one lattice point's bounded local search: a breadth-first
// walk outward from start over the cell graph, up to Params.SearchMaxCells
// cells visited, scoring every eligible one it finds (ADR 0028 section 3.2
// steps 2-3) and returning the best.
func searchAround(w *worldgen.World, start int32, existing []ExistingSettlement,
	deposits map[int32][]worldgen.Deposit, rules biomeRules, p Params,
) (Candidate, bool) {
	visited := map[int32]bool{start: true}
	queue := []int32{start}

	var best Candidate
	found := false

	visitedCount := 0
	for len(queue) > 0 && visitedCount < p.SearchMaxCells {
		id := queue[0]
		queue = queue[1:]
		visitedCount++

		if eligible(w, id, existing, rules, p.MinSpawnDistanceKm) {
			score := scoreCell(w, id, existing, deposits, p.ThreatRadiusKm) - rules.penalty[w.Cells[id].BiomeIdx]
			cell := w.Cells[id]
			// The lot-grid check is the expensive one, so it runs only for a
			// cell that would win; the result is the same as checking all.
			if !found || score > best.Score || (score == best.Score && id < best.CellID) {
				if rep, ok := FitSite(w, id, p.Site); ok {
					best = Candidate{CellID: id, LatDeg: cell.Point.LatDeg, LonDeg: cell.Point.LonDeg, Score: score,
						ShiftX: rep.ShiftX, ShiftY: rep.ShiftY, BuildableLots: rep.BuildableLots, TotalLots: rep.TotalLots}
					found = true
				}
			}
		}

		for _, nb := range w.Neighbors(id) {
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return best, found
}

// eligible reports whether a cell is habitable and at least minKm from every
// existing settlement's cell. Habitable means: not ocean, not a lake (the
// world generator's own flag), not an excluded biome (polar ice), and within
// the latitude cap. Harsh-but-livable biomes stay eligible and are penalised
// in the score instead.
func eligible(w *worldgen.World, id int32, existing []ExistingSettlement, rules biomeRules, minKm float64) bool {
	c := w.Cells[id]
	if c.IsOcean || c.IsLake {
		return false
	}
	if rules.excluded[c.BiomeIdx] {
		return false
	}
	if rules.maxLat > 0 && math.Abs(c.Point.LatDeg) > rules.maxLat {
		return false
	}
	for _, e := range existing {
		if greatCircleKm(w, id, e.CellID) < minKm {
			return false
		}
	}
	return true
}

// greatCircleKm is the great-circle distance between two cells' centres, via
// the exact chord/central-angle of their unit-sphere positions (never a
// lat/lon haversine, which would compound the same sin/cos rounding this
// package's callers already accept as VISUAL/QUERY ONLY — see worldgen's own
// package doc — at one remove further than necessary).
func greatCircleKm(w *worldgen.World, a, b int32) float64 {
	pa, pb := w.Cells[a].Point, w.Cells[b].Point
	dot := pa.X*pb.X + pa.Y*pb.Y + pa.Z*pb.Z
	angle := math.Acos(clamp(dot, -1, 1))
	return angle * w.Params.PlanetRadiusKm
}

// Scoring weights (ADR 0028 section 3.2 step 3: "score = resource_score -
// terrain_penalty(slope/water share) - threat_score"). These are the
// algorithm's own internal coefficients, not a player-facing or
// operator-facing tuning value (unlike config.Settlement's search bounds),
// so they stay here rather than travelling through configs/config.yml —
// the same way worldgen's own internal noise constants (fine.go) do.
const (
	// depositGradeWeight turns a deposit's grade (0..1000 permille) into
	// score points.
	depositGradeWeight = 1.0 / 1000
	// depositVarietyBonus rewards a distinct resource code found nearby,
	// once each, so a cell touching three different resources scores above
	// one touching three deposits of the same one (the owner's "some
	// resource variety within reach").
	depositVarietyBonus = 2.0
	// freshwaterBonus rewards a river or lake on the cell itself or one of
	// its neighbours (the owner's "fresh water or river nearby").
	freshwaterBonus = 3.0
	// slopeWeight scales the average elevation difference to a cell's
	// neighbours (a proxy for "flat enough" ahead of the real per-lot slope
	// bands section 6.1 defines once a settlement's own grid exists).
	slopeWeight = 0.01
	// oceanShareWeight penalises a candidate whose immediate neighbourhood
	// is mostly open water even though the cell itself is land (a narrow
	// peninsula or a spit) — "terrain_penalty(... water share)".
	oceanShareWeight = 4.0
	// minThreatDistanceKm floors the distance a threat score divides by, so
	// a settlement directly adjacent to another does not produce a
	// division blowing the score to implausible magnitudes.
	minThreatDistanceKm = 1.0
)

// scoreCell computes ADR 0028 section 3.2 step 3's score for one already-
// eligible cell.
func scoreCell(w *worldgen.World, id int32, existing []ExistingSettlement,
	deposits map[int32][]worldgen.Deposit, threatRadiusKm float64,
) float64 {
	base, threat := scoreParts(w, id, existing, deposits, threatRadiusKm)
	return base - threat
}

// scoreParts is scoreCell split into the land score (resources, fresh water, minus terrain) and the threat score,
// so the spawn circles can rank by the first and use the second only as a tie-breaker.
func scoreParts(w *worldgen.World, id int32, existing []ExistingSettlement,
	deposits map[int32][]worldgen.Deposit, threatRadiusKm float64,
) (base, threat float64) {
	neighbors := w.Neighbors(id)

	var resourceScore float64
	seenResource := map[string]bool{}
	nearby := append([]int32{id}, neighbors...)
	for _, c := range nearby {
		for _, d := range deposits[c] {
			resourceScore += float64(d.GradePermille) * depositGradeWeight
			if !seenResource[d.ResourceCode] {
				seenResource[d.ResourceCode] = true
				resourceScore += depositVarietyBonus
			}
		}
	}

	var freshwater float64
	if w.Cells[id].RiverFlow > 0 || w.Cells[id].IsLake {
		freshwater = freshwaterBonus
	} else {
		for _, nb := range neighbors {
			if w.Cells[nb].RiverFlow > 0 || w.Cells[nb].IsLake {
				freshwater = freshwaterBonus
				break
			}
		}
	}

	var slopeSum float64
	var oceanCount int
	for _, nb := range neighbors {
		diff := float64(w.Cells[id].Elevation) - float64(w.Cells[nb].Elevation)
		if diff < 0 {
			diff = -diff
		}
		slopeSum += diff
		if w.Cells[nb].IsOcean {
			oceanCount++
		}
	}
	var terrainPenalty float64
	if len(neighbors) > 0 {
		terrainPenalty = (slopeSum/float64(len(neighbors)))*slopeWeight +
			(float64(oceanCount)/float64(len(neighbors)))*oceanShareWeight
	}

	for _, e := range existing {
		if e.TierWeight <= 0 {
			continue
		}
		dist := greatCircleKm(w, id, e.CellID)
		if dist > threatRadiusKm {
			continue
		}
		if dist < minThreatDistanceKm {
			dist = minThreatDistanceKm
		}
		threat += e.TierWeight / dist
	}

	return resourceScore + freshwater - terrainPenalty, threat
}

// FindRelocation looks for a valid site for a settlement that already exists,
// as near to where it stands as the rules allow: its own cell first (a slid
// grid may be enough), then the cells around it, nearest first, in the order
// of the cell graph (deterministic). others are every OTHER settlement (the
// one being moved is left out, so it does not block its own cell). It stops
// after Params.SearchMaxCells cells and reports ErrNoEligibleSpot.
func FindRelocation(w *worldgen.World, cellID int32, others []ExistingSettlement, p Params) (Candidate, error) {
	rules := newBiomeRules(w, p)
	deposits := depositsByCell(w)
	visited := map[int32]bool{cellID: true}
	queue := []int32{cellID}
	for n := 0; len(queue) > 0 && n < p.SearchMaxCells; n++ {
		id := queue[0]
		queue = queue[1:]
		if eligible(w, id, others, rules, p.MinSpawnDistanceKm) {
			if rep, ok := FitSite(w, id, p.Site); ok {
				cell := w.Cells[id]
				return Candidate{CellID: id, LatDeg: cell.Point.LatDeg, LonDeg: cell.Point.LonDeg,
					Score:  scoreCell(w, id, others, deposits, p.ThreatRadiusKm) - rules.penalty[cell.BiomeIdx],
					ShiftX: rep.ShiftX, ShiftY: rep.ShiftY, BuildableLots: rep.BuildableLots, TotalLots: rep.TotalLots}, nil
			}
		}
		for _, nb := range w.Neighbors(id) {
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return Candidate{}, ErrNoEligibleSpot
}
