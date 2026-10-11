// Package climate is the environment of a settlement as a pure function (docs/adr/0070, slice E1): the outdoor temperature,
// rain and wind at an instant, from the world's cell, the settlement's latitude, hemisphere, local time zone and the calendar.
// Nothing is stored and nothing ticks: any replica asks the same question and gets the same answer, so warmth can be read
// forward lazily like the other needs.
//
// The yearly swing follows the latitude (small in the tropics, large near the poles) and is opposite in the two hemispheres;
// the night dip follows the settlement's LOCAL hour and is larger where the air is dry; the weather of a local day (rain, wind,
// a cold snap, a heat spell) is drawn from the seed by (cell, local day). Every number is content (climate.yml).
package climate

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/health"
)

// ErrInvalid means climate rules the package cannot use.
var ErrInvalid = errors.New("climate: invalid rules")

// bpsWhole is 100 percent.
const bpsWhole = 10_000

// Params are the numbers of the environment (climate.yml `weather`).
type Params struct {
	// GameYearDays and Seasons: the calendar the swing follows (a game day is a real day).
	GameYearDays, Seasons int
	// YearOffsetDays moves the start of the year, so the season the world is in when the rules start can be chosen.
	YearOffsetDays int
	// SwingEquatorC100 and SwingPoleC100: the half-amplitude of the yearly swing (centi-degrees) at the equator and at 90 degrees,
	// linear in the absolute latitude between.
	SwingEquatorC100, SwingPoleC100 int
	// NightDipHumidC100 and NightDipDryC100: the full day-night range (centi-degrees) in humid and in dry air; DryBelowMM is the
	// annual precipitation (mm) at and below which the air counts as dry, HumidAboveMM at and above which it is humid.
	NightDipHumidC100, NightDipDryC100 int
	DryBelowMM, HumidAboveMM           int
	// RainBPS[season] is the chance a local day is rainy; WindBPS the chance it is windy. Dry places rain less (RainDryPercent of it).
	RainBPS        []int
	WindBPS        int
	RainDryPercent int
	// ColdSnapBPS and HeatSpellBPS: the chance of an anomaly day, and ColdSnapC100 and HeatSpellC100 how far it moves the day.
	ColdSnapBPS, HeatSpellBPS   int
	ColdSnapC100, HeatSpellC100 int
	// RainCoolC100: a rainy day is this much cooler.
	RainCoolC100 int
}

// Validate refuses numbers the formulas cannot use.
func (p Params) Validate() error {
	switch {
	case p.GameYearDays < 1 || p.Seasons < 1 || p.GameYearDays%p.Seasons != 0 || len(p.RainBPS) != p.Seasons:
		return errors.Join(ErrInvalid, errors.New("the year must divide into whole seasons, with one rain chance each"))
	case p.SwingEquatorC100 < 0 || p.SwingPoleC100 < p.SwingEquatorC100:
		return errors.Join(ErrInvalid, errors.New("the swing grows with latitude"))
	case p.NightDipHumidC100 < 0 || p.NightDipDryC100 < p.NightDipHumidC100 || p.DryBelowMM >= p.HumidAboveMM:
		return errors.Join(ErrInvalid, errors.New("the night dip is larger in dry air"))
	case p.WindBPS < 0 || p.WindBPS > bpsWhole || p.ColdSnapBPS < 0 || p.HeatSpellBPS < 0 || p.ColdSnapBPS+p.HeatSpellBPS > bpsWhole ||
		p.RainDryPercent < 0 || p.RainDryPercent > 100:
		return errors.Join(ErrInvalid, errors.New("chances are 0 to 10000 basis points"))
	}
	for _, r := range p.RainBPS {
		if r < 0 || r > bpsWhole {
			return errors.Join(ErrInvalid, errors.New("a rain chance is 0 to 10000 basis points"))
		}
	}
	return nil
}

// Place is where a settlement stands: what the environment needs of the world and of the settlement.
type Place struct {
	// Key identifies the place in the weather seed (the world cell id).
	Key int64
	// BaseC100 is the cell's mean temperature (centi-degrees); LatDeg its latitude (negative: southern hemisphere);
	// PrecipMM its annual precipitation.
	BaseC100 int
	LatDeg   float64
	PrecipMM int
	// Zone is the settlement's own time zone.
	Zone time.Duration
}

// Env is the outdoors at one instant.
type Env struct {
	// TempC100 is the temperature in centi-degrees, with the season, the hour and the day's weather in it.
	TempC100 int
	Rain     bool
	Wind     bool
	// Snap and Spell say the day is a cold snap or a heat spell.
	Snap, Spell bool
	// Season is 0..Seasons-1 from the start of the year; Hour the settlement's local hour.
	Season, Hour int
}

// Calendar is the game calendar the year phase is counted from.
type Calendar struct {
	Epoch time.Time
}

