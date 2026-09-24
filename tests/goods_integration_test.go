//go:build integration

// Goods end to end against PostgreSQL: a shop sale paid through the ledger
// and eaten; a market order escrowed, matched and settled; an auction bid,
// outbid and closed exactly once; a thief's tools worn once, rested and taken
// as evidence. Every test ends with the ledger verifying.
package tests

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// purgeGoodsFor removes what a test player held, traded and walked: their
// goods and the journal rows naming them, their market orders and trades,
// their auctions and bids, their shop sales and walks. The two journals are
// append-only; like the ledger's purge, the test lifts their triggers inside
// its own transaction only, and they are back when it commits.
func purgeGoodsFor(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var present bool
	if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('public.item_movements') IS NOT NULL`).Scan(&present); err != nil || !present {
		return
	}
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
		`ALTER TABLE market_trades DISABLE TRIGGER market_trades_append_only`,
		`DELETE FROM market_trades WHERE buyer_id = $1::uuid OR seller_id = $1::uuid`,
		`DELETE FROM market_orders WHERE owner_id = $1::uuid`,
		`CREATE TEMP TABLE purge_pieces ON COMMIT DROP AS
		   SELECT DISTINCT piece_id AS id FROM item_movements
		    WHERE piece_id IS NOT NULL AND (from_player = $1::uuid OR to_player = $1::uuid)
		   UNION SELECT id FROM item_pieces WHERE owner_id = $1::uuid`,
		`CREATE TEMP TABLE purge_auctions ON COMMIT DROP AS
		   SELECT id FROM auctions WHERE seller_id = $1::uuid OR piece_id IN (SELECT id FROM purge_pieces)
		   UNION SELECT auction_id FROM auction_bids WHERE bidder_id = $1::uuid`,
		`UPDATE auctions SET high_bid_id = NULL WHERE id IN (SELECT id FROM purge_auctions)`,
		`DELETE FROM auction_bids WHERE auction_id IN (SELECT id FROM purge_auctions)`,
		`DELETE FROM auctions WHERE id IN (SELECT id FROM purge_auctions)`,
		`DELETE FROM item_movements WHERE from_player = $1::uuid OR to_player = $1::uuid
		    OR piece_id IN (SELECT id FROM purge_pieces)`,
		`UPDATE crimes SET stolen_piece_id = NULL WHERE stolen_piece_id IN (SELECT id FROM purge_pieces)`,
		`DELETE FROM item_pieces WHERE id IN (SELECT id FROM purge_pieces)`,
		`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
		`ALTER TABLE market_trades ENABLE TRIGGER market_trades_append_only`,
		`DELETE FROM item_stacks WHERE player_id = $1::uuid`,
		`DELETE FROM item_cooldowns WHERE player_id = $1::uuid`,
		`DELETE FROM shop_sales WHERE player_id = $1::uuid`,
		`DELETE FROM place_moves WHERE player_id = $1::uuid`,
		`DELETE FROM outbox WHERE (subject LIKE 'game.event.inventory.%' OR subject LIKE 'game.event.market.%'
		    OR subject LIKE 'game.event.auction.%') AND payload::text LIKE '%' || $1 || '%'`,
	} {
		var err error
		if strings.Contains(stmt, "$1") {
			_, err = tx.Exec(ctx, stmt, playerID)
		} else {
			_, err = tx.Exec(ctx, stmt)
		}
		if err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// goodsWorld is what every goods test needs: the live content with its goods,
// the shipped city, its levers and a clock the test moves.
type goodsWorld struct {
	pool     *postgres.Pool
	registry *content.Registry
	cities   application.CityRepository
	policy   application.PolicyReader
	city     *application.City
	uow      application.UnitOfWork
	now      time.Time
}

func (w *goodsWorld) clock() time.Time { return w.now }

func newGoodsWorld(t *testing.T) *goodsWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var ready bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.item_stacks') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("item_stacks does not exist; apply migration 0017 first")
	}
	registry := crimeRegistry(t, pool)
	if len(registry.Current().Items()) == 0 || len(registry.Current().Shops()) == 0 {
		t.Skip("the active content has no goods; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	policy := postgres.NewPolicyReader(pool, nil)
	if _, err := policy.Get(ctx, city.JurisdictionID, handlers.LeverSalesTax); err != nil {
		t.Skipf("the mayor's trade levers are not loaded: %v", err)
	}
	w := &goodsWorld{pool: pool, registry: registry, cities: cities, policy: policy, city: city,
		uow: postgres.NewUnitOfWork(pool, testDefaultLanguage), now: time.Now().UTC()}
	keepShelves(t, pool, city.ID)
	return w
}

// keepShelves puts the city's shop shelves back as the test found them.
func keepShelves(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	type shelf struct {
		shop, item string
		stock      int64
		at         time.Time
	}
	rows, err := pool.Raw().Query(testCtx(t), `SELECT shop_code, item_code, stock, restocked_at FROM shop_shelves WHERE city_id = $1::uuid`, cityID)
	if err != nil {
		t.Fatal(err)
	}
	var before []shelf
	for rows.Next() {
		var s shelf
		if err := rows.Scan(&s.shop, &s.item, &s.stock, &s.at); err != nil {
			t.Fatal(err)
		}
		before = append(before, s)
	}
	rows.Close()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM shop_shelves WHERE city_id = $1::uuid`, cityID); err != nil {
			t.Errorf("cleanup shelves: %v", err)
			return
		}
		for _, s := range before {
			if _, err := pool.Raw().Exec(ctx,
				`INSERT INTO shop_shelves (city_id, shop_code, item_code, stock, restocked_at) VALUES ($1::uuid, $2, $3, $4, $5)`,
				cityID, s.shop, s.item, s.stock, s.at); err != nil {
				t.Errorf("cleanup shelves: %v", err)
			}
		}
	})
}

// shopper is a player in the city with cash, standing at a place.
func (w *goodsWorld) shopper(t *testing.T, place string, cash int64) *application.Player {
	t.Helper()
	p := crimePlayer(t, w.pool, w.city.ID, w.now)
	if _, err := w.pool.Raw().Exec(testCtx(t), `UPDATE players SET place_code = $2, place_since = $3 WHERE id = $1::uuid`,
		p.ID, place, w.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if cash > 0 {
		grant(t, w.pool, application.AccountPlayerCash, p.ID, cash)
	}
	return p
}

func (w *goodsWorld) meta(t *testing.T, p *application.Player, command string) envelope.Metadata {
	t.Helper()
	m := validMeta(t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	m.IdempotencyKey = "it-" + randomToken(t, 16)
	return m
}

// nonce is a button's one-time token: twelve lower-case hex characters.
func (w *goodsWorld) nonce(t *testing.T) string { return strings.ReplaceAll(newUUID(t), "-", "")[:12] }

// held reads how many units of a good a player holds where.
func (w *goodsWorld) held(t *testing.T, playerID, item, holding string) int64 {
	t.Helper()
	var n int64
	if err := w.pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE(sum(quantity), 0) FROM item_stacks WHERE player_id = $1::uuid AND item_code = $2 AND holding = $3`,
		playerID, item, holding).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var pieces int64
	if err := w.pool.Raw().QueryRow(testCtx(t),
		`SELECT count(*) FROM item_pieces WHERE owner_id = $1::uuid AND item_code = $2 AND holding = $3`,
		playerID, item, holding).Scan(&pieces); err != nil {
		t.Fatal(err)
	}
	return n + pieces
}

