package application

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/fx"
)

// What the book would give for a conversion, found by walking its resting orders the way a matched order
// would (docs/adr/0033 6.10). A quote changes nothing; an order that executes it may do better or worse
// if the book moved, which is what the player's limit is for.

// FXQuote is the book's answer to one leg.
type FXQuote struct {
	// UnitsIn and SUPIn are what the player gives; UnitsOut and SUPOut what they receive (the units net of the
	// village's fee, the SUP net of nothing: the fee on selling SUP is the buyer's, on top).
	UnitsIn, SUPIn, UnitsOut, SUPOut int64
	// Units is the quantity of the order that executes it and Price its worst price (the limit it carries).
	Units, Price int64
	// Fee is the fee in the currency sold: SUP for a buy of units, units for a sale of units.
	Fee int64
	// Levels is how many resting orders it touches; Complete says the book can fill all of it.
	Levels   int
	Complete bool
}

// QuoteFXSell is what selling `units` of a currency at the market would bring: the SUP, walking the bids.
func QuoteFXSell(ctx context.Context, tx Tx, rules FXRules, settlementID string, units int64, now time.Time) (FXQuote, error) {
	q := FXQuote{UnitsIn: units, Units: units}
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return q, ErrFXNoMarket
	}
	bids, err := tx.FX().OpenOrders(ctx, settlementID, FXBuy, rules.BookLimit)
	if err != nil {
		return q, err
	}
	left, sold := units, int64(0)
	for _, b := range bids {
		if left <= 0 {
			break
		}
		if !b.ExpiresAt.After(now) {
			continue
		}
		take := min(left, b.Remaining())
		f, ok := fx.FillOf(take, b.Price, b.NotionalMicro, b.FeeBPS, sold, st.FXFeeBPS)
		if !ok {
			break
		}
		q.SUPOut += f.SUP
		q.Fee += f.UnitsFee
		q.Price = b.Price
		q.Levels++
		left -= take
		sold += take
	}
	q.Complete = left == 0
	return q, nil
}

// QuoteFXBuy is what spending up to `budget` SUP at the market would buy: the units, walking the asks, with
// the order quantity and the worst price that order must carry so that its escrow fits the budget.
func QuoteFXBuy(ctx context.Context, tx Tx, rules FXRules, settlementID string, budget int64, now time.Time) (FXQuote, error) {
	var q FXQuote
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return q, ErrFXNoMarket
	}
	asks, err := tx.FX().OpenOrders(ctx, settlementID, FXSell, rules.BookLimit)
	if err != nil {
		return q, err
	}
	live := asks[:0:0]
	for _, a := range asks {
		if a.ExpiresAt.After(now) {
			live = append(live, a)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].Price < live[j].Price })
	// how many units the budget can set aside for, level by level: the escrow is priced at the worst level
	var units, worst int64
	for _, a := range live {
		fits := func(n int64) bool { return fx.BuyEscrow(units+n, a.Price, rules.ReserveFeeBPS) <= budget }
		lo, hi := int64(0), a.Remaining()
		if !fits(1) {
			break
		}
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if fits(mid) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		units += lo
		worst = a.Price
		if lo < a.Remaining() {
			break
		}
	}
	q.Units, q.Price = units, worst
	if units == 0 {
		return q, nil
	}
	// and what walking those asks with that many units really costs and brings
	left := units
	var cum int64
	for _, a := range live {
		if left <= 0 {
			break
		}
		take := min(left, a.Remaining())
		f, ok := fx.FillOf(take, a.Price, cum, rules.ReserveFeeBPS, a.Filled, a.FeeBPS)
		if !ok {
			break
		}
		cum += f.Notional
		q.UnitsOut += f.Units - f.UnitsFee
		q.SUPIn += f.SUP + f.SUPFee
		q.Fee += f.SUPFee
		q.Levels++
		left -= take
	}
	q.Complete = left == 0
	return q, nil
}
