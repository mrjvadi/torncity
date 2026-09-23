package market

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// simulate drives one random order stream through Match, the way the
// application layer would, and keeps an independent set of books: escrow held
// per order, quantity placed and traded, money paid and received. After every
// step it checks that the engine's result and those books still agree.
//
// The stream is small on purpose — four owners, eleven price levels, clocks
// that often do not move — so that self-trades, exact price-time ties,
// expiries and multi-level sweeps happen constantly rather than rarely.
func simulate(t *testing.T, seed uint64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	owners := []string{"alice", "bob", "carol", "dave"}
	feeBps := rng.Int64N(MaxFeeBps + 1)
	if rng.IntN(2) == 0 {
		feeBps = rng.Int64N(500) // realistic rates as often as extreme ones
	}

	b := NewBook(testKey)
	now := t0
	escrow := map[string]Reservation{}
	orders := map[string]Order{} // as placed; side, kind and bound never change

	var placed, traded, cancelled int64
	var buyerPaid, sellerReceived, fees, escrowConsumed money.Amount
	add := func(dst *money.Amount, v money.Amount) {
		s, err := dst.Add(v)
		if err != nil {
			t.Fatalf("seed %d: overflow in test books: %v", seed, err)
		}
		*dst = s
	}
	release := func(o Order, why string) {
		want, err := Reserve(o)
		if err != nil {
			t.Fatalf("seed %d: Reserve(%s): %v", seed, o.ID, err)
		}
		if escrow[o.ID] != want {
			t.Fatalf("seed %d: %s of %s releases %+v, escrow holds %+v", seed, why, o.ID, want, escrow[o.ID])
		}
		cancelled += o.Remaining()
		delete(escrow, o.ID)
	}

	for step := 0; step < steps; step++ {
		now = now.Add(time.Duration(rng.IntN(3)) * time.Second)

		// Sometimes the owner cancels a resting order instead of placing one.
		if rng.IntN(10) == 0 && len(b.Bids)+len(b.Asks) > 0 {
			i := rng.IntN(len(b.Bids) + len(b.Asks))
			var o Order
			if i < len(b.Bids) {
				o = b.Bids[i]
				b.Bids = append(b.Bids[:i:i], b.Bids[i+1:]...)
			} else {
				i -= len(b.Bids)
				o = b.Asks[i]
				b.Asks = append(b.Asks[:i:i], b.Asks[i+1:]...)
			}
			release(o, "cancel")
			continue
		}

		o := Order{
			ID:        fmt.Sprintf("o%d", step),
			Side:      []Side{Buy, Sell}[rng.IntN(2)],
			Kind:      Limit,
			Asset:     testKey,
			Quantity:  1 + rng.Int64N(10),
			UnitPrice: money.FromMinor(95 + rng.Int64N(11)),
			Owner:     owners[rng.IntN(len(owners))],
			CreatedAt: now,
		}
		if rng.IntN(5) == 0 {
			o.Kind = Market
			if o.Side == Sell && rng.IntN(2) == 0 {
				o.UnitPrice = money.FromMinor(0)
			}
		}
		if rng.IntN(4) == 0 {
			o.ExpiresAt = now.Add(time.Duration(1+rng.IntN(20)) * time.Second)
		}

		r, err := Reserve(o)
		if err != nil {
			t.Fatalf("seed %d: Reserve: %v", seed, err)
		}
		escrow[o.ID] = r
		orders[o.ID] = o
		placed += o.Quantity

		snapshot := cloneBook(b)
		res, err := Match(b, o, now)
		if err != nil {
			t.Fatalf("seed %d step %d: Match: %v", seed, step, err)
		}
		if !reflect.DeepEqual(b, snapshot) {
			t.Fatalf("seed %d step %d: Match modified the book it was given", seed, step)
		}
		if err := Verify(b, o, now, res); err != nil {
			t.Fatalf("seed %d step %d: %v", seed, step, err)
		}

		for _, tr := range res.Trades {
			if tr.Buyer == tr.Seller {
				t.Fatalf("seed %d: self-trade %+v", seed, tr)
			}
			buy, sell := orders[tr.BuyOrderID], orders[tr.SellOrderID]
			if tr.UnitPrice.Minor() > buy.UnitPrice.Minor() || tr.UnitPrice.Minor() < sell.UnitPrice.Minor() {
				t.Fatalf("seed %d: trade at %s outside [%s, %s]", seed, tr.UnitPrice, sell.UnitPrice, buy.UnitPrice)
			}

			be, err := ReleaseOnFill(buy, tr)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			se, err := ReleaseOnFill(sell, tr)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			held := escrow[buy.ID]
			left, err := held.Money.Sub(be.Consumed.Money)
			if err == nil {
				left, err = left.Sub(be.Released.Money)
			}
			if err != nil || left.IsNegative() {
				t.Fatalf("seed %d: buy %s escrow %s cannot cover %+v", seed, buy.ID, held.Money, be)
			}
			escrow[buy.ID] = Reservation{Money: left}
			sh := escrow[sell.ID]
			if sh.Quantity < se.Consumed.Quantity {
				t.Fatalf("seed %d: sell %s escrow %d cannot cover %d", seed, sell.ID, sh.Quantity, se.Consumed.Quantity)
			}
			escrow[sell.ID] = Reservation{Quantity: sh.Quantity - se.Consumed.Quantity}

			s, err := Settle(tr, feeBps)
			if err != nil {
				t.Fatalf("seed %d: Settle: %v", seed, err)
			}
			add(&buyerPaid, s.BuyerPays)
			add(&sellerReceived, s.SellerReceives)
			add(&fees, s.Fee)
			add(&escrowConsumed, be.Consumed.Money)
			traded += tr.Quantity
		}

		for _, rm := range res.Removed {
			release(rm.Order, string(rm.Reason))
		}
		if !res.Rests {
			release(res.Incoming, "unrested remainder")
		}
		b = res.Book

		// The escrow still held is exactly what the book's orders need.
		if n := len(b.Bids) + len(b.Asks); n != len(escrow) {
			t.Fatalf("seed %d step %d: %d orders on the book, %d escrows", seed, step, n, len(escrow))
		}
		var resting int64
		for _, side := range [][]Order{b.Bids, b.Asks} {
			for _, ro := range side {
				want, _ := Reserve(ro)
				if escrow[ro.ID] != want {
					t.Fatalf("seed %d step %d: %s holds %+v, needs %+v", seed, step, ro.ID, escrow[ro.ID], want)
				}
				resting += ro.Remaining()
			}
		}
		// Every unit placed was traded (once on each side), cancelled, or rests.
		if placed != 2*traded+cancelled+resting {
			t.Fatalf("seed %d step %d: placed %d != 2×traded %d + cancelled %d + resting %d",
				seed, step, placed, traded, cancelled, resting)
		}
		// Money: the buyer's escrow paid exactly what sellers and the fee sink got.
		total, err := money.Sum(sellerReceived, fees)
		if err != nil || total != buyerPaid || buyerPaid != escrowConsumed {
			t.Fatalf("seed %d step %d: buyers paid %s, escrow drew %s, sellers got %s + fees %s",
				seed, step, buyerPaid, escrowConsumed, sellerReceived, fees)
		}
		// The book never crosses: best bid is strictly below best ask.
		if len(b.Bids) > 0 && len(b.Asks) > 0 && b.Bids[0].UnitPrice.Minor() >= b.Asks[0].UnitPrice.Minor() {
			t.Fatalf("seed %d step %d: crossed book, bid %s ask %s", seed, step, b.Bids[0].UnitPrice, b.Asks[0].UnitPrice)
		}
	}
}

// TestRandomOrderStreams runs fixed seeds so a failure reproduces exactly.
func TestRandomOrderStreams(t *testing.T) {
	for seed := uint64(1); seed <= 40; seed++ {
		simulate(t, seed, 300)
	}
}

// FuzzOrderStream lets `go test -fuzz=FuzzOrderStream` search seeds beyond the
// fixed ones; under plain `go test` it runs the corpus below.
func FuzzOrderStream(f *testing.F) {
	for _, s := range []uint64{0, 7, 42, 1 << 32, 0xdeadbeef} {
		f.Add(s, uint16(200))
	}
	f.Fuzz(func(t *testing.T, seed uint64, steps uint16) {
		simulate(t, seed, int(steps%1000))
	})
}
