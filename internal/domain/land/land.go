// Package land is the land model of a settlement (docs/adr/0065, ADR 0033 8.2, ADR 0041 7): the trees and rocks that stand
// on its lots before anyone clears them, a pure function of the world's seed, the lot and its ground, the same on every
// replica; and what cutting, quarrying, planting and growth have done to them since, kept as deltas.
package land

import "sort"

// Params are the numbers of the generator (content obstacles.yml).
type Params struct {
	GenVersion   int
	MaxTreesGrid int
	MaxTreesRing int
	MaxRocks     int
	// CoreDensityBPS is forced on the lots within one lot of the grid's centre; OuterFactorBPS scales the biome's density
	// on the rest of the founding 5x5 (the lots within two of the centre).
	CoreDensityBPS int
	OuterFactorBPS int
	// SlopeRockBonusBPS is added to the rock density of a steep lot.
	SlopeRockBonusBPS int
	// CoreClearShareBPS and CoreClearBlock: the guarantee that a new group can build at once.
	CoreClearShareBPS int
	CoreClearBlock    int
	// Biomes maps a biome code to its tree and rock density, basis points.
	Biomes map[string]Density
}

// Density is a biome's draw probabilities, basis points.
type Density struct{ TreeBPS, RockBPS int }

// Ground is what the generator needs to know of a lot.
type Ground struct {
	Biome string
	// Water lots (ocean, lake, river) carry nothing.
	Water bool
	// Steep lots hold more rock.
	Steep bool
}

// Obstacles are the units standing on a lot.
type Obstacles struct{ Trees, Rocks int }

// Pos is a lot's signed coordinates in the settlement's frame: the claimed grid is 0..side-1 on both axes, x grows east, y north.
type Pos struct{ X, Y int }

// Ring is the Chebyshev distance of a lot outside the claimed grid: 0 inside it, 1 for the lots touching it, and so on.
func Ring(p Pos, side int) int {
	d := 0
	for _, v := range []int{-p.X, p.X - (side - 1), -p.Y, p.Y - (side - 1)} {
		d = max(d, v)
	}
	return d
}

// mix is splitmix64 over the words: deterministic and exact on every replica.
func mix(words ...uint64) uint64 {
	var z uint64 = 0x9E3779B97F4A7C15
	for _, w := range words {
		z += w + 0x9E3779B97F4A7C15
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z ^= z >> 31
	}
	return z
}

func draw(seed uint64, cell int32, p Pos, kind, k uint64) int {
	return int(mix(seed, uint64(uint32(cell)), uint64(uint32(int32(p.X))), uint64(uint32(int32(p.Y))), kind, k) % 10_000)
}

// Raw is the generator's own answer for one lot, before the guarantee pass: the number of successes of the maximum draws,
// each succeeding with the lot's density.
func Raw(seed uint64, cell int32, pr Params, side int, p Pos, g Ground) Obstacles {
	if g.Water {
		return Obstacles{}
	}
	d := pr.Biomes[g.Biome]
	tree, rock := d.TreeBPS, d.RockBPS
	if g.Steep {
		rock += pr.SlopeRockBonusBPS
	}
	maxTrees := pr.MaxTreesGrid
	ring := Ring(p, side)
	if ring > 0 {
		maxTrees = pr.MaxTreesRing
	} else {
		cx, cy := (side-1)/2, (side-1)/2
		cheb := max(abs(p.X-cx), abs(p.Y-cy))
		switch {
		case cheb <= 1:
			tree, rock = pr.CoreDensityBPS, pr.CoreDensityBPS
		case cheb <= 2:
			tree, rock = tree*pr.OuterFactorBPS/10_000, rock*pr.OuterFactorBPS/10_000
		}
	}
	var o Obstacles
	for k := 0; k < maxTrees; k++ {
		if draw(seed, cell, p, 1, uint64(k)) < tree {
			o.Trees++
		}
	}
	for k := 0; k < pr.MaxRocks; k++ {
		if draw(seed, cell, p, 2, uint64(k)) < rock {
			o.Rocks++
		}
	}
	return o
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Generate is the full generator for the lots of a claimed grid and its ring: the raw counts, then the core guarantee over
// the founding 5x5 (at least CoreClearShareBPS of it clear and a clear block of CoreClearBlock lots), applied in a fixed
// order so every replica and the client agree. grounds gives the ground of every lot asked for.
func Generate(seed uint64, cell int32, pr Params, side int, grounds map[Pos]Ground) map[Pos]Obstacles {
	out := make(map[Pos]Obstacles, len(grounds))
	for p, g := range grounds {
		out[p] = Raw(seed, cell, pr, side, p, g)
	}
	guarantee(pr, side, grounds, out)
	return out
}

// guarantee clears the founding 5x5 until enough of it is clear: first the lots that carry most, in a fixed order, then
// whatever keeps a clear block of the wanted size from existing.
func guarantee(pr Params, side int, grounds map[Pos]Ground, out map[Pos]Obstacles) {
	if pr.CoreClearShareBPS <= 0 && pr.CoreClearBlock <= 1 {
		return
	}
	cx, cy := (side-1)/2, (side-1)/2
	var core []Pos
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			p := Pos{cx + dx, cy + dy}
			if g, ok := grounds[p]; ok && !g.Water {
				core = append(core, p)
			}
		}
	}
	clear := func(p Pos) bool { o := out[p]; return o.Trees == 0 && o.Rocks == 0 }
	count := func() int {
		n := 0
		for _, p := range core {
			if clear(p) {
				n++
			}
		}
		return n
	}
	// the heaviest lots first, ties by (y, x)
	order := append([]Pos(nil), core...)
	sort.SliceStable(order, func(i, j int) bool {
		a, b := out[order[i]], out[order[j]]
		if wa, wb := a.Trees+a.Rocks, b.Trees+b.Rocks; wa != wb {
			return wa > wb
		}
		if order[i].Y != order[j].Y {
			return order[i].Y < order[j].Y
		}
		return order[i].X < order[j].X
	})
	for _, p := range order {
		if count()*10_000 >= len(core)*pr.CoreClearShareBPS {
			break
		}
		out[p] = Obstacles{}
	}
	// a clear block: the block (inside the 5x5) needing the fewest clearings is cleared
	n := pr.CoreClearBlock
	if n <= 1 {
		return
	}
	best, bestCost := Pos{}, -1
	for y := cy - 2; y+n-1 <= cy+2; y++ {
		for x := cx - 2; x+n-1 <= cx+2; x++ {
			cost, ok := 0, true
			for dy := 0; dy < n && ok; dy++ {
				for dx := 0; dx < n; dx++ {
					p := Pos{x + dx, y + dy}
					g, has := grounds[p]
					if !has || g.Water {
						ok = false
						break
					}
					if !clear(p) {
						cost++
					}
				}
			}
			if ok && (bestCost < 0 || cost < bestCost) {
				best, bestCost = Pos{x, y}, cost
			}
		}
	}
	if bestCost > 0 {
		for dy := 0; dy < n; dy++ {
			for dx := 0; dx < n; dx++ {
				out[Pos{best.X + dx, best.Y + dy}] = Obstacles{}
			}
		}
	}
}
