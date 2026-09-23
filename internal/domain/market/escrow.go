package market

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Reservation is what an order holds in escrow: money for a buy, units of
// the asset for a sell. Exactly one of the two fields is non-zero for a
// non-empty reservation.
//
// Escrow is taken when an order is placed and must be exact (ADR 0010 §5,
// invariant 3: locked escrow equals the open orders). These functions give
// the application layer the amounts; locking and moving them is its job.
type Reservation struct {
	Money    money.Amount
	Quantity int64
}

// Reserve returns what order o must hold in escrow for its unfilled
// remainder:
//
//   - a buy reserves Remaining × UnitPrice in money. There is no fee term:
//     the fee is charged to the seller (see Fee), so the most a buy can ever
//     spend is its remainder at its own bound;
//   - a sell reserves Remaining units of the asset and no money.
//
// On placement that is the whole order. On cancel, expiry, self-trade
// removal or a market order's unfilled remainder, Reserve of the order in
// its final state is exactly the amount to release.
func Reserve(o Order) (Reservation, error) {
	if o.Side == Sell {
		return Reservation{Quantity: o.Remaining()}, nil
	}
	m, err := mulQtyPrice(o.Remaining(), o.UnitPrice)
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{Money: m}, nil
}

// FillEscrow is how one trade draws down one order's escrow.
type FillEscrow struct {
	// Consumed leaves escrow and goes to the counterparty: the notional for
	// a buy, the traded units for a sell.
	Consumed Reservation
	// Released returns to the order's owner. For a buy it is the price
	// improvement, Quantity × (limit − execution price): the order reserved
	// at its limit and traded at the resting price, which is never worse.
	// For a sell it is always zero.
	Released Reservation
}

// ReleaseOnFill returns how trade t changes the escrow of order o, which
// must be one of t's two sides.
//
// For every fill, Reserve(before) = Reserve(after) + Consumed + Released,
// field by field; the tests check that identity over random order streams.
func ReleaseOnFill(o Order, t Trade) (FillEscrow, error) {
	switch {
	case o.Side == Buy && t.BuyOrderID == o.ID:
		improvement, err := o.UnitPrice.Sub(t.UnitPrice)
		if err != nil {
			return FillEscrow{}, err
		}
		if improvement.IsNegative() {
			return FillEscrow{}, fmt.Errorf("%w: buy %s traded at %s above its bound %s",
				ErrInvariant, o.ID, t.UnitPrice, o.UnitPrice)
		}
		released, err := mulQtyPrice(t.Quantity, improvement)
		if err != nil {
			return FillEscrow{}, err
		}
		return FillEscrow{
			Consumed: Reservation{Money: t.Notional},
			Released: Reservation{Money: released},
		}, nil
	case o.Side == Sell && t.SellOrderID == o.ID:
		return FillEscrow{Consumed: Reservation{Quantity: t.Quantity}}, nil
	}
	return FillEscrow{}, fmt.Errorf("%w: order %s, trade %s/%s", ErrNotParty, o.ID, t.BuyOrderID, t.SellOrderID)
}