// At is the environment of place at the instant t. Pure and deterministic in (seed, place, t).
func At(seed int64, cal Calendar, p Params, pl Place, t time.Time) Env {
	local := t.UTC().Add(pl.Zone)
	hour := local.Hour()
	day := floorDays(local.Sub(cal.Epoch.UTC()))
	yearDay := mod(day+int64(p.YearOffsetDays), int64(p.GameYearDays))
	// the fraction of the year, with the hour in it so the swing is smooth
	frac := (float64(yearDay) + float64(hour)/24) / float64(p.GameYearDays)
	if pl.LatDeg < 0 {
		frac += 0.5 // the other hemisphere's summer is our winter
	}
	season := int(math.Floor(math.Mod(frac, 1)*float64(p.Seasons))) % p.Seasons
	absLat := math.Min(math.Abs(pl.LatDeg), 90) / 90
	amp := float64(p.SwingEquatorC100) + (float64(p.SwingPoleC100)-float64(p.SwingEquatorC100))*absLat
	// year phase 0 is midwinter of the northern hemisphere, so the swing is minus the cosine
	temp := float64(pl.BaseC100) - amp*math.Cos(2*math.Pi*math.Mod(frac, 1))

	// the day-night range, from dry to humid air; coldest at 05:00, warmest at 15:00
	dip := float64(p.NightDipHumidC100)
	switch {
	case pl.PrecipMM <= p.DryBelowMM:
		dip = float64(p.NightDipDryC100)
	case pl.PrecipMM < p.HumidAboveMM:
		share := float64(p.HumidAboveMM-pl.PrecipMM) / float64(p.HumidAboveMM-p.DryBelowMM)
		dip = float64(p.NightDipHumidC100) + share*float64(p.NightDipDryC100-p.NightDipHumidC100)
	}
	temp += dip / 2 * math.Cos(2*math.Pi*(float64(hour)-15)/24)

	// the weather of the local day
	env := Env{Season: season, Hour: hour}
	die := func(salt int64) int64 { return health.Die(seed, pl.Key, day, salt) }
	rain := p.RainBPS[season]
	if pl.PrecipMM <= p.DryBelowMM {
		rain = rain * p.RainDryPercent / 100
	}
	env.Rain = die(1) < int64(rain)
	env.Wind = die(2) < int64(p.WindBPS)
	switch a := die(3); {
	case a < int64(p.ColdSnapBPS):
		env.Snap = true
		temp -= float64(p.ColdSnapC100)
	case a >= bpsWhole-int64(p.HeatSpellBPS):
		env.Spell = true
		temp += float64(p.HeatSpellC100)
	}
	if env.Rain {
		temp -= float64(p.RainCoolC100)
	}
	env.TempC100 = int(math.Round(temp))
	return env
}

func floorDays(d time.Duration) int64 {
	day := 24 * time.Hour
	n := int64(d / day)
	if d%day < 0 {
		n--
	}
	return n
}

func mod(a, m int64) int64 { return ((a % m) + m) % m }

// Cache keeps the environment of recent (place, hour) pairs: the lazy warmth integration asks for up to 48 hours per read, and
// the same hour is asked by every player of a settlement. It is a bounded map, cleared when full; a miss is only a recomputation.
type Cache struct {
	mu   sync.Mutex
	max  int
	data map[cacheKey]Env
}

type cacheKey struct {
	place int64
	hour  int64
}

// NewCache returns a cache of at most max entries.
func NewCache(max int) *Cache { return &Cache{max: max, data: map[cacheKey]Env{}} }

// At is the cached environment of the hour t falls in. The environment is taken at the start of that UTC hour, so every player
// of the place reads the same value for it.
func (c *Cache) At(seed int64, cal Calendar, p Params, pl Place, t time.Time) Env {
	hour := t.UTC().Truncate(time.Hour)
	k := cacheKey{pl.Key, hour.Unix() / 3600}
	c.mu.Lock()
	if e, ok := c.data[k]; ok {
		c.mu.Unlock()
		return e
	}
	c.mu.Unlock()
	e := At(seed, cal, p, pl, hour)
	c.mu.Lock()
	if len(c.data) >= c.max {
		c.data = map[cacheKey]Env{}
	}
	c.data[k] = e
	c.mu.Unlock()
	return e
}

// Day is the weather of one coming local day: the least and the greatest temperature (centi-degrees) and what the day brings.
type Day struct {
	// Offset is how many local days from today (1 is tomorrow).
	Offset           int
	MinC100, MaxC100 int
	Rain, Wind       bool
	Snap, Spell      bool
}

// localMidnight is the UTC instant of the start of the local day that t falls in.
func localMidnight(t time.Time, zone time.Duration) time.Time {
	l := t.UTC().Add(zone)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC).Add(-zone)
}

func (c *Cache) orAt(seed int64, cal Calendar, p Params, pl Place, t time.Time) Env {
	if c != nil {
		return c.At(seed, cal, p, pl, t)
	}
	return At(seed, cal, p, pl, t)
}

// Forecast is the weather of the next n local days after the one now falls in, read from the same seeded function the
// present weather comes from: a forecast is a read, not a state. c may be nil.
func Forecast(seed int64, cal Calendar, p Params, pl Place, now time.Time, n int, c *Cache) []Day {
	start := localMidnight(now, pl.Zone)
	out := make([]Day, 0, n)
	for d := 1; d <= n; d++ {
		day := Day{Offset: d, MinC100: 1 << 30, MaxC100: -(1 << 30)}
		base := start.Add(time.Duration(d) * 24 * time.Hour)
		for h := 0; h < 24; h++ {
			e := c.orAt(seed, cal, p, pl, base.Add(time.Duration(h)*time.Hour))
			day.MinC100, day.MaxC100 = min(day.MinC100, e.TempC100), max(day.MaxC100, e.TempC100)
			day.Rain, day.Wind, day.Snap, day.Spell = e.Rain, e.Wind, e.Snap, e.Spell
		}
		out = append(out, day)
	}
	return out
}

// SnapEnd is when the cold snap the present day is in ends: the start of the first local day after it that is not a snap. ok is
// false when today is no cold snap. It looks at most a week ahead. c may be nil.
func SnapEnd(seed int64, cal Calendar, p Params, pl Place, now time.Time, c *Cache) (time.Time, bool) {
	if !c.orAt(seed, cal, p, pl, now).Snap {
		return time.Time{}, false
	}
	end := localMidnight(now, pl.Zone).Add(24 * time.Hour)
	for i := 0; i < 7 && c.orAt(seed, cal, p, pl, end.Add(12*time.Hour)).Snap; i++ {
		end = end.Add(24 * time.Hour)
	}
	return end, true
}
