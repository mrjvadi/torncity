package land

import (
	"sort"
	"time"
)

// Delta is what has been done to one lot since the generator spoke: stored, one row for a lot ever touched.
type Delta struct {
	Pos
	TreesCut int
	RocksCut int
	// RockWork is the shifts already spent on the rock being broken (a rock takes several).
	RockWork int
	// RegrowAnchor is when the lot's natural regrowth was last counted.
	RegrowAnchor *time.Time
	// ClearTrees and ClearRocks are the clearing orders on the lot.
	ClearTrees, ClearRocks bool
	// WoodlotBy marks a lot in the grid whose owner keeps it for foresters.
	Woodlot bool
}

// Sapling is a tree planted and not yet grown; it is a tree from ReadyAt on.
type Sapling struct {
	Pos
	PlantedAt, ReadyAt time.Time
}

// Params of the dynamics (config land.*).
type Dynamics struct {
	// RegrowEvery is how long a wooded lot with a wooded neighbour needs to regain one tree by itself; zero: never.
	RegrowEvery time.Duration
}

// Lot is the state of one lot at an instant.
type Lot struct {
	Pos
	Generated Obstacles
	Delta     Delta
	Saplings  []Sapling
}

// Trees is the trees standing at now: generated, less cut, plus the saplings grown, plus natural regrowth (never above the
// lot's maximum for what it can hold: the caller passes the cap).
func (l Lot) Trees(now time.Time, d Dynamics, maxTrees int, wooded bool) int {
	n := l.Generated.Trees - l.Delta.TreesCut
	for _, s := range l.Saplings {
		if !now.Before(s.ReadyAt) {
			n++
		}
	}
	if n < 0 {
		n = 0
	}
	// natural regrowth refills what was cut, one tree every RegrowEvery since the anchor, while the lot has a wooded neighbour
	if d.RegrowEvery > 0 && wooded && l.Delta.TreesCut > 0 && l.Delta.RegrowAnchor != nil {
		gained := int(now.Sub(*l.Delta.RegrowAnchor) / d.RegrowEvery)
		n += min(max(gained, 0), l.Delta.TreesCut)
	}
	return min(n, maxTrees)
}

// Rocks is the rocks standing.
func (l Lot) Rocks() int { return max(l.Generated.Rocks-l.Delta.RocksCut, 0) }

// Stumps is how many stumps to draw on the lot: the trees cut, at most three.
func (l Lot) Stumps() int { return min(l.Delta.TreesCut, 3) }

// Growing is the saplings planted and not yet grown at now, each with its progress 0..1.
func (l Lot) Growing(now time.Time) []float64 {
	var out []float64
	for _, s := range l.Saplings {
		if now.Before(s.ReadyAt) {
			total := s.ReadyAt.Sub(s.PlantedAt)
			if total <= 0 {
				continue
			}
			out = append(out, float64(now.Sub(s.PlantedAt))/float64(total))
		}
	}
	return out
}

// Target is a lot a crew may work, with how far it is from the crew's building.
type Target struct {
	Pos
	Dist    int
	Ordered bool
	Wood    int
}

// ByReach orders targets the way the crews take them: clearing orders first, then the nearest, then the most wooded, ties by
// (y, x), so every replica takes the same lot.
func ByReach(ts []Target) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i], ts[j]
		if a.Ordered != b.Ordered {
			return a.Ordered
		}
		if a.Dist != b.Dist {
			return a.Dist < b.Dist
		}
		if a.Wood != b.Wood {
			return a.Wood > b.Wood
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
}

// Dist is the Chebyshev distance of two lots.
func Dist(a, b Pos) int { return max(abs(a.X-b.X), abs(a.Y-b.Y)) }