func (w *goodsWorld) purse(t *testing.T, kind application.AccountKind, playerID string) int64 {
	t.Helper()
	return cashBalance(t, w.pool, kind, playerID)
}

func TestShopSaleThenEaten(t *testing.T) {
	w := newGoodsWorld(t)
	ctx := testCtx(t)
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	bag := handlers.NewInventoryHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, scale, crimeRules().Nerve, 10, time.Hour, w.clock)

	p := w.shopper(t, "bazaar", 1_000)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET energy = 50 WHERE player_id = $1::uuid`, p.ID); err != nil {
		t.Fatal(err)
	}
	treasuryBefore := w.purse(t, application.AccountCityTreasury, w.city.ID)
	nonce := w.nonce(t)
	buy := handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "2", Method: "cash", Nonce: nonce}
	for i := 0; i < 2; i++ {
		// The same button pressed twice is one sale.
		if _, err := shops.Buy(ctx, w.meta(t, p, "shop.buy"), buy); err != nil {
			t.Fatalf("Buy #%d: %v", i+1, err)
		}
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 2 {
		t.Fatalf("bread carried = %d, want 2", got)
	}
	var sales, total, tax int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT count(*), COALESCE(sum(total), 0), COALESCE(sum(tax), 0) FROM shop_sales WHERE player_id = $1::uuid`,
		p.ID).Scan(&sales, &total, &tax); err != nil {
		t.Fatal(err)
	}
	if sales != 1 {
		t.Fatalf("shop sales = %d, want 1", sales)
	}
	if got := 1_000 - w.purse(t, application.AccountPlayerCash, p.ID); got != total+tax {
		t.Errorf("cash spent = %d, want the price %d plus the tax %d", got, total, tax)
	}
	if got := w.purse(t, application.AccountCityTreasury, w.city.ID) - treasuryBefore; got != tax {
		t.Errorf("the city's sales tax = %d, want %d", got, tax)
	}

	use := handlers.ItemRequest{Item: "bread", Nonce: w.nonce(t)}
	for i := 0; i < 2; i++ {
		if _, err := bag.Use(ctx, w.meta(t, p, "inventory.use"), use); err != nil {
			t.Fatalf("Use #%d: %v", i+1, err)
		}
	}
	var energy int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if energy != 60 {
		t.Errorf("energy after one loaf = %d, want 60", energy)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 1 {
		t.Errorf("bread left = %d, want 1 (a double press eats one)", got)
	}
	// The second loaf waits for the food group to rest.
	if _, err := bag.Use(ctx, w.meta(t, p, "inventory.use"), handlers.ItemRequest{Item: "bread", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 1 {
		t.Errorf("bread left while resting = %d, want 1", got)
	}
	w.now = w.now.Add(scale.RealWait(30*time.Minute) + time.Second)
	if _, err := bag.Use(ctx, w.meta(t, p, "inventory.use"), handlers.ItemRequest{Item: "bread", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 0 {
		t.Errorf("bread left after the rest = %d, want 0", got)
	}
	verifyLedger(t, w.pool)
}

func TestMarketEscrowMatchAndSettle(t *testing.T) {
	w := newGoodsWorld(t)
	ctx := testCtx(t)
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	market := handlers.NewMarketHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale,
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, w.clock)

	seller := w.shopper(t, "bazaar", 1_000)
	buyer := w.shopper(t, "bazaar", 500)
	if _, err := shops.Buy(ctx, w.meta(t, seller, "shop.buy"),
		handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "3", Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}
	sellerCash, sellerBank := w.purse(t, application.AccountPlayerCash, seller.ID), w.purse(t, application.AccountPlayerBank, seller.ID)

	sell := handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "2", Price: "50", Nonce: w.nonce(t)}
	for i := 0; i < 2; i++ {
		if _, err := market.Order(ctx, w.meta(t, seller, "market.order"), sell); err != nil {
			t.Fatalf("sell order #%d: %v", i+1, err)
		}
	}
	if got := w.held(t, seller.ID, "bread", application.HoldEscrow); got != 2 {
		t.Fatalf("bread in escrow = %d, want 2 (one order, however many presses)", got)
	}
	if got := w.held(t, seller.ID, "bread", application.HoldCarried); got != 1 {
		t.Fatalf("bread carried = %d, want 1", got)
	}

	buy := handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "1", Price: "50", Nonce: w.nonce(t), Method: "cash"}
	for i := 0; i < 2; i++ {
		if _, err := market.Order(ctx, w.meta(t, buyer, "market.order"), buy); err != nil {
			t.Fatalf("buy order #%d: %v", i+1, err)
		}
	}
	if got := w.held(t, buyer.ID, "bread", application.HoldCarried); got != 1 {
		t.Errorf("the buyer's bread = %d, want 1", got)
	}
	if got := 500 - w.purse(t, application.AccountPlayerCash, buyer.ID); got != 50 {
		t.Errorf("the buyer paid %d, want 50", got)
	}
	if got := w.purse(t, application.AccountPlayerEscrow, buyer.ID); got != 0 {
		t.Errorf("the buyer's escrow = %d, want 0 once filled", got)
	}
	var trades, fee int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT count(*), COALESCE(sum(fee), 0) FROM market_trades WHERE seller_id = $1::uuid`,
		seller.ID).Scan(&trades, &fee); err != nil {
		t.Fatal(err)
	}
	if trades != 1 || fee < 1 {
		t.Fatalf("trades = %d with fee %d, want one trade and a fee rounded up to at least 1", trades, fee)
	}
	got := w.purse(t, application.AccountPlayerCash, seller.ID) - sellerCash + w.purse(t, application.AccountPlayerBank, seller.ID) - sellerBank
	if got != 50-fee {
		t.Errorf("the seller received %d, want %d", got, 50-fee)
	}

	// The rest of the sell order comes back on a cancel.
	var no int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no FROM market_orders WHERE owner_id = $1::uuid AND status = 'open'`, seller.ID).Scan(&no); err != nil {
		t.Fatal(err)
	}
	if _, err := market.Cancel(ctx, w.meta(t, seller, "market.cancel"), handlers.MarketRequest{No: itoa(no)}); err != nil {
		t.Fatal(err)
	}
	if got := w.held(t, seller.ID, "bread", application.HoldCarried); got != 2 {
		t.Errorf("bread carried after the cancel = %d, want 2", got)
	}
	if got := w.held(t, seller.ID, "bread", application.HoldEscrow); got != 0 {
		t.Errorf("bread in escrow after the cancel = %d, want 0", got)
	}
	verifyLedger(t, w.pool)
}

