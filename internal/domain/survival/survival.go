// Package survival holds the rules of thirst and warmth (docs/adr/0070, slices E2 and E4): two needs that drift on the clock and
// are read forward lazily, like hunger (internal/domain/life). Warmth drifts toward what the weather asks of a body, less what
// the body wears and what shelters it; thirst rises with the days and faster in the heat. Nothing ticks: a need is stored with
// the instant it describes, and its value at a later instant is a pure function of the two and of the environment, which is itself
// a pure function (internal/domain/climate).
//
// The lazy integration walks whole hours and is capped at MaxSteps of them, so a read costs a bounded amount whatever the time
// since the last one; what happened before the window only counts at the base rate of thirst and not at all for warmth, which
// settles within a day.
package survival

import (
	"errors"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/climate"
)

// ErrInvalid means survival rules the package cannot use.
var ErrInvalid = errors.New("survival: invalid rules")

// Milli is the resolution of a need: a need of 42.5 points is 42500. Points run 0..MaxPoints; higher is worse.
const (
	Milli     = 1000
	MaxPoints = 100
)

// Band is one temperature band: from MinTempC up, a body needs Required points of insulation. Bands run warm to cold.
type Band struct {
	MinTempC int
	Required int
}

// Params are the numbers of both needs (climate.yml `survival`).
type Params struct {
	Bands                          []Band
	RainInsulation, WindInsulation int
	ThirstPerDay                   int64
	HotAboveC100                   int
	HotBPS                         int64
	DrinkPoints                    int64
	PointsPerDeficit               int64
	DriftPerHour                   int64
	Chilly, Cold, Freezing         int64
	Collapse                       int64
	ThirstHigh, ThirstSevere       int64
	NightFrom, NightTo             int
	MaxSteps                       int
	ChillAfterHours                int
}

// Validate refuses numbers the formulas cannot use.
func (p Params) Validate() error {
	if len(p.Bands) < 2 || p.ThirstPerDay < 1 || p.PointsPerDeficit < 1 || p.DriftPerHour < 1 || p.MaxSteps < 1 || p.MaxSteps > 240 ||
		!(0 < p.Chilly && p.Chilly < p.Cold && p.Cold < p.Freezing && p.Freezing < p.Collapse && p.Collapse <= MaxPoints) ||
		!(0 < p.ThirstHigh && p.ThirstHigh < p.ThirstSevere && p.ThirstSevere <= MaxPoints) || p.DrinkPoints < 1 || p.HotBPS < 10_000 {
		return ErrInvalid
	}
	for i, b := range p.Bands {
		if b.Required < 0 || (i > 0 && b.MinTempC >= p.Bands[i-1].MinTempC) {
			return ErrInvalid
		}
	}
	return nil
}

// Required is the insulation a body needs outdoors in env: the band of the temperature, plus one for rain and one for wind.
func (p Params) Required(env climate.Env) int {
	t := env.TempC100 / 100
	if env.TempC100 < 0 && env.TempC100%100 != 0 {
		t-- // floor
	}
	need := p.Bands[len(p.Bands)-1].Required
	for _, b := range p.Bands {
		if t >= b.MinTempC {
			need = b.Required
			break
		}
	}
	if env.Rain {
		need += p.RainInsulation
	}
	if env.Wind {
		need += p.WindInsulation
	}
	return need
}

// Garment is what a worn garment gives in the integration: its warmth, how much of it a wet day takes, and how much of the
// heat's extra thirst it cuts (a hat).
type Garment struct {
	Warmth, WetBPS, HeatCutBPS int
}

// Body is what the integration needs to know of the person: what they wear, and what shelters them.
type Body struct {
	Worn []Garment
	// ShelterUntil and ShelterBonus: a warm place (an inn, a warming house) shelters them until then.
	ShelterUntil time.Time
	ShelterBonus int
	// HomeBonus shelters them through the local night (their own dwelling, with its hearth lit or not); HasHome says they have one.
	HomeBonus int
	// HeavyWork says the person worked hard these hours (their thirst rises faster); the caller passes the share in bps.
	HeavyBPS int64
}

// Needs are thirst and warmth in milli-points, each with the instant it describes.
type Needs struct {
	Thirst, Warmth     int64
	ThirstAt, WarmthAt time.Time
}

// Reading is what a read found out besides the new values.
type Reading struct {
	Needs
	// ColdHours counts the whole hours in the window warmth stood at or above the Cold line.
	ColdHours int
}

func clamp(v int64) int64 { return min(max(v, 0), MaxPoints*Milli) }

