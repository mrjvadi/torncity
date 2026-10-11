package climate

import (
	"testing"
	"time"
)

func params() Params {
	return Params{GameYearDays: 28, Seasons: 4, SwingEquatorC100: 200, SwingPoleC100: 1800, NightDipHumidC100: 500, NightDipDryC100: 1000,
		DryBelowMM: 300, HumidAboveMM: 1000, RainBPS: []int{2000, 3000, 1500, 2500}, WindBPS: 2500, RainDryPercent: 30,
		ColdSnapBPS: 400, ColdSnapC100: 800, HeatSpellBPS: 400, HeatSpellC100: 600, RainCoolC100: 200}
}

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestParamsValidate(t *testing.T) {
	if err := params().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := params()
	bad.RainBPS = []int{1, 2}
	if bad.Validate() == nil {
		t.Error("one rain chance per season")
	}
	bad = params()
	bad.NightDipDryC100 = 100
	if bad.Validate() == nil {
		t.Error("the night dip is larger in dry air")
	}
}

// The environment is a pure function: the same question has the same answer, whoever asks.
func TestAtIsDeterministic(t *testing.T) {
	pl := Place{Key: 5, BaseC100: 1000, LatDeg: 40, PrecipMM: 600}
	now := epoch.Add(100*24*time.Hour + 5*time.Hour)
	if a, b := At(9, Calendar{epoch}, params(), pl, now), At(9, Calendar{epoch}, params(), pl, now); a != b {
		t.Errorf("not deterministic: %+v %+v", a, b)
	}
}

// Winter is colder than summer at 50 degrees north, the other way round at 50 degrees south, and the swing is small at the equator.
func TestSeasonsFollowTheHemisphere(t *testing.T) {
	p := params()
	north := Place{Key: 1, BaseC100: 1000, LatDeg: 50, PrecipMM: 700}
	south := north
	south.Key, south.LatDeg = 2, -50
	tropic := Place{Key: 3, BaseC100: 2800, LatDeg: 2, PrecipMM: 2000}
	noon := func(yearDay int) time.Time { return epoch.AddDate(0, 0, 28*5+yearDay).Add(15 * time.Hour) }
	// mean over a season smooths out the weather: compare the middle of winter (year day 3) and of summer (year day 17)
	mean := func(pl Place, yearDay int) float64 {
		sum := 0.0
		for k := 0; k < 40; k++ {
			pl.Key = int64(100 + k)
			sum += float64(At(3, Calendar{epoch}, p, pl, noon(yearDay)).TempC100)
		}
		return sum / 40
	}
	if w, s := mean(north, 3), mean(north, 17); s-w < 1200 {
		t.Errorf("the northern summer is much warmer than its winter: %v vs %v", s, w)
	}
	if w, s := mean(south, 3), mean(south, 17); w-s < 1200 {
		t.Errorf("the southern hemisphere is flipped: %v vs %v", w, s)
	}
	if w, s := mean(tropic, 3), mean(tropic, 17); s-w > 600 || w-s > 600 {
		t.Errorf("the tropics hardly have seasons: %v vs %v", s, w)
	}
}

// The night is the settlement's local night: the same instant is cold where it is night and warm where it is afternoon.
func TestTheNightFollowsTheLocalHour(t *testing.T) {
	p := params()
	p.ColdSnapBPS, p.HeatSpellBPS, p.RainBPS, p.WindBPS = 0, 0, []int{0, 0, 0, 0}, 0
	here := Place{Key: 1, BaseC100: 1000, LatDeg: 30, PrecipMM: 800}
	there := here
	there.Zone = 10 * time.Hour
	at := epoch.Add(30*24*time.Hour + 5*time.Hour) // 05:00 UTC
	a, b := At(1, Calendar{epoch}, p, here, at), At(1, Calendar{epoch}, p, there, at)
	if a.Hour != 5 || b.Hour != 15 || b.TempC100-a.TempC100 < 300 {
		t.Errorf("05:00 local is colder than 15:00 local: %+v %+v", a, b)
	}
}

// Dry air swings more between day and night than humid air.
func TestDryAirSwingsMore(t *testing.T) {
	p := params()
	p.ColdSnapBPS, p.HeatSpellBPS, p.RainBPS, p.WindBPS = 0, 0, []int{0, 0, 0, 0}, 0
	humid := Place{Key: 1, BaseC100: 1500, LatDeg: 20, PrecipMM: 1500}
	dry := humid
	dry.PrecipMM = 100
	day := epoch.Add(40 * 24 * time.Hour)
	rng := func(pl Place) int {
		lo, hi := 1<<30, -1<<30
		for h := 0; h < 24; h++ {
			v := At(1, Calendar{epoch}, p, pl, day.Add(time.Duration(h)*time.Hour)).TempC100
			lo, hi = min(lo, v), max(hi, v)
		}
		return hi - lo
	}
	if rng(dry) <= rng(humid) {
		t.Errorf("dry air swings more: dry %d, humid %d", rng(dry), rng(humid))
	}
}

// The cache returns what the function returns and never grows past its bound.
func TestCacheAgreesAndIsBounded(t *testing.T) {
	p := params()
	pl := Place{Key: 4, BaseC100: 500, LatDeg: 55, PrecipMM: 500}
	c := NewCache(8)
	for h := 0; h < 100; h++ {
		at := epoch.Add(time.Duration(h)*time.Hour + 17*time.Minute)
		want := At(5, Calendar{epoch}, p, pl, at.Truncate(time.Hour))
		if got := c.At(5, Calendar{epoch}, p, pl, at); got != want {
			t.Fatalf("hour %d: %+v != %+v", h, got, want)
		}
	}
	if len(c.data) > 8 {
		t.Errorf("the cache holds %d entries, bound 8", len(c.data))
	}
}

// A forecast is the weather the function gives for the coming local days, and a cold snap names the day it ends.
func TestForecastAndSnapEnd(t *testing.T) {
	p := params()
	pl := Place{Key: 6, BaseC100: 500, LatDeg: 50, PrecipMM: 600, Zone: 3 * time.Hour}
	now := epoch.Add(40*24*time.Hour + 5*time.Hour)
	f := Forecast(4, Calendar{epoch}, p, pl, now, 3, nil)
	if len(f) != 3 || f[0].Offset != 1 || f[0].MinC100 >= f[0].MaxC100 {
		t.Fatalf("three coming days with a daily range: %+v", f)
	}
	if g := Forecast(4, Calendar{epoch}, p, pl, now, 3, NewCache(64)); g[1] != f[1] {
		t.Errorf("the cache agrees: %+v vs %+v", g[1], f[1])
	}
	found := false
	for d := 0; d < 400 && !found; d++ {
		at := epoch.Add(time.Duration(d)*24*time.Hour + 12*time.Hour)
		if end, ok := SnapEnd(4, Calendar{epoch}, p, pl, at, nil); ok {
			found = true
			if !end.After(at) || end.Sub(at) > 8*24*time.Hour || end.Add(pl.Zone).Hour() != 0 {
				t.Errorf("a snap ends at a later local midnight: %v from %v", end, at)
			}
		} else if At(4, Calendar{epoch}, p, pl, at).Snap {
			t.Errorf("a snap day must report its end")
		}
	}
	if !found {
		t.Error("no cold snap in 400 days at 4 percent")
	}
}
