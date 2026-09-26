package screens

import (
	"sync/atomic"
	"time"
)

// Clock times as a player reads them.
//
// Every DURATION a screen shows is already the real wait (the game clock,
// docs/adr/0018-game-clock.md, did that before the screen was reached); a
// CLOCK TIME — "arrives at 14:32" — is an instant, and an instant means
// nothing until it is placed in the reader's time zone. Players are never
// shown UTC: the zone is the context's, or the process default the service
// sets from config player.default_timezone at startup.
//
// The shape "14:32" is the catalogue's (format.clock), and its digits are the
// language's, through the same numeral data every other number uses.

// keyClock is the catalogue key of a clock time: {hour} and {minute}, each
// already two digits in the language's numerals.
const keyClock = "format.clock"

// defaultZone is the zone a Context without one renders in. It starts as UTC
// only so a test that never configures it still renders; every service that
// shows a clock time sets it from configuration before serving.
var defaultZone atomic.Pointer[time.Location]

// SetDefaultZone sets the zone a clock time is shown in when the Context
// names none. A nil zone is ignored.
func SetDefaultZone(loc *time.Location) {
	if loc != nil {
		defaultZone.Store(loc)
	}
}

// zone is the zone this context renders clock times in.
func (c Context) zone() *time.Location {
	if c.Zone != nil {
		return c.Zone
	}
	if loc := defaultZone.Load(); loc != nil {
		return loc
	}
	return time.UTC
}

// FormatClock renders the time of day of t in this context's zone and
// language: "14:32" in both English and Persian, since numbers are always
// written in Western digits. A zero t renders empty, so a screen given no
// instant shows no clock.
func FormatClock(c Context, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(c.zone())
	n := c.numerals()
	return c.T(keyClock, map[string]any{
		"hour":   n.localise(pad2(local.Hour())),
		"minute": n.localise(pad2(local.Minute())),
	})
}

// clockLine renders key with {time} as t's clock time, or nothing when t is
// zero: a view that carries no instant shows no clock line.
func clockLine(c Context, key string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return c.T(key, map[string]any{"time": FormatClock(c, t)})
}
