package content

import (
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/climate"
	"github.com/mrjvadi/torncity/internal/domain/survival"
)

// The numbers of survival (docs/adr/0070), read from climate.yml: the environment, thirst and warmth, the garments and the
// sicknesses. The rules are internal/domain/climate and internal/domain/survival.

// WeatherDef is the environment's numbers (climate.Params). Temperatures are in centi-degrees.
type WeatherDef struct {
	YearOffsetDays    int   `yaml:"year_offset_days" json:"year_offset_days"`
	SwingEquatorC100  int   `yaml:"swing_equator_c100" json:"swing_equator_c100"`
	SwingPoleC100     int   `yaml:"swing_pole_c100" json:"swing_pole_c100"`
	NightDipHumidC100 int   `yaml:"night_dip_humid_c100" json:"night_dip_humid_c100"`
	NightDipDryC100   int   `yaml:"night_dip_dry_c100" json:"night_dip_dry_c100"`
	DryBelowMM        int   `yaml:"dry_below_mm" json:"dry_below_mm"`
	HumidAboveMM      int   `yaml:"humid_above_mm" json:"humid_above_mm"`
	RainBPS           []int `yaml:"rain_bps" json:"rain_bps"`
	WindBPS           int   `yaml:"wind_bps" json:"wind_bps"`
	RainDryPercent    int   `yaml:"rain_dry_percent" json:"rain_dry_percent"`
	ColdSnapBPS       int   `yaml:"cold_snap_bps" json:"cold_snap_bps"`
	ColdSnapC100      int   `yaml:"cold_snap_c100" json:"cold_snap_c100"`
	HeatSpellBPS      int   `yaml:"heat_spell_bps" json:"heat_spell_bps"`
	HeatSpellC100     int   `yaml:"heat_spell_c100" json:"heat_spell_c100"`
	RainCoolC100      int   `yaml:"rain_cool_c100" json:"rain_cool_c100"`
}

// SurvivalDef is the numbers of thirst and warmth, the shelters, the effects and the sicknesses.
type SurvivalDef struct {
	HotAboveC int `yaml:"hot_above_c" json:"hot_above_c"`
	// HeatstrokeAboveC: above this temperature a person who is also severely thirsty is shown as heatstroke (the hot band shows from HotAboveC).
	HeatstrokeAboveC int    `yaml:"heatstroke_above_c" json:"heatstroke_above_c"`
	HotThirstBPS     int    `yaml:"hot_thirst_bps" json:"hot_thirst_bps"`
	DrinkPoints      int    `yaml:"drink_points" json:"drink_points"`
	DrinkCooldown    string `yaml:"drink_cooldown" json:"drink_cooldown"`
	ThirstHigh       int    `yaml:"thirst_high" json:"thirst_high"`
	ThirstSevere     int    `yaml:"thirst_severe" json:"thirst_severe"`
	PointsPerDeficit int    `yaml:"points_per_deficit" json:"points_per_deficit"`
	DriftPerHour     int    `yaml:"drift_per_hour" json:"drift_per_hour"`
	Chilly           int    `yaml:"chilly" json:"chilly"`
	Cold             int    `yaml:"cold" json:"cold"`
	Freezing         int    `yaml:"freezing" json:"freezing"`
	Collapse         int    `yaml:"collapse" json:"collapse"`
	NightFrom        int    `yaml:"night_from" json:"night_from"`
	NightTo          int    `yaml:"night_to" json:"night_to"`
	HomeBonus        int    `yaml:"home_bonus" json:"home_bonus"`
	HearthBonus      int    `yaml:"hearth_bonus" json:"hearth_bonus"`
	WarmingBonus     int    `yaml:"warming_bonus" json:"warming_bonus"`
	ShelterHours     int    `yaml:"shelter_hours" json:"shelter_hours"`
	MaxSteps         int    `yaml:"max_steps" json:"max_steps"`
	CacheEntries     int    `yaml:"cache_entries" json:"cache_entries"`
	CollapseDamage   int    `yaml:"collapse_damage" json:"collapse_damage"`
	// Effects on the work of a shift, in basis points taken off the output.
	ChillyBPS       int `yaml:"chilly_bps" json:"chilly_bps"`
	ColdBPS         int `yaml:"cold_bps" json:"cold_bps"`
	FreezingBPS     int `yaml:"freezing_bps" json:"freezing_bps"`
	ThirstHighBPS   int `yaml:"thirst_high_bps" json:"thirst_high_bps"`
	ThirstSevereBPS int `yaml:"thirst_severe_bps" json:"thirst_severe_bps"`
	IllnessBPS      int `yaml:"illness_bps" json:"illness_bps"`
	// Sickness.
	ChillAfterHours       int `yaml:"chill_after_hours" json:"chill_after_hours"`
	ChillCourseHours      int `yaml:"chill_course_hours" json:"chill_course_hours"`
	WaterborneBPS         int `yaml:"waterborne_bps" json:"waterborne_bps"`
	WaterborneWarmBPS     int `yaml:"waterborne_warm_bps" json:"waterborne_warm_bps"`
	WaterborneWarmAboveC  int `yaml:"waterborne_warm_above_c" json:"waterborne_warm_above_c"`
	WaterborneCourseHours int `yaml:"waterborne_course_hours" json:"waterborne_course_hours"`
	// The medicines that shorten a sickness (the better first) and the share of the course a treatment takes off.
	Medicines       []string `yaml:"medicines" json:"medicines"`
	TreatCutPercent int      `yaml:"treat_cut_percent" json:"treat_cut_percent"`
	// BoilItem is the fuel a boiling burns; WaterItem the good a drink at the well or from the bag uses.
	BoilItem  string `yaml:"boil_item" json:"boil_item"`
	WaterItem string `yaml:"water_item" json:"water_item"`
}

