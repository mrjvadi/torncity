package survival

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/climate"
)

func params() Params {
	return Params{Bands: []Band{{15, 1}, {5, 2}, {-5, 4}, {-60, 6}}, RainInsulation: 1, WindInsulation: 1, ThirstPerDay: 40,
		HotAboveC100: 3000, HotBPS: 15000, DrinkPoints: 25, PointsPerDeficit: 25, DriftPerHour: 4,
		Chilly: 40, Cold: 70, Freezing: 90, Collapse: 95, ThirstHigh: 70, ThirstSevere: 90, NightFrom: 21, NightTo: 6, MaxSteps: 48}
}

var t0 = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func constant(e climate.Env) func(time.Time) climate.Env {
	return func(time.Time) climate.Env { return e }
}

func TestParamsValidate(t *testing.T) {
	if err := params().Validate(); err != nil {
		t.Fatal(err)
	}
	b := params()
	b.Cold = b.Freezing
	if b.Validate() == nil {
		t.Error("the levels rise")
	}
}

func TestRequiredFollowsTheBandAndTheWeather(t *testing.T) {
	p := params()
	for _, c := range []struct {
		env  climate.Env
		want int
	}{
		{climate.Env{TempC100: 2000}, 1}, {climate.Env{TempC100: 1000}, 2}, {climate.Env{TempC100: 0}, 4}, {climate.Env{TempC100: -500}, 4},
		{climate.Env{TempC100: -501}, 6}, {climate.Env{TempC100: 0, Rain: true, Wind: true}, 6},
	} {
		if got := p.Required(c.env); got != c.want {
			t.Errorf("%+v: required %d, want %d", c.env, got, c.want)
		}
	}
}

// Thirst rises 40 a day at the baseline, faster in the heat, and a drink takes 25 off.
func TestThirstRisesAndDrinkingLowersIt(t *testing.T) {
	p := params()
	mild := constant(climate.Env{TempC100: 1500})
	r := p.Read(Needs{ThirstAt: t0, WarmthAt: t0}, Body{}, t0.Add(24*time.Hour), 0, mild)
	if got := r.Thirst / Milli; got < 39 || got > 41 {
		t.Errorf("a day: thirst %d, want 40", got)
	}
	hot := p.Read(Needs{ThirstAt: t0, WarmthAt: t0}, Body{}, t0.Add(24*time.Hour), 0, constant(climate.Env{TempC100: 3500}))
	if hot.Thirst <= r.Thirst {
		t.Errorf("heat adds to thirst: %d vs %d", hot.Thirst, r.Thirst)
	}
	if got := p.Drink(r.Thirst) / Milli; got < 14 || got > 16 {
		t.Errorf("a drink takes 25 off: %d", got)
	}
	if p.Drink(10*Milli) != 0 {
		t.Error("thirst never goes below zero")
	}
}

// Warmth settles toward what the weather asks less what is worn, and shelter at night lowers it.
func TestWarmthSettlesAndShelterHelps(t *testing.T) {
	p := params()
	cold := constant(climate.Env{TempC100: 0}) // needs 4
	bare := p.Read(Needs{WarmthAt: t0, ThirstAt: t0}, Body{}, t0.Add(40*time.Hour), 0, cold)
	if bare.Warmth/Milli != 100 {
		t.Errorf("bare in the cold, warmth goes to the top: %d", bare.Warmth/Milli)
	}
	dressed := p.Read(Needs{WarmthAt: t0, ThirstAt: t0}, Body{Worn: []Garment{{Warmth: 2}, {Warmth: 2}}}, t0.Add(40*time.Hour), 0, cold)
	if dressed.Warmth != 0 {
		t.Errorf("four points of clothes meet a need of four: %d", dressed.Warmth/Milli)
	}
	// a cold night: sheltered hours settle lower than unsheltered
	night := time.Date(2026, 10, 12, 21, 0, 0, 0, time.UTC)
	a := p.Read(Needs{Warmth: 80 * Milli, WarmthAt: night, ThirstAt: night}, Body{HomeBonus: 4}, night.Add(8*time.Hour), 0, cold)
	b := p.Read(Needs{Warmth: 80 * Milli, WarmthAt: night, ThirstAt: night}, Body{}, night.Add(8*time.Hour), 0, cold)
	if a.Warmth >= b.Warmth {
		t.Errorf("a warm home through the night lowers the cold: %d vs %d", a.Warmth, b.Warmth)
	}
	// wet clothes insulate less
	rain := constant(climate.Env{TempC100: 1000, Rain: true})
	w := p.Read(Needs{WarmthAt: t0, ThirstAt: t0}, Body{Worn: []Garment{{Warmth: 2, WetBPS: 5000}}}, t0.Add(40*time.Hour), 0, rain)
	d := p.Read(Needs{WarmthAt: t0, ThirstAt: t0}, Body{Worn: []Garment{{Warmth: 2, WetBPS: 5000}}}, t0.Add(40*time.Hour), 0, constant(climate.Env{TempC100: 1000}))
	if w.Warmth <= d.Warmth {
		t.Errorf("rain wets the garments and adds to the need: %d vs %d", w.Warmth, d.Warmth)
	}
}

// A read costs a bounded number of hours however long ago the last one was.
func TestReadIsCapped(t *testing.T) {
	p := params()
	calls := 0
	env := func(time.Time) climate.Env { calls++; return climate.Env{TempC100: 1500} }
	r := p.Read(Needs{ThirstAt: t0, WarmthAt: t0}, Body{}, t0.Add(1000*24*time.Hour), 0, env)
	if calls > 2*(p.MaxSteps+2) {
		t.Errorf("a read asked for the environment %d times", calls)
	}
	if r.Thirst != 100*Milli {
		t.Errorf("a thousand days without water: %d", r.Thirst/Milli)
	}
}

func TestLevelsAndCollapse(t *testing.T) {
	p := params()
	if p.WarmthLevel(30*Milli) != Fine || p.WarmthLevel(45*Milli) != Chilly || p.WarmthLevel(75*Milli) != Cold || p.WarmthLevel(92*Milli) != Freezing {
		t.Error("warmth levels")
	}
	if p.ThirstLevel(50*Milli) != 0 || p.ThirstLevel(75*Milli) != 1 || p.ThirstLevel(95*Milli) != 2 {
		t.Error("thirst levels")
	}
	if p.Collapsed(94*Milli) || !p.Collapsed(95*Milli) {
		t.Error("collapse at 95")
	}
}
