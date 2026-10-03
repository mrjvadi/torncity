// Package landroad is the lot-level half of "a road opens the land it reaches"
// (ADR 0044 5.5 and the owner decision of 2026-10-03).
//
// The world router (internal/domain/roads, ADR 0042 6.2) finds the cheapest
// line over base tiles (305 m). A settlement's land is counted in lots (30 m,
// a tenth of a tile), so the line is refined here: an A* over lots, kept
// inside the tiles the world router chose, that avoids what cannot carry a
// road (buildings, other people's lots, other settlements' land), crosses
// brooks by ford or culvert, refuses open water, and prices every step by the
// same slope rule as the world router. Then the band of lots along the line
// (the frontage) is cut: those are the lots that open for sale.
//
// Everything here is a PURE function of its inputs, with integer costs and a
// fixed tie-break, so every replica plans the same line (the discipline of
// internal/domain/roads and settlementbuilding/access.go). No database, no
// clock.
package landroad

import (
	"container/heap"
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/roads"
)

// Lot is a lot of a settlement's land: counted from the south-west corner of
// its first grid, x east and y north, negative west and south of it.
type Lot struct{ X, Y int }

// Add is the lot offset by (dx, dy).
func (l Lot) Add(dx, dy int) Lot { return Lot{l.X + dx, l.Y + dy} }

// Water is what covers a lot.
type Water int

const (
	WaterNone   Water = iota
	WaterStream       // a brook: a ford or a culvert carries the road
	WaterRiver        // a river: a bridge, only as long as the class carries
	WaterStill        // a lake or the sea: no road
)

// Info is what the router needs to know about one lot.
type Info struct {
	Water      Water
	ElevationM float64
	// SlopeM is the largest height difference to a side neighbour.
	SlopeM float64
	// TerrainBPS is the clearing multiplier of the lot's biome (10000 = grass).
	TerrainBPS int
}

// Ground reads lots.
type Ground interface {
	Info(l Lot) Info
}

// Class is the planner content of a road class (roads.yml).
type Class struct {
	Code           string
	MaxGradeBPS    int
	BridgeMaxSpanM int
	Fords          bool
}

// Planner failures.
var (
	// ErrNoRoute: no line exists inside the corridor.
	ErrNoRoute = errors.New("landroad: no route")
	// ErrInvalid: the request cannot be planned (same lot, no class limit).
	ErrInvalid = errors.New("landroad: invalid request")
	// ErrTooLong: the line is longer than the plan bound.
	ErrTooLong = errors.New("landroad: the road is longer than a plan may be")
	// ErrWater: the end lies in open water.
	ErrWater = errors.New("landroad: the end lies in open water")
)

// BridgeError: the best line crosses water over a longer span than the class
// carries (research, ADR 0042 6.3, gives the longer bridge).
type BridgeError struct {
	SpanM, ClassMaxM int
	At               Lot
}

func (e *BridgeError) Error() string {
	return fmt.Sprintf("landroad: the line crosses %d m of water at %d,%d, the class carries %d m", e.SpanM, e.At.X, e.At.Y, e.ClassMaxM)
}

// Env is everything the router reads.
type Env struct {
	Ground Ground
	Class  Class
	// LotM is the side of a lot in metres.
	LotM float64
	// Blocked closes a lot to the road: a standing building, a lot somebody
	// owns, right-of-way of another road, land of another settlement. A road
	// of this settlement is never blocked (the caller leaves it out).
	Blocked func(Lot) bool
	// Cheap marks lots that already carry a road of this settlement: the line
	// may run along them at a fraction of the price.
	Cheap func(Lot) bool
	// Corridor limits the search to the lots the world router chose (nil: any).
	Corridor func(Lot) bool
	// MaxLots is the longest plan (0: no bound but the search budget).
	MaxLots int
	// MaxExpansions is the search budget (0: 400,000).
	MaxExpansions int
	// StreamRun is the most consecutive brook lots one line may cross.
	StreamRun int
}

// Step is one lot of a planned line.
type Step struct {
	Lot        Lot
	Water      Water
	ElevationM float64
}

// Path is a planned line from the lot after Env's start to the end.
type Path struct {
	Steps []Step
	// Climb is the sum of every rise along the line, in metres.
	ClimbM float64
	// MaxGradeBPS is the steepest step, rise over run in basis points.
	MaxGradeBPS int
	// Crossings counts the lots that are water (fords, culverts, bridges).
	Crossings int
	// LongestSpanM is the longest run of river lots, in metres.
	LongestSpanM int
	Expansions   int
}

const (
	// unit is the price of a step over flat grass.
	unit        = 1000
	cheapDiv    = 5 // an existing road costs a fifth
	streamExtra = 3 * unit
	riverExtra  = 25 * unit
)

type node struct {
	l    Lot
	f, g int64
}

type queue []node

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.f != b.f {
		return a.f < b.f
	}
	if a.g != b.g {
		return a.g < b.g
	}
	if a.l.Y != b.l.Y {
		return a.l.Y < b.l.Y
	}
	return a.l.X < b.l.X
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *queue) Push(x any)   { *q = append(*q, x.(node)) }
func (q *queue) Pop() any     { o := *q; n := len(o); x := o[n-1]; *q = o[:n-1]; return x }

