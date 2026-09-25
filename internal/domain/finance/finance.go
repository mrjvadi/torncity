// Package finance holds the rules of the game's finance (docs/adr/0026): a
// player's credit score, what a loan costs and how its instalments are
// collected, what an insurance claim pays, how the gold dealer's price walks
// on the game clock, and the rules of the stock exchange — who may list a
// company, how a dividend divides, and when a holder takes control.
//
// Every number here is CONTENT (configs/content/finance.yml) or a lever
// value (the central banker's policy rate) handed in by the layer above; the
// formulas are the code. The package reads no clock, draws no random number
// of its own and moves no money: the application layer books what these
// functions decide through the ledger.
//
// MONEY IS INTEGER MINOR UNITS. Every proportion is taken with mulDiv, exact
// in 128 bits, and a product that does not fit is refused, never wrapped.
package finance

import (
	"errors"
	"math"
	"math/bits"
)

// ErrInvalid means rules content cannot use.
var ErrInvalid = errors.New("finance: invalid rules")

// ErrArithmetic means a proportion was asked of a negative quantity or over a
// non-positive denominator, or its result does not fit in int64.
var ErrArithmetic = errors.New("finance: arithmetic out of range")

// BPSWhole is one hundred percent in basis points.
const BPSWhole = 10_000

// mulDiv returns floor(a * num / den) for a, num >= 0 and den > 0, exactly.
func mulDiv(a, num, den int64) (int64, error) {
	if a < 0 || num < 0 || den <= 0 {
		return 0, ErrArithmetic
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		return 0, ErrArithmetic
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return 0, ErrArithmetic
	}
	return int64(q), nil
}

// mulDivCeil is mulDiv rounded up.
func mulDivCeil(a, num, den int64) (int64, error) {
	q, err := mulDiv(a, num, den)
	if err != nil {
		return 0, err
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	_, rem := bits.Div64(hi, lo, uint64(den))
	if rem != 0 {
		if q == math.MaxInt64 {
			return 0, ErrArithmetic
		}
		q++
	}
	return q, nil
}

// OfBPS is floor(amount × bps / 10000), zero for anything negative.
func OfBPS(amount int64, bps int64) int64 {
	if amount <= 0 || bps <= 0 {
		return 0
	}
	v, err := mulDiv(amount, bps, BPSWhole)
	if err != nil {
		return math.MaxInt64
	}
	return v
}

// clamp holds v within lo..hi.
func clamp(v, lo, hi int64) int64 { return min(max(v, lo), hi) }
