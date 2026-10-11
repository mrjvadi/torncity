// Package household is the rules of the NPC households (docs/adr/0073): a household is a small group that lives in one home of a
// settlement, eats, drinks, keeps warm, works a seat of the labour pool, pays for what it consumes, and stays or leaves by how
// satisfied it is. Everything here is a pure function of its arguments and a seed, so two replicas that settle the same day reach
// the same answer. The application layer (handlers/village_households.go) reads the settlement, applies the plan and writes the
// ledger and the item journal.
package household

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// BPS is 100 percent in basis points.
const BPS = 10_000

// The needs of a household, in the order of the weights.
const (
	NeedFood    = "food"
	NeedWater   = "water"
	NeedWarmth  = "warmth"
	NeedShelter = "shelter"
	NeedWork    = "work"
	NeedCare    = "care"
)

// Needs lists the needs in a fixed order.
var Needs = []string{NeedFood, NeedWater, NeedWarmth, NeedShelter, NeedWork, NeedCare}

// ErrInvalidRules is returned by Rules.Validate.
var ErrInvalidRules = errors.New("household: invalid rules")

// Weights are the shares of the needs in the day's score; they add up to BPS.
type Weights struct{ Food, Water, Warmth, Shelter, Work, Care int64 }

// Sum is the total of the weights.
func (w Weights) Sum() int64 { return w.Food + w.Water + w.Warmth + w.Shelter + w.Work + w.Care }

// Composition is one kind of household an arrival may be, with its weight among the kinds.
type Composition struct {
	Adults, Dependants int64
	Weight             int64
}

// Rules are the tuning of the households (config households.*); the zero value is invalid.
type Rules struct {
	Weights Weights
	// SatisfactionAlphaBPS is the share of a day's score that enters the moving average; the rest is the old value.
	SatisfactionAlphaBPS int64
	// LeaveBelowBPS and LeaveAfterDays: satisfaction under the first for that many days in a row makes a household leave.
	LeaveBelowBPS  int64
	LeaveAfterDays int64
	// ArriveAboveBPS: the average satisfaction of the living households that lets more arrive (also when none live yet).
	ArriveAboveBPS int64
	// VacancyMarginBPS keeps this share of the home slots free before anyone arrives.
	VacancyMarginBPS int64
	// ArrivalsPerDay is the most households that arrive in one settlement-day.
	ArrivalsPerDay int64
	// BirthAboveBPS and BirthChanceBPS: a satisfied household with a free home slot has a dependant with this chance a day.
	BirthAboveBPS  int64
	BirthChanceBPS int64
	// EarnerBPS is the mean number of earners a household has, in basis points of one earner: the mean over many households.
	EarnerBPS int64
	// DependantWeightBPS is what a dependant counts as against an adult in what the household eats and drinks.
	DependantWeightBPS int64
	// DependantToEarnerDays is how long a dependant takes to grow into an adult of the household.
	DependantToEarnerDays int64
	// Compositions are the kinds of household an arrival may be.
	Compositions []Composition
}

// Validate reports a rule set that cannot run.
func (r Rules) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidRules, fmt.Sprintf(format, a...))
	}
	if r.Weights.Sum() != BPS {
		return fail("the weights add up to %d, not %d", r.Weights.Sum(), BPS)
	}
	for _, v := range []int64{r.Weights.Food, r.Weights.Water, r.Weights.Warmth, r.Weights.Shelter, r.Weights.Work, r.Weights.Care} {
		if v < 0 {
			return fail("a weight is negative")
		}
	}
	for name, v := range map[string]int64{"satisfaction_alpha_bps": r.SatisfactionAlphaBPS, "leave_below_bps": r.LeaveBelowBPS,
		"arrive_above_bps": r.ArriveAboveBPS, "vacancy_margin_bps": r.VacancyMarginBPS, "birth_above_bps": r.BirthAboveBPS,
		"birth_chance_bps": r.BirthChanceBPS, "earner_bps": r.EarnerBPS, "dependant_weight_bps": r.DependantWeightBPS} {
		if v < 0 || v > BPS {
			return fail("%s is %d", name, v)
		}
	}
	if r.SatisfactionAlphaBPS < 1 || r.LeaveAfterDays < 1 || r.ArrivalsPerDay < 1 || r.DependantToEarnerDays < 1 {
		return fail("alpha, leave days, arrivals a day and growth days must be at least 1")
	}
	if len(r.Compositions) == 0 {
		return fail("no household compositions")
	}
	for _, c := range r.Compositions {
		if c.Adults < 1 || c.Dependants < 0 || c.Weight < 1 {
			return fail("composition %+v", c)
		}
	}
	return nil
}

// Hash is the seed of a decision: a hash of the parts, the same on every replica.
func Hash(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8])
}

// Draw is a number in [0, n) from a seed (0 when n is not positive).
func Draw(seed uint64, n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(seed % uint64(n))
}

// Shape is the people of a new household.
type Shape struct {
	Adults, Dependants, Earners int64
}

// Compose draws the people of a household from a seed: a composition by weight, then the earners. Each adult works with the
// probability that makes the mean number of earners over many households EarnerBPS / BPS (capped at the adults).
func (r Rules) Compose(seed uint64) Shape {
	var total, adultsWeighted int64
	for _, c := range r.Compositions {
		total += c.Weight
		adultsWeighted += c.Adults * c.Weight
	}
	pick := Draw(seed, total)
	c := r.Compositions[len(r.Compositions)-1]
	for _, k := range r.Compositions {
		if pick < k.Weight {
			c = k
			break
		}
		pick -= k.Weight
	}
	p := r.EarnerBPS * total / adultsWeighted // the chance an adult works, in basis points
	earners := int64(0)
	for i := int64(0); i < c.Adults; i++ {
		if Draw(Hash(fmt.Sprint(seed), "earner", fmt.Sprint(i)), BPS) < p {
			earners++
		}
	}
	return Shape{Adults: c.Adults, Dependants: c.Dependants, Earners: min(earners, c.Adults)}
}

