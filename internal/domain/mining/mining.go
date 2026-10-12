// Package mining holds the pure rules of deposits and mines (docs/adr/0075-mining-rail-and-ratios.md): what share of a deposit a
// method of mining reaches, which grade band an ore belongs to, how a face thins and runs out, what a hoist and a crew of haulers
// can lift, and when a level is unsafe and closes. It reads no database and no clock; every figure arrives in Rules (config
// mining.*), so every replica reaches the same answer.
package mining

import "math"

// BPS is one whole, in basis points.
const BPS = 10_000

// Band is the grade band of an ore: the world gives every deposit a grade in permille, three bands name how rich it is.
type Band string

// The bands. Medium is the ore the game has always had (item iron_ore).
const (
	BandLean   Band = "lean"
	BandMedium Band = "medium"
	BandRich   Band = "rich"
)

// Bands lists them from poorest to richest.
var Bands = []Band{BandLean, BandMedium, BandRich}

// Depth bands (ADR 0075 4.1): 0 is the surface (a pit, to 3 m), 1 a timbered shaft with an adit (to 30 m), 2 a ventilated and
// pumped second level (to 60 m), 3 the deeper ground that waits for steam and is not reachable in the first release.
const (
	DepthSurface = 0
	DepthShaft1  = 1
	DepthShaft2  = 2
	DepthDeep    = 3
	// ReachableDepths is how many depth bands the first release can open (0..2).
	ReachableDepths = 3
)

// Rules are the tuning of deposits and mines (config mining.*).
type Rules struct {
	// ReserveDivisor scales the world's abstract reserve down to units a settlement can dig (capacity = reserve / divisor).
	ReserveDivisor int64
	// ShareBPS is the share of the capacity each depth band holds (surface, shaft1, shaft2, deep); they add up to BPS.
	ShareBPS [4]int64
	// ThinBelowBPS: below this share of the reachable ore left the yield falls linearly to ThinFloorBPS.
	ThinBelowBPS, ThinFloorBPS int64
	// LeanBelow and RichFrom are the band bounds in permille of grade: below LeanBelow lean, from RichFrom rich.
	LeanBelow, RichFrom int32
	// OutBPS is the output factor of each band, indexed lean, medium, rich.
	OutBPS [3]int64
	// DepthM is the depth of the bottom of each depth band, metres.
	DepthM [4]int64
	// HoistKgMin is what a hoist lifts a minute at the surface: windlass, horse gin. HoistDepthRefM derates it with depth.
	HoistKgMin     [2]int64
	HoistDepthRefM int64
	// HaulerKgMin is what one hauler carries a minute by hand cart.
	HaulerKgMin int64
	// UnmadeWayBPS is the pace of a hauler on ground with no road.
	UnmadeWayBPS int64
	// AditGradient is the fall of an adit: metres of length per metre of fall (150 is 1:150).
	AditGradient int64
	// MinConditionBPS: a level whose props stand below this is unsafe and closes. WaterLimitBPS: above it the level is flooded.
	MinConditionBPS, WaterLimitBPS int64
}

// Normalise fills the zero values with the safe defaults, so a wiring that sets nothing still computes.
func (r Rules) Normalise() Rules {
	if r.ReserveDivisor <= 0 {
		r.ReserveDivisor = 1
	}
	if r.ShareBPS == [4]int64{} {
		r.ShareBPS = [4]int64{500, 2500, 4000, 3000}
	}
	if r.ThinBelowBPS <= 0 {
		r.ThinBelowBPS = 2000
	}
	if r.OutBPS == [3]int64{} {
		r.OutBPS = [3]int64{8000, 10000, 12000}
	}
	if r.DepthM == [4]int64{} {
		r.DepthM = [4]int64{3, 30, 60, 90}
	}
	if r.HoistDepthRefM <= 0 {
		r.HoistDepthRefM = 30
	}
	if r.AditGradient <= 0 {
		r.AditGradient = 150
	}
	return r
}

// BandOf is the grade band of a grade in permille.
func (r Rules) BandOf(gradePermille int32) Band {
	switch {
	case gradePermille < r.LeanBelow:
		return BandLean
	case gradePermille >= r.RichFrom:
		return BandRich
	}
	return BandMedium
}

// BandOutBPS is the output factor of a band.
func (r Rules) BandOutBPS(b Band) int64 {
	switch b {
	case BandLean:
		return r.OutBPS[0]
	case BandRich:
		return r.OutBPS[2]
	}
	return r.OutBPS[1]
}

// Capacity is the ore a deposit holds in game units.
func (r Rules) Capacity(reserve int64) int64 {
	if reserve <= 0 {
		return 0
	}
	return reserve / max(r.ReserveDivisor, 1)
}

// Cumulative is the share of the capacity reached down to a depth band, inclusive, in basis points. A band beyond the first
// release is not reached.
func (r Rules) Cumulative(depth int) int64 {
	var sum int64
	for i := 0; i <= min(depth, ReachableDepths-1); i++ {
		sum += r.ShareBPS[i]
	}
	return min(sum, BPS)
}

