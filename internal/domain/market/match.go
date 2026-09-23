package market

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Book is the resting orders for one asset key. Bids are resting buys and
// Asks resting sells. The slices need not be sorted on input; Match sorts a
// copy into price-time priority and returns the book in that order.
type Book struct {
	Asset AssetKey
	Bids  []Order
	Asks  []Order
}

// NewBook returns an empty book for key.
func NewBook(key AssetKey) Book { return Book{Asset: key} }

// Trade is one fill between a buy and a sell order: one row of trades.
//
// It carries no fee. The fee is a policy input applied afterwards by Fee and
// Settle, so the same trade can be priced under whatever rate the lever held
// when it was settled, and the matcher never needs to know the rate.
type Trade struct {
	Asset       AssetKey
	BuyOrderID  string
	SellOrderID string
	Buyer       string
	Seller      string
	Quantity    int64
	// UnitPrice is the resting order's price; see Match.
	UnitPrice money.Amount
	// Notional is Quantity × UnitPrice: what the buyer pays.
	Notional money.Amount
	// Aggressor is the side of the incoming order that took liquidity.
	Aggressor  Side
	ExecutedAt time.Time
}

// RemovalReason says why a resting order left the book.
type RemovalReason string

const (
	// RemovedFilled: the order was completely filled by this match.
	RemovedFilled RemovalReason = "filled"
	// RemovedExpired: the order had expired at now. Its remainder was not
	// traded and its escrow must be released.
	RemovedExpired RemovalReason = "expired"
	// RemovedSelfTrade: the order would have traded against the incoming
	// order of the same owner and was cancelled instead. See Match.
	RemovedSelfTrade RemovalReason = "self_trade"
)

// Removal is a resting order leaving the book, in its final state.
type Removal struct {
	Order  Order
	Reason RemovalReason
}

// Result is everything a match changed. Nothing in it aliases the inputs.
type Result struct {
	// Trades in execution order.
	Trades []Trade
	// Incoming is the incoming order with Filled updated.
	Incoming Order
	// Rests reports whether Incoming's remainder was added to the book. It
	// is false when the order filled completely, and always false for a
	// market order: a market order's unfilled remainder is cancelled, and the
	// caller releases escrow for Incoming.Remaining().
	Rests bool
	// Updated holds resting orders that were partially filled and are still
	// on the book, with Filled updated.
	Updated []Order
	// Removed holds resting orders that left the book, with the reason.
	Removed []Removal
	// Book is the book after the match, in price-time priority.
	Book Book
}

