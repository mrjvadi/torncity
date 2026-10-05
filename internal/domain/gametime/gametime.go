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

// Clock reads the game's days and hours from the real clock (docs/adr/0018). Game
// time is real time, on UTC; what a settlement calls "today" is the day of its own
// local time zone (an offset from UTC, set by its charter or derived from its place
// on the world). Every replica gets the same answer for the same offset.
//
// THE CUT-OVER (2026-10-05, "game time is real time"). Until then a game day was
// LegacyScale times shorter than a real one (24 real minutes at 60), counted from
// Epoch, for everyone. From the cut-over a settlement's day is a real day of its
// local time. Each settlement switches at its own first local midnight at or after
// Cutover (a UTC midnight), and its day NUMBER keeps counting up across the switch:
// the first real day is numbered one above the legacy day running at that instant,
// so a key written before (a shop's delivery day, a store's settled day) never
// collides with, or sorts after, one written later. Nothing stored is converted.
//
// Changing Epoch, LegacyScale or Cutover after rows exist renumbers days, so they are
// set once, together, and never moved.
type Clock struct {
	// Epoch is the real instant at which legacy game day 0 began at 00:00.
	Epoch time.Time
	// Scale is the duration scale: how many seconds of game time pass in a real
	// second for a duration written in content (RealWait). 1 is real time.
	Scale Scale
	// LegacyScale is the scale the day numbers used before the cut-over (60 in the
	// live game). Zero means Scale.
	LegacyScale Scale
	// Cutover is the UTC midnight from which a game day is a real day. Zero: no
	// cut-over and the days are counted by the legacy rule alone.
	Cutover time.Time
}

// Day is the length of a game day.
const Day = 24 * time.Hour

// MaxOffset bounds a settlement's offset from UTC (real zones run from -12h to +14h).
const (
	MinOffset = -12 * time.Hour
	MaxOffset = 14 * time.Hour
)

// ValidOffset reports whether d is a usable time zone offset: a whole number of
// minutes within the real zones' range.
func ValidOffset(d time.Duration) bool {
	return d >= MinOffset && d <= MaxOffset && d%time.Minute == 0
}

// OffsetFromLongitude is the standard zone of a longitude: one hour per 15 degrees,
// rounded to the nearest hour, like the nautical zones.
func OffsetFromLongitude(lonDeg float64) time.Duration {
	h := int(lonDeg/15 + 0.5)
	if lonDeg < 0 {
		h = int(lonDeg/15 - 0.5)
	}
	d := time.Duration(h) * time.Hour
	return min(max(d, MinOffset), MaxOffset)
}

// Validate reports whether the clock is usable.
func (c Clock) Validate() error {
	if c.Epoch.IsZero() {
		return errors.New("gametime: the clock has no epoch")
	}
	if c.LegacyScale != 0 {
		if err := c.LegacyScale.Validate(); err != nil {
			return err
		}
	}
	if !c.Cutover.IsZero() && c.Cutover.UTC().Truncate(Day) != c.Cutover.UTC() {
		return errors.New("gametime: the cut-over must be a UTC midnight")
	}
	return c.Scale.Validate()
}

func (c Clock) legacyScale() Scale {
	s := c.LegacyScale
	if s == 0 {
		s = c.Scale
	}
	if s.Validate() != nil {
		s = 1
	}
	return s
}

// cutoverFor is the instant a settlement with this offset switches to real days:
// its first local midnight at or after Cutover.
func (c Clock) cutoverFor(off time.Duration) time.Time {
	m := (-off) % Day
	if m < 0 {
		m += Day
	}
	return c.Cutover.Add(m)
}

func (c Clock) afterFor(now time.Time, off time.Duration) bool {
	return !c.Cutover.IsZero() && !now.Before(c.cutoverFor(off))
}

// legacySince is the game time elapsed at now by the legacy rule.
func (c Clock) legacySince(now time.Time) time.Duration {
	if !now.After(c.Epoch) {
		return 0
	}
	return now.Sub(c.Epoch) * time.Duration(c.legacyScale())
}

// Since is the game time elapsed at now by the legacy rule; zero before the epoch.
func (c Clock) Since(now time.Time) time.Duration { return c.legacySince(now) }

func (c Clock) legacyDayAtHour(now time.Time, hour int) int64 {
	g := c.legacySince(now) - time.Duration(hour)*time.Hour
	if g < 0 {
		return -1
	}
	return int64(g / Day)
}

// DayAt is the number of the game day at now on UTC (personal clocks).
func (c Clock) DayAt(now time.Time) int64 { return c.DayAtHourIn(now, 0, 0) }

// DayAtHour is DayAtHourIn on UTC.
func (c Clock) DayAtHour(now time.Time, hour int) int64 { return c.DayAtHourIn(now, hour, 0) }

// DayAtIn is the number of the game day at now in the zone off.
func (c Clock) DayAtIn(now time.Time, off time.Duration) int64 { return c.DayAtHourIn(now, 0, off) }

// base is the first number of the real days: above every legacy number any
// settlement can have written, whatever its zone (they all switch within 24 hours
// of Cutover), so the numbers only ever go up across the switch.
func (c Clock) base(hour int) int64 { return c.legacyDayAtHour(c.Cutover.Add(Day), hour) + 2 }

// DayAtHourIn is the number of the last game "day" whose boundary at `hour` o'clock
// local time has passed, in the zone off: the counter that ticks at that hour
// instead of at midnight. At 05:59 it is still yesterday's number; at 06:00
// (hour 6) it is today's. -1 means the first boundary has not come.
//
// After a settlement's switch the number is base + the whole local days since
// Cutover (counted in its local time), so it ticks at the local hour and a change
// of the zone moves it by at most a day either way.
func (c Clock) DayAtHourIn(now time.Time, hour int, off time.Duration) int64 {
	if !c.afterFor(now, off) {
		return c.legacyDayAtHour(now, hour)
	}
	g := now.Add(off).Sub(c.Cutover) - time.Duration(hour)*time.Hour
	return c.base(hour) + floorDiv(g, Day)
}

func floorDiv(a, b time.Duration) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return int64(q)
}

// IsLegacyDay reports that a day number was counted by the legacy rule: below every
// real day's number.
func (c Clock) IsLegacyDay(day int64) bool {
	return !c.Cutover.IsZero() && day < c.base(0)-1
}

// RealAtHourIn is the real instant at which the boundary of game day n at `hour`
// o'clock local time falls in the zone off.
func (c Clock) RealAtHourIn(day int64, hour int, off time.Duration) time.Time {
	if !c.Cutover.IsZero() && day >= c.base(hour)-1 {
		return c.Cutover.Add(time.Duration(day-c.base(hour))*Day + time.Duration(hour)*time.Hour - off)
	}
	game := time.Duration(day)*Day + time.Duration(hour)*time.Hour
	return c.Epoch.Add(game / time.Duration(c.legacyScale()))
}

// RealAtHour is RealAtHourIn on UTC.
func (c Clock) RealAtHour(day int64, hour int) time.Time { return c.RealAtHourIn(day, hour, 0) }

// LocalMidnight is the start of the local day that t falls in, in the zone off.
func LocalMidnight(t time.Time, off time.Duration) time.Time {
	local := t.UTC().Add(off)
	return local.Truncate(Day).Add(-off)
}
