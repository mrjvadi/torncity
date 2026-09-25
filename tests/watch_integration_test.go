//go:build integration

// Integration test of the watch (migration 0023,
// docs/adr/0023-health-missions-factions.md), through the real market and
// bank handlers, unit of work and ledger.
//
//	A seller lists a loaf of bread on the market at a hundred and fifty
//	times its price; a second account buys it — value moved from one
//	account to the other, and the watch flags the buyer, linking the two.
//	The buyer then pays the seller more than the hold threshold: the
//	payment, pressed twice, is held once in the buyer's escrow and the
//	seller receives nothing. Nobody is banned. An operator releases it
//	and the seller is paid, once.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/watch"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

func TestWatchFlagsWashTradeAndHoldsPayment(t *testing.T) {
	w := newGoodsWorld(t)
	requireStageE(t, w.pool)
	ctx := testCtx(t)
	scale := gametime.Scale(gameScale)
	// The shipped defaults of config anticheat.*.
	th := watch.Thresholds{Window: 24 * time.Hour, OneWayCount: 4, OneWayMinTotal: 20_000, OneWayRatioBPS: 9000,
		OffMarketBPS: 5000, OffMarketMinValue: 5000, SinglePartnerMinCount: 6, SinglePartnerShareBPS: 9000,
		CommandsPerMinute: 60, HoldAbove: 5000, WashTradeCount: 2}
	if err := th.Validate(); err != nil {
		t.Fatal(err)
	}
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	market := handlers.NewMarketHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale,
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, w.clock).
		WithWatch(th)
	limits, err := bank.NewLimits(1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	banker := handlers.NewBankHandler(w.uow, workIDs{t}, nil, w.cities, w.policy, postgres.NewPlayerSearchRepository(w.pool),
		limits, time.Hour, w.clock).WithWatch(th)

	seller := w.shopper(t, "bazaar", 1_000)
	buyer := w.shopper(t, "bazaar", 30_000)
	t.Cleanup(func() { purgeStageE(t, w.pool, seller.ID, buyer.ID) })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, id := range []string{seller.ID, buyer.ID} {
			if _, err := w.pool.Raw().Exec(ctx, `DELETE FROM outbox WHERE (subject LIKE 'game.event.bank.%'
			   OR subject LIKE 'game.event.market.%') AND payload::text LIKE '%' || $1 || '%'`, id); err != nil {
				t.Errorf("cleaning up events: %v", err)
			}
		}
	})
	if _, err := shops.Buy(ctx, w.meta(t, seller, "shop.buy"),
		handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "1", Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}

	// 1. The wash trade: a loaf at 6,000.
	if _, err := market.Order(ctx, w.meta(t, seller, "market.order"),
		handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "1", Price: "6000", Nonce: w.nonce(t)}); err != nil {
		t.Fatalf("sell order: %v", err)
	}
	if _, err := market.Order(ctx, w.meta(t, buyer, "market.order"),
		handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "1", Price: "6000", Nonce: w.nonce(t), Method: "cash"}); err != nil {
		t.Fatalf("buy order: %v", err)
	}
	if got := w.held(t, buyer.ID, "bread", application.HoldCarried); got != 1 {
		t.Fatalf("the trade did not happen: the buyer holds %d bread", got)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM watch_flags WHERE rule = 'off_market_trade' AND status = 'open'
	  AND player_id = $1::uuid AND other_player_id = $2::uuid`, buyer.ID, seller.ID); n != 1 {
		t.Fatalf("off-market flags on the buyer = %d, want 1", n)
	}

	// 2. A payment between the linked accounts above the threshold is held,
	//    once for a double press.
	sellerCash := w.purse(t, application.AccountPlayerCash, seller.ID)
	buyerCash := w.purse(t, application.AccountPlayerCash, buyer.ID)
	pay := handlers.PayRequest{To: seller.PublicCode, Amount: "8000", Method: "cash", Nonce: w.nonce(t)}
	for range 2 {
		if _, err := banker.PaySend(ctx, w.meta(t, buyer, "bank.pay.send"), pay); err != nil {
			t.Fatalf("pay: %v", err)
		}
	}
	var (
		holdNo int64
		status string
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no, status FROM payment_holds WHERE payer_id = $1::uuid AND payee_id = $2::uuid
	  AND amount = 8000`, buyer.ID, seller.ID).Scan(&holdNo, &status); err != nil {
		t.Fatalf("the payment was not held: %v", err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM payment_holds WHERE payer_id = $1::uuid`, buyer.ID); n != 1 {
		t.Fatalf("holds = %d, want 1 for a double press", n)
	}
	if got := buyerCash - w.purse(t, application.AccountPlayerCash, buyer.ID); got != 8000 {
		t.Fatalf("the buyer paid %d, want 8000 once", got)
	}
	if got := w.purse(t, application.AccountPlayerEscrow, buyer.ID); got != 8000 {
		t.Fatalf("the buyer's escrow holds %d, want the 8000 held", got)
	}
	if got := w.purse(t, application.AccountPlayerCash, seller.ID) - sellerCash; got != 0 {
		t.Fatalf("the seller received %d of a held payment", got)
	}
	// Nobody is banned for a flag.
	if n := countRows(t, w.pool, `SELECT count(*) FROM players WHERE id IN ($1::uuid, $2::uuid) AND status = 'active'`,
		seller.ID, buyer.ID); n != 2 {
		t.Fatal("a flagged account is no longer active")
	}

	// 3. An operator releases it: paid on to the seller once.
	for i := range 2 {
		err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			_, err := application.SettleHold(ctx, tx, holdNo, true, "admin:integration", "looked into", w.now)
			return err
		})
		if i == 0 && err != nil {
			t.Fatalf("release: %v", err)
		}
		if i == 1 && err == nil {
			t.Fatal("a released payment was released again")
		}
	}
	if got := w.purse(t, application.AccountPlayerCash, seller.ID) - sellerCash; got != 8000 {
		t.Fatalf("the seller received %d on the release, want 8000", got)
	}
	if got := w.purse(t, application.AccountPlayerEscrow, buyer.ID); got != 0 {
		t.Fatalf("the buyer's escrow holds %d after the release", got)
	}
	verifyLedger(t, w.pool)
}
