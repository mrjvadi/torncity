// Package player holds the rules that govern a character: the statistics a
// player carries, how effort is paid for in energy, and how activity turns
// into levels.
//
// Nothing in this package reads or writes anything. Every rule is a pure
// function over value types, so it can be exercised in a test with no
// database, no broker and no clock running: the passage of time arrives as an
// argument. Storing a character is the outer layer's problem, and it is
// injected there as an interface that layer declares, never one declared here.
//
// Every method takes a value receiver and returns a NEW Stats. Nothing mutates
// in place, which means a rule cannot be half-applied: either the caller keeps
// the returned value or the change never happened. It also makes each rule
// trivially testable, because the input is still there to compare against.
//
// The field names mirror the player_stats and player_skills columns in
// docs/database.md deliberately. A storage row maps onto these values without
// a translation table, which is the cheapest way to stop the two drifting.
package player

import (
	"errors"
	"math"
	"time"
)

// Sentinel errors returned by the rules in this package. Callers compare with
// errors.Is; the outer layer is what turns one of these into a classified,
// player-facing failure.
var (
	// ErrNotEnoughEnergy means the character could not pay the full energy
	// cost of an action.
	//
	// The action is refused outright rather than run at reduced effect with
	// whatever energy was left. Silently succeeding for less is how a player
	// stops trusting what the game tells them: they asked for a thing, the
	// game said yes, and they got something else.
	ErrNotEnoughEnergy = errors.New("player: not enough energy")

	// ErrInvalidEnergyCost means a caller asked to spend a negative amount of
	// energy. Spending a negative cost would be a back door that grants
	// energy, so it is rejected instead of normalised away.
	ErrInvalidEnergyCost = errors.New("player: energy cost cannot be negative")
)

// Energy regeneration rate.
//
// Five energy every quarter of an hour refills a default hundred-point bar in
// five hours. That is slow enough that energy is a resource worth planning a
// session around, and fast enough that a player who looks in twice a day never
// finds an empty bar. The rate lives here, as one constant pair, so that
// tuning it is a one-line change rather than a hunt through handlers.
const (
	EnergyRegenAmount   = 5
	EnergyRegenInterval = 15 * time.Minute
)

// Starting values for a freshly created character, used by NewStats.
const (
	DefaultMaxHealth = 100
	DefaultMaxEnergy = 100
	DefaultHappiness = 100
	DefaultStamina   = 100
)

// MaxLevel is where the character level curve stops.
//
// A cap exists for two reasons beyond game design. It bounds the slice AddXP
// can return, so a single absurd XP award cannot allocate millions of level-up
// events, and it keeps XPForLevel far away from the edge of int64.
const MaxLevel = 100

// Stats is the mutable-by-the-minute part of a character, mirroring the
// player_stats row. It is a value: copying it is the intended way to use it.
type Stats struct {
	Level      int
	XP         int64
	Health     int
	MaxHealth  int
	Energy     int
	MaxEnergy  int
	Happiness  int
	Stamina    int
	Reputation int
}

// NewStats returns the statistics a character starts the game with: level one,
// no XP, and every bar full.
func NewStats() Stats {
	return Stats{
		Level:      1,
		XP:         0,
		Health:     DefaultMaxHealth,
		MaxHealth:  DefaultMaxHealth,
		Energy:     DefaultMaxEnergy,
		MaxEnergy:  DefaultMaxEnergy,
		Happiness:  DefaultHappiness,
		Stamina:    DefaultStamina,
		Reputation: 0,
	}
}

// LevelUp records one level boundary being crossed.
//
// AddXP returns these instead of applying rewards itself: what a level is
// worth (a bigger energy bar, an unlocked job, a notification) is a decision
// for the layer that owns those systems. This package knows only that the
// boundary was crossed and at what cost.
type LevelUp struct {
	// Level is the level now reached.
	Level int
	// Threshold is the XP total that unlocked it, straight from XPForLevel.
	Threshold int64
}

// XPForLevel returns the total XP a character must have accumulated to stand
// at level. It is the character curve, and it is the only place that curve
// exists.
//
// The curve is quadratic, XPForLevel(L) = 25*(L-1)*L, so stepping from L to
// L+1 costs 50*L: level two costs 50, level three another 100, level four
// another 150. Quadratic was chosen over exponential because each level should
// cost visibly more than the last without the top of the curve running away to
// a number no player will ever reach, and because it is exact in integers at
// every point, with no rounding to argue about.
func XPForLevel(level int) int64 {
	if level <= 1 {
		return 0
	}
	l := int64(level)
	return 25 * (l - 1) * l
}

