package market

import (
	"slices"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

var (
	testKey = AssetKey{Type: AssetItem, ID: "item-phone", City: "city-a", Channel: ChannelPublic}
	t0      = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
)

// at returns t0 plus n seconds, so tests read as a sequence of instants.
func at(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }

// ord builds a valid limit order on testKey created at t0 + created seconds.
func ord(id string, side Side, qty, price int64, owner string, created int) Order {
	return Order{
		ID:        id,
		Side:      side,
		Kind:      Limit,
		Asset:     testKey,
		Quantity:  qty,
		UnitPrice: money.FromMinor(price),
		Owner:     owner,
		CreatedAt: at(created),
	}
}

func mkt(o Order) Order { o.Kind = Market; return o }

func filled(o Order, n int64) Order { o.Filled = n; return o }

func expiring(o Order, sec int) Order { o.ExpiresAt = at(sec); return o }

func book(bids, asks []Order) Book { return Book{Asset: testKey, Bids: bids, Asks: asks} }

// cloneBook deep-copies a book so a test can prove Match left its input alone.
func cloneBook(b Book) Book {
	return Book{Asset: b.Asset, Bids: slices.Clone(b.Bids), Asks: slices.Clone(b.Asks)}
}

func ids(orders []Order) []string {
	out := make([]string, len(orders))
	for i, o := range orders {
		out[i] = o.ID
	}
	return out
}