// the four directions, in the fixed order every replica visits them
var dirs = [4]Lot{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// Route finds the cheapest line from the lot beside from to the lot to. from
// is a lot that already carries the settlement's road (or is its hall): it is
// not part of the path. to is the end the player chose.
func Route(env Env, from, to Lot) (Path, error) {
	if from == to || env.Ground == nil || env.Class.MaxGradeBPS <= 0 || env.LotM < 1 {
		return Path{}, ErrInvalid
	}
	if env.Ground.Info(to).Water == WaterStill {
		return Path{}, ErrWater
	}
	budget := env.MaxExpansions
	if budget <= 0 {
		budget = 400_000
	}
	lotM := int(env.LotM)
	passable := func(l Lot) bool {
		if env.Corridor != nil && !env.Corridor(l) {
			return false
		}
		if env.Blocked != nil && env.Blocked(l) {
			return false
		}
		switch env.Ground.Info(l).Water {
		case WaterStill:
			return false
		case WaterRiver:
			return float64(env.Class.BridgeMaxSpanM) >= env.LotM
		}
		return true
	}
	// stepCost is the price of entering l from p; ok is false when the grade is
	// past three times the class limit.
	stepCost := func(p, l Lot) (int64, bool) {
		a, b := env.Ground.Info(p), env.Ground.Info(l)
		slope, ok := roads.SlopeBPS(int(b.ElevationM-a.ElevationM), lotM, env.Class.MaxGradeBPS)
		if !ok {
			return 0, false
		}
		terrain := b.TerrainBPS
		if terrain < 10000 {
			terrain = 10000
		}
		c := int64(unit) * int64(terrain) * int64(slope) / 100_000_000
		switch b.Water {
		case WaterStream:
			c += streamExtra
		case WaterRiver:
			c += riverExtra
		}
		if env.Cheap != nil && env.Cheap(l) {
			c = int64(unit) / cheapDiv
		}
		return c, true
	}
	h := func(l Lot) int64 { return int64(abs(l.X-to.X)+abs(l.Y-to.Y)) * (unit / cheapDiv) }

	g := map[Lot]int64{from: 0}
	prev := map[Lot]Lot{}
	open := &queue{}
	heap.Push(open, node{l: from, g: 0, f: h(from)})
	expansions := 0
	closed := map[Lot]bool{}
	for open.Len() > 0 {
		cur := heap.Pop(open).(node)
		if closed[cur.l] {
			continue
		}
		closed[cur.l] = true
		if cur.l == to {
			return build(env, from, to, prev, expansions)
		}
		expansions++
		if expansions > budget {
			return Path{}, ErrNoRoute
		}
		for _, d := range dirs {
			nb := cur.l.Add(d.X, d.Y)
			if closed[nb] || !passable(nb) {
				continue
			}
			c, ok := stepCost(cur.l, nb)
			if !ok {
				continue
			}
			ng := cur.g + c
			if old, seen := g[nb]; seen && old <= ng {
				continue
			}
			g[nb] = ng
			prev[nb] = cur.l
			heap.Push(open, node{l: nb, g: ng, f: ng + h(nb)})
		}
	}
	return Path{}, ErrNoRoute
}

func build(env Env, from, to Lot, prev map[Lot]Lot, expansions int) (Path, error) {
	var rev []Lot
	for cur := to; cur != from; cur = prev[cur] {
		rev = append(rev, cur)
	}
	steps := make([]Step, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		in := env.Ground.Info(rev[i])
		steps = append(steps, Step{Lot: rev[i], Water: in.Water, ElevationM: in.ElevationM})
	}
	if env.MaxLots > 0 && len(steps) > env.MaxLots {
		return Path{}, ErrTooLong
	}
	p := Path{Steps: steps, Expansions: expansions}
	lastElev := env.Ground.Info(from).ElevationM
	riverRun, streamRun := 0, 0
	for _, s := range steps {
		if d := s.ElevationM - lastElev; d > 0 {
			p.ClimbM += d
		}
		if env.LotM > 0 {
			g := int(abs(int(s.ElevationM-lastElev)) * 10000 / int(env.LotM))
			if g > p.MaxGradeBPS {
				p.MaxGradeBPS = g
			}
		}
		lastElev = s.ElevationM
		switch s.Water {
		case WaterRiver:
			p.Crossings++
			riverRun++
			streamRun = 0
			if span := int(float64(riverRun) * env.LotM); span > p.LongestSpanM {
				p.LongestSpanM = span
			}
			if span := int(float64(riverRun) * env.LotM); span > env.Class.BridgeMaxSpanM {
				return Path{}, &BridgeError{SpanM: span, ClassMaxM: env.Class.BridgeMaxSpanM, At: s.Lot}
			}
		case WaterStream:
			p.Crossings++
			streamRun++
			riverRun = 0
			if env.StreamRun > 0 && streamRun > env.StreamRun {
				return Path{}, &BridgeError{SpanM: int(float64(streamRun) * env.LotM), ClassMaxM: int(float64(env.StreamRun) * env.LotM), At: s.Lot}
			}
		default:
			riverRun, streamRun = 0, 0
		}
	}
	return p, nil
}
