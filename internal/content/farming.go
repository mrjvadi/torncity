package content

import "github.com/mrjvadi/torncity/internal/domain/farm"

// FarmingDef is farming.yml (docs/adr/0067, ADR 0041 7.6): the farm cycle, the soil, the water and the miller's toll.
// The numbers are the ADR's first draft, all of them tuning, none of them a survey of real yields.
type FarmingDef struct {
	Head `yaml:",inline"`
	// The work each stage asks for, in shifts of the farm.
	SowShifts     int `yaml:"sow_shifts" json:"sow_shifts"`
	TendMax       int `yaml:"tend_max" json:"tend_max"`
	HarvestShifts int `yaml:"harvest_shifts" json:"harvest_shifts"`
	// TendBPS is the yield one tending shift adds.
	TendBPS int `yaml:"tend_bps" json:"tend_bps"`
	// Grow is how long the crop grows after the last sowing shift, Window how long it stays ripe, RotStep how often a
	// crop past its window loses RotStepBPS of its yield (durations, real time: the game clock is the real clock).
	Grow       string `yaml:"grow" json:"grow"`
	Window     string `yaml:"window" json:"window"`
	RotStep    string `yaml:"rot_step" json:"rot_step"`
	RotStepBPS int    `yaml:"rot_step_bps" json:"rot_step_bps"`
	// Seed and Base are wheat per cycle for irrigated and for rain-fed farms.
	Seed FarmingPair `yaml:"seed" json:"seed"`
	Base FarmingPair `yaml:"base" json:"base"`
	// Water is the water factor and the reach of a water work.
	Water FarmingWater `yaml:"water" json:"water"`
	// Soil is the factor of a farm's lots by biome, less SteepPenaltyBPS for a steep lot; SoilDefaultBPS where the biome
	// is not listed.
	SoilDefaultBPS   int              `yaml:"soil_default_bps" json:"soil_default_bps"`
	SteepPenaltyBPS  int              `yaml:"steep_penalty_bps" json:"steep_penalty_bps"`
	Soil             []FarmingSoilDef `yaml:"soil" json:"soil"`
	// Branches ties each farm to the water work it draws from; a rain-fed farm has none.
	Branches []FarmingBranchDef `yaml:"branches" json:"branches"`
	// Toll is the miller's toll the settlement sets, in basis points of the grain (statute: 1/30 to 1/10).
	Toll FarmingToll `yaml:"toll" json:"toll"`
	// Pasture is what a grazing farm needs of the land.
	Pasture FarmingPasture `yaml:"pasture" json:"pasture"`
}

// FarmingPair is a figure for irrigated and for rain-fed fields.
type FarmingPair struct {
	Irrigated int64 `yaml:"irrigated" json:"irrigated"`
	Rainfed   int64 `yaml:"rainfed" json:"rainfed"`
}

// FarmingWater is the water factor and the reach of a water work.
type FarmingWater struct {
	ServedBPS       int `yaml:"served_bps" json:"served_bps"`
	UnservedBPS     int `yaml:"unserved_bps" json:"unserved_bps"`
	Reach           int `yaml:"reach" json:"reach"`
	Serves          int `yaml:"serves" json:"serves"`
	MinConditionBPS int `yaml:"min_condition_bps" json:"min_condition_bps"`
}

// FarmingSoilDef is the soil factor of a biome.
type FarmingSoilDef struct {
	Biome string `yaml:"biome" json:"biome"`
	BPS   int    `yaml:"bps" json:"bps"`
}

// FarmingBranchDef ties a farm building to the water work it draws from (Work empty and Rainfed true: it needs none).
type FarmingBranchDef struct {
	Farm    string `yaml:"farm" json:"farm"`
	Work    string `yaml:"work,omitempty" json:"work,omitempty"`
	Rainfed bool   `yaml:"rainfed,omitempty" json:"rainfed,omitempty"`
}

// FarmingToll is the statute of the miller's toll.
type FarmingToll struct {
	MinBPS     int `yaml:"min_bps" json:"min_bps"`
	MaxBPS     int `yaml:"max_bps" json:"max_bps"`
	DefaultBPS int `yaml:"default_bps" json:"default_bps"`
}

// FarmingPasture is what a grazing farm needs: open lots (no tree, no rock, no building) within Radius of it.
type FarmingPasture struct {
	GrazingLots int `yaml:"grazing_lots" json:"grazing_lots"`
	Radius      int `yaml:"radius" json:"radius"`
}

// Cycle converts the numbers to the pure package's config; the pack has been validated.
func (d FarmingDef) Cycle() farm.Config {
	grow, _ := optionalDuration(d.Grow)
	window, _ := optionalDuration(d.Window)
	rot, _ := optionalDuration(d.RotStep)
	return farm.Config{
		SowShifts: d.SowShifts, TendMax: d.TendMax, HarvestShifts: d.HarvestShifts, TendBPS: int64(d.TendBPS),
		Grow: grow, Window: window, RotStep: rot, RotStepBPS: int64(d.RotStepBPS),
		SeedIrrigated: d.Seed.Irrigated, SeedRainfed: d.Seed.Rainfed, BaseIrrigated: d.Base.Irrigated, BaseRainfed: d.Base.Rainfed,
		WaterServedBPS: int64(d.Water.ServedBPS), WaterUnservedBPS: int64(d.Water.UnservedBPS),
		Reach: d.Water.Reach, Serves: d.Water.Serves, MinConditionBPS: int64(d.Water.MinConditionBPS),
	}
}

// SoilBPS is the soil factor of a biome.
func (d FarmingDef) SoilBPS(biome string) int64 {
	for _, s := range d.Soil {
		if s.Biome == biome {
			return int64(s.BPS)
		}
	}
	return int64(d.SoilDefaultBPS)
}

// BranchOf is the branch a farm building belongs to.
func (d FarmingDef) BranchOf(farmCode string) (FarmingBranchDef, bool) {
	for _, b := range d.Branches {
		if b.Farm == farmCode {
			return b, true
		}
	}
	return FarmingBranchDef{}, false
}

// WorksFor lists the water work codes a branch draws from (empty for a rain-fed farm).
func (d FarmingDef) WorksFor(farmCode string) []string {
	if b, ok := d.BranchOf(farmCode); ok && b.Work != "" {
		return []string{b.Work}
	}
	return nil
}
