// Package rail holds the pure rules of a built railway (docs/adr/0075-mining-rail-and-ratios.md section 7): what a class of track
// costs to lay, how long a train takes over a block of track, how many wagons a team can draw, how single track is shared by
// reservation, and when a leg of a journey is applied exactly once. It reads no database and no clock; the stored reservation
// table (migration 0153) enforces the same rule across replicas.
package rail

import (
	"errors"
	"sort"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Class is one class of track (content rail.yml). Money and materials are per 30 m lot.
type Class struct {
	Code string
	// MaxConsistKg is the most one team or engine draws; WagonKg what one wagon holds.
	MaxConsistKg, WagonKg int64
	SpeedKmh              int64
	// MaxGradeBPS is the steepest grade the class lays.
	MaxGradeBPS int64
	// LotMaterials are the goods one 30 m lot of track takes; LotWorkHours the builders' hours; LotMinutes how long laying it lasts.
	LotMaterials map[string]int64
	LotWorkHours int64
	LotMinutes   int64
	// DecayBPSPerDay is the wear of a lot in a day; BrokenBelowBPS the condition under which the section blocks the line.
	DecayBPSPerDay, BrokenBelowBPS int64
	// MaxTrains is the most trains a line of the class carries (ADR 0075 7.3).
	MaxTrains int
	// Teams is the draught animals one wagon train needs (a team), 0 for a class that needs none.
	Teams int64
}

// LotM is the length of one lot in metres.
const LotM = 30

// Lots is the number of lots that cover a length of track, at least one.
func Lots(lengthM int64) int64 {
	return max((lengthM+LotM-1)/LotM, 1)
}

// Quote is the cost of laying a stretch of track.
type Quote struct {
	Lots      int64
	Materials map[string]int64
	WorkHours int64
	Duration  time.Duration
}

// QuoteOf is what laying lengthM metres of the class takes.
func (c Class) QuoteOf(lengthM int64) Quote {
	n := Lots(lengthM)
	q := Quote{Lots: n, Materials: map[string]int64{}, WorkHours: n * c.LotWorkHours, Duration: time.Duration(n*c.LotMinutes) * time.Minute}
	for it, per := range c.LotMaterials {
		q.Materials[it] = per * n
	}
	return q
}

// CapacityKg is what a train of wagons carries: the wagons hold WagonKg each, a team draws at most MaxConsistKg.
func (c Class) CapacityKg(wagons int64) int64 {
	return min(max(wagons, 0)*c.WagonKg, c.MaxConsistKg)
}

// Wagons is how many wagons a consist may have so that a team can draw them.
func (c Class) Wagons() int64 {
	if c.WagonKg <= 0 {
		return 0
	}
	return c.MaxConsistKg / c.WagonKg
}

// Segment is a stretch of track between two nodes.
type Segment struct {
	ID           string
	LengthM      int64
	ConditionBPS int64
	// Loop: a passing loop, where two trains may stand. Built: the section is laid.
	Loop, Built bool
}

// ErrBlocked means a section of the route is not laid or has broken down.
var ErrBlocked = errors.New("rail: a section of the route is not usable")

// Block checks a route: every section laid and sound. It names the first that is not.
func Block(segs []Segment, brokenBelowBPS int64) (string, error) {
	for _, s := range segs {
		if !s.Built || s.ConditionBPS < brokenBelowBPS {
			return s.ID, ErrBlocked
		}
	}
	return "", nil
}

// Duration is how long a train takes over a route. A worn section (below worn BPS) slows the train to half pace; the time of each
// section is rounded up to the second, so the same route takes the same time on every replica.
func (c Class) Duration(segs []Segment, wornBelowBPS int64) time.Duration {
	var total time.Duration
	for _, s := range segs {
		speed := max(c.SpeedKmh, 1)
		secs := (s.LengthM*3600 + speed*1000 - 1) / (speed * 1000)
		if s.ConditionBPS < wornBelowBPS {
			secs *= 2
		}
		total += time.Duration(secs) * time.Second
	}
	return total
}

// Length is the total length of a route.
func Length(segs []Segment) int64 {
	var n int64
	for _, s := range segs {
		n += s.LengthM
	}
	return n
}

// Table is the reservation table of single track in memory (the stored table of migration 0153 is its counterpart): a section holds
// one train, a loop two. It exists to state the rule once and to be tested without a database.
type Table struct {
	held map[string][]string
	loop map[string]bool
}

// NewTable starts an empty table.
func NewTable() *Table { return &Table{held: map[string][]string{}, loop: map[string]bool{}} }

// Slots is how many trains a section can hold.
func Slots(s Segment) int {
	if s.Loop {
		return 2
	}
	return 1
}

// Reserve takes every section of a route for a train or none of them (all or nothing, sections in a fixed order). A train that
// already holds a section keeps it (the call is idempotent). It returns the sections' holders that refused it.
func (t *Table) Reserve(train string, segs []Segment) (bool, []string) {
	ordered := append([]Segment(nil), segs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var blockers []string
	for _, s := range ordered {
		t.loop[s.ID] = s.Loop
		held := t.held[s.ID]
		if contains(held, train) {
			continue
		}
		if len(held) >= Slots(s) {
			blockers = append(blockers, held...)
		}
	}
	if len(blockers) > 0 {
		return false, blockers
	}
	for _, s := range ordered {
		if !contains(t.held[s.ID], train) {
			t.held[s.ID] = append(t.held[s.ID], train)
		}
	}
	return true, nil
}

// Release gives a train's sections back.
func (t *Table) Release(train string) {
	for id, h := range t.held {
		var keep []string
		for _, x := range h {
			if x != train {
				keep = append(keep, x)
			}
		}
		if len(keep) == 0 {
			delete(t.held, id)
		} else {
			t.held[id] = keep
		}
	}
}

// Holders of a section.
func (t *Table) Holders(seg string) []string { return append([]string(nil), t.held[seg]...) }

// Over reports the sections that hold more trains than they can: it must always be empty.
func (t *Table) Over() []string {
	var out []string
	for id, h := range t.held {
		cap := 1
		if t.loop[id] {
			cap = 2
		}
		if len(h) > cap {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func contains(in []string, s string) bool {
	for _, x := range in {
		if x == s {
			return true
		}
	}
	return false
}

// Apply says whether an event for a leg is to be applied: only the event of the leg the train is on, once. A second delivery of
// the same event, or one for a leg already left behind, changes nothing (docs/adr/0075 7.4: idempotent by train and leg).
func Apply(trainLeg, eventLeg int64) bool { return trainLeg == eventLeg }