// GarmentDef is a garment: the item it is, where it is worn, the warmth it gives, how much of it a wet day takes, how much of the
// heat's extra thirst it cuts, and how many days worn it lasts.
type GarmentDef struct {
	Item           string `yaml:"item" json:"item"`
	Slot           string `yaml:"slot" json:"slot"`
	Warmth         int    `yaml:"warmth" json:"warmth"`
	WetBPS         int    `yaml:"wet_bps" json:"wet_bps"`
	HeatCutBPS     int    `yaml:"heat_cut_bps,omitempty" json:"heat_cut_bps,omitempty"`
	DurabilityDays int    `yaml:"durability_days" json:"durability_days"`
}

// GarmentSlots are the places a garment is worn, in the order of the dress screen.
var GarmentSlots = []string{"base", "mid", "outer", "head", "hands", "feet"}

// ClimateParams is the environment's numbers as the pure package takes them.
func (d ClimateDef) ClimateParams() climate.Params {
	w := d.Weather
	return climate.Params{GameYearDays: d.GameYearDays, Seasons: d.Seasons, YearOffsetDays: w.YearOffsetDays,
		SwingEquatorC100: w.SwingEquatorC100, SwingPoleC100: w.SwingPoleC100,
		NightDipHumidC100: w.NightDipHumidC100, NightDipDryC100: w.NightDipDryC100, DryBelowMM: w.DryBelowMM, HumidAboveMM: w.HumidAboveMM,
		RainBPS: append([]int(nil), w.RainBPS...), WindBPS: w.WindBPS, RainDryPercent: w.RainDryPercent,
		ColdSnapBPS: w.ColdSnapBPS, HeatSpellBPS: w.HeatSpellBPS, ColdSnapC100: w.ColdSnapC100, HeatSpellC100: w.HeatSpellC100,
		RainCoolC100: w.RainCoolC100}
}

