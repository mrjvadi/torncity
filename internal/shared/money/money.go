// Package money represents currency amounts as integer minor units.
//
// There is deliberately no float constructor and no float accessor. Every
// monetary value in this system is an exact integer count of the smallest
// currency unit; floating point cannot represent that exactly and would let
// rounding error accumulate across the ledger.
package money

import (
	"errors"
	"fmt"
	"math"
)

// ErrOverflow is returned when an operation would exceed the range of int64.
var ErrOverflow = errors.New("money: amount overflows int64")

// Amount is a quantity of money in minor units (for example, rial).
type Amount struct {
	minor int64
}

// FromMinor builds an Amount from a count of minor units.
func FromMinor(v int64) Amount { return Amount{minor: v} }

// Minor returns the raw count of minor units.
func (a Amount) Minor() int64 { return a.minor }

// IsZero reports whether the amount is exactly zero.
func (a Amount) IsZero() bool { return a.minor == 0 }

// IsNegative reports whether the amount is below zero. Negative amounts are
// representable on purpose: a ledger entry's debit side is negative.
func (a Amount) IsNegative() bool { return a.minor < 0 }

// Add returns a+b, or ErrOverflow if the result does not fit in int64.
func (a Amount) Add(b Amount) (Amount, error) {
	if (b.minor > 0 && a.minor > math.MaxInt64-b.minor) ||
		(b.minor < 0 && a.minor < math.MinInt64-b.minor) {
		return Amount{}, ErrOverflow
	}
	return Amount{minor: a.minor + b.minor}, nil
}

// Sub returns a-b, or ErrOverflow if the result does not fit in int64.
func (a Amount) Sub(b Amount) (Amount, error) {
	if b.minor == math.MinInt64 {
		return Amount{}, ErrOverflow
	}
	return a.Add(Amount{minor: -b.minor})
}

// Neg returns -a.
func (a Amount) Neg() (Amount, error) {
	if a.minor == math.MinInt64 {
		return Amount{}, ErrOverflow
	}
	return Amount{minor: -a.minor}, nil
}

func (a Amount) String() string { return fmt.Sprintf("%d", a.minor) }

// Sum adds every amount, failing on overflow. An empty sum is zero.
//
// This is the operation the ledger invariant is checked with: the entries of
// one transaction must Sum to zero.
func Sum(amounts ...Amount) (Amount, error) {
	var total Amount
	for _, x := range amounts {
		var err error
		if total, err = total.Add(x); err != nil {
			return Amount{}, err
		}
	}
	return total, nil
}
