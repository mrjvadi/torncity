// Package market holds the rules of the order book: what an order is, how an
// incoming order is matched against resting ones, what a trade costs in fees
// and exactly how much each order must hold in escrow.
//
// ONE ENGINE FOR EVERYTHING THAT TRADES. ADR 0006 §4 puts items and company
// shares on the same book, ADR 0010 §5 adds the currency exchange, and ADR
// 0013 §3 puts the black market on the same engine under another channel.
// None of them gets a second matcher. What differs between them is carried as
// data in AssetKey (asset_type, asset_id, city, channel), and a Book only ever
// holds orders for one key, so a share order can never fill against an item
// order and a black-market order can never fill against a public one. What
// differs in policy — the fee rate, who may see the black channel — is an
// input, never a branch in here.
//
// PURE FUNCTIONS OVER VALUES. Match takes a book and an order by value and
// returns what happened; it mutates nothing it was given, stores nothing and
// talks to nothing. Persisting the trades, moving the money through the
// ledger and locking rows is the application layer's job. That split is what
// lets every invariant below be checked with random data and no database:
// the matcher that runs in production is the one the property test drives.
//
// TIME IS AN INPUT, NEVER A WAIT. Expiry is decided against a now argument.
// There is no clock, no timer and no goroutine; an order does not expire
// because time passed, it is found expired the next time something looks.
//
// MONEY IS INTEGER MINOR UNITS. Every price, notional, fee and escrow amount
// is a money.Amount, and every multiplication of a quantity by a price is
// checked for int64 overflow. An order whose worst-case cost cannot be
// represented is rejected rather than wrapped around into a small number.
package market

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Sentinel errors. Overflow is reported by wrapping money.ErrOverflow, so
// errors.Is(err, money.ErrOverflow) holds for every arithmetic failure here.
var (
	// ErrInvalidOrder means an order is malformed: an unknown side or kind,
	// a non-positive quantity, a fill beyond the quantity, a missing owner.
	ErrInvalidOrder = errors.New("market: invalid order")

	// ErrInvalidBook means a book cannot be matched against: it holds an
	// order on the wrong side, a market order, a fully filled order or two
	// orders with the same id. A book like that is corrupt state, and
	// matching through it would compound the corruption.
	ErrInvalidBook = errors.New("market: invalid book")

	// ErrAssetMismatch means an order was sent to a book for a different
	// asset key — a different asset, city or channel.
	ErrAssetMismatch = errors.New("market: order and book are for different assets")

	// ErrExpired means the incoming order had already expired at now. It is
	// an error rather than an empty result so the caller releases the
	// order's escrow instead of believing it was worked.
	ErrExpired = errors.New("market: order has expired")

	// ErrInvalidFeeRate means a fee rate outside [0, MaxFeeBps].
	ErrInvalidFeeRate = errors.New("market: fee rate out of range")

	// ErrNotParty means a trade was applied to an order that is not one of
	// its two sides.
	ErrNotParty = errors.New("market: order is not a party to the trade")

	// ErrInvariant means a match produced a result that breaks one of the
	// book's invariants. It indicates a bug in this package; Match returns
	// it instead of a result so that a wrong answer can never be settled.
	ErrInvariant = errors.New("market: invariant violated")
)

// Side is which way an order trades. The values are the ones stored in
// market_orders.side.
type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Opposite returns the side an order of side s trades against.
func (s Side) Opposite() Side {
	if s == Buy {
		return Sell
	}
	return Buy
}

func (s Side) valid() bool { return s == Buy || s == Sell }

// Kind is how an order behaves when it cannot fill immediately. The values
// are the ones stored in market_orders.order_type.
type Kind string

const (
	// Limit orders trade at their price or better, and any unfilled
	// remainder rests on the book.
	Limit Kind = "limit"

	// Market orders take whatever liquidity is available at the moment they
	// arrive and never rest; the unfilled remainder is cancelled.
	Market Kind = "market"
)

func (k Kind) valid() bool { return k == Limit || k == Market }

// AssetType is what kind of thing a book trades (ADR 0006 §4, ADR 0010 §5).
type AssetType string

const (
	AssetItem     AssetType = "item"
	AssetShare    AssetType = "share"
	AssetCurrency AssetType = "currency"
)

func (a AssetType) valid() bool {
	return a == AssetItem || a == AssetShare || a == AssetCurrency
}

// Channel is which market an order is listed on (ADR 0013 §3). The black
// market is the same engine; the channel only keeps its orders on a
// separate book.
type Channel string

const (
	ChannelPublic Channel = "public"
	ChannelBlack  Channel = "black"
)

func (c Channel) valid() bool { return c == ChannelPublic || c == ChannelBlack }