// Match works incoming against book at instant now.
//
// Rules, in the order they are applied:
//
//  1. Every resting order already expired at now is removed from BOTH sides
//     and reported as RemovedExpired, whether or not the incoming order would
//     have reached it, so the returned book never holds an expired order.
//  2. The opposite side is walked in price-time priority: best price first
//     (lowest ask for a buy, highest bid for a sell), and among equal prices
//     the oldest CreatedAt first, then the lower ID so that equal timestamps
//     still give one deterministic order.
//  3. The walk stops at the first resting order whose price the incoming
//     order does not accept (a buy crosses asks priced at or below its
//     bound; a sell crosses bids at or above it), or when the incoming order
//     is filled.
//  4. EXECUTION PRICE IS THE RESTING ORDER'S PRICE. The resting order was
//     there first and published that price; the incoming order said it would
//     pay up to (or accept down to) its own bound and gets any improvement.
//     This is the standard continuous-auction rule, and it means neither side
//     ever trades worse than its own limit.
//  5. SELF-TRADE PREVENTION: CANCEL THE RESTING ORDER. If the next resting
//     order belongs to the incoming order's owner, it is removed from the book
//     (RemovedSelfTrade) without trading, and the walk continues. A trade with
//     oneself moves nothing but prints a price and volume, which is the whole
//     mechanism of wash trading (ADR 0006 §8). Skipping the resting order and
//     leaving it on the book was rejected because a limit remainder could
//     then rest on the other side of it and leave the owner's own book
//     crossed; cancelling the incoming order was rejected because the newer
//     order is the owner's current intent. Cancelling the resting order is no
//     power a player lacks — they could cancel it themselves.
//  6. Each trade fills min(incoming remaining, resting remaining). A resting
//     order that is completely filled is RemovedFilled; a partly filled one
//     stays at its place in the queue and is reported in Updated.
//  7. A limit order's unfilled remainder rests on its own side. A market
//     order never rests; its remainder is cancelled.
//
// The inputs are not modified. The result is checked by Verify before it is
// returned; a failure there is returned as ErrInvariant, never as a result.
func Match(book Book, incoming Order, now time.Time) (Result, error) {
	if err := incoming.Validate(); err != nil {
		return Result{}, err
	}
	if incoming.Asset != book.Asset {
		return Result{}, fmt.Errorf("%w: order %s is for %+v, book is for %+v",
			ErrAssetMismatch, incoming.ID, incoming.Asset, book.Asset)
	}
	if err := book.Validate(); err != nil {
		return Result{}, err
	}
	for _, side := range [][]Order{book.Bids, book.Asks} {
		for _, r := range side {
			if r.ID == incoming.ID {
				return Result{}, fmt.Errorf("%w: incoming order %s is already on the book", ErrInvalidBook, incoming.ID)
			}
		}
	}
	if incoming.Remaining() == 0 {
		return Result{}, fmt.Errorf("%w: order %s has nothing left to fill", ErrInvalidOrder, incoming.ID)
	}
	if incoming.Expired(now) {
		return Result{}, fmt.Errorf("%w: order %s at %s", ErrExpired, incoming.ID, now.Format(time.RFC3339))
	}

	var res Result
	bids := dropExpired(sortedSide(book.Bids, Buy), now, &res.Removed)
	asks := dropExpired(sortedSide(book.Asks, Sell), now, &res.Removed)

	opposite := asks
	if incoming.Side == Sell {
		opposite = bids
	}

	in := incoming
	kept := make([]Order, 0, len(opposite))
	i := 0
	for ; i < len(opposite) && in.Remaining() > 0; i++ {
		r := opposite[i]
		if !in.acceptsPrice(r.UnitPrice) {
			break
		}
		if r.Owner == in.Owner {
			res.Removed = append(res.Removed, Removal{Order: r, Reason: RemovedSelfTrade})
			continue
		}

		qty := min(in.Remaining(), r.Remaining())
		notional, err := mulQtyPrice(qty, r.UnitPrice)
		if err != nil {
			return Result{}, err
		}
		t := Trade{
			Asset:      book.Asset,
			Quantity:   qty,
			UnitPrice:  r.UnitPrice,
			Notional:   notional,
			Aggressor:  in.Side,
			ExecutedAt: now,
		}
		if in.Side == Buy {
			t.BuyOrderID, t.Buyer, t.SellOrderID, t.Seller = in.ID, in.Owner, r.ID, r.Owner
		} else {
			t.BuyOrderID, t.Buyer, t.SellOrderID, t.Seller = r.ID, r.Owner, in.ID, in.Owner
		}
		res.Trades = append(res.Trades, t)

		in.Filled += qty
		r.Filled += qty
		if r.Remaining() == 0 {
			res.Removed = append(res.Removed, Removal{Order: r, Reason: RemovedFilled})
		} else {
			res.Updated = append(res.Updated, r)
			kept = append(kept, r)
		}
	}
	kept = append(kept, opposite[i:]...)

	if in.Side == Buy {
		asks = kept
	} else {
		bids = kept
	}

	if in.Kind == Limit && in.Remaining() > 0 {
		res.Rests = true
		if in.Side == Buy {
			bids = sortedSide(append(bids, in), Buy)
		} else {
			asks = sortedSide(append(asks, in), Sell)
		}
	}

	res.Incoming = in
	res.Book = Book{Asset: book.Asset, Bids: bids, Asks: asks}

	if err := Verify(book, incoming, now, res); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Validate checks that every resting order is well formed, belongs to this
// book's key, sits on the side matching its own Side, is a limit order with
// something left to fill, and has an id unique within the book.
//
// Expired orders are valid here: expiry is relative to a now, and removing
// them is part of matching, not a precondition of it.
func (b Book) Validate() error {
	if err := b.Asset.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidBook, err)
	}
	seen := make(map[string]bool, len(b.Bids)+len(b.Asks))
	check := func(o Order, side Side) error {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("%w: resting order %s: %w", ErrInvalidBook, o.ID, err)
		}
		if o.Asset != b.Asset {
			return fmt.Errorf("%w: resting order %s is for another asset", ErrInvalidBook, o.ID)
		}
		if o.Side != side {
			return fmt.Errorf("%w: %s order %s is on the %s side", ErrInvalidBook, o.Side, o.ID, side)
		}
		if o.Kind != Limit {
			return fmt.Errorf("%w: %s order %s is resting; only limit orders rest", ErrInvalidBook, o.Kind, o.ID)
		}
		if o.Remaining() == 0 {
			return fmt.Errorf("%w: resting order %s is already filled", ErrInvalidBook, o.ID)
		}
		if seen[o.ID] {
			return fmt.Errorf("%w: order id %s appears twice", ErrInvalidBook, o.ID)
		}
		seen[o.ID] = true
		return nil
	}
	for _, o := range b.Bids {
		if err := check(o, Buy); err != nil {
			return err
		}
	}
	for _, o := range b.Asks {
		if err := check(o, Sell); err != nil {
			return err
		}
	}
	return nil
}

// comparePriority orders two resting orders on the same side: negative if a
// has priority over b. Better price first, then earlier CreatedAt, then the
// lower ID as a deterministic final tie-break.
func comparePriority(side Side, a, b Order) int {
	if c := cmp.Compare(a.UnitPrice.Minor(), b.UnitPrice.Minor()); c != 0 {
		if side == Buy {
			return -c
		}
		return c
	}
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// sortedSide returns a sorted copy of orders in price-time priority.
func sortedSide(orders []Order, side Side) []Order {
	out := slices.Clone(orders)
	slices.SortFunc(out, func(a, b Order) int { return comparePriority(side, a, b) })
	return out
}

// dropExpired returns orders without those expired at now, appending each
// dropped one to removed. It does not modify orders' backing array.
func dropExpired(orders []Order, now time.Time, removed *[]Removal) []Order {
	out := make([]Order, 0, len(orders))
	for _, o := range orders {
		if o.Expired(now) {
			*removed = append(*removed, Removal{Order: o, Reason: RemovedExpired})
			continue
		}
		out = append(out, o)
	}
	return out
}
