package content

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/climate"
	"github.com/mrjvadi/torncity/internal/domain/survival"
)

// simulate lives `days` days in a place from the start instant: outdoors by day, in the dwelling at night (night bonus from the
// home and, when lit, its hearth), reading the needs forward hour by hour as the handler does. It returns the worst warmth and
// thirst reached (points) and whether the person would have collapsed.
func simulate(def ClimateDef, pl climate.Place, start time.Time, days int, worn []survival.Garment, hearth, drinks bool) (warmth, thirst int64, collapsed bool) {
	sp, cp := def.SurvivalParams(), def.ClimateParams()
	cal := climate.Calendar{Epoch: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	cache := climate.NewCache(4096)
	envAt := func(t time.Time) climate.Env { return cache.At(7, cal, cp, pl, t) }
	home := def.Survival.HomeBonus
	if hearth {
		home += def.Survival.HearthBonus
	}
	n := survival.Needs{ThirstAt: start, WarmthAt: start}
	b := survival.Body{Worn: worn, HomeBonus: home}
	for h := 1; h <= days*24; h++ {
		now := start.Add(time.Duration(h) * time.Hour)
		rd := sp.Read(n, b, now, pl.Zone, envAt)
		n = rd.Needs
		if drinks && n.Thirst/survival.Milli >= sp.ThirstHigh {
			n.Thirst = sp.Drink(sp.Drink(n.Thirst)) // a water-wise person drinks twice when thirsty
		}
		warmth, thirst = max(warmth, n.Warmth/survival.Milli), max(thirst, n.Thirst/survival.Milli)
		if sp.Collapsed(n.Warmth) || sp.Collapsed(n.Thirst) {
			collapsed = true
		}
	}
	return warmth, thirst, collapsed
}

func garmentsOf(def ClimateDef, items ...string) []survival.Garment {
	var out []survival.Garment
	for _, it := range items {
		g, _ := def.Garment(it)
		out = append(out, survival.Garment{Warmth: g.Warmth, WetBPS: g.WetBPS, HeatCutBPS: g.HeatCutBPS})
	}
	return out
}

// The first winter must be survivable with tier 0 clothes and a fire (ADR 0045 12, ADR 0070): in a cold temperate place, through
// the deepest part of the year, a person who sleeps at home by a lit hearth and works outdoors by day in a hide wrap, hide boots,
// a wool tunic, cap and mittens never collapses; the same person with no clothes and no fire does.
func TestTheFirstWinterIsSurvivableWithTierZeroClothesAndAFire(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := snap.Climate()
	if !ok {
		t.Fatal("climate.yml is shipped")
	}
	cold := climate.Place{Key: 11, BaseC100: -300, LatDeg: 50, PrecipMM: 700}
	// the deepest winter: year day 3 of the 28-day year, counted from the epoch with the shipped offset
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 28*9+3-def.Weather.YearOffsetDays)
	tier0 := garmentsOf(def, "hide_wrap", "hide_boots", "wool_tunic", "wool_cap", "wool_mittens")
	w, th, collapsed := simulate(def, cold, start, 21, tier0, true, true)
	if collapsed {
		t.Errorf("tier 0 clothes and a fire must carry a person through a hard winter: warmth reached %d, thirst %d", w, th)
	}
	if w >= int64(def.Survival.Freezing) {
		t.Errorf("a person in tier 0 clothes at a fire should stay above freezing: warmth %d", w)
	}
	_, _, naked := simulate(def, cold, start, 21, nil, false, true)
	if !naked {
		t.Error("nobody lives through a hard winter with no clothes and no fire: the cold must matter")
	}
	// a full outfit is comfortable
	full := garmentsOf(def, "wool_tunic", "wool_vest", "wool_cloak", "wool_cap", "wool_mittens", "hide_boots")
	if w, _, c := simulate(def, cold, start, 21, full, true, true); c || w >= int64(def.Survival.Cold) {
		t.Errorf("a full wool outfit keeps a person above the cold line: warmth %d", w)
	}
}

// A hot place has its own pressure (ADR 0070): the same person thirsts faster in the tropics at the heat of the year, and a sun hat
// cuts the extra thirst.
func TestHeatRaisesThirstAndAHatCutsIt(t *testing.T) {
	p := shippedPack(t)
	snap, err := BuildSnapshot(1, p)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := snap.Climate()
	hot := climate.Place{Key: 12, BaseC100: 3300, LatDeg: 8, PrecipMM: 300}
	mild := climate.Place{Key: 13, BaseC100: 1600, LatDeg: 45, PrecipMM: 800}
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	_, tHot, _ := simulate(def, hot, start, 1, nil, false, false)
	_, tMild, _ := simulate(def, mild, start, 1, nil, false, false)
	if tHot <= tMild {
		t.Errorf("heat adds to thirst: hot %d, mild %d", tHot, tMild)
	}
	_, tHat, _ := simulate(def, hot, start, 1, garmentsOf(def, "sun_hat"), false, false)
	if tHat >= tHot {
		t.Errorf("a sun hat cuts the heat's thirst: with %d, without %d", tHat, tHot)
	}
}