// Reachable is the ore a method that reaches down to a depth band can ever take from a deposit.
func (r Rules) Reachable(reserve int64, depth int) int64 {
	return r.Capacity(reserve) * r.Cumulative(depth) / BPS
}

// Remaining is the ore still to take within a depth, never negative.
func (r Rules) Remaining(reserve, taken int64, depth int) int64 {
	return max(r.Reachable(reserve, depth)-taken, 0)
}

// Thin is the yield factor of a face: whole while the face is full, falling in a straight line to the floor as the reachable
// ore left drops under ThinBelowBPS, and nothing once none is left. It has no step but the last (at zero).
func (r Rules) Thin(remaining, reachable int64) int64 {
	if remaining <= 0 || reachable <= 0 {
		return 0
	}
	left := remaining * BPS / reachable
	if left >= r.ThinBelowBPS {
		return BPS
	}
	return r.ThinFloorBPS + (BPS-r.ThinFloorBPS)*left/max(r.ThinBelowBPS, 1)
}

// Grant is how much of a wish a face gives: never more than is left within the depth.
func (r Rules) Grant(want, reserve, taken int64, depth int) int64 {
	return min(max(want, 0), r.Remaining(reserve, taken, depth))
}

// Hoist kinds.
const (
	HoistWindlass = iota
	HoistGin
)

// HoistKg is what a crewed hoist lifts in a stretch of minutes from a depth. An uncrewed hoist lifts nothing.
func (r Rules) HoistKg(kind int, depthM int64, crewed bool, minutes int64) int64 {
	if !crewed || kind < 0 || kind >= len(r.HoistKgMin) {
		return 0
	}
	ref := max(r.HoistDepthRefM, 1)
	return r.HoistKgMin[kind] * minutes * ref / (ref + max(depthM, 0))
}

// HaulKg is what the haulers carry from the face to the bin in a stretch of minutes; roadBPS is the surface factor (an
// unmade way halves it).
func (r Rules) HaulKg(haulers int64, roadBPS int64, minutes int64) int64 {
	return max(haulers, 0) * r.HaulerKgMin * minutes * roadBPS / BPS
}

// Limit names what holds a mine's output down.
type Limit string

// The limits.
const (
	LimitFace  Limit = "face"
	LimitHoist Limit = "hoist"
	LimitHaul  Limit = "haul"
)

// Throughput is the kilograms a shift of the minehead delivers: the least of what the face breaks, what the hoist lifts and
// what the haulers carry, and which of them it was (the face when they tie).
func Throughput(faceKg, hoistKg, haulKg int64) (int64, Limit) {
	kg, lim := faceKg, LimitFace
	if hoistKg < kg {
		kg, lim = hoistKg, LimitHoist
	}
	if haulKg < kg {
		kg, lim = haulKg, LimitHaul
	}
	return max(kg, 0), lim
}

// Level is the state of one level of a mine.
type Level struct {
	// ConditionBPS is how sound the props stand (10000 whole). WaterBPS how full of water the level is (0 dry).
	ConditionBPS, WaterBPS int64
}

// Status of a level.
type Status string

// The statuses. A level that is not ok is closed: nobody works it, nothing is lost or destroyed.
const (
	StatusOK          Status = "ok"
	StatusNeedsRepair Status = "needs_repair"
	StatusFlooded     Status = "flooded"
)

// Check says whether a level may be worked. Flooding is judged first: a flooded level needs pumping before props matter.
func (r Rules) Check(l Level) Status {
	switch {
	case l.WaterBPS > r.WaterLimitBPS:
		return StatusFlooded
	case l.ConditionBPS < r.MinConditionBPS:
		return StatusNeedsRepair
	}
	return StatusOK
}

// Day is a day of a level's life: the props wear by wearBPS, the water rises by inflowBPS and the adit or pump takes outflowBPS
// away. Both stay in 0..BPS; the level is never removed, only closed by Check.
func Day(l Level, wearBPS, inflowBPS, outflowBPS int64) Level {
	l.ConditionBPS = clamp(l.ConditionBPS-wearBPS, 0, BPS)
	l.WaterBPS = clamp(l.WaterBPS+inflowBPS-outflowBPS, 0, BPS)
	return l
}

// Set is the work of a timberman's shift: it sets props and gives back gainBPS of condition.
func Set(l Level, gainBPS int64) Level {
	l.ConditionBPS = clamp(l.ConditionBPS+gainBPS, 0, BPS)
	return l
}

func clamp(v, lo, hi int64) int64 { return min(max(v, lo), hi) }

// AditLengthM is the length of the adit that drains a level whose floor lies depthM metres under a lot that stands fallM metres
// above the nearest outlet (the lowest water or valley floor around). The adit rises one metre in AditGradient metres, so it
// reaches the floor only when the outlet lies lower than the floor; otherwise there is no gravity drainage and only a pump
// keeps the level dry (false).
func (r Rules) AditLengthM(fallM, depthM float64) (int64, bool) {
	head := fallM - depthM
	if head <= 0 {
		return 0, false
	}
	return int64(math.Ceil(head * float64(r.AditGradient))), true
}