func TestAuctionBidOutbidAndCloseOnce(t *testing.T) {
	w := newGoodsWorld(t)
	ctx := testCtx(t)
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clock)
	house := handlers.NewAuctionsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale,
		handlers.AuctionRules{Durations: []time.Duration{time.Hour}, MaxReserve: 1_000_000, StepBPS: 500, MinStep: 10, MaxOpen: 5,
			ReservesBPS: []int{5000, 10000}}, 10, time.Hour, w.clock)

	seller := w.shopper(t, "business_district", 5_000)
	first := w.shopper(t, "business_district", 5_000)
	second := w.shopper(t, "business_district", 5_000)
	if _, err := shops.Buy(ctx, w.meta(t, seller, "shop.buy"),
		handlers.ShopRequest{Shop: "electronics_store", Item: "phone", Qty: "1", Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}
	var serial string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'phone'`, seller.ID).Scan(&serial); err != nil {
		t.Fatalf("the phone bought: %v", err)
	}
	put := handlers.AuctionRequest{Item: serial, Reserve: "1000", Duration: "0", Nonce: w.nonce(t)}
	for i := 0; i < 2; i++ {
		if _, err := house.New(ctx, w.meta(t, seller, "auction.new"), put); err != nil {
			t.Fatalf("New #%d: %v", i+1, err)
		}
	}
	var auctionID string
	var no int64
	var endsAt time.Time
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, no, ends_at FROM auctions WHERE seller_id = $1::uuid`, seller.ID).
		Scan(&auctionID, &no, &endsAt); err != nil {
		t.Fatalf("one auction: %v", err)
	}
	if got := w.held(t, seller.ID, "phone", application.HoldEscrow); got != 1 {
		t.Fatalf("the phone in escrow = %d, want 1", got)
	}

	bid := func(p *application.Player, amount string) {
		t.Helper()
		if _, err := house.Bid(ctx, w.meta(t, p, "auction.bid"),
			handlers.AuctionRequest{No: itoa(no), Amount: amount, Nonce: w.nonce(t), Method: "cash"}); err != nil {
			t.Fatal(err)
		}
	}
	bid(first, "1000")
	if got := w.purse(t, application.AccountPlayerEscrow, first.ID); got != 1000 {
		t.Fatalf("the first bid in escrow = %d, want 1000", got)
	}
	bid(second, "2000")
	if got := w.purse(t, application.AccountPlayerCash, first.ID); got != 5_000 {
		t.Errorf("the outbid bidder's cash = %d, want it all back", got)
	}
	if got := w.purse(t, application.AccountPlayerEscrow, second.ID); got != 2000 {
		t.Errorf("the standing bid in escrow = %d, want 2000", got)
	}

	sellerBank := w.purse(t, application.AccountPlayerBank, seller.ID)
	w.now = endsAt.Add(time.Second)
	payload, _ := json.Marshal(handlers.CrimeActionPayload{ReferenceID: auctionID, PlayerID: seller.ID})
	for i := 0; i < 2; i++ {
		m := w.meta(t, seller, "auction.close")
		if _, err := house.Close(ctx, m, handlers.CrimeScheduledRequest{
			ActorID: seller.ID, ReferenceType: "auctions", ReferenceID: auctionID, Payload: payload}); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	var status string
	var fee int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT status, fee FROM auctions WHERE id = $1::uuid`, auctionID).Scan(&status, &fee); err != nil {
		t.Fatal(err)
	}
	if status != application.AuctionSold {
		t.Fatalf("status = %s, want sold", status)
	}
	if got := w.held(t, second.ID, "phone", application.HoldCarried); got != 1 {
		t.Errorf("the winner's phone = %d, want 1", got)
	}
	if got := w.purse(t, application.AccountPlayerBank, seller.ID) - sellerBank; got != 2000-fee {
		t.Errorf("the seller received %d, want %d, once", got, 2000-fee)
	}
	if got := w.purse(t, application.AccountPlayerEscrow, second.ID); got != 0 {
		t.Errorf("the winner's escrow = %d, want 0", got)
	}
	var sales int
	if err := w.pool.Raw().QueryRow(ctx,
		`SELECT count(DISTINCT transaction_id) FROM ledger_entries WHERE reason = 'auction_sale' AND reference_id = $1::uuid`, auctionID).Scan(&sales); err != nil {
		t.Fatal(err)
	}
	if sales != 1 {
		t.Errorf("auction_sale transactions = %d, want exactly 1", sales)
	}
	verifyLedger(t, w.pool)
}

// TestCrimeGearWearsOnceRestsAndIsTakenAsEvidence runs a pickpocket with
// gloves: a double press wears one pair, a second try waits for the rest, and
// an arrest takes the gloves as evidence.
func TestCrimeGearWearsOnceRestsAndIsTakenAsEvidence(t *testing.T) {
	w := newGoodsWorld(t)
	ctx := testCtx(t)
	if _, err := w.policy.Get(ctx, w.city.JurisdictionID, application.LeverCrimeReportFee); err != nil {
		t.Skipf("the police chief's levers are not loaded: %v", err)
	}
	restoreNPCProceeds(t, w.pool)
	thief := w.shopper(t, "city_centre", 0)
	var others int
	if err := w.pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM players WHERE city_id = $1::uuid AND status = 'active' AND last_active_at >= $2 AND id <> $3::uuid`,
		w.city.ID, w.now.Add(-time.Hour), thief.ID).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others > 0 {
		t.Skipf("%d other players are active in ostmarch; the victim draw would not be deterministic", others)
	}
	// Three pairs of gloves, granted from a recorded origin.
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "gloves", Qty: 3, To: thief.ID,
			ToHolding: application.HoldCarried, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: w.now})
	}); err != nil {
		t.Fatal(err)
	}

	rules := crimeRules()
	rules.GearCaps = crime.GearCaps{SuccessBPS: 2500, CatchBPS: 2500, WitnessBPS: 3000, SolveBPS: 3000, RewardBPS: 5000, Nerve: 5}
	dice := &crimeDice{}
	h := handlers.NewCrimeHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, gametime.Scale(gameScale), dice, rules, time.Hour, w.clock)
	commit := func(nonce string) {
		t.Helper()
		if _, err := h.Commit(ctx, w.meta(t, thief, "crime.commit"), handlers.CrimeCommitRequest{Crime: "pickpocketing", Nonce: nonce}); err != nil {
			t.Fatal(err)
		}
	}
	attempts := func() (n int, solve int) {
		t.Helper()
		if err := w.pool.Raw().QueryRow(ctx,
			`SELECT count(*), COALESCE(min(gear_solve_bps), 0) FROM crimes WHERE player_id = $1::uuid`, thief.ID).Scan(&n, &solve); err != nil {
			t.Fatal(err)
		}
		return n, solve
	}

	once := w.nonce(t)
	dice.script(0, 0)
	commit(once)
	commit(once)
	if n, solve := attempts(); n != 1 || solve != -1500 {
		t.Fatalf("attempts = %d with gear solve %d, want one attempt carrying the gloves' -1500", n, solve)
	}
	if got := w.held(t, thief.ID, "gloves", application.HoldCarried); got != 2 {
		t.Fatalf("gloves left = %d, want 2: a double press wears one pair", got)
	}

	// Straight away again: pickpocketing is resting.
	dice.script(0, 0)
	commit(w.nonce(t))
	if n, _ := attempts(); n != 1 {
		t.Fatalf("attempts during the rest = %d, want 1", n)
	}

	// After the rest, caught in the act: one more pair wears, and the last
	// is kept as evidence.
	cr, _ := w.registry.Current().Crime("pickpocketing")
	w.now = w.now.Add(gametime.Scale(gameScale).RealWait(cr.Cooldown) + time.Second)
	dice.script(9999, 0)
	commit(w.nonce(t))
	var result string
	if err := w.pool.Raw().QueryRow(ctx,
		`SELECT status FROM crimes WHERE player_id = $1::uuid ORDER BY started_at DESC LIMIT 1`, thief.ID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != application.CrimeCaught {
		t.Fatalf("the last attempt = %s, want caught", result)
	}
	if got := w.held(t, thief.ID, "gloves", application.HoldCarried); got != 0 {
		t.Errorf("gloves after the arrest = %d, want 0", got)
	}
	var taken int
	if err := w.pool.Raw().QueryRow(ctx,
		`SELECT count(*) FROM item_movements WHERE from_player = $1::uuid AND reason = 'confiscated'`, thief.ID).Scan(&taken); err != nil {
		t.Fatal(err)
	}
	if taken != 1 {
		t.Errorf("confiscations = %d, want 1", taken)
	}
	verifyLedger(t, w.pool)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
