package settlementbuilding

import (
	"container/heap"
	"sort"
)

// Lot access (docs/adr/0043). A parcel is worth nothing if no road reaches
// it, so the village never sells a lot it cannot serve: before a sale the
// classifier below says whether the lot already fronts the road network,
// needs a road (how many lots, at what price), needs a culvert or a footbridge
// over water, or can never be reached. Everything here is pure: the caller
// hands over the grid, the network, who holds what and the reserved
// right-of-way, and gets the same answer on every replica.
//
// THE ROUTER may use:
//   - free public lots, and lots reserved as right-of-way (a street platted
//     ahead or the corridor of an earlier sale: public road to be);
//   - water lots, as a culvert or footbridge, at a higher price and never
//     more than AccessRules.MaxCrossing in one route;
//   - the asker's OWN lots, only when carving is allowed (the owner consents
//     to turn them into road).
//
// It never uses another resident's lot, a building, or a steep lot.

// AccessKind is how a lot is served.
type AccessKind string

const (
	// AccessRoad: the lot already touches the road network.
	AccessRoad AccessKind = "road"
	// AccessNeedsRoad: a road of Roads lots over public land reaches it.
	AccessNeedsRoad AccessKind = "needs_road"
	// AccessNeedsBridge: the road has to cross water (a culvert or footbridge).
	AccessNeedsBridge AccessKind = "needs_bridge"
	// AccessNone: no road can reach it.
	AccessNone AccessKind = "none"
)

// AccessRules are the prices and limits of a connection (config
// settlement.lot_access_*).
type AccessRules struct {
	// RoadLotCost is the price of one lot of road; CrossingLotCost of one
	// lot of culvert or footbridge over water.
	RoadLotCost, CrossingLotCost int64
	// MaxCrossing is the most water lots one route may cross.
	MaxCrossing int
}

// AccessMap is what the router sees of the village.
type AccessMap struct {
	// Grid carries Buildable and Occupied (a building or a road stands).
	Grid Grid
	// Network is every road lot (finished or going up) plus the civic hall's
	// footprint: a lot beside one of them is connected.
	Network [][2]int
	// Held is the owner of every private lot.
	Held map[[2]int]string
	// Reserved is the right-of-way: lots that are never sold.
	Reserved map[[2]int]bool
}

// Access is the answer for one lot.
type Access struct {
	Kind AccessKind
	// Path is the lots to turn into road, from the lot outwards to the network
	// (empty when the lot already touches it).
	Path [][2]int
	// Carved is the part of Path that is the asker's own land.
	Carved [][2]int
	// Crossings is how many lots of Path are water.
	Crossings int
	// Cost is the price of laying Path.
	Cost int64
}

// Roads is how many lots of Path are plain road (water crossings and carved
// lots included in neither count).
func (a Access) Roads() int { return len(a.Path) - a.Crossings - len(a.Carved) }

// Feasible reports whether the lot is, or can be made, reachable.
func (a Access) Feasible() bool { return a.Kind != AccessNone }

const (
	accessStep     = 10
	accessReserved = 6   // a lot already reserved as right-of-way is the cheapest road
	accessTurn     = 5   // streets run straight
	accessCrossing = 60  // routing weight of a water lot
	accessCarve    = 500 // routing weight of one's own lot: a last resort
)

// touches reports whether a lot sits beside a lot of the network.
func (m AccessMap) touches(network map[[2]int]bool, x, y int) bool {
	for _, d := range roadDirs {
		if network[[2]int{x + d[0], y + d[1]}] {
			return true
		}
	}
	return false
}

