// Package gametime is the game's one clock: how a duration written in GAME
// time — a course of 24h, a shift of 8h, a bus ride of 3h — becomes the REAL
// wait a player experiences.
//
// WHY ONE CLOCK. Content speaks in game time because that is how a world is
// authored: a working day, a degree, a flight. Players live in real time. If
// each system scaled its own durations — or forgot to — a "24h" course would
// take a real day while a "3h" bus ride took three minutes, and every screen
// would mix the two. So there is exactly one mapping, Scale.RealWait, and
// every gameplay duration from content passes through it before it meets the
// wall clock: a scheduled finish, a remaining time, a time-in-tier bar, a
// fatigue window. What a player is SHOWN is always the real wait.
//
// WHAT IS NOT GAME TIME (docs/adr/0018-game-clock.md). Governance guarantees
// (a policy's notice and cooldown, an office term), energy regeneration, a
// transport mode's demand window and every operational timeout are real
// time and never pass through here: they protect players and operators from
// surprises measured on their own clocks.
//
// The package is pure: it reads no clock and no configuration. The scale is
// tuning (config game.time_scale) handed in by the layer above.
package gametime

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidScale means a time scale is below one or above MaxScale.
var ErrInvalidScale = errors.New("gametime: invalid time scale")

// MaxScale caps the scale at one game day per real second. Beyond it every
// duration the content can author collapses into the one-second floor.
const MaxScale = 86_400

// Scale is how many seconds of game time pass in one real second. At 60 a
// game hour is a real minute. 1 means game time IS real time.
type Scale int

// Validate reports whether the scale is usable.
func (s Scale) Validate() error {
	if s < 1 || s > MaxScale {
		return fmt.Errorf("%w: %d is outside 1..%d", ErrInvalidScale, int(s), MaxScale)
	}
	return nil
}

// RealWait maps a game-time duration to the real wait:
//
//	wait = ceil(game / scale), rounded UP to the whole second, at least 1s
//
// Rounded up so a countdown never promises an end that lands after it, and
// never zero for a positive duration, because an activity that ends the
// instant it starts is not an activity. A zero or negative game duration is
// "none" and maps to zero: a promotion bar of no time, a disabled window.
//
// An invalid scale is treated as 1 (game time unchanged) rather than
// panicking; callers validate the scale where it enters (Validate), and the
// conservative reading of a broken scale is the longer wait.
func (s Scale) RealWait(game time.Duration) time.Duration {
	if game <= 0 {
		return 0
	}
	if s.Validate() != nil {
		s = 1
	}
	scale := time.Duration(s)
	wait := game / scale
	if game%scale != 0 {
		wait++
	}
	if rem := wait % time.Second; rem != 0 {
		wait += time.Second - rem
	}
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}

// Clock reads the game's time of day from the real clock (docs/adr/0018): game
// time runs Scale times faster than real time from Epoch, the real instant at
// which game day 0 began at 00:00. It exists so a thing that happens "once per
// game day at 06:00" (a shop's morning delivery, ADR 0046) has one definition
// of what day it is, shared by every replica.
//
// Changing the scale or the epoch renumbers the days: the keys written under
// the old numbering stay unique but no longer mean what they did, so both are
// changed together with the data that keys on them or not at all.
type Clock struct {
	Epoch time.Time
	Scale Scale
}

// Day is the length of a game day.
const Day = 24 * time.Hour

// Validate reports whether the clock is usable.
func (c Clock) Validate() error {
	if c.Epoch.IsZero() {
		return errors.New("gametime: the clock has no epoch")
	}
	return c.Scale.Validate()
}

// Since is the game time elapsed at the real instant now. Before the epoch it
// is zero.
func (c Clock) Since(now time.Time) time.Duration {
	if !now.After(c.Epoch) {
		return 0
	}
	s := c.Scale
	if s.Validate() != nil {
		s = 1
	}
	return now.Sub(c.Epoch) * time.Duration(s)
}

// DayAt is the number of the game day at now, counting from the epoch's
// midnight: 0 on the first game day.
func (c Clock) DayAt(now time.Time) int64 { return int64(c.Since(now) / Day) }

// DayAtHour is the number of the last game "day" whose boundary at `hour`
// o'clock game time has passed: the day counter that ticks at that hour
// instead of at midnight. At 05:59 game time it is still yesterday's number;
// at 06:00 (hour 6) it is today's. -1 means the first boundary has not come.
func (c Clock) DayAtHour(now time.Time, hour int) int64 {
	g := c.Since(now) - time.Duration(hour)*time.Hour
	if g < 0 {
		return -1
	}
	return int64(g / Day)
}

// RealAtHour is the real instant at which the boundary of game day n at `hour`
// o'clock game time falls.
func (c Clock) RealAtHour(day int64, hour int) time.Time {
	s := c.Scale
	if s.Validate() != nil {
		s = 1
	}
	game := time.Duration(day)*Day + time.Duration(hour)*time.Hour
	return c.Epoch.Add(game / time.Duration(s))
}
