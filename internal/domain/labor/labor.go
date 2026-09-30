// Package labor is the village labour market (docs/adr/0035): construction
// is done by workers, not by a timer, and the wage an NPC labourer asks moves
// with how scarce labour is.
//
// It is the pure part: no I/O, no clock, no randomness. The rules mirror what
// real labour markets do, at village scale:
//
//   - Labour supply follows housing. Only people who live somewhere can work
//     there: the NPC labour pool of a settlement is its housing capacity minus
//     the players who already live in it, times the share that is of working
//     age and looking for work. More homes -> more residents -> more workers.
//   - Demand is work in progress plus open vacancies. Tightness is demand over
//     the labour force (pool plus resident players), the same ratio labour
//     economists read off the Beveridge curve as vacancies against
//     unemployed.
//   - Wages follow tightness along a curve: a slack market (surplus labour)
//     pays below the base wage, a tight one (shortage) above it. A statutory
//     minimum wage per settlement tier is a floor under the curve.
//   - Skill is productivity: an apprentice gets less done in a shift than a
//     journeyman, and a master more; experience is shifts worked.
//   - A building's build time is what a reference crew takes, so the work it
//     needs is build time times the crew: worker-minutes.
package labor

import (
	"errors"
	"fmt"
	"sort"
)

// BPS is one hundredth of a percent: 10 000 = 100 %.
const BPS = 10_000

// Sentinel errors.
var ErrInvalidRules = errors.New("labor: invalid rules")

// Point is one corner of the wage curve: at this tightness the wage is this
// share of the base wage.
type Point struct {
	TightnessBPS int64
	WageBPS      int64
}

// Level is a rung of the skill ladder: from MinShifts worked on, a worker's
// shift counts ProductivityBPS.
type Level struct {
	Code            string
	MinShifts       int64
	ProductivityBPS int64
}

// Rules are the tuning numbers of the market (config: labor.*).
type Rules struct {
	// ShiftMinutes is one construction shift, GAME minutes.
	ShiftMinutes int64
	// ReferenceCrew is how many workers the content's build time assumes.
	ReferenceCrew int64
	// BaseWage is the wage of an NPC labourer per shift in a balanced market.
	BaseWage int64
	// MinWage is the statutory floor per settlement tier.
	MinWage map[string]int64
	// Curve is the wage curve, ascending by tightness.
	Curve []Point
	// ParticipationBPS is the share of NPC residents who work.
	ParticipationBPS int64
	// BaseHousing is the households a settlement has before any home is built.
	BaseHousing int64
	// NPCProductivityBPS is an NPC labourer's productivity.
	NPCProductivityBPS int64
	// Levels are the skill ladder, ascending by MinShifts.
	Levels []Level
	// FeeBPS is the village's levy on a wage a citizen employer pays.
	FeeBPS int64
	// BudgetSlackBPS is how many more shifts than the work strictly needs an
	// automatic job may be paid for (apprentices are slower).
	BudgetSlackBPS int64
}

// Enabled reports whether construction is done by work at all: a zero Rules
// keeps the old timer, which the tests of the older phases rely on.
func (r Rules) Enabled() bool { return r.ShiftMinutes > 0 }

// Validate applies the load-time rules.
func (r Rules) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidRules, fmt.Sprintf(format, args...)))
	}
	if r.ShiftMinutes < 1 {
		fail("shift minutes %d", r.ShiftMinutes)
	}
	if r.ReferenceCrew < 1 {
		fail("reference crew %d", r.ReferenceCrew)
	}
	if r.BaseWage < 0 {
		fail("base wage %d", r.BaseWage)
	}
	if r.ParticipationBPS < 0 || r.ParticipationBPS > BPS {
		fail("participation %d", r.ParticipationBPS)
	}
	if r.NPCProductivityBPS < 1 {
		fail("npc productivity %d", r.NPCProductivityBPS)
	}
	if r.FeeBPS < 0 || r.FeeBPS > BPS {
		fail("fee %d", r.FeeBPS)
	}
	if len(r.Curve) < 2 {
		fail("the wage curve needs two points")
	}
	for i, p := range r.Curve {
		if p.TightnessBPS < 0 || p.WageBPS < 0 {
			fail("wage curve point %d is negative", i)
		}
		if i > 0 && p.TightnessBPS <= r.Curve[i-1].TightnessBPS {
			fail("the wage curve is not ascending at point %d", i)
		}
		if i > 0 && p.WageBPS < r.Curve[i-1].WageBPS {
			fail("the wage curve falls at point %d: scarcity must not lower wages", i)
		}
	}
	if len(r.Levels) == 0 || r.Levels[0].MinShifts != 0 {
		fail("the skill ladder must start at zero shifts")
	}
	for i, l := range r.Levels {
		if l.ProductivityBPS < 1 || l.Code == "" {
			fail("skill level %d", i)
		}
		if i > 0 && l.MinShifts <= r.Levels[i-1].MinShifts {
			fail("the skill ladder is not ascending at level %d", i)
		}
	}
	return errors.Join(errs...)
}

