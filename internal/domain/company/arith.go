package company

import (
	"errors"
	"math"
	"math/bits"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// bpsWhole is one hundred percent in basis points.
const bpsWhole = 10_000

// errArithmetic means a proportion was asked of a negative quantity or over a
// non-positive denominator: a bug in this package, surfaced so a test sees it.
var errArithmetic = errors.New("company: arithmetic on invalid operands")

// mulDiv returns floor(a * num / den) for a, num >= 0 and den > 0, exactly:
// the product is formed in 128 bits, and only a quotient beyond int64 is
// refused, with money.ErrOverflow.
func mulDiv(a, num, den int64) (int64, error) {
	if a < 0 || num < 0 || den <= 0 {
		return 0, errArithmetic
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		return 0, money.ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return 0, money.ErrOverflow
	}
	return int64(q), nil
}

// bps returns floor(a * rate / 10000) for a money amount.
func bps(a money.Amount, rate int) (money.Amount, error) {
	if rate < 0 || rate > bpsWhole {
		return money.Amount{}, ErrInvalidTaxRate
	}
	v, err := mulDiv(a.Minor(), int64(rate), bpsWhole)
	if err != nil {
		return money.Amount{}, err
	}
	return money.FromMinor(v), nil
}
