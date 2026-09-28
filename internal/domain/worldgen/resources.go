package worldgen

import (
	"fmt"
	"math"
	"sort"
)

// Deposit is one finite, gradable occurrence of a resource at a cell.
//
// ID is stable across regenerations of the same (seed, GeneratorVersion,
// Content): it is derived only from the resource's code and its ordinal
// among that resource's deposits, both of which are themselves deterministic
// outputs of Generate. That is what lets the database store just the MUTABLE
// remainder of a deposit (docs on Deposit.Reserve) keyed by this ID, instead
// of storing the deposit itself — the seed-first schema in the project
// report.
type Deposit struct {
	ID            string
	ResourceCode  string
	CellID        int32
	Reserve       int64 // finite reserve, in the resource's abstract units
	GradePermille int32 // ore grade / purity, 0..1000 (0.0%..100.0%)
}

// placeResources runs one weighted sample-without-replacement per resource
// type over the cells geology.go scored as eligible, then draws each
// deposit's size and grade from the content-authored range.
//
// EXACT. Weighted sampling without replacement is done with a Fenwick
// (binary indexed) tree over integer weights and this package's own
// Rand.UintN — never the more common "key = u^(1/w), take the top K" trick,
// which needs a floating power/log and would reopen exactly the cross-
// platform rounding question rng.go exists to close.
func placeResources(mesh *Mesh, elevation []int32, biome []uint8, content Content, geo [][numGeologyCategories]int16, r *Rand) []Deposit {
	var deposits []Deposit

	for _, rule := range content.Resources {
		rr := r.Sub("resource:" + rule.Code)

		minZ, maxZ := latitudeBoundsToZ(rule.MinAbsLatitudeDeg, rule.MaxAbsLatitudeDeg)

		var candidates []int32
		var weights []int64
		for c := 0; c < len(elevation); c++ {
			if len(rule.BiomeWhitelist) > 0 && !biomeAllowed(content, biome[c], rule.BiomeWhitelist) {
				continue
			}
			absZ := mesh.Points[c].Z
			if absZ < 0 {
				absZ = -absZ
			}
			if absZ < minZ || absZ > maxZ {
				continue
			}

			var score int64
			for _, g := range rule.Geology {
				cat, ok := geologyCategoryByName(g.Category)
				if !ok {
					continue
				}
				score += int64(geo[c][cat]) * int64(g.Weight)
			}
			if score > 0 {
				candidates = append(candidates, int32(c))
				weights = append(weights, score)
			}
		}

		target := rule.DepositsTarget
		if target > len(candidates) {
			target = len(candidates)
		}
		chosen := weightedSampleWithoutReplacement(rr, weights, target)

		reserveSpan := uint64(rule.ReserveMax-rule.ReserveMin) + 1
		gradeSpan := uint64(rule.GradeMaxPermille-rule.GradeMinPermille) + 1

		for ordinal, idx := range chosen {
			cell := candidates[idx]
			reserve := rule.ReserveMin + int64(rr.UintN(reserveSpan))
			grade := rule.GradeMinPermille + int32(rr.UintN(gradeSpan))
			deposits = append(deposits, Deposit{
				ID:            fmt.Sprintf("%s-%d", rule.Code, ordinal),
				ResourceCode:  rule.Code,
				CellID:        cell,
				Reserve:       reserve,
				GradePermille: grade,
			})
		}
	}

	// Deterministic overall order: by resource code, then ordinal (already
	// implied by construction, but sorted explicitly so the fingerprint in
	// world.go never depends on map iteration or content file order).
	sort.Slice(deposits, func(i, j int) bool {
		if deposits[i].ResourceCode != deposits[j].ResourceCode {
			return deposits[i].ResourceCode < deposits[j].ResourceCode
		}
		return deposits[i].ID < deposits[j].ID
	})

	return deposits
}

func biomeAllowed(content Content, biomeIdx uint8, whitelist []string) bool {
	code := content.Biomes[biomeIdx].Code
	for _, w := range whitelist {
		if w == code {
			return true
		}
	}
	return false
}

// latitudeBoundsToZ converts a degree band to the |Z| range it corresponds
// to. Z already IS sin(latitude) for every cell by construction
// (geometry.go), so this is the one place a resource's latitude restriction
// needs a trig call at all — once per resource type, not once per cell —
// converting an author-entered degree figure into the same units cell
// positions are already expressed in.
func latitudeBoundsToZ(minDeg, maxDeg int) (float64, float64) {
	toZ := func(deg int) float64 {
		return math.Sin(float64(deg) * math.Pi / 180)
	}
	lo, hi := toZ(minDeg), toZ(maxDeg)
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// weightedSampleWithoutReplacement draws k distinct indices into weights,
// each round picking index i with probability proportional to its current
// (not-yet-picked) weight.
//
// EXACT, and deliberately simple: each round rebuilds the prefix sum over
// the remaining candidates (O(m)) and walks it to find where a single
// r.UintN draw lands, then removes that candidate by swapping it with the
// last remaining one (O(1), order does not matter for a weighted draw).
// This is O(k*m) rather than the O(k log m) a Fenwick tree would give, but k
// (DepositsTarget) is at most a few hundred and m (eligible cells) at most
// the whole mesh, which is comfortably inside this generator's time budget
// and is far less risk than a fenced-tree index arithmetic bug would be in a
// function whose output is which cell gets a resource deposit.
func weightedSampleWithoutReplacement(r *Rand, weights []int64, k int) []int {
	n := len(weights)
	if k <= 0 || n == 0 {
		return nil
	}
	idx := make([]int, n)
	w := make([]int64, n)
	for i := range idx {
		idx[i] = i
		w[i] = weights[i]
	}

	out := make([]int, 0, k)
	m := n
	for len(out) < k && m > 0 {
		var total int64
		for i := 0; i < m; i++ {
			total += w[i]
		}
		if total <= 0 {
			break
		}
		target := int64(r.UintN(uint64(total)))
		var acc int64
		pick := m - 1
		for i := 0; i < m; i++ {
			acc += w[i]
			if target < acc {
				pick = i
				break
			}
		}
		out = append(out, idx[pick])
		m--
		idx[pick] = idx[m]
		w[pick] = w[m]
	}
	return out
}
