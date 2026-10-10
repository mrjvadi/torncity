// Package farm is the pure part of the farm cycle (docs/adr/0067): a crop is sown, grows, ripens, is harvested, and the harvest
// is the product of the crop's base, the soil, the water and the tending. Everything here is a function of stored timestamps
// and counters, never of a ticker, so every replica reads the same stage and the same yield. No I/O, no clock, no randomness.
package farm

import (
	"sort"
	"time"
)

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Config is the crop's numbers (content farming.yml).
type Config struct {
	// SowShifts, TendMax and HarvestShifts are the work the stages ask for, in shifts of the farm.
	SowShifts, TendMax, HarvestShifts int
	// TendBPS is the yield a tending shift adds (weeding, bird scaring, water turns).
	TendBPS int64
	// Grow is how long the crop grows after the last sowing shift ends; Window how long it stays ripe before it starts to
	// spoil; a ripe crop loses RotStepBPS of its yield every RotStep past the window.
	Grow, Window, RotStep time.Duration
	RotStepBPS            int64
	// Seed and Base are wheat, for irrigated and for rain-fed fields.
	SeedIrrigated, SeedRainfed int64
	BaseIrrigated, BaseRainfed int64
	// WaterServedBPS and WaterUnservedBPS are the water factor with and without a working water work; a work serves at most
	// Serves farms within Reach lots and counts only at MinConditionBPS or better.
	WaterServedBPS, WaterUnservedBPS int64
	Reach, Serves                    int
	MinConditionBPS                  int64
}

// Stage is where a cycle stands at an instant.
type Stage string

// The stages of a crop.
const (
	// StageIdle: no open cycle; a sow order starts one.
	StageIdle Stage = "idle"
	// StageSowing: seed goes into the ground, shift by shift.
	StageSowing Stage = "sowing"
	// StageGrowing: the crop grows; tending shifts are the only work.
	StageGrowing Stage = "growing"
	// StageRipe: the crop is ready and in its window.
	StageRipe Stage = "ripe"
	// StageOverripe: the window is over and the crop spoils a step at a time.
	StageOverripe Stage = "overripe"
	// StageHarvest: harvest shifts have begun; the yield is fixed.
	StageHarvest Stage = "harvest"
	// StageHarvested: every harvest shift has started.
	StageHarvested Stage = "harvested"
	// StageRotted: the crop spoiled before anyone harvested it.
	StageRotted Stage = "rotted"
)

// Cycle is one crop on one farm, as stored.
type Cycle struct {
	ID, SettlementID, BuildingID string
	Rainfed                      bool
	// SoilBPS is the farm's soil at the order.
	SoilBPS   int64
	OrderedBy string
	OrderedAt time.Time
	// SowStarted counts the sowing shifts begun; GrowFrom is the finish of the last one (nil while sowing).
	SowStarted int
	GrowFrom   *time.Time
	Tended     int
	// WaterSum and WaterN sum the water factor sampled at every sowing and tending shift and at the first harvest shift.
	WaterSum int64
	WaterN   int
	// HarvestStarted counts the harvest shifts begun; YieldTotal is fixed at the first one.
	HarvestStarted int
	YieldTotal     int64
	// SeedSpent is the seed that went into the ground.
	SeedSpent int64
	// ClosedAt closes the cycle (a new sow order, or the rot), Result says how it ended.
	ClosedAt *time.Time
	Result   string
}

// RipeAt is the instant the crop is ripe; zero while it is still being sown.
func (c Cycle) RipeAt(cfg Config) time.Time {
	if c.GrowFrom == nil {
		return time.Time{}
	}
	return c.GrowFrom.Add(cfg.Grow)
}

// SpoilAt is the instant the window ends and the crop starts to spoil.
func (c Cycle) SpoilAt(cfg Config) time.Time {
	r := c.RipeAt(cfg)
	if r.IsZero() {
		return r
	}
	return r.Add(cfg.Window)
}

// LossBPS is the share of the yield the crop has lost to spoiling at now (never above one hundred percent).
func (c Cycle) LossBPS(cfg Config, now time.Time) int64 {
	spoil := c.SpoilAt(cfg)
	if spoil.IsZero() || !now.After(spoil) || cfg.RotStep <= 0 {
		return 0
	}
	steps := int64(now.Sub(spoil)/cfg.RotStep) + 1
	return min(steps*cfg.RotStepBPS, BPS)
}

// StageAt is the stage of the cycle at now. A nil cycle, or a closed one, is idle.
func StageAt(c *Cycle, cfg Config, now time.Time) Stage {
	if c == nil || c.ClosedAt != nil {
		return StageIdle
	}
	if c.HarvestStarted >= cfg.HarvestShifts && c.HarvestStarted > 0 {
		return StageHarvested
	}
	if c.HarvestStarted > 0 {
		return StageHarvest
	}
	if c.SowStarted < cfg.SowShifts || c.GrowFrom == nil {
		return StageSowing
	}
	switch {
	case now.Before(c.RipeAt(cfg)):
		return StageGrowing
	case !now.After(c.SpoilAt(cfg)):
		return StageRipe
	case c.LossBPS(cfg, now) >= BPS:
		return StageRotted
	}
	return StageOverripe
}

