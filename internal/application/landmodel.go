package application

import (
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/land"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// The land model of a settlement (docs/adr/0065): every lot of its claimed grid, the ring of commons round it and the lots
// its roads opened, with the trees and rocks standing on each now. It is computed, not stored: the generator is a pure
// function of the world's seed (domain/land), the deltas and saplings are the stored part (Tx.Land).

// LandRules are the settings of the land (config settlement.land_*).
type LandRules struct {
	// RuleAt is when trees and rocks began to stand on the land: a settlement founded before it keeps its founding grid clear
	// (generator version 0); LandRules zero value switches the model off.
	RuleAt    time.Time
	GraceDays int64
	// Ring is how many lots deep the commons round the grid are.
	Ring int
	// RegrowEvery is how long a wooded commons lot needs to regain one cut tree; SaplingFor how long a sapling grows.
	RegrowEvery, SaplingFor time.Duration
	// FellRadius is the reach of a woodcutter's camp or a forester's lodge, QuarryRadius of a pit, in lots; RockShifts the
	// shifts a field rock takes.
	FellRadius, QuarryRadius, RockShifts int
}

// Enabled reports whether the land model is on.
func (r LandRules) Enabled() bool { return !r.RuleAt.IsZero() && r.Ring > 0 }

// GraceUntil is when a woodcutter's camp stops cutting without a tree in reach; the zero time when there is no grace.
func (r LandRules) GraceUntil() time.Time {
	if r.GraceDays <= 0 || r.RuleAt.IsZero() {
		return time.Time{}
	}
	return r.RuleAt.AddDate(0, 0, int(r.GraceDays))
}

// InGrace reports whether a camp may still cut with no tree in reach.
func (r LandRules) InGrace(now time.Time) bool {
	u := r.GraceUntil()
	return !u.IsZero() && now.Before(u)
}

// LandParams turns the content block into the generator's parameters.
func LandParams(def content.LandDef) land.Params {
	pr := land.Params{GenVersion: def.GenVersion, MaxTreesGrid: def.MaxTreesGrid, MaxTreesRing: def.MaxTreesRing, MaxRocks: def.MaxRocks,
		CoreDensityBPS: def.CoreDensityBPS, OuterFactorBPS: def.OuterFactorBPS, SlopeRockBonusBPS: def.SlopeRockBonusBPS,
		CoreClearShareBPS: def.CoreClearShareBPS, CoreClearBlock: def.CoreClearBlock, Biomes: map[string]land.Density{}}
	for _, b := range def.Biomes {
		pr.Biomes[b.Biome] = land.Density{TreeBPS: b.TreeBPS, RockBPS: b.RockBPS}
	}
	return pr
}

// LandLot is one lot of the land now.
type LandLot struct {
	land.Lot
	Ground land.Ground
	// Ring is 0 inside the claimed grid, 1.. outside it; Commons says the lot is the village's commons (a ring lot no road
	// opened for sale).
	Ring    int
	Commons bool
	// Occupied says a building or a road holds the lot.
	Occupied bool
	// Trees, Rocks, Stumps, Growing are what stands now (Growing: the progress 0..1 of each sapling still growing).
	Trees, Rocks, Stumps int
	Growing              []float64
	// Standing is a lot with a tree or a rock on it.
	Obstructed bool
}

// LandView is the land of a settlement at an instant.
type LandView struct {
	Rules LandRules
	Side  int
	Now   time.Time
	Lots  map[land.Pos]*LandLot
	// Version is the generator version the settlement is under (0: its founding grid stays clear).
	Version int
}

// Pos lists the lots in a fixed order (y, then x).
func (v *LandView) Order() []land.Pos {
	out := make([]land.Pos, 0, len(v.Lots))
	for p := range v.Lots {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].X < out[j].X
	})
	return out
}

// Forest is the share of the settlement's wood that is left, basis points, and the trees generated and standing, over the
// grid, the ring and the opened lots.
func (v *LandView) Forest() (bps, generated, left int) {
	for _, l := range v.Lots {
		generated += l.Generated.Trees
		left += l.Trees
	}
	if generated == 0 {
		return 10_000, 0, left
	}
	return min(left*10_000/generated, 10_000), generated, left
}

