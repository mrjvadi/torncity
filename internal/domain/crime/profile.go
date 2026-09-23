package crime

import (
	"errors"
	"fmt"
	"time"
)

// Nerve and heat: the two quantities that move with time.
//
// NERVE is what an attempt costs: the resolve to go through with it. It
// regenerates like energy, a fixed amount per fixed interval up to a maximum,
// and it is its own resource rather than energy because committing a crime
// must not compete with working a shift — a player who worked all day still
// has the nerve for a pickpocket, and a thief who spent the night on
// burglaries can still go to work. It is also the crime engine's rate limit:
// a thief who has spent it waits.
//
// HEAT is how much the police are looking for someone: every crime adds some,
// an arrest adds more, and it cools off by a fixed amount per hour. Heat
// lowers the odds of the next attempt and raises the odds a report against
// the thief is solved. Its bands are the public WANTED level.
//
// Both regenerate in REAL time, like energy: they are a player's pacing, not
// a duration in the story (docs/adr/0018-game-clock.md).

// Errors of the two resources.
var (
	// ErrInvalidRules means nerve or heat rules are unusable.
	ErrInvalidRules = errors.New("crime: invalid rules")
	// ErrNotEnoughNerve means an attempt costs more nerve than the player
	// has. The detail is a NerveShortfall.
	ErrNotEnoughNerve = errors.New("crime: not enough nerve")
)

// NerveShortfall details ErrNotEnoughNerve.
type NerveShortfall struct{ Need, Have int }

func (e NerveShortfall) Error() string {
	return fmt.Sprintf("%v: need %d, have %d", ErrNotEnoughNerve, e.Need, e.Have)
}

// Unwrap lets errors.Is match ErrNotEnoughNerve.
func (e NerveShortfall) Unwrap() error { return ErrNotEnoughNerve }

// NerveRules is the tuning of nerve (config crime.nerve_*).
type NerveRules struct {
	Max           int
	RegenAmount   int
	RegenInterval time.Duration
}

// Validate reports whether the rules are usable.
func (r NerveRules) Validate() error {
	if r.Max < 1 || r.Max > MaxNerveCost*100 {
		return fmt.Errorf("%w: nerve max %d", ErrInvalidRules, r.Max)
	}
	if r.RegenAmount < 1 || r.RegenInterval <= 0 {
		return fmt.Errorf("%w: nerve regenerates %d per %s", ErrInvalidRules, r.RegenAmount, r.RegenInterval)
	}
	return nil
}

// Nerve is a player's nerve and the instant it was last brought up to date.
type Nerve struct {
	Current   int
	UpdatedAt time.Time
}

// Regenerate brings n up to now.
//
// Only whole intervals count, and the timestamp advances by exactly the
// intervals paid out, so the part of an interval already waited is never
// lost to a screen being opened. A full bar is stamped now: the wait for the
// next point starts when a point is spent, not when the bar last changed. A
// zero timestamp is stamped now, and a clock that stepped backwards pays
// nothing.
func (r NerveRules) Regenerate(n Nerve, now time.Time) Nerve {
	if n.UpdatedAt.IsZero() {
		return Nerve{Current: min(max(n.Current, 0), r.Max), UpdatedAt: now}
	}
	if n.Current >= r.Max {
		return Nerve{Current: r.Max, UpdatedAt: now}
	}
	elapsed := now.Sub(n.UpdatedAt)
	if elapsed <= 0 || r.RegenInterval <= 0 {
		return n
	}
	ticks := int64(elapsed / r.RegenInterval)
	gained := ticks * int64(r.RegenAmount)
	missing := int64(r.Max - max(n.Current, 0))
	if gained >= missing {
		return Nerve{Current: r.Max, UpdatedAt: now}
	}
	return Nerve{
		Current:   n.Current + int(gained),
		UpdatedAt: n.UpdatedAt.Add(time.Duration(ticks) * r.RegenInterval),
	}
}

// Spend pays cost from n, which must already be regenerated to the moment of
// spending, or refuses with a NerveShortfall. It never goes below zero.
func (r NerveRules) Spend(n Nerve, cost int) (Nerve, error) {
	if cost < 0 {
		return n, fmt.Errorf("%w: negative cost %d", ErrInvalidRules, cost)
	}
	if cost > n.Current {
		return n, NerveShortfall{Need: cost, Have: n.Current}
	}
	return Nerve{Current: n.Current - cost, UpdatedAt: n.UpdatedAt}, nil
}

// FullIn is how long until a regenerated n is full again, zero when it is.
func (r NerveRules) FullIn(n Nerve, now time.Time) time.Duration {
	missing := r.Max - n.Current
	if missing <= 0 || r.RegenAmount <= 0 {
		return 0
	}
	ticks := (missing + r.RegenAmount - 1) / r.RegenAmount
	left := time.Duration(ticks)*r.RegenInterval - now.Sub(n.UpdatedAt)
	return max(left, 0)
}

// HeatRules is the tuning of heat (config crime.heat_*).
type HeatRules struct {
	Max int
	// DecayPerHour is how much heat cools off each real hour.
	DecayPerHour int
}

// Validate reports whether the rules are usable.
func (r HeatRules) Validate() error {
	if r.Max < 1 || r.Max > 10_000 {
		return fmt.Errorf("%w: heat max %d", ErrInvalidRules, r.Max)
	}
	if r.DecayPerHour < 1 {
		return fmt.Errorf("%w: heat decays %d per hour", ErrInvalidRules, r.DecayPerHour)
	}
	return nil
}

// Heat is a player's heat and when it was last brought up to date.
type Heat struct {
	Level     int
	UpdatedAt time.Time
}

// Cool brings h up to now: DecayPerHour off per whole hour, never below
// zero, the timestamp advanced by the hours consumed. Cold heat is stamped
// now, so the next crime's heat starts cooling from when it is added.
func (r HeatRules) Cool(h Heat, now time.Time) Heat {
	if h.UpdatedAt.IsZero() || h.Level <= 0 {
		return Heat{Level: 0, UpdatedAt: now}
	}
	elapsed := now.Sub(h.UpdatedAt)
	if elapsed <= 0 {
		return h
	}
	hours := int64(elapsed / time.Hour)
	cooled := hours * int64(r.DecayPerHour)
	if cooled >= int64(h.Level) {
		return Heat{Level: 0, UpdatedAt: now}
	}
	return Heat{Level: h.Level - int(cooled), UpdatedAt: h.UpdatedAt.Add(time.Duration(hours) * time.Hour)}
}

// Add raises a cooled h by n, capped at Max.
func (r HeatRules) Add(h Heat, n int) Heat {
	if n <= 0 {
		return h
	}
	return Heat{Level: min(h.Level+n, r.Max), UpdatedAt: h.UpdatedAt}
}

// WantedStars is the number of wanted stars shown for a heat level.
const WantedStars = 5

// WantedLevel is the public band of a heat level: 0 for none, then 1 to
// WantedStars, rounding up so any heat at all shows at least one star.
func (r HeatRules) WantedLevel(heat int) int {
	if heat <= 0 || r.Max <= 0 {
		return 0
	}
	stars := (min(heat, r.Max)*WantedStars + r.Max - 1) / r.Max
	return min(stars, WantedStars)
}