// AddXP awards XP and reports every level boundary crossed on the way.
//
// Crossing several thresholds with one award yields several LevelUp values, in
// ascending order: a player who finishes a long mission and jumps three levels
// gets three events, not one, because each level may unlock something and the
// outer layer must see them all.
//
// XP never decreases. A negative or zero award is ignored rather than
// subtracted, because there is no rule in this game that takes XP away and a
// negative award reaching here is a bug in the caller, not an intent.
func (s Stats) AddXP(n int64) (Stats, []LevelUp) {
	if n <= 0 {
		return s, nil
	}

	next := s
	if next.XP > math.MaxInt64-n {
		// Saturate rather than wrap. Wrapping would hand a player a negative
		// XP total, which is a worse lie than an unreachable ceiling.
		next.XP = math.MaxInt64
	} else {
		next.XP += n
	}

	// A zero value is not a real character; treat it as level one rather than
	// announcing a level-up nobody earned.
	if next.Level < 1 {
		next.Level = 1
	}

	var ups []LevelUp
	for next.Level < MaxLevel && next.XP >= XPForLevel(next.Level+1) {
		next.Level++
		ups = append(ups, LevelUp{Level: next.Level, Threshold: XPForLevel(next.Level)})
	}
	return next, ups
}

// SpendEnergy pays cost energy for an action, or refuses.
//
// See ErrNotEnoughEnergy for why this refuses instead of clamping to zero.
func (s Stats) SpendEnergy(cost int) (Stats, error) {
	if cost < 0 {
		return s, ErrInvalidEnergyCost
	}
	if cost > s.Energy {
		return s, ErrNotEnoughEnergy
	}
	next := s
	next.Energy -= cost
	return next, nil
}

// RegenerateEnergy returns the character's stats after elapsed has passed.
//
// Only whole ticks count: energy arrives in EnergyRegenAmount steps every
// EnergyRegenInterval, and the remainder is not paid out early. The result is
// capped at MaxEnergy, and the arithmetic is done in int64 so that an elapsed
// time of hours or days — a player who was away all night — produces the same
// answer as the equivalent run of short intervals rather than overflowing an
// int.
//
// A negative elapsed returns the stats unchanged. Time going backwards means a
// clock correction, not a reason to take energy away.
//
// The caller that stores "energy last regenerated at" must advance that
// timestamp by EnergyRegenConsumed(elapsed), not by elapsed.
func (s Stats) RegenerateEnergy(elapsed time.Duration) Stats {
	if elapsed <= 0 || s.Energy >= s.MaxEnergy {
		return s
	}

	ticks := int64(elapsed / EnergyRegenInterval)
	if ticks <= 0 {
		return s
	}

	gained := ticks * EnergyRegenAmount
	missing := int64(s.MaxEnergy) - int64(s.Energy)
	if gained > missing {
		gained = missing
	}

	next := s
	next.Energy += int(gained)
	return next
}

// EnergyRegenConsumed reports how much of elapsed RegenerateEnergy actually
// used: whole ticks, nothing more.
//
// A caller that advances its stored "last regenerated at" by elapsed instead
// throws away the leftover part of every tick, and a player who opens the game
// often then regenerates measurably slower than one who leaves it alone. That
// is a real bug and this function exists to make it avoidable.
func EnergyRegenConsumed(elapsed time.Duration) time.Duration {
	if elapsed <= 0 {
		return 0
	}
	return (elapsed / EnergyRegenInterval) * EnergyRegenInterval
}

// TakeDamage removes health, floored at zero.
//
// A negative amount is ignored. Healing has its own rule with its own cap, and
// letting damage run backwards would be a way around it.
func (s Stats) TakeDamage(amount int) Stats {
	if amount <= 0 {
		return s
	}
	next := s
	if amount >= next.Health {
		next.Health = 0
		return next
	}
	next.Health -= amount
	return next
}

// Heal restores health, capped at MaxHealth.
//
// A negative amount is ignored, for the mirror image of the reason in
// TakeDamage. Health already above MaxHealth is left alone rather than pulled
// down: that is corrupt data, and quietly correcting it here would hide it.
func (s Stats) Heal(amount int) Stats {
	if amount <= 0 || s.Health >= s.MaxHealth {
		return s
	}
	next := s
	if amount >= next.MaxHealth-next.Health {
		next.Health = next.MaxHealth
		return next
	}
	next.Health += amount
	return next
}

// IsDead reports whether the character has run out of health. Hospitalisation,
// respawning and whatever else follows from it are rules other packages own.
func (s Stats) IsDead() bool { return s.Health <= 0 }
