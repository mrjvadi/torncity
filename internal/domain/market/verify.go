package market

import (
	"fmt"
	"time"
)

// Verify checks a match result against the book and incoming order it was
// computed from. Match runs it on every result; it is exported so the tests
// and any caller holding a result from elsewhere can run the same checks.
//
// Invariants:
//
//  1. Filled stays within [0, Quantity] for every order in the result, and
//     no order's Filled goes down.
//  2. Every trade is between the incoming order and one resting order of
//     the book, for a positive quantity, owned by two different owners.
//  3. Every trade executes at the resting order's price, and that price is
//     acceptable to both sides: no trade is worse than either limit.
//  4. Every trade's Notional is exactly Quantity × UnitPrice.
//  5. Quantity conservation: the sum of trade quantities equals the
//     increase in the incoming order's Filled, and also equals the total
//     increase in Filled across the resting orders — nothing is traded that
//     was not taken off one side and put on the other.
//  6. Book accounting: every resting order ends up exactly once either in
//     the returned book or in Removed, and each removal's reason matches its
//     state (filled means nothing remains, expired means expired at now, a
//     self-trade removal shares the incoming owner and did not trade).
//  7. Only a limit order rests; the returned book holds the incoming order
//     iff Rests, holds no expired and no fully filled order, and a resting
//     remainder does not cross the opposite side.
func Verify(before Book, incoming Order, now time.Time, res Result) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvariant}, args...)...)
	}

	orig := make(map[string]Order, len(before.Bids)+len(before.Asks))
	for _, side := range [][]Order{before.Bids, before.Asks} {
		for _, o := range side {
			orig[o.ID] = o
		}
	}

	// 1. Fill bounds on everything the result reports.
	checkFill := func(o Order) error {
		if o.Filled < 0 || o.Filled > o.Quantity {
			return fail("order %s filled %d outside [0, %d]", o.ID, o.Filled, o.Quantity)
		}
		if prev, ok := orig[o.ID]; ok && o.Filled < prev.Filled {
			return fail("order %s filled went down from %d to %d", o.ID, prev.Filled, o.Filled)
		}
		return nil
	}
	if res.Incoming.ID != incoming.ID {
		return fail("result is for order %s, not %s", res.Incoming.ID, incoming.ID)
	}
	if err := checkFill(res.Incoming); err != nil {
		return err
	}
	if res.Incoming.Filled < incoming.Filled {
		return fail("incoming filled went down from %d to %d", incoming.Filled, res.Incoming.Filled)
	}

	// 2–4. Trades.
	tradedByResting := make(map[string]int64)
	var traded int64
	for i, t := range res.Trades {
		if t.Quantity <= 0 {
			return fail("trade %d has quantity %d", i, t.Quantity)
		}
		if t.Asset != before.Asset {
			return fail("trade %d is for another asset", i)
		}
		if t.Buyer == t.Seller {
			return fail("trade %d is a self-trade by %s", i, t.Buyer)
		}
		var restingID string
		switch incoming.Side {
		case Buy:
			if t.BuyOrderID != incoming.ID || t.Buyer != incoming.Owner {
				return fail("trade %d buy side is not the incoming order", i)
			}
			restingID = t.SellOrderID
		case Sell:
			if t.SellOrderID != incoming.ID || t.Seller != incoming.Owner {
				return fail("trade %d sell side is not the incoming order", i)
			}
			restingID = t.BuyOrderID
		}
		r, ok := orig[restingID]
		if !ok || r.Side != incoming.Side.Opposite() {
			return fail("trade %d counterparty %s is not a resting %s order", i, restingID, incoming.Side.Opposite())
		}
		if r.Expired(now) {
			return fail("trade %d filled expired order %s", i, r.ID)
		}
		if incoming.Side == Buy && t.Seller != r.Owner || incoming.Side == Sell && t.Buyer != r.Owner {
			return fail("trade %d names the wrong owner for order %s", i, r.ID)
		}
		if t.UnitPrice != r.UnitPrice {
			return fail("trade %d executed at %s, resting order %s is priced %s", i, t.UnitPrice, r.ID, r.UnitPrice)
		}
		if !incoming.acceptsPrice(t.UnitPrice) || !r.acceptsPrice(t.UnitPrice) {
			return fail("trade %d at %s is worse than a limit", i, t.UnitPrice)
		}
		n, err := mulQtyPrice(t.Quantity, t.UnitPrice)
		if err != nil {
			return err
		}
		if t.Notional != n {
			return fail("trade %d notional %s, want %s", i, t.Notional, n)
		}
		traded += t.Quantity
		tradedByResting[restingID] += t.Quantity
	}

	// 5. Quantity conservation, incoming side.
	if got := res.Incoming.Filled - incoming.Filled; got != traded {
		return fail("incoming filled rose by %d, trades total %d", got, traded)
	}

	// 6. Every original resting order is accounted for exactly once.
	final := make(map[string]Order, len(orig))
	place := func(o Order, where string) error {
		if _, dup := final[o.ID]; dup {
			return fail("order %s appears twice in the result (%s)", o.ID, where)
		}
		if _, ok := orig[o.ID]; !ok && o.ID != incoming.ID {
			return fail("order %s in the result was never on the book", o.ID)
		}
		if err := checkFill(o); err != nil {
			return err
		}
		final[o.ID] = o
		return nil
	}
	for _, rm := range res.Removed {
		o := rm.Order
		if err := place(o, "removed"); err != nil {
			return err
		}
		switch rm.Reason {
		case RemovedFilled:
			if o.Remaining() != 0 {
				return fail("order %s removed as filled with %d remaining", o.ID, o.Remaining())
			}
		case RemovedExpired:
			if !o.Expired(now) {
				return fail("order %s removed as expired but is live", o.ID)
			}
		case RemovedSelfTrade:
			if o.Owner != incoming.Owner || tradedByResting[o.ID] != 0 {
				return fail("order %s removed as self-trade wrongly", o.ID)
			}
		default:
			return fail("order %s removed for unknown reason %q", o.ID, rm.Reason)
		}
	}
	restsInBook := false
	for _, side := range []struct {
		orders []Order
		side   Side
	}{{res.Book.Bids, Buy}, {res.Book.Asks, Sell}} {
		for _, o := range side.orders {
			if o.ID == incoming.ID {
				if restsInBook {
					return fail("incoming order %s rests twice", o.ID)
				}
				restsInBook = true
				if o != res.Incoming {
					return fail("resting incoming order differs from Result.Incoming")
				}
				continue
			}
			if err := place(o, "book"); err != nil {
				return err
			}
			if o.Side != side.side || o.Remaining() == 0 || o.Expired(now) {
				return fail("order %s should not be on the %s side of the book", o.ID, side.side)
			}
		}
	}
	for id := range orig {
		if _, ok := final[id]; !ok {
			return fail("resting order %s vanished from the result", id)
		}
	}
	var restingFilled int64
	for id, o := range final {
		restingFilled += o.Filled - orig[id].Filled
		if o.Filled-orig[id].Filled != tradedByResting[id] {
			return fail("order %s filled rose by %d, its trades total %d", id, o.Filled-orig[id].Filled, tradedByResting[id])
		}
	}
	if restingFilled != traded {
		return fail("resting orders filled %d, trades total %d", restingFilled, traded)
	}
	for _, u := range res.Updated {
		f, ok := final[u.ID]
		if !ok || f != u || u.Remaining() == 0 {
			return fail("updated order %s does not match the book", u.ID)
		}
	}

	// 7. Resting rules for the incoming remainder.
	if res.Rests != restsInBook {
		return fail("Rests is %v but the book says %v", res.Rests, restsInBook)
	}
	if res.Rests {
		if incoming.Kind != Limit {
			return fail("%s order %s rests on the book", incoming.Kind, incoming.ID)
		}
		if res.Incoming.Remaining() == 0 {
			return fail("filled order %s rests on the book", incoming.ID)
		}
		opp := res.Book.Asks
		if incoming.Side == Sell {
			opp = res.Book.Bids
		}
		for _, o := range opp {
			if res.Incoming.acceptsPrice(o.UnitPrice) {
				return fail("resting order %s crosses order %s at %s", incoming.ID, o.ID, o.UnitPrice)
			}
		}
	} else if incoming.Kind == Limit && res.Incoming.Remaining() > 0 {
		return fail("limit order %s has %d left but does not rest", incoming.ID, res.Incoming.Remaining())
	}
	return nil
}
