// Package reserve is the arithmetic of a settlement money's reserve and macroeconomy (docs/adr/0033 sections
// 6.6, 6.7, 6.13 and 7): the excess over the backing basis, the coverage, the limits of the head's
// intervention, a holder's share of the pot in a wind-down, and the price indices of the macro tick. It is
// pure: integers only, no clock, no storage.
package reserve

import "math/bits"

// BPS is the scale of basis points; PPM of parts per million.
const (
	BPS = 10_000
	PPM = 1_000_000
)

func mulDiv(a, b, c int64) (q int64, ok bool) {
	if a < 0 || b < 0 || c <= 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 0, false
	}
	qq, _ := bits.Div64(hi, lo, uint64(c))
	if qq > uint64(1<<63-1) {
		return 0, false
	}
	return int64(qq), true
}

// Floor is floor(a*b/c), 0 on overflow or a bad divisor.
func Floor(a, b, c int64) int64 { q, _ := mulDiv(a, b, c); return q }

// Excess is the part of the pot above the backing basis: the only part the head may withdraw (ADR 0033 6.1).
func Excess(pot, basis int64) int64 {
	if pot <= basis {
		return 0
	}
	return pot - basis
}

// MarketCap is the market value of the supply in SUP: supply x x_ref / r0 (ADR 0033 6.6), x_ref in ppm.
func MarketCap(supply, xRefPPM, r0 int64) int64 {
	if r0 <= 0 {
		return 0
	}
	return Floor(supply, xRefPPM, r0*PPM)
}

// Coverage is the pot's value over the market value of the supply, in basis points; ok is false when
// there is no supply to cover. Informational: nobody is owed it.
func Coverage(pot, supply, xRefPPM, r0 int64) (bps int64, ok bool) {
	mc := MarketCap(supply, xRefPPM, r0)
	if mc <= 0 {
		return 0, false
	}
	return Floor(pot, BPS, mc), true
}

// BuyBudget is the most SUP a head's purchase on the book may take from the pot now (ADR 0033 6.7): at
// most capBPS of the pot per period (counting what the period already used), and never so much that the
// pot falls below floorBPS of the basis. Zero when nothing may be spent.
func BuyBudget(pot, basis, usedThisPeriod, capBPS, floorBPS int64) int64 {
	periodCap := Floor(pot+usedThisPeriod, capBPS, BPS) - usedThisPeriod
	floor := Floor(basis, floorBPS, BPS)
	room := pot - floor
	return max(min(periodCap, room), 0)
}

// SellBudget is the most units the treasury may offer on the book now: at most capBPS of the units it holds
// (counting what the period already offered).
func SellBudget(held, usedThisPeriod, capBPS int64) int64 {
	return max(Floor(held+usedThisPeriod, capBPS, BPS)-usedThisPeriod, 0)
}

// ClaimShare is what a holder of `units` units receives of the pot in a wind-down: the remaining pot times
// their units over the units still claimable (the supply without the treasury's own), rounded down. Taking
// each claim from what is left makes the shares equal whatever the order of the claims.
func ClaimShare(potRemaining, claimable, units int64) int64 {
	if claimable <= 0 || units <= 0 || units > claimable {
		return 0
	}
	return Floor(potRemaining, units, claimable)
}

// Macro is what one macro tick reads and carries (ADR 0033 7.1, 7.3).
type Macro struct {
	// SupplyUnits and Stabilisation are the supply and the treasury's intervention stock; M is the money in
	// circulation in SUP-equivalent at charter (units outside the stock / r0); Y the period's output in SUP.
	SupplyUnits, Stabilisation, M, Y int64
	// XRefPPM is the reference rate; PrevXRefPPM the one at the previous tick.
	XRefPPM, PrevXRefPPM int64
	// Tradable, NonTradable and Price are the cumulative indices before this tick (ppm, 1000000 is 1.00).
	Tradable, NonTradable, Price int64
	// PrevSupply is the supply at the previous tick, for its growth.
	PrevSupply int64
}

// MacroRules are the constants of the model (ADR 0033 7.10).
type MacroRules struct {
	// MNormBPS is the stock of money, in periods of output, a village needs to trade (m_norm, 2000 is 0.2);
	// KappaBPS how far a gap moves local prices; PiMaxBPS the per-period bound of local inflation;
	// WTradableBPS the weight of tradable goods in the basket.
	MNormBPS, KappaBPS, PiMaxBPS, WTradableBPS int64
}

// Reading is the macro tick's result.
type Reading struct {
	// Tradable is the tradable price index: 1/x relative to charter, cumulative (a tradable good costs the
	// same in SUP everywhere, so its price in the money moves with r = 1/x).
	Tradable int64
	// PiLocalBPS is the period's local inflation of non-tradables; NonTradable its cumulative index.
	PiLocalBPS, NonTradable int64
	// Price is the basket index: the weighted mix of the two, applied to last period's.
	Price int64
	// SupplyGrowthBPS is the supply's growth since the previous tick, can be negative.
	SupplyGrowthBPS int64
}

func clamp(v, lo, hi int64) int64 { return max(lo, min(v, hi)) }

// Tick computes one period's reading. The tradable index follows the reference rate directly; the local
// inflation is pi = clamp(kappa x gap, -piMax, +piMax) with gap = M x x / (m_norm x Y) - 1, where x is
// x_ref (the policy-rate term of the ADR is zero while the rate is not yet a lever: the neutral rate
// applies). With no measured output (Y = 0) there is no gap and local inflation is zero.
func Tick(m Macro, r MacroRules) Reading {
	var out Reading
	if m.XRefPPM <= 0 || m.PrevXRefPPM <= 0 {
		return Reading{Tradable: m.Tradable, NonTradable: m.NonTradable, Price: m.Price}
	}
	// r = 1/x: the tradable index relative to charter, kept as the cumulative product of r_t / r_{t-1}
	rRatio := Floor(PPM, m.PrevXRefPPM, m.XRefPPM) // r_t / r_{t-1} = x_{t-1} / x_t, in ppm
	out.Tradable = Floor(m.Tradable, rRatio, PPM)
	if m.Y > 0 && r.MNormBPS > 0 {
		// gap in ppm: (M x / (m_norm Y)) - 1
		num := Floor(m.M, m.XRefPPM, PPM) // M x, SUP
		den := Floor(m.Y, r.MNormBPS, BPS)
		if den > 0 {
			gapPPM := Floor(num, PPM, den) - PPM
			out.PiLocalBPS = clamp(gapPPM*r.KappaBPS/PPM, -r.PiMaxBPS, r.PiMaxBPS)
		}
	}
	out.NonTradable = Floor(m.NonTradable, PPM+out.PiLocalBPS*100, PPM)
	if out.NonTradable <= 0 {
		out.NonTradable = m.NonTradable
	}
	mix := Floor(r.WTradableBPS, rRatio, BPS) + Floor(BPS-r.WTradableBPS, PPM+out.PiLocalBPS*100, BPS)
	out.Price = Floor(m.Price, mix, PPM)
	if out.Price <= 0 {
		out.Price = m.Price
	}
	if m.PrevSupply > 0 {
		out.SupplyGrowthBPS = (m.SupplyUnits - m.PrevSupply) * BPS / m.PrevSupply
	}
	return out
}