// AssetKey identifies one book. Two orders can trade only if their keys are
// equal in every field. City may be empty for an asset that is not traded
// locally; the engine does not care, it only compares keys.
type AssetKey struct {
	Type    AssetType
	ID      string
	City    string
	Channel Channel
}

// Validate rejects a key with an unknown type or channel or no asset id.
func (k AssetKey) Validate() error {
	if !k.Type.valid() {
		return fmt.Errorf("%w: unknown asset type %q", ErrInvalidOrder, string(k.Type))
	}
	if k.ID == "" {
		return fmt.Errorf("%w: asset id is empty", ErrInvalidOrder)
	}
	if !k.Channel.valid() {
		return fmt.Errorf("%w: unknown channel %q", ErrInvalidOrder, string(k.Channel))
	}
	return nil
}

// Order is one row of market_orders, as a value.
//
// UnitPrice is the order's price bound in minor units per unit of asset:
//
//   - a limit buy pays at most UnitPrice, a limit sell accepts at least it;
//   - a market buy still carries a positive UnitPrice, as a worst price. A
//     buy must escrow its money before it is matched (ADR 0010 §5), and a buy
//     with no ceiling has no finite escrow. The ceiling is a protection bound,
//     not the execution price: the order sweeps the book up to it;
//   - a market sell may carry zero, meaning it accepts any bid, or a
//     positive floor.
//
// Trades never execute at UnitPrice of the incoming order unless it happens to
// equal the resting order's price; see Match.
type Order struct {
	ID        string
	Side      Side
	Kind      Kind
	Asset     AssetKey
	Quantity  int64
	Filled    int64
	UnitPrice money.Amount
	Owner     string
	CreatedAt time.Time
	// ExpiresAt is the instant the order stops being matchable. The zero
	// value means it never expires.
	ExpiresAt time.Time
}

// Remaining is the unfilled quantity.
func (o Order) Remaining() int64 { return o.Quantity - o.Filled }

// Expired reports whether the order has expired at now. An order expires at
// the instant ExpiresAt, not one tick after it.
func (o Order) Expired(now time.Time) bool {
	return !o.ExpiresAt.IsZero() && !now.Before(o.ExpiresAt)
}

// Validate checks everything about an order that does not depend on a book.
//
// It includes the escrow check: a buy whose Remaining × UnitPrice overflows
// int64 is rejected here, which is also what guarantees that no trade's
// notional can overflow later — every trade's quantity and price are bounded
// by the buy side's remaining quantity and limit.
func (o Order) Validate() error {
	if o.ID == "" {
		return fmt.Errorf("%w: id is empty", ErrInvalidOrder)
	}
	if !o.Side.valid() {
		return fmt.Errorf("%w: unknown side %q", ErrInvalidOrder, string(o.Side))
	}
	if !o.Kind.valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidOrder, string(o.Kind))
	}
	if err := o.Asset.Validate(); err != nil {
		return err
	}
	if o.Quantity <= 0 {
		return fmt.Errorf("%w: quantity %d is not positive", ErrInvalidOrder, o.Quantity)
	}
	if o.Filled < 0 || o.Filled > o.Quantity {
		return fmt.Errorf("%w: filled %d outside [0, %d]", ErrInvalidOrder, o.Filled, o.Quantity)
	}
	if o.UnitPrice.IsNegative() {
		return fmt.Errorf("%w: negative unit price %s", ErrInvalidOrder, o.UnitPrice)
	}
	if o.UnitPrice.IsZero() && (o.Kind == Limit || o.Side == Buy) {
		return fmt.Errorf("%w: a %s %s order needs a positive unit price", ErrInvalidOrder, o.Kind, o.Side)
	}
	if o.Owner == "" {
		return fmt.Errorf("%w: owner is empty", ErrInvalidOrder)
	}
	if o.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is not set", ErrInvalidOrder)
	}
	if !o.ExpiresAt.IsZero() && !o.ExpiresAt.After(o.CreatedAt) {
		return fmt.Errorf("%w: expires_at is not after created_at", ErrInvalidOrder)
	}
	if o.Side == Buy {
		if _, err := mulQtyPrice(o.Quantity, o.UnitPrice); err != nil {
			return fmt.Errorf("market: order %s cannot be escrowed: %w", o.ID, err)
		}
	}
	return nil
}

// acceptsPrice reports whether o is willing to trade at price p: a buy at p
// no higher than its bound, a sell at p no lower than it.
func (o Order) acceptsPrice(p money.Amount) bool {
	if o.Side == Buy {
		return p.Minor() <= o.UnitPrice.Minor()
	}
	return p.Minor() >= o.UnitPrice.Minor()
}
