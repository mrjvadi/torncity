package crime

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// Gear: what the tools a thief carries do to an attempt, and how long a crime
// must rest before the same player tries it again.
//
// Which item helps which crime, and by how much, is content (items.yml, each
// item's `gear:` block); this package takes the numbers only — never an item
// — and applies them by one rule. Gear is signed basis points: a lockpick set
// adds to the success chance, gloves take from the chance a later
// investigation solves the case, a mask from the chance of being seen.
// Stacked gear is summed and then held inside caps (config crime.gear_*), so
// no bag of tools makes a crime certain: the success chance is still clamped
// to ChanceCeilingBPS after the gear.

// ErrInvalidGearCaps means gear caps are unusable.
var ErrInvalidGearCaps = errors.New("crime: invalid gear caps")

// Gear is what carried tools add to one attempt, in signed basis points
// (Nerve in nerve points).
type Gear struct {
	// SuccessBPS is added to the success chance.
	SuccessBPS int
	// CatchBPS is added to the chance a failure ends in an arrest.
	CatchBPS int
	// WitnessBPS is added to the chance a success against a player is seen.
	WitnessBPS int
	// SolveBPS is added to the chance a report of this attempt is solved.
	SolveBPS int
	// RewardBPS scales the take: 1000 is ten percent more, never past the
	// crime's own caps.
	RewardBPS int
	// Nerve is added to the nerve an attempt costs, which never drops
	// below one.
	Nerve int
}

// Zero reports whether the gear changes nothing.
func (g Gear) Zero() bool { return g == Gear{} }

// Add sums two sets of gear, field by field.
func (g Gear) Add(o Gear) Gear {
	return Gear{
		SuccessBPS: g.SuccessBPS + o.SuccessBPS, CatchBPS: g.CatchBPS + o.CatchBPS,
		WitnessBPS: g.WitnessBPS + o.WitnessBPS, SolveBPS: g.SolveBPS + o.SolveBPS,
		RewardBPS: g.RewardBPS + o.RewardBPS, Nerve: g.Nerve + o.Nerve,
	}
}

// GearCaps bound what gear may do in total, by magnitude: a field of summed
// gear is held within [-cap, cap].
type GearCaps struct {
	SuccessBPS, CatchBPS, WitnessBPS, SolveBPS, RewardBPS, Nerve int
}

// MaxGearBPS bounds any cap: gear can at most move a chance by all of it.
const MaxGearBPS = BPSWhole

// Validate reports whether the caps are usable.
func (c GearCaps) Validate() error {
	for name, v := range map[string]int{
		"success": c.SuccessBPS, "catch": c.CatchBPS, "witness": c.WitnessBPS,
		"solve": c.SolveBPS, "reward": c.RewardBPS,
	} {
		if v < 0 || v > MaxGearBPS {
			return fmt.Errorf("%w: %s cap %d is outside 0..%d", ErrInvalidGearCaps, name, v, MaxGearBPS)
		}
	}
	if c.Nerve < 0 || c.Nerve > MaxNerveCost {
		return fmt.Errorf("%w: nerve cap %d is outside 0..%d", ErrInvalidGearCaps, c.Nerve, MaxNerveCost)
	}
	return nil
}

func within(v, bound int) int {
	return min(max(v, -bound), bound)
}

// Combine sums pieces of gear and holds the total inside the caps.
func Combine(caps GearCaps, pieces ...Gear) Gear {
	var sum Gear
	for _, p := range pieces {
		sum = sum.Add(p)
	}
	return Gear{
		SuccessBPS: within(sum.SuccessBPS, caps.SuccessBPS),
		CatchBPS:   within(sum.CatchBPS, caps.CatchBPS),
		WitnessBPS: within(sum.WitnessBPS, caps.WitnessBPS),
		SolveBPS:   within(sum.SolveBPS, caps.SolveBPS),
		RewardBPS:  within(sum.RewardBPS, caps.RewardBPS),
		Nerve:      within(sum.Nerve, caps.Nerve),
	}
}

// NerveCost is what an attempt costs with gear: the crime's own cost moved
// by the gear, never below one.
func (g Gear) NerveCost(base int) int { return max(base+g.Nerve, 1) }

// bps holds a chance moved by gear inside 0..10000.
func bps(base, delta int) int { return min(max(base+delta, 0), BPSWhole) }

// CooldownLeft is how long a player must still wait before trying a crime
// again: the crime's own cooldown and its category's, whichever ends later,
// each counted from that player's last attempt of it (zero when none),
// waited in real time through the game clock. Zero means it may be tried.
func CooldownLeft(lastCrime time.Time, crimeCooldown time.Duration, lastInCategory time.Time,
	categoryCooldown time.Duration, scale gametime.Scale, now time.Time,
) time.Duration {
	left := time.Duration(0)
	for _, w := range []struct {
		last time.Time
		cd   time.Duration
	}{{lastCrime, crimeCooldown}, {lastInCategory, categoryCooldown}} {
		if w.last.IsZero() || w.cd <= 0 {
			continue
		}
		if l := w.last.Add(scale.RealWait(w.cd)).Sub(now); l > left {
			left = l
		}
	}
	return left
}
