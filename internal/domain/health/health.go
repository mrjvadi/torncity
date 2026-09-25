// Package health holds the rules of a player's health: how an injury lowers
// it, when it puts a player in hospital and for how long, how a stay and rest
// give it back on the game clock, what a treatment takes off a stay, and what
// the city hospital charges for one.
//
// Nobody dies. Health never falls below the floor (at least 1): an injury
// that would take more only takes the player to the floor, and a player at or
// below the hospital line is admitted instead. Every duration a rule is given
// or returns is GAME time unless it says otherwise; the caller turns it into
// a real wait through gametime.Scale.RealWait, the one conversion
// (docs/adr/0018-game-clock.md).
//
// Nothing here reads or writes anything: every rule is a pure function, and
// every chance is decided by a die the caller seeds (Roll), so a replay of the
// same event decides the same way.
package health

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// WholeBPS is one hundred percent in basis points.
const WholeBPS = 10_000

// Rules are a world's health rules (configs/content/health.yml).
type Rules struct {
	// Floor is the least health a player ever has: at least 1.
	Floor int
	// HospitalBelow is the line: a player whose health an injury leaves at
	// or below it is admitted to hospital.
	HospitalBelow int
	// DischargeAt is the health a patient leaves with (capped by their
	// maximum).
	DischargeAt int
	// RecoveryPerHour is the health a patient recovers per GAME hour in
	// hospital: what decides how long a stay is.
	RecoveryPerHour int
	// RestPerHour is the health a player out of hospital recovers per GAME
	// hour. Zero: none, only medicine and hospitals heal.
	RestPerHour int
	// MinStay and MaxStay bound a stay, GAME time.
	MinStay, MaxStay time.Duration
}

// Validation failures.
var (
	ErrInvalidRules  = errors.New("health: invalid rules")
	ErrInvalidInjury = errors.New("health: invalid injury")
	ErrInvalidCare   = errors.New("health: invalid care")
)

// Validate reports whether the rules can be played.
func (r Rules) Validate() error {
	switch {
	case r.Floor < 1:
		return fmt.Errorf("%w: floor %d is below 1", ErrInvalidRules, r.Floor)
	case r.HospitalBelow < r.Floor:
		return fmt.Errorf("%w: hospital line %d is below the floor %d", ErrInvalidRules, r.HospitalBelow, r.Floor)
	case r.DischargeAt <= r.HospitalBelow:
		return fmt.Errorf("%w: discharge health %d is not above the hospital line %d", ErrInvalidRules, r.DischargeAt, r.HospitalBelow)
	case r.RecoveryPerHour < 1:
		return fmt.Errorf("%w: recovery per hour %d is below 1", ErrInvalidRules, r.RecoveryPerHour)
	case r.RestPerHour < 0:
		return fmt.Errorf("%w: rest per hour %d is negative", ErrInvalidRules, r.RestPerHour)
	case r.MinStay <= 0 || r.MaxStay < r.MinStay:
		return fmt.Errorf("%w: stay bounds %s..%s", ErrInvalidRules, r.MinStay, r.MaxStay)
	}
	return nil
}

// Hurt applies an injury of damage to health: the new health, floored, and
// whether it puts the player in hospital. A damage of zero or less changes
// nothing and admits nobody.
func (r Rules) Hurt(health, damage int) (int, bool) {
	if damage <= 0 {
		return health, false
	}
	next := health - damage
	if next < r.Floor || next > health {
		next = r.Floor
	}
	return next, next <= r.HospitalBelow
}

// Discharge is the health a patient with maximum top leaves hospital with:
// DischargeAt, never above their maximum, never below the floor.
func (r Rules) Discharge(top int) int {
	out := min(r.DischargeAt, top)
	return max(out, r.Floor)
}

// Stay is how long a patient admitted at health, with maximum top, stays:
// the GAME time recovery takes them to their discharge health, within
// MinStay..MaxStay.
func (r Rules) Stay(health, top int) time.Duration {
	missing := r.Discharge(top) - health
	var d time.Duration
	if missing > 0 && r.RecoveryPerHour > 0 {
		// Whole minutes, rounded up: a stay is never shorter than the
		// recovery it stands for.
		minutes := (int64(missing)*60 + int64(r.RecoveryPerHour) - 1) / int64(r.RecoveryPerHour)
		d = time.Duration(minutes) * time.Minute
	}
	if d < r.MinStay {
		d = r.MinStay
	}
	if d > r.MaxStay {
		d = r.MaxStay
	}
	return d
}

// Recovering is a patient's health at now during a stay that began at
// admitted with health in and ends at ends with health out: it climbs
// evenly, on the wall clock of the stay. Before admission it is in, after
// the end it is out.
func Recovering(in, out int, admitted, ends, now time.Time) int {
	if out <= in || !now.After(admitted) {
		return in
	}
	if !now.Before(ends) {
		return out
	}
	total := ends.Sub(admitted)
	gone := now.Sub(admitted)
	return in + int(int64(out-in)*int64(gone)/int64(total))
}

