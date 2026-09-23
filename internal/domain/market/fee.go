package market

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// MaxFeeBps is the largest meaningful fee rate: 10000 basis points is 100% of
// the notional. It is a bound on the input, not a rate; the rate itself is
// the market_fee lever (ADR 0009, ADR 0015) and always arrives as an argument.
const MaxFeeBps = 10000

// Fee returns the fee on trade at feeBps basis points of its notional.
//
// WHO PAYS: THE SELLER, out of the proceeds. The buyer pays exactly the
// notional and the seller receives the notional minus the fee (see Settle).
// Charging the seller was chosen because:
//
//   - a buy order's escrow is then exactly remaining × limit price, with no
//     fee term to over-reserve and later refund, and a change of fee rate
//     between placing an order and filling it cannot leave a buyer's escrow
//     short;
//   - with the rate capped at 100% the fee can never exceed the proceeds it
//     is taken from, so the seller needs no money escrow at all — only the
//     asset;
//   - trades keeps a single fee column, which fits one payer.
//
// ROUNDING: UP, to the next whole minor unit. Any trade with a non-zero rate
// and a non-zero notional pays at least one minor unit, so splitting an order
// into many tiny fills cannot round the fee away to nothing. The cost to the
// seller is under one minor unit per trade. Rounding up cannot push the fee
// past the notional, because at most MaxFeeBps the exact fee is already at
// most the notional, and the notional is a whole number.
//
// The arithmetic is integer only and cannot overflow for any notional that
// fits in int64: the notional is split as q×10000 + r, so the fee is
// q×bps + ceil(r×bps/10000), where q×bps ≤ notional and r×bps < 10^8.
func Fee(trade Trade, feeBps int64) (money.Amount, error) {
	if feeBps < 0 || feeBps > MaxFeeBps {
		return money.Amount{}, fmt.Errorf("%w: %d bps", ErrInvalidFeeRate, feeBps)
	}
	n := trade.Notional.Minor()
	if n < 0 {
		return money.Amount{}, fmt.Errorf("%w: negative notional %d", ErrInvalidOrder, n)
	}
	q, r := n/MaxFeeBps, n%MaxFeeBps
	fee := q*feeBps + (r*feeBps+MaxFeeBps-1)/MaxFeeBps
	return money.FromMinor(fee), nil
}

// Settlement is the money side of one trade.
type Settlement struct {
	// BuyerPays is taken from the buyer's escrow: the notional.
	BuyerPays money.Amount
	// SellerReceives is credited to the seller: the notional minus the fee.
	SellerReceives money.Amount
	// Fee goes to the market-fee sink (ADR 0008, ADR 0009).
	Fee money.Amount
}

// Settle splits a trade's notional between seller and fee at feeBps.
//
// It checks money conservation before returning: BuyerPays must equal
// SellerReceives + Fee exactly. That is the invariant the ledger transaction
// for the trade is built from — its entries sum to zero — so a settlement
// that failed it could not be booked.
func Settle(trade Trade, feeBps int64) (Settlement, error) {
	fee, err := Fee(trade, feeBps)
	if err != nil {
		return Settlement{}, err
	}
	proceeds, err := trade.Notional.Sub(fee)
	if err != nil {
		return Settlement{}, err
	}
	s := Settlement{BuyerPays: trade.Notional, SellerReceives: proceeds, Fee: fee}
	if err := s.check(); err != nil {
		return Settlement{}, err
	}
	return s, nil
}

// check verifies conservation and that nobody receives a negative amount.
func (s Settlement) check() error {
	if s.Fee.IsNegative() || s.SellerReceives.IsNegative() || s.BuyerPays.IsNegative() {
		return fmt.Errorf("%w: negative leg in settlement %+v", ErrInvariant, s)
	}
	out, err := money.Sum(s.SellerReceives, s.Fee)
	if err != nil {
		return err
	}
	if out != s.BuyerPays {
		return fmt.Errorf("%w: buyer pays %s but seller receives %s and fee is %s",
			ErrInvariant, s.BuyerPays, s.SellerReceives, s.Fee)
	}
	return nil
}