// Plan routes a road to lot from the network for asker. carve says the
// asker accepts turning their own free lots into road on the way.
func (m AccessMap) Plan(lot [2]int, asker string, carve bool, r AccessRules) Access {
	network := make(map[[2]int]bool, len(m.Network))
	for _, p := range m.Network {
		network[p] = true
	}
	w, h := m.Grid.Width(), m.Grid.Height()
	inside := func(x, y int) bool { return x >= 0 && y >= 0 && x < w && y < h }
	if m.touches(network, lot[0], lot[1]) {
		return Access{Kind: AccessRoad}
	}
	if len(network) == 0 {
		// A village with no street at all has nothing to be served by.
		return Access{Kind: AccessNone}
	}
	// kind of lot a route may enter: 0 no, 1 public, 2 water, 3 own (carve)
	enter := func(x, y int) int {
		if !inside(x, y) {
			return 0
		}
		p := [2]int{x, y}
		if p == lot {
			return 0
		}
		g := m.Grid[y][x]
		if g.Occupied || network[p] {
			return 0
		}
		if owner, held := m.Held[p]; held {
			if carve && owner == asker && g.Buildable {
				return 3
			}
			return 0
		}
		if g.Buildable {
			return 1
		}
		if hasTag(g.TerrainTags, "sloped_lot") {
			return 0
		}
		return 2
	}
	type key struct{ x, y, dir, water int }
	dist := map[key]int{}
	prev := map[key]key{}
	pq := &accessQueue{}
	heap.Init(pq)
	weight := func(x, y int) int {
		switch enter(x, y) {
		case 2:
			return accessCrossing
		case 3:
			return accessCarve
		}
		if m.Reserved[[2]int{x, y}] {
			return accessReserved
		}
		return accessStep
	}
	push := func(k key, c int, from *key) {
		if old, ok := dist[k]; ok && old <= c {
			return
		}
		dist[k] = c
		if from != nil {
			prev[k] = *from
		}
		heap.Push(pq, accessItem{cost: c, x: k.x, y: k.y, dir: k.dir, water: k.water})
	}
	for i, d := range roadDirs {
		x, y := lot[0]+d[0], lot[1]+d[1]
		kind := enter(x, y)
		if kind == 0 {
			continue
		}
		wc := 0
		if kind == 2 {
			wc = 1
		}
		if wc > r.MaxCrossing {
			continue
		}
		push(key{x, y, i, wc}, weight(x, y), nil)
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(accessItem)
		k := key{it.x, it.y, it.dir, it.water}
		if it.cost > dist[k] {
			continue
		}
		if m.touches(network, it.x, it.y) {
			var path [][2]int
			for {
				path = append(path, [2]int{k.x, k.y})
				pk, ok := prev[k]
				if !ok {
					break
				}
				k = pk
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return m.describe(path, r)
		}
		for i, d := range roadDirs {
			x, y := it.x+d[0], it.y+d[1]
			kind := enter(x, y)
			if kind == 0 {
				continue
			}
			wc := it.water
			if kind == 2 {
				wc++
			}
			if wc > r.MaxCrossing {
				continue
			}
			c := it.cost + weight(x, y)
			if i != it.dir {
				c += accessTurn
			}
			from := k
			push(key{x, y, i, wc}, c, &from)
		}
	}
	return Access{Kind: AccessNone}
}

// describe prices a path.
func (m AccessMap) describe(path [][2]int, r AccessRules) Access {
	a := Access{Path: path}
	for _, p := range path {
		g := m.Grid[p[1]][p[0]]
		switch {
		case !g.Buildable:
			a.Crossings++
			a.Cost += r.CrossingLotCost
		default:
			a.Cost += r.RoadLotCost
			if _, held := m.Held[p]; held {
				a.Carved = append(a.Carved, p)
			}
		}
	}
	a.Kind = AccessNeedsRoad
	if a.Crossings > 0 {
		a.Kind = AccessNeedsBridge
	}
	return a
}

// Nearby lists up to limit free lots that are better served than lot, by
// distance: the lots a buyer is offered instead of one that cannot be served.
// ok says whether a lot is on offer at all (free, not reserved); only lots whose
// access is feasible without carving are listed, those that already touch a
// road first.
func (m AccessMap) Nearby(lot [2]int, asker string, r AccessRules, ok func(x, y int) bool, limit int) []NearbyLot {
	var out []NearbyLot
	for y := range m.Grid {
		for x := range m.Grid[y] {
			p := [2]int{x, y}
			if p == lot || !ok(x, y) {
				continue
			}
			a := m.Plan(p, asker, false, r)
			if !a.Feasible() {
				continue
			}
			d := abs(x-lot[0]) + abs(y-lot[1])
			out = append(out, NearbyLot{X: x, Y: y, Distance: d, Access: a})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Access.Kind == AccessRoad) != (b.Access.Kind == AccessRoad) {
			return a.Access.Kind == AccessRoad
		}
		if a.Distance != b.Distance {
			return a.Distance < b.Distance
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// NearbyLot is one lot offered instead of another.
type NearbyLot struct {
	X, Y     int
	Distance int
	Access   Access
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PlanStreets platts the village's streets ahead: a grid of straight streets
// every pitch lots, anchored on the first lot of the network, kept only where
// the lot is free public land and the street joins the network without a
// gap. It is the road reserve of a village that wants one: those lots are
// never sold. pitch < 3 means no plan. Deterministic.
func PlanStreets(grid Grid, network [][2]int, held map[[2]int]string, pitch int) [][2]int {
	if pitch < 3 || len(network) == 0 {
		return nil
	}
	anchor := network[0]
	for _, p := range network {
		if p[1] < anchor[1] || (p[1] == anchor[1] && p[0] < anchor[0]) {
			anchor = p
		}
	}
	w, h := grid.Width(), grid.Height()
	isNet := map[[2]int]bool{}
	for _, p := range network {
		isNet[p] = true
	}
	cand := map[[2]int]bool{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mod(x-anchor[0], pitch) != 0 && mod(y-anchor[1], pitch) != 0 {
				continue
			}
			p := [2]int{x, y}
			if _, taken := held[p]; taken || isNet[p] || grid[y][x].Occupied || !grid[y][x].Buildable {
				continue
			}
			cand[p] = true
		}
	}
	// keep what joins the network through candidate lots (breadth first)
	keep := map[[2]int]bool{}
	var queue [][2]int
	for p := range cand {
		for _, d := range roadDirs {
			if isNet[[2]int{p[0] + d[0], p[1] + d[1]}] {
				keep[p] = true
				queue = append(queue, p)
				break
			}
		}
	}
	sort.Slice(queue, func(i, j int) bool { return lessLot(queue[i], queue[j]) })
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range roadDirs {
			n := [2]int{p[0] + d[0], p[1] + d[1]}
			if cand[n] && !keep[n] {
				keep[n] = true
				queue = append(queue, n)
			}
		}
	}
	out := make([][2]int, 0, len(keep))
	for p := range keep {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return lessLot(out[i], out[j]) })
	return out
}

func lessLot(a, b [2]int) bool {
	if a[1] != b[1] {
		return a[1] < b[1]
	}
	return a[0] < b[0]
}

func mod(a, m int) int { return ((a % m) + m) % m }

type accessItem struct{ cost, x, y, dir, water int }

type accessQueue []accessItem

func (q accessQueue) Len() int { return len(q) }
func (q accessQueue) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.y != b.y {
		return a.y < b.y
	}
	if a.x != b.x {
		return a.x < b.x
	}
	if a.dir != b.dir {
		return a.dir < b.dir
	}
	return a.water < b.water
}
func (q accessQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *accessQueue) Push(x any)   { *q = append(*q, x.(accessItem)) }
func (q *accessQueue) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}
