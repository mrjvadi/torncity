package market

import (
	"fmt"
	"math"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// mulQtyPrice returns qty × price, or an error wrapping money.ErrOverflow if
// the product does not fit in int64. Both operands must be non-negative;
// quantities and prices in this package never are negative once validated.
//
// The money package deliberately has no multiplication (an amount times an
// amount is not an amount), so the one product this package needs — a count
// of units times a price per unit — is checked here.
func mulQtyPrice(qty int64, price money.Amount) (money.Amount, error) {
	p := price.Minor()
	if qty < 0 || p < 0 {
		return money.Amount{}, fmt.Errorf("market: negative operand %d × %d", qty, p)
	}
	if qty == 0 || p == 0 {
		return money.FromMinor(0), nil
	}
	if qty > math.MaxInt64/p {
		return money.Amount{}, fmt.Errorf("market: %d × %d: %w", qty, p, money.ErrOverflow)
	}
	return money.FromMinor(qty * p), nil
}