// worn is the insulation of the garments in env: wet takes the wet share of each garment's warmth.
func worn(gs []Garment, wet bool) int {
	sum := 0
	for _, g := range gs {
		w := g.Warmth * 10_000
		if wet {
			w = g.Warmth * (10_000 - g.WetBPS)
		}
		sum += w
	}
	return sum / 10_000
}

func localHour(t time.Time, zone time.Duration) int { return t.UTC().Add(zone).Hour() }

func (p Params) night(hour int) bool {
	if p.NightFrom > p.NightTo {
		return hour >= p.NightFrom || hour < p.NightTo
	}
	return hour >= p.NightFrom && hour < p.NightTo
}

// Read brings both needs from their instants to now. envAt is the environment of an hour (a cached climate.At); zone the
// settlement's own time zone. The window is the last MaxSteps whole hours before now.
func (p Params) Read(n Needs, b Body, now time.Time, zone time.Duration, envAt func(time.Time) climate.Env) Reading {
	out := Reading{Needs: n}
	// thirst: the base rate for time before the window, the hour-by-hour rate inside it
	windowStart := now.Add(-time.Duration(p.MaxSteps) * time.Hour)
	basePerHour := float64(p.ThirstPerDay) * Milli / 24
	if n.ThirstAt.Before(now) {
		from := n.ThirstAt
		thirst := float64(n.Thirst)
		if from.Before(windowStart) {
			thirst += basePerHour * windowStart.Sub(from).Hours()
			from = windowStart
		}
		for t := from; t.Before(now); {
			step := min(time.Hour-time.Duration(t.Sub(t.Truncate(time.Hour))), now.Sub(t))
			env := envAt(t)
			rate := basePerHour
			if env.TempC100 > p.HotAboveC100 {
				extra := float64(p.HotBPS-10_000) / 10_000
				cut := 0
				for _, g := range b.Worn {
					cut += g.HeatCutBPS
				}
				extra *= float64(max(10_000-cut, 0)) / 10_000
				rate *= 1 + extra
			}
			if b.HeavyBPS > 0 {
				rate *= 1 + float64(b.HeavyBPS)/10_000
			}
			thirst += rate * step.Hours()
			t = t.Add(step)
		}
		out.Thirst, out.ThirstAt = clamp(int64(thirst)), now
	}
	// warmth: hour by hour inside the window; what came before it has settled
	if n.WarmthAt.Before(now) {
		from := n.WarmthAt
		w := n.Warmth
		if from.Before(windowStart) {
			from = windowStart
		}
		for t := from; t.Before(now); {
			step := min(time.Hour-time.Duration(t.Sub(t.Truncate(time.Hour))), now.Sub(t))
			env := envAt(t)
			shelter := 0
			sheltered := false
			if t.Before(b.ShelterUntil) {
				shelter, sheltered = b.ShelterBonus, true
			}
			if p.night(localHour(t, zone)) && b.HomeBonus > shelter {
				shelter, sheltered = b.HomeBonus, true
			}
			e := env
			if sheltered {
				e.Rain, e.Wind = false, false
			}
			deficit := p.Required(e) - worn(b.Worn, e.Rain) - shelter
			target := clamp(int64(deficit) * p.PointsPerDeficit * Milli)
			delta := p.DriftPerHour * Milli * int64(step) / int64(time.Hour)
			switch {
			case w < target:
				w = min(w+delta, target)
			case w > target:
				w = max(w-delta, target)
			}
			if w >= p.Cold*Milli && step == time.Hour {
				out.ColdHours++
			}
			t = t.Add(step)
		}
		out.Warmth, out.WarmthAt = clamp(w), now
	}
	return out
}

// Drink lowers thirst by a drink's points.
func (p Params) Drink(thirst int64) int64 { return clamp(thirst - p.DrinkPoints*Milli) }

// Level names how bad a need is.
type Level int

// The levels of warmth, worst last.
const (
	Fine Level = iota
	Chilly
	Cold
	Freezing
)

// WarmthLevel is the level of a warmth value.
func (p Params) WarmthLevel(w int64) Level {
	switch pts := w / Milli; {
	case pts >= p.Freezing:
		return Freezing
	case pts >= p.Cold:
		return Cold
	case pts >= p.Chilly:
		return Chilly
	}
	return Fine
}

// ThirstLevel is 0 fine, 1 high, 2 severe.
func (p Params) ThirstLevel(t int64) int {
	switch pts := t / Milli; {
	case pts >= p.ThirstSevere:
		return 2
	case pts >= p.ThirstHigh:
		return 1
	}
	return 0
}

// Collapsed reports whether a need is at the point where the body gives out.
func (p Params) Collapsed(v int64) bool { return v/Milli >= p.Collapse }