// MaxDependants is the most children a household keeps: one more than the largest family an arrival may be.
func (r Rules) MaxDependants() int64 {
	var m int64
	for _, c := range r.Compositions {
		m = max(m, c.Dependants)
	}
	return m + 1
}

// PersonsBPS is how many adult-equivalents a household is, in basis points (an adult is BPS).
func (r Rules) PersonsBPS(adults, dependants int64) int64 {
	return adults*BPS + dependants*r.DependantWeightBPS
}

// Quantity is the units a household needs of a good in a day: persons x milli-units a person, rounded up, at least one unit for
// any positive rate.
func Quantity(personsBPS, milliPerPerson int64) int64 {
	if personsBPS <= 0 || milliPerPerson <= 0 {
		return 0
	}
	num := personsBPS * milliPerPerson
	den := int64(BPS) * 1000
	return (num + den - 1) / den
}

// Met is a need met, in basis points of what was needed (BPS: fully).
type Met struct{ Food, Water, Warmth, Shelter, Work, Care int64 }

// Ratio is have/need in basis points, capped at BPS; nothing needed is fully met.
func Ratio(have, need int64) int64 {
	if need <= 0 {
		return BPS
	}
	if have <= 0 {
		return 0
	}
	return min(have*BPS/need, BPS)
}

// Score is the weighted mean of the met needs, in basis points.
func (w Weights) Score(m Met) int64 {
	return (w.Food*m.Food + w.Water*m.Water + w.Warmth*m.Warmth + w.Shelter*m.Shelter + w.Work*m.Work + w.Care*m.Care) / BPS
}

// Worst is the need that was met least (the first on a tie, in the order of Needs); empty when all are met.
func (m Met) Worst() string {
	vals := []int64{m.Food, m.Water, m.Warmth, m.Shelter, m.Work, m.Care}
	worst, at := int64(BPS), -1
	for i, v := range vals {
		if v < worst {
			worst, at = v, i
		}
	}
	if at < 0 {
		return ""
	}
	return Needs[at]
}

// State is a household's mood.
type State struct {
	SatisfactionBPS int64
	LowDays         int64
}

// Next folds one day's score into the mood: a moving average, and the count of days in a row under LeaveBelowBPS. leave is true
// when the household has been low for LeaveAfterDays days.
func (r Rules) Next(s State, score int64) (next State, leave bool) {
	sat := (s.SatisfactionBPS*(BPS-r.SatisfactionAlphaBPS) + score*r.SatisfactionAlphaBPS) / BPS
	sat = max(0, min(sat, BPS))
	low := int64(0)
	if sat < r.LeaveBelowBPS {
		low = s.LowDays + 1
	}
	return State{SatisfactionBPS: sat, LowDays: low}, low >= r.LeaveAfterDays
}

// Mood is the average satisfaction of a settlement's living households in basis points (none: BPS, a settlement with nobody is
// attractive to the first arrivals).
func Mood(sats []int64) int64 {
	if len(sats) == 0 {
		return BPS
	}
	var sum int64
	for _, s := range sats {
		sum += s
	}
	return sum / int64(len(sats))
}

// Arrivals is how many households arrive in a settlement-day: none when the settlement is closed to them or the mood is too low;
// otherwise as many as the free slots beyond the vacancy margin allow, up to ArrivalsPerDay. With households already living and no
// open post there is no pull: nobody arrives.
func (r Rules) Arrivals(slots, living, mood int64, admitted bool, openPosts int64) int64 {
	if !admitted || mood < r.ArriveAboveBPS {
		return 0
	}
	margin := slots * r.VacancyMarginBPS / BPS
	free := slots - living - margin
	if free <= 0 {
		return 0
	}
	if openPosts <= 0 && living > 0 {
		return 0
	}
	return min(free, r.ArrivalsPerDay)
}

// Born reports whether a satisfied household with room has a dependant today.
func (r Rules) Born(s State, freeSlots int64, seed uint64) bool {
	return freeSlots > 0 && s.SatisfactionBPS >= r.BirthAboveBPS && Draw(seed, BPS) < r.BirthChanceBPS
}

// Earners is a household and the people of it who work.
type Earners struct {
	ID      string
	Earners int64
}

// SeatOwner assigns a claim on the pool of earners: the household whose earners cover the claim's place in the cumulative order,
// picked by a hash of the claim's key, so every replica gives the same household and a household with more earners gets
// proportionally more claims. The order is fixed by id. It returns "" when nobody works.
func SeatOwner(key string, earners []Earners) string {
	list := append([]Earners(nil), earners...)
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	var total int64
	for _, e := range list {
		total += e.Earners
	}
	if total <= 0 {
		return ""
	}
	pick := Draw(Hash(key), total)
	for _, e := range list {
		if pick < e.Earners {
			return e.ID
		}
		pick -= e.Earners
	}
	return ""
}

// Capped is the part of an inflow a balance under a cap can take (all of it with no cap).
func Capped(balance, inflow, cap int64) int64 {
	if cap <= 0 {
		return inflow
	}
	return max(0, min(inflow, cap-balance))
}
