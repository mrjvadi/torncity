package job

import (
	"errors"
	"math"
	"math/bits"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// errArithmetic means a proportion was asked of a negative quantity or over a
// non-positive denominator. It is internal: every caller validates its inputs
// first, so reaching it is a bug in this package, and it is surfaced rather
// than hidden so a test catches that bug.
var errArithmetic = errors.New("job: arithmetic on invalid operands")

// bpsWhole is one hundred percent in basis points.
const bpsWhole = 10_000

// mulDiv returns floor(a * num / den) for a, num >= 0 and den > 0, exactly.
//
// The product is formed in 128 bits, so it never overflows however large a and
// num are; only a quotient that itself exceeds int64 is refused, with
// money.ErrOverflow. That is why pay, tax and proration here need no bounds of
// their own to be correct: the content bounds exist to catch typos, not to keep
// this arithmetic safe.
//
// Flooring is the rounding rule everywhere this is used, and each caller
// documents who that favours.
func mulDiv(a, num, den int64) (int64, error) {
	if a < 0 || num < 0 || den <= 0 {
		return 0, errArithmetic
	}
	hi, lo := bits.Mul64(uint64(a), uint64(num))
	if hi >= uint64(den) {
		// The quotient would not fit in 64 bits (and bits.Div64 would panic).
		return 0, money.ErrOverflow
	}
	q, _ := bits.Div64(hi, lo, uint64(den))
	if q > math.MaxInt64 {
		return 0, money.ErrOverflow
	}
	return int64(q), nil
}
