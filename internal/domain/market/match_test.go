package market

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// wantTrade is the part of a trade a case states; ids and owners are checked
// against the incoming order and the book by Verify on every match.
type wantTrade struct {
	buy, sell string
	qty       int64
	price     int64
}

func TestMatch(t *testing.T) {
	tests := []struct {
		name       string
		bids, asks []Order
		incoming   Order
		now        time.Time

		trades   []wantTrade
		inFilled int64
		rests    bool
		updated  []string
		removed  map[string]RemovalReason
		bookBids []string
		bookAsks []string
	}{
		{
			name:     "full fill of both sides",
			asks:     []Order{ord("s1", Sell, 10, 100, "alice", 0)},
			incoming: ord("b1", Buy, 10, 100, "bob", 1),
			now:      at(2),
			trades:   []wantTrade{{"b1", "s1", 10, 100}},
			inFilled: 10,
			removed:  map[string]RemovalReason{"s1": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name:     "incoming partially filled rests; trade at resting price",
			asks:     []Order{ord("s1", Sell, 4, 100, "alice", 0)},
			incoming: ord("b1", Buy, 10, 105, "bob", 1),
			now:      at(2),
			trades:   []wantTrade{{"b1", "s1", 4, 100}},
			inFilled: 4,
			rests:    true,
			removed:  map[string]RemovalReason{"s1": RemovedFilled},
			bookBids: []string{"b1"}, bookAsks: []string{},
		},
		{
			name:     "resting order partially filled keeps its place",
			asks:     []Order{ord("s1", Sell, 10, 100, "alice", 0), ord("s2", Sell, 10, 100, "carol", 1)},
			incoming: ord("b1", Buy, 3, 100, "bob", 2),
			now:      at(3),
			trades:   []wantTrade{{"b1", "s1", 3, 100}},
			inFilled: 3,
			updated:  []string{"s1"},
			removed:  map[string]RemovalReason{},
			bookBids: []string{}, bookAsks: []string{"s1", "s2"},
		},
		{
			name: "multi-level sweep stops at the limit",
			asks: []Order{
				ord("s4", Sell, 4, 104, "dave", 0),
				ord("s1", Sell, 2, 100, "alice", 3),
				ord("s3", Sell, 5, 103, "carol", 1),
				ord("s2", Sell, 3, 101, "erin", 2),
			},
			incoming: ord("b1", Buy, 8, 103, "bob", 5),
			now:      at(6),
			trades:   []wantTrade{{"b1", "s1", 2, 100}, {"b1", "s2", 3, 101}, {"b1", "s3", 3, 103}},
			inFilled: 8,
			updated:  []string{"s3"},
			removed:  map[string]RemovalReason{"s1": RemovedFilled, "s2": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{"s3", "s4"},
		},
		{
			name: "price-time priority: earlier first, id breaks an exact tie",
			asks: []Order{
				ord("s-late", Sell, 5, 100, "alice", 2),
				ord("s-b", Sell, 5, 100, "carol", 1),
				ord("s-a", Sell, 5, 100, "dave", 1),
			},
			incoming: ord("b1", Buy, 7, 100, "bob", 3),
			now:      at(4),
			trades:   []wantTrade{{"b1", "s-a", 5, 100}, {"b1", "s-b", 2, 100}},
			inFilled: 7,
			updated:  []string{"s-b"},
			removed:  map[string]RemovalReason{"s-a": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{"s-b", "s-late"},
		},
		{
			name: "better price beats earlier time",
			asks: []Order{
				ord("s-old", Sell, 5, 101, "alice", 0),
				ord("s-new", Sell, 5, 100, "carol", 5),
			},
			incoming: ord("b1", Buy, 5, 101, "bob", 6),
			now:      at(7),
			trades:   []wantTrade{{"b1", "s-new", 5, 100}},
			inFilled: 5,
			removed:  map[string]RemovalReason{"s-new": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{"s-old"},
		},
		{
			name: "sell crosses bids at or above its limit, remainder rests",
			bids: []Order{
				ord("b3", Buy, 5, 99, "carol", 0),
				ord("b1", Buy, 5, 110, "alice", 1),
				ord("b2", Buy, 5, 100, "dave", 2),
			},
			incoming: ord("s1", Sell, 12, 100, "bob", 3),
			now:      at(4),
			trades:   []wantTrade{{"b1", "s1", 5, 110}, {"b2", "s1", 5, 100}},
			inFilled: 10,
			rests:    true,
			removed:  map[string]RemovalReason{"b1": RemovedFilled, "b2": RemovedFilled},
			bookBids: []string{"b3"}, bookAsks: []string{"s1"},
		},
		{
			name:     "limit that does not cross rests untouched",
			asks:     []Order{ord("s1", Sell, 5, 101, "alice", 0)},
			incoming: ord("b1", Buy, 5, 100, "bob", 1),
			now:      at(2),
			rests:    true,
			removed:  map[string]RemovalReason{},
			bookBids: []string{"b1"}, bookAsks: []string{"s1"},
		},
		{
			name:     "market buy on an empty book does nothing and does not rest",
			incoming: mkt(ord("b1", Buy, 10, 1000, "bob", 0)),
			now:      at(1),
			removed:  map[string]RemovalReason{},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name:     "market buy takes what there is, remainder cancelled",
			asks:     []Order{ord("s1", Sell, 3, 100, "alice", 0)},
			incoming: mkt(ord("b1", Buy, 10, 1000, "bob", 1)),
			now:      at(2),
			trades:   []wantTrade{{"b1", "s1", 3, 100}},
			inFilled: 3,
			removed:  map[string]RemovalReason{"s1": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name:     "market buy stops at its protection bound",
			asks:     []Order{ord("s1", Sell, 3, 100, "alice", 0), ord("s2", Sell, 3, 200, "carol", 0)},
			incoming: mkt(ord("b1", Buy, 10, 150, "bob", 1)),
			now:      at(2),
			trades:   []wantTrade{{"b1", "s1", 3, 100}},
			inFilled: 3,
			removed:  map[string]RemovalReason{"s1": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{"s2"},
		},
		{
			name:     "market sell with no floor takes every bid",
			bids:     []Order{ord("b1", Buy, 2, 50, "alice", 0), ord("b2", Buy, 2, 1, "carol", 1)},
			incoming: mkt(ord("s1", Sell, 9, 0, "bob", 2)),
			now:      at(3),
			trades:   []wantTrade{{"b1", "s1", 2, 50}, {"b2", "s1", 2, 1}},
			inFilled: 4,
			removed:  map[string]RemovalReason{"b1": RemovedFilled, "b2": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name: "expired resting orders are skipped and removed from both sides",
			bids: []Order{
				expiring(ord("b-old", Buy, 5, 50, "dave", 0), 10),
				ord("b-live", Buy, 5, 40, "dave", 0),
			},
			asks: []Order{
				expiring(ord("s-exp", Sell, 5, 90, "alice", 0), 10),
				ord("s1", Sell, 5, 100, "carol", 1),
			},
			incoming: ord("b1", Buy, 5, 100, "bob", 11),
			now:      at(12),
			trades:   []wantTrade{{"b1", "s1", 5, 100}},
			inFilled: 5,
			removed: map[string]RemovalReason{
				"b-old": RemovedExpired, "s-exp": RemovedExpired, "s1": RemovedFilled,
			},
			bookBids: []string{"b-live"}, bookAsks: []string{},
		},
		{
			name:     "an order expires at the exact instant",
			asks:     []Order{expiring(ord("s-exp", Sell, 5, 90, "alice", 0), 10)},
			incoming: ord("b1", Buy, 5, 100, "bob", 1),
			now:      at(10),
			rests:    true,
			removed:  map[string]RemovalReason{"s-exp": RemovedExpired},
			bookBids: []string{"b1"}, bookAsks: []string{},
		},
		{
			name:     "an order one instant before expiry still trades",
			asks:     []Order{expiring(ord("s1", Sell, 5, 90, "alice", 0), 10)},
			incoming: ord("b1", Buy, 5, 100, "bob", 1),
			now:      at(10).Add(-time.Nanosecond),
			trades:   []wantTrade{{"b1", "s1", 5, 90}},
			inFilled: 5,
			removed:  map[string]RemovalReason{"s1": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name:     "self-trade cancels the resting order and matching continues",
			asks:     []Order{ord("s-own", Sell, 5, 99, "bob", 0), ord("s1", Sell, 5, 100, "alice", 1)},
			incoming: ord("b1", Buy, 5, 100, "bob", 2),
			now:      at(3),
			trades:   []wantTrade{{"b1", "s1", 5, 100}},
			inFilled: 5,
			removed:  map[string]RemovalReason{"s-own": RemovedSelfTrade, "s1": RemovedFilled},
			bookBids: []string{}, bookAsks: []string{},
		},
		{
			name:     "self-trade as the only liquidity leaves an uncrossed book",
			asks:     []Order{ord("s-own", Sell, 5, 100, "bob", 0)},
			incoming: ord("b1", Buy, 5, 100, "bob", 1),
			now:      at(2),
			rests:    true,
			removed:  map[string]RemovalReason{"s-own": RemovedSelfTrade},
			bookBids: []string{"b1"}, bookAsks: []string{},
		},
		{
			name:     "own order beyond the limit is not touched",
			asks:     []Order{ord("s-own", Sell, 5, 120, "bob", 0)},
			incoming: ord("b1", Buy, 5, 100, "bob", 1),
			now:      at(2),
			rests:    true,
			removed:  map[string]RemovalReason{},
			bookBids: []string{"b1"}, bookAsks: []string{"s-own"},
		},
		{
			name:     "previously partly filled incoming fills only its remainder",
			asks:     []Order{ord("s1", Sell, 10, 100, "alice", 0)},
			incoming: filled(ord("b1", Buy, 10, 100, "bob", 1), 7),
			now:      at(2),
			trades:   []wantTrade{{"b1", "s1", 3, 100}},
			inFilled: 10,
			updated:  []string{"s1"},
			removed:  map[string]RemovalReason{},
			bookBids: []string{}, bookAsks: []string{"s1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := book(tt.bids, tt.asks)
			snapshot := cloneBook(in)
			incomingCopy := tt.incoming

			res, err := Match(in, tt.incoming, tt.now)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}

			if !reflect.DeepEqual(in, snapshot) || tt.incoming != incomingCopy {
				t.Fatal("Match modified its inputs")
			}

			var got []wantTrade
			for _, tr := range res.Trades {
				got = append(got, wantTrade{tr.BuyOrderID, tr.SellOrderID, tr.Quantity, tr.UnitPrice.Minor()})
				if !tr.ExecutedAt.Equal(tt.now) || tr.Aggressor != tt.incoming.Side {
					t.Errorf("trade stamped %v by %s, want %v by %s", tr.ExecutedAt, tr.Aggressor, tt.now, tt.incoming.Side)
				}
			}
			if !slices.Equal(got, tt.trades) {
				t.Errorf("trades = %v, want %v", got, tt.trades)
			}
			if res.Incoming.Filled != tt.inFilled {
				t.Errorf("incoming filled = %d, want %d", res.Incoming.Filled, tt.inFilled)
			}
			if res.Rests != tt.rests {
				t.Errorf("rests = %v, want %v", res.Rests, tt.rests)
			}
			if g := ids(res.Updated); !slices.Equal(g, tt.updated) && !(len(g) == 0 && len(tt.updated) == 0) {
				t.Errorf("updated = %v, want %v", g, tt.updated)
			}
			gotRemoved := map[string]RemovalReason{}
			for _, rm := range res.Removed {
				gotRemoved[rm.Order.ID] = rm.Reason
			}
			if !reflect.DeepEqual(gotRemoved, tt.removed) {
				t.Errorf("removed = %v, want %v", gotRemoved, tt.removed)
			}
			if g := ids(res.Book.Bids); !slices.Equal(g, tt.bookBids) {
				t.Errorf("book bids = %v, want %v", g, tt.bookBids)
			}
			if g := ids(res.Book.Asks); !slices.Equal(g, tt.bookAsks) {
				t.Errorf("book asks = %v, want %v", g, tt.bookAsks)
			}
		})
	}
}

func TestMatchErrors(t *testing.T) {
	otherKey := testKey
	otherKey.Channel = ChannelBlack

	blackOrder := ord("b1", Buy, 1, 100, "bob", 0)
	blackOrder.Asset = otherKey

	restingMarket := mkt(ord("s1", Sell, 5, 100, "alice", 0))

	tests := []struct {
		name     string
		bids     []Order
		asks     []Order
		incoming Order
		now      time.Time
		want     error
	}{
		{"black-market order on the public book", nil, nil, blackOrder, at(1), ErrAssetMismatch},
		{"invalid incoming", nil, nil, ord("", Buy, 1, 100, "bob", 0), at(1), ErrInvalidOrder},
		{"incoming already expired", nil, nil, expiring(ord("b1", Buy, 1, 100, "bob", 0), 5), at(5), ErrExpired},
		{"incoming with nothing left", nil, nil, filled(ord("b1", Buy, 1, 100, "bob", 0), 1), at(1), ErrInvalidOrder},
		{"incoming id already resting", []Order{ord("b1", Buy, 1, 90, "bob", 0)}, nil, ord("b1", Buy, 1, 100, "bob", 0), at(1), ErrInvalidBook},
		{"market order resting", nil, []Order{restingMarket}, ord("b1", Buy, 1, 100, "bob", 1), at(2), ErrInvalidBook},
		{"sell on the bid side", []Order{ord("s1", Sell, 1, 100, "alice", 0)}, nil, ord("b1", Buy, 1, 100, "bob", 1), at(2), ErrInvalidBook},
		{"filled order resting", nil, []Order{filled(ord("s1", Sell, 1, 100, "alice", 0), 1)}, ord("b1", Buy, 1, 100, "bob", 1), at(2), ErrInvalidBook},
		{"duplicate resting id", nil, []Order{ord("s1", Sell, 1, 100, "alice", 0), ord("s1", Sell, 1, 101, "carol", 0)}, ord("b1", Buy, 1, 100, "bob", 1), at(2), ErrInvalidBook},
		{"buy whose escrow overflows", nil, nil, ord("b1", Buy, math.MaxInt64/2, 3, "bob", 0), at(1), money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Match(book(tt.bids, tt.asks), tt.incoming, tt.now)
			if !errors.Is(err, tt.want) {
				t.Errorf("Match error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestMatchLargeValues exercises trades near the int64 ceiling: a buy whose
// escrow just fits may trade, and its notional must be exact.
func TestMatchLargeValues(t *testing.T) {
	const price = 1 << 31
	qty := int64(math.MaxInt64 / price) // qty × price fits, (qty+1) × price does not
	res, err := Match(
		book(nil, []Order{ord("s1", Sell, qty, price, "alice", 0)}),
		ord("b1", Buy, qty, price, "bob", 1), at(2))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(res.Trades) != 1 || res.Trades[0].Notional.Minor() != qty*price {
		t.Fatalf("trades = %+v", res.Trades)
	}
	// A sell may be large; only the buy side bounds the notional.
	if _, err := Match(
		book(nil, []Order{ord("s1", Sell, math.MaxInt64, math.MaxInt64, "alice", 0)}),
		ord("b1", Buy, 1, math.MaxInt64, "bob", 1), at(2)); err != nil {
		t.Fatalf("Match with a huge sell: %v", err)
	}
	if _, err := mulQtyPrice(qty+1, money.FromMinor(price)); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("mulQtyPrice past the ceiling = %v, want overflow", err)
	}
}

func TestOrderValidate(t *testing.T) {
	base := ord("o1", Buy, 5, 100, "bob", 0)
	tests := []struct {
		name   string
		mutate func(*Order)
		ok     bool
	}{
		{"valid limit buy", func(*Order) {}, true},
		{"valid market sell with no floor", func(o *Order) { o.Side, o.Kind, o.UnitPrice = Sell, Market, money.FromMinor(0) }, true},
		{"market buy needs a bound", func(o *Order) { o.Kind, o.UnitPrice = Market, money.FromMinor(0) }, false},
		{"limit sell needs a price", func(o *Order) { o.Side, o.UnitPrice = Sell, money.FromMinor(0) }, false},
		{"negative price", func(o *Order) { o.UnitPrice = money.FromMinor(-1) }, false},
		{"unknown side", func(o *Order) { o.Side = "short" }, false},
		{"unknown kind", func(o *Order) { o.Kind = "stop" }, false},
		{"unknown asset type", func(o *Order) { o.Asset.Type = "bond" }, false},
		{"unknown channel", func(o *Order) { o.Asset.Channel = "grey" }, false},
		{"empty asset id", func(o *Order) { o.Asset.ID = "" }, false},
		{"zero quantity", func(o *Order) { o.Quantity = 0 }, false},
		{"filled over quantity", func(o *Order) { o.Filled = 6 }, false},
		{"negative filled", func(o *Order) { o.Filled = -1 }, false},
		{"no owner", func(o *Order) { o.Owner = "" }, false},
		{"no created_at", func(o *Order) { o.CreatedAt = time.Time{} }, false},
		{"expires before created", func(o *Order) { o.ExpiresAt = o.CreatedAt }, false},
		{"escrow overflows", func(o *Order) { o.Quantity, o.UnitPrice = math.MaxInt64, money.FromMinor(2) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := base
			tt.mutate(&o)
			err := o.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

// TestVerifyRejects feeds Verify results that break each invariant, so a
// check that silently stopped checking would fail here.
func TestVerifyRejects(t *testing.T) {
	b := book(nil, []Order{ord("s1", Sell, 5, 100, "alice", 0), ord("s2", Sell, 5, 110, "carol", 0)})
	in := ord("b1", Buy, 5, 105, "bob", 1)
	now := at(2)
	good, err := Match(b, in, now)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if err := Verify(b, in, now, good); err != nil {
		t.Fatalf("Verify(good) = %v", err)
	}

	clone := func() Result {
		r := good
		r.Trades = slices.Clone(good.Trades)
		r.Removed = slices.Clone(good.Removed)
		r.Updated = slices.Clone(good.Updated)
		r.Book = cloneBook(good.Book)
		return r
	}
	breaks := map[string]func(*Result){
		"price above the buy limit": func(r *Result) {
			r.Trades[0].UnitPrice = money.FromMinor(106)
			r.Trades[0].Notional = money.FromMinor(530)
		},
		"price is not the resting price": func(r *Result) {
			r.Trades[0].UnitPrice = money.FromMinor(101)
			r.Trades[0].Notional = money.FromMinor(505)
		},
		"wrong notional":         func(r *Result) { r.Trades[0].Notional = money.FromMinor(1) },
		"quantity not conserved": func(r *Result) { r.Incoming.Filled = 4 },
		"overfilled":             func(r *Result) { r.Incoming.Filled = 6 },
		"self-trade":             func(r *Result) { r.Trades[0].Seller = "bob" },
		"resting order vanished": func(r *Result) { r.Book.Asks = nil },
		"removed twice": func(r *Result) {
			r.Removed = append(r.Removed, r.Removed[0])
		},
		"market order rests": func(r *Result) {},
	}
	for name, mutate := range breaks {
		t.Run(name, func(t *testing.T) {
			r := clone()
			incoming := in
			if name == "market order rests" {
				incoming = mkt(ord("b1", Buy, 6, 105, "bob", 1))
				r.Incoming = filled(incoming, 5)
				r.Rests = true
				r.Book.Bids = []Order{r.Incoming}
			}
			mutate(&r)
			if err := Verify(b, incoming, now, r); !errors.Is(err, ErrInvariant) {
				t.Errorf("Verify = %v, want ErrInvariant", err)
			}
		})
	}
}
