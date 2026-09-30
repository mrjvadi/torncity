package settlementbuilding

import (
	"container/heap"
	"errors"
)

// ErrNoRoadAccess means a building could never be reached by road: no free,
// buildable lot beside it leads to the village's network.
var ErrNoRoadAccess = errors.New("settlementbuilding: no road can reach this building")

// This file is the village's own street planner. When a building is placed,
// the game lays the road that connects it to the village network, so the head
// never draws roads by hand (they still may, with place / place_many).
//
// THE NETWORK is every road lot that holds its lot (finished or going up) plus
// the footprint of the civic hall, the village's heart: a building touching a
// network lot (side by side, not diagonal) is connected. The founding kit puts
// a road against the hall, so a village always has a network.
//
// THE PATH is the cheapest run of free, buildable lots from a lot beside the
// new building to a lot beside the network (Dijkstra over the four
// neighbours). Cost is what keeps streets out of the way of future plots:
//   - every lot costs a base;
//   - a lot in open ground (few blocked neighbours) costs more than one that
//     hugs a building, the grid's edge or water, so streets run along the
//     edges of what already stands, not through the middle of free ground;
//   - a turn costs extra, so streets run straight.
//
// It is deterministic: costs are integers, neighbours are visited in a fixed
// order and ties break on (cost, y, x, direction), so every replica plans the
// same path for the same grid.

// Costs of the planner, in arbitrary integer units.
const (
	roadStepCost = 10
	roadOpenCost = 3 // extra per free neighbour lot
	roadTurnCost = 5
)

// PlanRoads returns the lots to lay as road so that a building standing on
// footprint touches the network, in path order from the building outwards.
// grid is the village grid with the new building's footprint ALREADY marked
// occupied; roads are the lots of existing road buildings, hall the lots of
// the civic hall (either may be empty). A nil path with a nil error means the
// building is already connected.
func PlanRoads(grid Grid, footprint, roads, hall [][2]int) ([][2]int, error) {
	network := map[[2]int]bool{}
	for _, p := range roads {
		network[p] = true
	}
	for _, p := range hall {
		network[p] = true
	}
	if len(network) == 0 {
		// Nothing to connect to (no kit): there is no street to speak of.
		return nil, nil
	}
	w, h := grid.Width(), grid.Height()
	inside := func(x, y int) bool { return x >= 0 && y >= 0 && x < w && y < h }
	free := func(x, y int) bool { return inside(x, y) && grid[y][x].Buildable && !grid[y][x].Occupied }
	touchesNetwork := func(x, y int) bool {
		for _, d := range roadDirs {
			if network[[2]int{x + d[0], y + d[1]}] {
				return true
			}
		}
		return false
	}
	for _, p := range footprint {
		if touchesNetwork(p[0], p[1]) {
			return nil, nil
		}
	}

	// hug is how many of a lot's neighbours are closed (edge, water, a
	// building): the more, the more a street there follows something.
	hug := func(x, y int) int {
		n := 0
		for _, d := range roadDirs {
			if !free(x+d[0], y+d[1]) {
				n++
			}
		}
		return n
	}
	step := func(x, y int) int { return roadStepCost + (4-hug(x, y))*roadOpenCost }

	type key struct{ x, y, dir int }
	dist := map[key]int{}
	prev := map[key]key{}
	pq := &roadQueue{}
	heap.Init(pq)
	seen := map[[2]int]bool{}
	for _, p := range footprint {
		for i, d := range roadDirs {
			x, y := p[0]+d[0], p[1]+d[1]
			if !free(x, y) || seen[[2]int{x, y}] {
				continue
			}
			seen[[2]int{x, y}] = true
			k := key{x, y, i}
			dist[k] = step(x, y)
			heap.Push(pq, roadItem{cost: dist[k], x: x, y: y, dir: i})
		}
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(roadItem)
		k := key{it.x, it.y, it.dir}
		if it.cost > dist[k] {
			continue
		}
		if touchesNetwork(it.x, it.y) {
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
			return path, nil
		}
		for i, d := range roadDirs {
			x, y := it.x+d[0], it.y+d[1]
			if !free(x, y) {
				continue
			}
			c := it.cost + step(x, y)
			if i != it.dir {
				c += roadTurnCost
			}
			nk := key{x, y, i}
			if old, ok := dist[nk]; ok && old <= c {
				continue
			}
			dist[nk] = c
			prev[nk] = k
			heap.Push(pq, roadItem{cost: c, x: x, y: y, dir: i})
		}
	}
	return nil, ErrNoRoadAccess
}

// roadDirs are the four neighbours in a fixed order (east, south, west,
// north), the order every tie is broken in.
var roadDirs = [4][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

type roadItem struct{ cost, x, y, dir int }

type roadQueue []roadItem

func (q roadQueue) Len() int { return len(q) }
func (q roadQueue) Less(i, j int) bool {
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
	return a.dir < b.dir
}
func (q roadQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *roadQueue) Push(x any)   { *q = append(*q, x.(roadItem)) }
func (q *roadQueue) Pop() any {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}
