// Package moneyvalue is how a settlement's money is read against the neutral
// currency and against Nil (docs/adr/0046-bags-merchants-currency-exchange.md
// section 7): the market rate, the quote in Nil, and the basket reading of what
// the money buys.
//
// EVERYTHING HERE IS A DISPLAY. Nothing in this package moves money, creates a
// balance or converts anything: Nil never becomes settlement money (ADR 0029
// 10.4). The quote answers "how much is this worth in Nil" and the basket
// answers "what do the village's prices say against the reference prices".
//
// Rates are fixed point: a rate of 1.0 is Micro (1 000 000). Amounts are in
// minor units, like the ledger's.
package moneyvalue

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// Micro is the fixed-point scale of a rate: 1.0 is Micro.
const Micro = 1_000_000

// BPS is one hundred percent in basis points.
const BPS = 10_000

// Failures.
var (
	// ErrNoRate means a rate needed to quote is zero or missing.
	ErrNoRate = errors.New("moneyvalue: no rate to quote with")
	// ErrOverflow means an amount times a rate does not fit.
	ErrOverflow = errors.New("moneyvalue: amount out of range")
)

// mulDiv returns floor(a x b / c) in 128 bits.
func mulDiv(a, b, c int64) (int64, error) {
	if a < 0 || b < 0 || c <= 0 {
		return 0, ErrNoRate
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 0, ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(c))
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(q), nil
}

// SupPerUnit is what one unit of a village currency is worth in the neutral
// currency: x_ref / r0 (ADR 0046 7.1). xrefMicro is the market rate (the book's
// reference rate) and r0Micro the charter rate, both in micro-units; the answer
// is in micro-units of the neutral currency per unit of the village currency.
func SupPerUnit(xrefMicro, r0Micro int64) (int64, error) {
	if xrefMicro <= 0 || r0Micro <= 0 {
		return 0, ErrNoRate
	}
	return mulDiv(xrefMicro, Micro, r0Micro)
}

// NilPerUnit is the quote of ADR 0046 7.4: nil_per_unit = (x_ref / r0) /
// nil_unit_sup, in micro-Nil per unit of the currency. nilUnitSup is how many
// neutral-currency units one Nil stands for. For the neutral currency itself
// x_ref = r0, so a unit is worth 1 / nil_unit_sup Nil.
func NilPerUnit(xrefMicro, r0Micro, nilUnitSup int64) (int64, error) {
	if nilUnitSup <= 0 {
		return 0, ErrNoRate
	}
	sup, err := SupPerUnit(xrefMicro, r0Micro)
	if err != nil {
		return 0, err
	}
	return sup / nilUnitSup, nil
}

// NeutralNilPerUnit is the quote of the neutral currency: 1 / nil_unit_sup Nil
// per unit, in micro-Nil.
func NeutralNilPerUnit(nilUnitSup int64) (int64, error) { return NilPerUnit(Micro, Micro, nilUnitSup) }

// NilOf is an amount of a currency in micro-Nil at a quote (micro-Nil per unit),
// rounded down.
func NilOf(amount, nilPerUnitMicro int64) (int64, error) {
	if amount < 0 || nilPerUnitMicro < 0 {
		return 0, ErrNoRate
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(nilPerUnitMicro))
	if hi != 0 || lo > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(lo), nil
}

// UnitsOfNil is how many whole units of a currency a Nil price comes to at a
// quote, rounded UP (the player sees what the Nil price costs in the village's
// money as information; a price is never rounded in the buyer's favour).
// nilMicro is the Nil amount in micro-Nil.
func UnitsOfNil(nilMicro, nilPerUnitMicro int64) (int64, error) {
	if nilMicro < 0 || nilPerUnitMicro <= 0 {
		return 0, ErrNoRate
	}
	q, err := mulDiv(nilMicro, 1, nilPerUnitMicro)
	if err != nil {
		return 0, err
	}
	if q*nilPerUnitMicro < nilMicro {
		q++
	}
	return q, nil
}

// BasketLine is one good of the fixed basket: how many a resident draws, what
// its reference price is, and what the village charges for it now.
type BasketLine struct {
	// Weight is the units per resident per week, in thousandths.
	WeightMilli int64
	// Ref is the reference price of a unit; Price what the village charges
	// now, 0 when the good is not on the village's shelf today.
	Ref, Price int64
}

// BasketReading is what a basket says about a village's prices.
type BasketReading struct {
	// IndexBPS is what the basket costs in the village as a share of what it
	// costs at the reference prices, over the lines that are on the shelf:
	// 10000 means prices at the reference, 11000 ten percent over. 0 when no
	// line of the basket is on the shelf.
	IndexBPS int64
	// CoverBPS is the share of the basket, at reference prices, that is on the
	// shelf today.
	CoverBPS int64
	// RefCost and Cost are the cost of one resident-week of the lines on the
	// shelf at the reference and at the village's prices, minor units x1000.
	RefCost, Cost int64
}

// ReadBasket reads a basket. A line off the shelf is left out of the index
// (there is no price to compare) and counts against the cover.
func ReadBasket(lines []BasketLine) BasketReading {
	var total, shelfRef, shelfCost int64
	for _, l := range lines {
		total += l.WeightMilli * l.Ref
		if l.Price <= 0 {
			continue
		}
		shelfRef += l.WeightMilli * l.Ref
		shelfCost += l.WeightMilli * l.Price
	}
	var r BasketReading
	r.RefCost, r.Cost = shelfRef, shelfCost
	if shelfRef > 0 {
		r.IndexBPS = shelfCost * BPS / shelfRef
	}
	if total > 0 {
		r.CoverBPS = shelfRef * BPS / total
	}
	return r
}

// PPPRate is the rate a currency would have if it bought what the neutral
// currency buys (ADR 0046 7.3): x_ppp = basket_ref_sup x r0 / basket_vc. Here
// the basket costs refCost at the reference prices in the neutral currency and
// cost in the village's currency. It exists for the day a village has its own
// currency; micro-units.
func PPPRate(refCost, r0Micro, cost int64) (int64, error) {
	if refCost <= 0 || r0Micro <= 0 || cost <= 0 {
		return 0, ErrNoRate
	}
	hi, lo := bits.Mul64(uint64(refCost), uint64(r0Micro))
	if hi >= uint64(cost) {
		return 0, ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(cost))
	if q > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(q), nil
}

// GapBPS is how far the market rate stands over (positive) or under (negative)
// the purchasing-power rate, in basis points: x_ref / x_ppp - 1.
func GapBPS(xrefMicro, xpppMicro int64) (int64, error) {
	if xrefMicro <= 0 || xpppMicro <= 0 {
		return 0, ErrNoRate
	}
	v, err := mulDiv(xrefMicro, BPS, xpppMicro)
	if err != nil {
		return 0, err
	}
	return v - BPS, nil
}

// String spells a micro rate as a decimal for logs and tests.
func String(micro int64) string { return fmt.Sprintf("%d.%06d", micro/Micro, micro%Micro) }