// CanOrder says whether a new sow order may open a cycle: nothing is growing or being harvested.
func CanOrder(c *Cycle, cfg Config, now time.Time) bool {
	switch StageAt(c, cfg, now) {
	case StageIdle, StageHarvested, StageRotted:
		return true
	}
	return false
}

// WaterAvg is the cycle's mean water factor; the unserved factor when nothing was sampled.
func (c Cycle) WaterAvg(cfg Config) int64 {
	if c.WaterN == 0 {
		return cfg.WaterUnservedBPS
	}
	return c.WaterSum / int64(c.WaterN)
}

// TendingBPS is the factor the tending shifts give: one hundred percent plus TendBPS for each, at most TendMax of them.
func (c Cycle) TendingBPS(cfg Config) int64 {
	return BPS + int64(min(c.Tended, cfg.TendMax))*cfg.TendBPS
}

// Seed is the wheat a sowing takes.
func (c Cycle) Seed(cfg Config) int64 {
	if c.Rainfed {
		return cfg.SeedRainfed
	}
	return cfg.SeedIrrigated
}

// Base is the crop's base yield for the field.
func (c Cycle) Base(cfg Config) int64 {
	if c.Rainfed {
		return cfg.BaseRainfed
	}
	return cfg.BaseIrrigated
}

// Yield is the whole harvest if it began at now: base x soil x water x tending, less what spoiling took. The product is
// worked in integer steps, base scaled by a thousand first so the rounding does not eat a small field.
func (c Cycle) Yield(cfg Config, now time.Time) int64 {
	y := c.Base(cfg) * 1000
	y = y * c.SoilBPS / BPS
	y = y * c.WaterAvg(cfg) / BPS
	y = y * c.TendingBPS(cfg) / BPS
	y = y * (BPS - c.LossBPS(cfg, now)) / BPS
	return y / 1000
}

// ShareOfHarvest is what harvest shift number k (from 0) of n brings in: the shares add up to the yield exactly.
func ShareOfHarvest(yield int64, k, n int) int64 {
	if n <= 0 || k < 0 || k >= n {
		return 0
	}
	return yield*int64(k+1)/int64(n) - yield*int64(k)/int64(n)
}

// Soil is the soil factor of a farm: the mean of its lots' biome factors, less the steepness penalty per steep lot (as a
// mean), never below a tenth.
func Soil(biomeBPS []int64, steep int, steepPenaltyBPS int64, def int64) int64 {
	if len(biomeBPS) == 0 {
		return def
	}
	var sum int64
	for _, b := range biomeBPS {
		sum += b
	}
	soil := sum / int64(len(biomeBPS))
	soil -= int64(steep) * steepPenaltyBPS / int64(len(biomeBPS))
	return max(soil, BPS/10)
}

// Site is a farm or a water work on the land: its corner, its size, its id.
type Site struct {
	ID   string
	X, Y int
	W, H int
}

// Gap is the Chebyshev distance between the rectangles of two sites in lots (0 when they touch or overlap).
func Gap(a, b Site) int {
	dx := max(0, a.X-(b.X+b.W-1), b.X-(a.X+a.W-1))
	dy := max(0, a.Y-(b.Y+b.H-1), b.Y-(a.Y+a.H-1))
	return max(dx, dy)
}

// Serving says which farm is served by which water work: each work, in id order, serves the nearest farms of its branch
// within reach that no earlier work serves, at most serves of them. A farm that is not in the map has no work to draw from.
func Serving(works, farms []Site, reach, serves int) map[string]string {
	works = append([]Site(nil), works...)
	sort.Slice(works, func(i, j int) bool { return works[i].ID < works[j].ID })
	out := map[string]string{}
	for _, w := range works {
		type cand struct {
			f Site
			d int
		}
		var cands []cand
		for _, f := range farms {
			if _, taken := out[f.ID]; taken {
				continue
			}
			if d := Gap(w, f); d <= reach {
				cands = append(cands, cand{f, d})
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].d != cands[j].d {
				return cands[i].d < cands[j].d
			}
			return cands[i].f.ID < cands[j].f.ID
		})
		for i := 0; i < len(cands) && i < serves; i++ {
			out[cands[i].f.ID] = w.ID
		}
	}
	return out
}

// Toll takes the miller's share of a batch in kind: qty x bps, with the fraction carried so many batches add up exactly.
// It returns the toll in whole units and the new carry.
func Toll(qty, bps, carry int64) (toll, rest int64) {
	x := qty*bps + carry
	return x / BPS, x % BPS
}