// WorkRequired is the worker-minutes a building of this build time (GAME
// minutes) takes to raise: what the reference crew does in that time.
func (r Rules) WorkRequired(buildMinutes int64) int64 {
	if buildMinutes < 1 {
		buildMinutes = 1
	}
	return buildMinutes * r.ReferenceCrew
}

// LevelOf is the skill level of a worker who has worked this many shifts.
func (r Rules) LevelOf(shifts int64) Level {
	out := r.Levels[0]
	for _, l := range r.Levels {
		if shifts >= l.MinShifts {
			out = l
		}
	}
	return out
}

// Points is the work one shift adds at a productivity, worker-minutes.
func (r Rules) Points(productivityBPS int64) int64 {
	return r.ShiftMinutes * productivityBPS / BPS
}

// PoolSize is the NPC labour pool of a settlement: the housing capacity it
// has (base plus homes) minus the players living in it, times participation,
// rounded up so a settlement with any free home has someone to hire.
func (r Rules) PoolSize(housingCapacity, playerResidents int64) int64 {
	npc := r.BaseHousing + housingCapacity - playerResidents
	if npc <= 0 {
		return 0
	}
	return (npc*r.ParticipationBPS + BPS - 1) / BPS
}

// Tightness is demand over the labour force, in bps. An empty labour force
// with any demand is treated as the tightest point of the curve.
func (r Rules) Tightness(demand, labourForce int64) int64 {
	if demand <= 0 {
		return 0
	}
	if labourForce < 1 {
		return r.Curve[len(r.Curve)-1].TightnessBPS
	}
	return demand * BPS / labourForce
}

// WageMultiplierBPS reads the wage curve at a tightness, interpolating
// linearly between its points and holding the ends flat.
func (r Rules) WageMultiplierBPS(tightnessBPS int64) int64 {
	c := r.Curve
	if tightnessBPS <= c[0].TightnessBPS {
		return c[0].WageBPS
	}
	i := sort.Search(len(c), func(i int) bool { return c[i].TightnessBPS >= tightnessBPS })
	if i >= len(c) {
		return c[len(c)-1].WageBPS
	}
	lo, hi := c[i-1], c[i]
	return lo.WageBPS + (hi.WageBPS-lo.WageBPS)*(tightnessBPS-lo.TightnessBPS)/(hi.TightnessBPS-lo.TightnessBPS)
}

// NPCWage is what an NPC labourer asks for one shift in a settlement of this
// tier at this tightness: the base wage along the curve, never under the
// statutory minimum.
func (r Rules) NPCWage(tier string, tightnessBPS int64) int64 {
	w := r.BaseWage * r.WageMultiplierBPS(tightnessBPS) / BPS
	if floor := r.MinWage[tier]; w < floor {
		w = floor
	}
	return w
}

// Fee is the village's levy on a wage a citizen pays.
func (r Rules) Fee(wage int64) int64 { return wage * r.FeeBPS / BPS }

// ProgressBPS is done over required, clamped to 0..10 000.
func ProgressBPS(done, required int64) int64 {
	if required <= 0 {
		return BPS
	}
	if done <= 0 {
		return 0
	}
	if done >= required {
		return BPS
	}
	return done * BPS / required
}

// ShiftsNeeded is how many shifts of a given size finish the work left.
func ShiftsNeeded(remaining, pointsPerShift int64) int64 {
	if remaining <= 0 || pointsPerShift <= 0 {
		return 0
	}
	return (remaining + pointsPerShift - 1) / pointsPerShift
}

// Default is the rule set the shipped config carries.
func Default() Rules {
	return Rules{
		ShiftMinutes: 60, ReferenceCrew: 4, BaseWage: 30,
		MinWage:          map[string]int64{"village": 10, "town": 15, "city": 25},
		Curve:            []Point{{0, 7_000}, {5_000, 10_000}, {10_000, 15_000}, {20_000, 25_000}},
		ParticipationBPS: 6_000, BaseHousing: 8, NPCProductivityBPS: 8_500, FeeBPS: 500, BudgetSlackBPS: 5_000,
		Levels: []Level{{"apprentice", 0, 7_000}, {"journeyman", 6, 10_000}, {"master", 30, 13_000}},
	}
}