// Rest is health recovered at rest from since to now: RestPerHour for every
// whole GAME second's share, capped at top. It returns the new health and
// the instant the recovery is now counted from — advanced only by the time
// actually turned into health, so a player looking often loses nothing.
// Health at or above top, or no rest rate, recovers nothing and moves the
// instant to now.
func (r Rules) Rest(health, top int, since, now time.Time, scale gametime.Scale) (int, time.Time) {
	if r.RestPerHour <= 0 || health >= top || !now.After(since) || scale <= 0 {
		if now.After(since) {
			return health, now
		}
		return health, since
	}
	// Game time passed: whole real seconds times the scale. A year of real
	// time at the largest scale still fits an int64 many times over.
	gameSecs := int64(now.Sub(since)/time.Second) * int64(scale)
	gained := gameSecs * int64(r.RestPerHour) / 3600
	if gained <= 0 {
		return health, since
	}
	if gained >= int64(top-health) {
		return top, now
	}
	// The game seconds that made the health gained, back on the wall clock.
	usedGame := time.Duration((gained*3600+int64(r.RestPerHour)-1)/int64(r.RestPerHour)) * time.Second
	return health + int(gained), since.Add(scale.RealWait(usedGame))
}

// Injury is what can hurt a player in one kind of event: the chance it
// does, and how much health it takes, drawn evenly from Min..Max.
type Injury struct {
	ChanceBPS int
	Min, Max  int
}

// Validate reports whether the injury can be rolled.
func (i Injury) Validate() error {
	switch {
	case i.ChanceBPS < 0 || i.ChanceBPS > WholeBPS:
		return fmt.Errorf("%w: chance %d bps is outside 0..10000", ErrInvalidInjury, i.ChanceBPS)
	case i.ChanceBPS > 0 && (i.Min < 1 || i.Max < i.Min):
		return fmt.Errorf("%w: damage %d..%d", ErrInvalidInjury, i.Min, i.Max)
	}
	return nil
}

// Zero reports whether the injury can never happen.
func (i Injury) Zero() bool { return i.ChanceBPS <= 0 || i.Max <= 0 }

// Roll decides whether the injury happens and how much health it takes,
// from the seed and the coordinates: the same arguments always decide the
// same way.
func (i Injury) Roll(seed int64, coords ...int64) (int, bool) {
	if i.Zero() {
		return 0, false
	}
	if Die(seed, append([]int64{1}, coords...)...) >= int64(i.ChanceBPS) {
		return 0, false
	}
	span := int64(i.Max - i.Min + 1)
	return i.Min + int(Die(seed, append([]int64{2}, coords...)...)%span), true
}

// Care is how well a hospital treats: what share of a stay's remaining time
// a treatment takes off, and how much the treating doctor's skill adds.
type Care struct {
	// ReductionBPS is the share taken off with no skill.
	ReductionBPS int
	// BPSPerLevel is added per level of the doctor's skill, up to
	// MaxSkillBPS in all.
	BPSPerLevel int
	MaxSkillBPS int
}

// Validate reports whether the care is usable.
func (c Care) Validate() error {
	switch {
	case c.ReductionBPS < 0 || c.ReductionBPS > WholeBPS:
		return fmt.Errorf("%w: reduction %d bps", ErrInvalidCare, c.ReductionBPS)
	case c.BPSPerLevel < 0 || c.MaxSkillBPS < 0 || c.ReductionBPS+c.MaxSkillBPS > WholeBPS:
		return fmt.Errorf("%w: skill %d bps a level up to %d, on top of %d", ErrInvalidCare, c.BPSPerLevel, c.MaxSkillBPS, c.ReductionBPS)
	}
	return nil
}

// Reduction is the share of the remaining stay a treatment by a doctor of
// level takes off, in basis points.
func (c Care) Reduction(level int) int {
	bonus := 0
	if level > 0 && c.BPSPerLevel > 0 {
		bonus = min(level*c.BPSPerLevel, c.MaxSkillBPS)
	}
	return min(c.ReductionBPS+bonus, WholeBPS)
}

// Shorten is what remains of a stay after a treatment taking reductionBPS
// off it: whole seconds, rounded up, never negative.
func Shorten(remaining time.Duration, reductionBPS int) time.Duration {
	if remaining <= 0 {
		return 0
	}
	reductionBPS = min(max(reductionBPS, 0), WholeBPS)
	secs := int64((remaining + time.Second - 1) / time.Second)
	left := (secs*int64(WholeBPS-reductionBPS) + WholeBPS - 1) / WholeBPS
	return time.Duration(left) * time.Second
}

// CityPrice is what the city hospital charges to treat a patient whose stay
// has remaining GAME time left: a base, and a rate for every GAME hour or
// part of one.
func CityPrice(base, perHour int64, remaining time.Duration) int64 {
	if remaining <= 0 {
		return max(base, 0)
	}
	hours := int64((remaining + time.Hour - 1) / time.Hour)
	return max(base, 0) + max(perHour, 0)*hours
}

// Die is a die in 0..9999 for the seed and the coordinates (SplitMix64).
func Die(seed int64, coords ...int64) int64 {
	return int64(Mix(seed, coords...) % WholeBPS)
}

// Mix is 64 well-mixed bits for the seed and the coordinates: the same
// arguments always give the same bits.
func Mix(seed int64, coords ...int64) uint64 {
	h := splitmix64(uint64(seed))
	for _, c := range coords {
		h = splitmix64(h ^ uint64(c))
	}
	return h
}

// Seed turns an identifier into a die seed (FNV-1a), so an event's own id
// decides its dice.
func Seed(id string) int64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return int64(h)
}

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