// BuildLand computes the land of a settlement. side is the claimed grid's side; open are the lots its roads opened; occupied are
// the lots a building or a road holds (they carry nothing).
func BuildLand(w *worldgen.World, s FoundedSettlement, side int, open []OpenLotRow, occupied map[land.Pos]bool, def content.LandDef, rules LandRules,
	deltas []land.Delta, saplings []land.Sapling, now time.Time,
) *LandView {
	view := &LandView{Rules: rules, Side: side, Now: now, Lots: map[land.Pos]*LandLot{}}
	if !rules.Enabled() || w == nil || int(s.WorldCellID) >= len(w.Cells) {
		return view
	}
	pr := LandParams(def)
	version := 0
	if !s.FoundedAt.Before(rules.RuleAt) {
		version = def.GenVersion
	}
	view.Version = version

	// the ground of the grid and the ring, from one sample of the larger square round the same centre
	ring := rules.Ring
	pt := w.Cells[s.WorldCellID].Point
	lat, lon := wsettle.GridCentreGrown(w, pt.LatDeg, pt.LonDeg, s.GridShiftX, s.GridShiftY, s.GridGrowth)
	big := wsettle.SampleGridDetail(w, lat, lon, side+2*ring, s.WorldCellID)
	grounds := map[land.Pos]land.Ground{}
	for y := range big.Lots {
		for x, l := range big.Lots[y] {
			p := land.Pos{X: x - ring, Y: y - ring}
			steep := false
			for _, t := range l.Tags {
				if t == "sloped_lot" {
					steep = true
				}
			}
			grounds[p] = land.Ground{Biome: l.Biome, Water: l.Ocean || l.Lake || l.Stream == "river", Steep: steep}
		}
	}
	opened := map[land.Pos]bool{}
	for _, o := range open {
		p := land.Pos{X: o.X, Y: o.Y}
		opened[p] = true
		if _, ok := grounds[p]; ok {
			continue
		}
		steep := false
		for _, t := range o.Tags {
			if t == "sloped_lot" {
				steep = true
			}
		}
		grounds[p] = land.Ground{Biome: o.Biome, Water: o.Water == "ocean" || o.Water == "lake" || o.Water == "river", Steep: steep}
	}
	generated := land.Generate(uint64(w.Seed), s.WorldCellID, pr, side, grounds)
	if version == 0 {
		// a settlement founded before the land model keeps its founding grid clear
		for p := range generated {
			if land.Ring(p, side) == 0 {
				generated[p] = land.Obstacles{}
			}
		}
	}
	// nothing stands on a lot a building or a road holds
	for p := range occupied {
		if _, ok := generated[p]; ok {
			generated[p] = land.Obstacles{}
		}
	}
	dmap := map[land.Pos]land.Delta{}
	for _, d := range deltas {
		dmap[d.Pos] = d
	}
	smap := map[land.Pos][]land.Sapling{}
	for _, sp := range saplings {
		smap[sp.Pos] = append(smap[sp.Pos], sp)
	}
	dyn := land.Dynamics{RegrowEvery: rules.RegrowEvery}
	for p, g := range grounds {
		r := land.Ring(p, side)
		if r > ring && !opened[p] {
			continue
		}
		l := &LandLot{Lot: land.Lot{Pos: p, Generated: generated[p], Delta: dmap[p], Saplings: smap[p]}, Ground: g, Ring: r}
		l.Commons = r > 0 && !opened[p]
		l.Occupied = occupied[p]
		maxTrees := pr.MaxTreesGrid
		if r > 0 {
			maxTrees = pr.MaxTreesRing
		}
		wooded := false
		for dy := -1; dy <= 1 && !wooded; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if (dx != 0 || dy != 0) && generated[land.Pos{X: p.X + dx, Y: p.Y + dy}].Trees > 0 {
					wooded = true
					break
				}
			}
		}
		l.Trees = l.Lot.Trees(now, dyn, maxTrees, wooded && r > 0)
		l.Rocks, l.Stumps, l.Growing = l.Lot.Rocks(), l.Lot.Stumps(), l.Lot.Growing(now)
		l.Obstructed = l.Trees > 0 || l.Rocks > 0
		view.Lots[p] = l
	}
	return view
}

// Mark is a short hash of what stands on every lot, folded into the layout version: it changes exactly when a client would
// draw something different (a tree felled, a rock broken, a sapling planted or grown, natural regrowth).
func (v *LandView) Mark() string {
	if v == nil || len(v.Lots) == 0 {
		return ""
	}
	h := fnv.New64a()
	for _, p := range v.Order() {
		l := v.Lots[p]
		fmt.Fprintf(h, "%d,%d,%d,%d,%d,%d;", p.X, p.Y, l.Trees, l.Rocks, l.Stumps, len(l.Growing))
	}
	return fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)
}