// SurvivalParams is the two needs' numbers as the pure package takes them.
func (d ClimateDef) SurvivalParams() survival.Params {
	s := d.Survival
	p := survival.Params{RainInsulation: d.RainInsulation, WindInsulation: d.WindInsulation, ThirstPerDay: int64(d.ThirstPerGameDay),
		HotAboveC100: s.HotAboveC * 100, HotBPS: int64(s.HotThirstBPS), DrinkPoints: int64(s.DrinkPoints),
		PointsPerDeficit: int64(s.PointsPerDeficit), DriftPerHour: int64(s.DriftPerHour),
		Chilly: int64(s.Chilly), Cold: int64(s.Cold), Freezing: int64(s.Freezing), Collapse: int64(s.Collapse),
		ThirstHigh: int64(s.ThirstHigh), ThirstSevere: int64(s.ThirstSevere), NightFrom: s.NightFrom, NightTo: s.NightTo,
		MaxSteps: s.MaxSteps, ChillAfterHours: s.ChillAfterHours}
	for _, b := range d.Bands {
		p.Bands = append(p.Bands, survival.Band{MinTempC: b.MinTempC, Required: b.RequiredInsulation})
	}
	return p
}

// Garment returns the garment of an item.
func (d ClimateDef) Garment(item string) (GarmentDef, bool) {
	for _, g := range d.Garments {
		if g.Item == item {
			return g, true
		}
	}
	return GarmentDef{}, false
}

// DrinkEvery is the cooldown between two drinks.
func (s SurvivalDef) DrinkEvery() time.Duration { d, _ := optionalDuration(s.DrinkCooldown); return d }

// survival checks the survival numbers of the climate block.
func (l *schemaLint) survival(c ClimateDef) {
	if err := c.ClimateParams().Validate(); err != nil {
		l.bad("climate.weather: %v", err)
	}
	if err := c.SurvivalParams().Validate(); err != nil {
		l.bad("climate.survival: %v", err)
	}
	s := c.Survival
	for name, v := range map[string]int{"hot_thirst_bps": s.HotThirstBPS, "chilly_bps": s.ChillyBPS, "cold_bps": s.ColdBPS,
		"freezing_bps": s.FreezingBPS, "thirst_high_bps": s.ThirstHighBPS, "thirst_severe_bps": s.ThirstSevereBPS, "illness_bps": s.IllnessBPS,
		"waterborne_bps": s.WaterborneBPS, "waterborne_warm_bps": s.WaterborneWarmBPS} {
		if v < 0 || v > 100_000 {
			l.bad("climate.survival: %s %d out of range", name, v)
		}
	}
	if s.DrinkEvery() < 0 || s.ShelterHours < 1 || s.CollapseDamage < 1 || s.CacheEntries < 1 || s.ChillCourseHours < 1 ||
		s.WaterborneCourseHours < 1 || s.TreatCutPercent < 1 || s.TreatCutPercent > 100 || s.NightFrom < 0 || s.NightFrom > 23 || s.NightTo < 0 || s.NightTo > 23 {
		l.bad("climate.survival: a duration, a count or a share is out of range")
	}
	if s.HeatstrokeAboveC <= s.HotAboveC {
		l.bad("climate.survival: heatstroke_above_c must be above hot_above_c")
	}
	if len(s.Medicines) == 0 || s.BoilItem == "" || s.WaterItem == "" {
		l.bad("climate.survival: medicines, the boiling fuel and the water good are needed")
	}
	slots := map[string]bool{}
	for _, sl := range GarmentSlots {
		slots[sl] = true
	}
	seen := map[string]bool{}
	for _, g := range c.Garments {
		key := fmt.Sprintf("climate.garments[%s]", g.Item)
		switch {
		case seen[g.Item] || !l.itemKnown(g.Item):
			l.bad("%s: unknown or repeated item", key)
		case !slots[g.Slot]:
			l.bad("%s: slot %q is not one of the six", key, g.Slot)
		case g.Warmth < 0 || g.Warmth > 4 || g.WetBPS < 0 || g.WetBPS > 10_000 || g.HeatCutBPS < 0 || g.HeatCutBPS > 10_000 || g.DurabilityDays < 1:
			l.bad("%s: warmth, wet share, heat cut or durability out of range", key)
		}
		seen[g.Item] = true
	}
}
