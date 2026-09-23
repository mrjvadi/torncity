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
