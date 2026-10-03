package landroad

import "sort"

// Open is a lot that a road opens for sale: it lies within the frontage band
// of a road cell.
type Open struct {
	Lot Lot
	// Dist is how many lots it is from the road (1 = it touches the road).
	Dist int
	// Serves is the road cell that opened it (the nearest; ties go to the
	// lowest y, then x).
	Serves Lot
}

// Frontage cuts the band of lots along a road: every lot within depth lots of
// a road cell (walking over side neighbours), that is not itself a road cell
// and that skip does not close. It is a breadth-first walk from the road
// cells in a fixed order, so the same roads always open the same lots, each
// served by the nearest road cell. Depth 1 opens only the lots that touch the
// road.
func Frontage(road []Lot, depth int, skip func(Lot) bool) []Open {
	if depth < 1 || len(road) == 0 {
		return nil
	}
	src := append([]Lot(nil), road...)
	sort.Slice(src, func(i, j int) bool {
		if src[i].Y != src[j].Y {
			return src[i].Y < src[j].Y
		}
		return src[i].X < src[j].X
	})
	isRoad := make(map[Lot]bool, len(src))
	for _, l := range src {
		isRoad[l] = true
	}
	type item struct {
		l      Lot
		serves Lot
		d      int
	}
	seen := make(map[Lot]bool, len(src)*4)
	var out []Open
	var queue []item
	for _, l := range src {
		queue = append(queue, item{l: l, serves: l})
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.d >= depth {
			continue
		}
		for _, d := range dirs {
			nb := cur.l.Add(d.X, d.Y)
			if isRoad[nb] || seen[nb] {
				continue
			}
			if skip != nil && skip(nb) {
				continue
			}
			seen[nb] = true
			out = append(out, Open{Lot: nb, Dist: cur.d + 1, Serves: cur.serves})
			queue = append(queue, item{l: nb, serves: cur.serves, d: cur.d + 1})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lot.Y != out[j].Lot.Y {
			return out[i].Lot.Y < out[j].Lot.Y
		}
		return out[i].Lot.X < out[j].Lot.X
	})
	return out
}

// Cell is one lot of a stored road plan.
type Cell struct {
	Lot Lot
	// Parent is the cell this one hangs from; HasParent is false for the first
	// cell of a plan, which touches the settlement's standing network.
	Parent    Lot
	HasParent bool
	Built     bool
}

// ToBuild lists the cells that must be laid to make target a working road,
// from the root of the plan outwards: target and every unbuilt ancestor up to
// the first built one (or the root). ok is false for an unknown cell or a plan
// that loops (never stored, but never trusted either).
func ToBuild(cells map[Lot]Cell, target Lot) (todo []Lot, ok bool) {
	var rev []Lot
	seen := map[Lot]bool{}
	for cur := target; ; {
		c, found := cells[cur]
		if !found || seen[cur] {
			return nil, false
		}
		seen[cur] = true
		if c.Built {
			break
		}
		rev = append(rev, cur)
		if !c.HasParent {
			break
		}
		cur = c.Parent
	}
	for i := len(rev) - 1; i >= 0; i-- {
		todo = append(todo, rev[i])
	}
	return todo, true
}

// Price is what laying roadLots plain lots and crossingLots lots of ford,
// culvert or bridge costs, with the surface's multiplier in basis points
// (10000 = the base price).
func Price(roadLots, crossingLots int, roadLotCost, crossingLotCost int64, surfaceBPS int) int64 {
	if surfaceBPS <= 0 {
		surfaceBPS = 10000
	}
	base := int64(roadLots)*roadLotCost + int64(crossingLots)*crossingLotCost
	return base * int64(surfaceBPS) / 10000
}
