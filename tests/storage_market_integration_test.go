//go:build integration

// Integration test of the storage and market fixes (docs/research/2026-10-03-
// storage-market-audit.md, P0): a founded settlement without a market post has
// no market (F4) and its Economy hub does not list one (F6).
package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/shared/money"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func TestSettlementMarketFailsClosed(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	cities := postgres.NewCityRepository(pool)
	snap := staticContentSource{snap: loadTestContent(t)}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, snap, e.cache, cities, gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now)
	market := handlers.NewMarketHandler(uow, workIDs{t}, catalog, snap, cities, postgres.NewPolicyReader(pool, nil), gametime.Scale(1),
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, e.clock.Now).
		WithHome("support")

	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	head := asPlayer(meta, founder)
	mk := func(command, action string) envelope.Metadata {
		m := head
		m.Command, m.Action = command, action
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	setPost := func(status string) {
		t.Helper()
		if _, err := pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET status = $2,
		    completed_at = CASE WHEN $2 = 'complete' THEN now() END WHERE settlement_id = $1::uuid AND type_code = 'barter_post'`,
			cityID, status); err != nil {
			t.Fatal(err)
		}
	}
	hubCodes := func() string {
		t.Helper()
		resp, err := rrcm(mk("economy.hub", "hub"))(village.EconomyHub(ctx, mk("economy.hub", "hub")))
		if err != nil {
			t.Fatal(err)
		}
		return econButtonData(resp)
	}

	// With the founding kit's market post standing: the book opens, the hub
	// lists «بازار» (the market) and the storehouse.
	books, err := rrcm(mk("market.list", "list"))(market.Books(ctx, mk("market.list", "list"), handlers.MarketRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(books.Text, "اینجا نیست") {
		t.Errorf("a settlement with a market post has no market:\n%s", books.Text)
	}
	if data := hubCodes(); !strings.Contains(data, "market:list") || !strings.Contains(data, "settlement:materials") ||
		strings.Count(data, "settlement:materials") != 1 {
		t.Errorf("the Economy hub with a market post:\n%s", data)
	}

	// Without it the market is closed for every door, and says where to go.
	setPost("building")
	for name, call := range map[string]func() (string, error){
		"list": func() (string, error) {
			r, err := rrcm(mk("market.list", "list"))(market.Books(ctx, mk("market.list", "list"), handlers.MarketRequest{}))
			return textOf(r), err
		},
		"book": func() (string, error) {
			r, err := rrcm(mk("market.book", "book"))(market.Book(ctx, mk("market.book", "book"), handlers.MarketRequest{Item: "bread"}))
			return textOf(r), err
		},
		"order": func() (string, error) {
			r, err := rrcm(mk("market.order", "order"))(market.Order(ctx, mk("market.order", "order"),
				handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "1", Price: "5", Nonce: randomToken(t, 8)}))
			return textOf(r), err
		},
	} {
		text, err := call()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(text, "اینجا نیست") || !strings.Contains(text, "بازارچه") {
			t.Errorf("market.%s without a market post:\n%s", name, text)
		}
	}
	data := hubCodes()
	if strings.Contains(data, "market:list") {
		t.Errorf("the Economy hub lists a market the settlement lacks:\n%s", data)
	}
	if !strings.Contains(data, "settlement:materials") {
		t.Errorf("the Economy hub has lost the storehouse:\n%s", data)
	}

	setPost("complete")
	if r, err := rrcm(mk("market.list", "list"))(market.Books(ctx, mk("market.list", "list"), handlers.MarketRequest{})); err != nil ||
		strings.Contains(r.Text, "اینجا نیست") {
		t.Errorf("the market did not open again: %v\n%s", err, textOf(r))
	}
}

// textOf is a response's text, empty for none.
func textOf(r *presenter.Response) string {
	if r == nil {
		return ""
	}
	return r.Text
}

// The working storehouse (P2): a store gives its room only while a keeper keeps
// it, the keeper's day is paid once, food spoils once a day, and players give to
// the stock while the head takes from it.
func TestWorkingStorehouse(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	seedTreasury(t, pool, cityID, 60_000)
	head := asPlayer(meta, founder)
	mk := func(command string) envelope.Metadata {
		m := head
		m.Command = command
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	view := func() vpres.MaterialsView {
		t.Helper()
		resp, err := rrcm(mk("settlement.materials"))(village.Materials(ctx, mk("settlement.materials")))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.MaterialsView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	room := func(v vpres.MaterialsView, class string) (used, capacity int64) {
		for _, c := range v.Classes {
			if c.Class == class {
				return c.Used, c.Capacity
			}
		}
		return 0, 0
	}
	resettle := func() { // the next game day: the next look settles it afresh
		t.Helper()
		e.clock.Advance(24*time.Hour + time.Minute)
	}
	addBuilding := func(code string, x int) {
		t.Helper()
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		    VALUES ($1::uuid, $2::uuid, $3, $4, 3, 'complete', now(), now())`, newUUID(t), cityID, code, x); err != nil {
			t.Fatal(err)
		}
	}

	// drain empties the treasury to the sink (a construction bill): no wage can be paid.
	drain := func() {
		t.Helper()
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
			if err != nil {
				return err
			}
			bal := treasuryOf(t, pool, cityID)
			if bal <= 0 {
				return nil
			}
			_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{ID: newUUID(t), Reason: application.ReasonSettlementConstruction,
				CreatedAt: e.clock.Now(), ReferenceType: "test", ReferenceID: newUUID(t),
				Entries: []application.LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(-bal)}, {AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(bal)}}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. The treasury cannot pay a keeper: the granary is unkept, its room does not
	// count, and food has only the base room of its class; bulk the open yard.
	drain()
	resettle()
	v := view()
	if _, c := room(v, "food"); c != 120 {
		t.Errorf("food room with an unkept granary = %d, want the base 20 plus the 100 the residents keep by rota", c)
	}
	if _, c := room(v, "bulk"); c != 60 {
		t.Errorf("bulk room = %d, want the open yard 60", c)
	}
	if len(v.Stores) != 1 || v.Stores[0].Kept {
		t.Errorf("stores = %+v, want the granary unkept", v.Stores)
	}

	// 2. Money in the treasury and a storehouse standing: both stores are kept the
	// next day, the wage is paid once and their room appears.
	seedTreasury(t, pool, cityID, 60_000)
	addBuilding("storehouse", 80)
	resettle()
	treasury0 := treasuryOf(t, pool, cityID)
	v = view()
	if len(v.Stores) != 2 || !v.Stores[0].Kept || !v.Stores[1].Kept {
		t.Fatalf("stores = %+v, want both kept", v.Stores)
	}
	paid := treasury0 - treasuryOf(t, pool, cityID)
	if paid <= 0 {
		t.Errorf("the keepers were not paid (%d)", paid)
	}
	for i := 0; i < 3; i++ { // looking again, or two replicas, pays nothing more
		view()
	}
	if got := treasury0 - treasuryOf(t, pool, cityID); got != paid {
		t.Errorf("the day was paid again: %d, want %d", got, paid)
	}
	var rows int64
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(wage), 0) FROM village_storage_days WHERE settlement_id = $1::uuid`, cityID).Scan(&rows); err != nil || rows != paid {
		t.Errorf("the day row says %d (%v), the treasury paid %d", rows, err, paid)
	}
	if _, c := room(v, "food"); c != 320 {
		t.Errorf("food room with the granary kept = %d, want 20 + 300", c)
	}
	if _, c := room(v, "bulk"); c != 260 {
		t.Errorf("bulk room with the storehouse kept = %d, want 60 + 200", c)
	}
	if _, c := room(v, "goods"); c != 170 {
		t.Errorf("goods room with the storehouse kept = %d, want 20 + 150", c)
	}

	// 3. Food spoils once per game day, in whole units, journalled as spoiled; a
	// day with no keeper (the treasury empty) spoils the unkept share.
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "wheat", Qty: 100, ToOrg: application.SettlementOrg(cityID),
			ToHolding: application.HoldWarehouse, Reason: application.ItemSupplied, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	resettle()
	drain()
	view()
	view() // a second look spoils nothing more
	if got := stockOfItem(t, pool, cityID, "wheat"); got != 70 {
		t.Errorf("wheat after an unkept day at 30 %% = %d, want 70", got)
	}
	var journal int64
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0) FROM item_movements WHERE reason = 'spoiled' AND reference_id = $1::uuid`, cityID).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != 30 {
		t.Errorf("spoiled in the journal = %d, want 30", journal)
	}
	seedTreasury(t, pool, cityID, 60_000)

	// 4. A resident gives to the stock; only the head takes from it.
	give := func(n int64) {
		t.Helper()
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "timber", Qty: n, To: founder.ID,
				ToHolding: application.HoldCarried, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
		}); err != nil {
			t.Fatal(err)
		}
	}
	give(5)
	timber0 := stockOfItem(t, pool, cityID, "timber")
	donate := func(m envelope.Metadata, qty string) string {
		t.Helper()
		m.Command = "settlement.stock.donate"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		r, err := rrcm(m)(village.StockDonate(ctx, m, handlers.VillageStockMoveRequest{Item: "timber", Qty: qty}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	donate(mk("settlement.stock.donate"), "3")
	if got := stockOfItem(t, pool, cityID, "timber"); got != timber0+3 {
		t.Fatalf("stock after a donation of 3 = %d, want %d", got, timber0+3)
	}
	if text := donate(mk("settlement.stock.donate"), "50"); !strings.Contains(text, "ندارید") && !strings.Contains(text, "در دسترس نیست") {
		t.Errorf("giving more than carried was not refused:\n%s", text)
	}
	other := insertPlayer(t, pool)
	om := asPlayer(meta, other)
	if text := donate(om, "1"); stockOfItem(t, pool, cityID, "timber") != timber0+3 {
		t.Errorf("a stranger's gift changed the stock:\n%s", text)
	}
	take := func(m envelope.Metadata, qty string) string {
		t.Helper()
		m.Command = "settlement.stock.take"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		r, err := rrcm(m)(village.StockTake(ctx, m, handlers.VillageStockMoveRequest{Item: "timber", Qty: qty}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	take(om, "1")
	if got := stockOfItem(t, pool, cityID, "timber"); got != timber0+3 {
		t.Errorf("a non-head took from the stock: %d", got)
	}
	take(mk("settlement.stock.take"), "2")
	if got := stockOfItem(t, pool, cityID, "timber"); got != timber0+1 {
		t.Errorf("stock after the head took 2 = %d, want %d", got, timber0+1)
	}
	// Leave nothing of the player-side moves behind: their journal rows go with
	// the player, so the stock must go back to what it was.
	take(mk("settlement.stock.take"), "1")
	if got := stockOfItem(t, pool, cityID, "timber"); got != timber0 {
		t.Errorf("stock after taking it all back = %d, want %d", got, timber0)
	}

	// 5. The books: wages in the ledger equal the rows; nothing drifted.
	v1, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v1.LedgerSum != "0" || len(v1.Unbalanced) > 0 || len(v1.DriftedStacks) > 0 {
		t.Errorf("the books drifted: %+v", v1)
	}
}

// The village book (P3): a stall costs a listing fee and a trade pays dues, both
// to the treasury; stalls are limited; a stall sells only while its owner is in
// the settlement.
func TestTheVillageBook(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	cities := postgres.NewCityRepository(pool)
	market := handlers.NewMarketHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, cities,
		postgres.NewPolicyReader(pool, nil), gametime.Scale(1),
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, e.clock.Now).
		WithHome("support").
		WithVillageBook(handlers.VillageMarketRules{StallsPost: 6, StallsHall: 20, StallsPerPlayerPost: 3, StallsPerPlayerHall: 6})

	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID, supportID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE code = 'support'`).Scan(&supportID); err != nil {
		t.Fatal(err)
	}
	buyer := insertPlayer(t, pool)
	setCity := func(playerID, city string) {
		t.Helper()
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, playerID, city); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { // registered last, so it runs before the players' own cleanup
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM game_actions WHERE reference_type = 'market_orders' AND reference_id IN (SELECT id FROM market_orders WHERE city_id = $1::uuid)`,
			`DELETE FROM market_listing_fees WHERE city_id = $1::uuid`,
		} {
			if _, err := pool.Raw().Exec(c, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		// the trades are append-only: the scratch database lifts that for its own rows
		for _, stmt := range []string{`ALTER TABLE market_trades DISABLE TRIGGER market_trades_append_only`} {
			if _, err := pool.Raw().Exec(c, stmt); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		for _, stmt := range []string{`DELETE FROM market_trades WHERE city_id = $1::uuid`, `DELETE FROM market_orders WHERE city_id = $1::uuid`} {
			if _, err := pool.Raw().Exec(c, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if _, err := pool.Raw().Exec(c, `ALTER TABLE market_trades ENABLE TRIGGER market_trades_append_only`); err != nil {
			t.Errorf("cleanup enabling the trigger: %v", err)
		}
	})
	setCity(buyer.ID, cityID)
	grant(t, pool, application.AccountPlayerCash, founder.ID, 10_000)
	grant(t, pool, application.AccountPlayerCash, buyer.ID, 10_000)
	give := func(n int64) {
		t.Helper()
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: n, To: founder.ID,
				ToHolding: application.HoldCarried, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
		}); err != nil {
			t.Fatal(err)
		}
	}
	give(40)
	order := func(who *application.Player, side, qty, price string) string {
		t.Helper()
		m := asPlayer(meta, who)
		m.Command, m.Language = "market.order", "en"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		r, err := rrcm(m)(market.Order(ctx, m, handlers.MarketRequest{Side: side, Item: "bread", Qty: qty, Price: price, Method: "cash", Nonce: randomToken(t, 8)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	treasury0 := treasuryOf(t, pool, cityID)
	sellerCash0 := cashBalance(t, pool, application.AccountPlayerCash, founder.ID)

	// 1. A sell takes a stall: 1 % of 10 x 100 = 10 to the treasury, not refundable.
	order(founder, "sell", "10", "100")
	if got := treasuryOf(t, pool, cityID) - treasury0; got != 10 {
		t.Fatalf("the treasury got %d from the listing fee, want 10", got)
	}
	if got := sellerCash0 - cashBalance(t, pool, application.AccountPlayerCash, founder.ID); got != 10 {
		t.Errorf("the seller paid %d, want 10", got)
	}

	// 2. A buyer takes it: dues 3 % of 1000 = 30 to the treasury out of the proceeds.
	order(buyer, "buy", "10", "100")
	if got := treasuryOf(t, pool, cityID) - treasury0; got != 50 {
		t.Errorf("the treasury got %d in all (a listing fee of 10 from each of the two orders, 30 dues), want 50", got)
	}
	var trades, fee int64
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*), COALESCE(SUM(fee), 0) FROM market_trades WHERE city_id = $1::uuid`, cityID).Scan(&trades, &fee); err != nil || trades != 1 || fee != 30 {
		t.Errorf("trades %d fee %d (%v), want one trade with 30 dues", trades, fee, err)
	}

	// 3. A stall sells only while its owner is in the settlement.
	order(founder, "sell", "5", "100")
	setCity(founder.ID, supportID)
	order(buyer, "buy", "5", "100")
	var filled int64
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(filled), 0) FROM market_orders WHERE city_id = $1::uuid AND side = 'sell' AND status = 'open'`, cityID).Scan(&filled); err != nil || filled != 0 {
		t.Errorf("a stall sold %d with its owner away (%v)", filled, err)
	}
	// 3b. An owner who hired a keeper keeps the stall open while he is away; the keeper takes his share of the proceeds
	// (ADR 0062): 500 sold, 15 dues, 485 to share: 10 percent is 48 to the keeper, 437 to the seller.
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO stall_keepers (id, settlement_id, owner_id, share_bps, hired_at) VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 1000, now())`, cityID, founder.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Raw().Exec(context.Background(), `DELETE FROM stall_keepers WHERE settlement_id = $1::uuid`, cityID) })
	bank0 := cashBalance(t, pool, application.AccountPlayerBank, founder.ID)
	order(buyer, "buy", "5", "100")
	var keeperCut int64
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(keeper_cut), 0) FROM market_trades WHERE city_id = $1::uuid`, cityID).Scan(&keeperCut); err != nil || keeperCut != 48 {
		t.Errorf("the keeper took %d (%v), want 48", keeperCut, err)
	}
	if got := cashBalance(t, pool, application.AccountPlayerBank, founder.ID) - bank0; got != 437 {
		t.Errorf("the owner received %d, want 437", got)
	}
	if _, err := pool.Raw().Exec(ctx, `UPDATE stall_keepers SET ended_at = now() WHERE settlement_id = $1::uuid`, cityID); err != nil {
		t.Fatal(err)
	}
	setCity(founder.ID, cityID)
	order(buyer, "buy", "5", "100")
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(filled), 0) FROM market_orders WHERE city_id = $1::uuid AND side = 'sell'`, cityID).Scan(&filled); err != nil || filled != 15 {
		t.Errorf("sold units after the owner came back = %d (%v), want 15", filled, err)
	}

	// 4. A player holds three stalls at a market post: the fourth is refused.
	for i := 0; i < 3; i++ {
		order(founder, "sell", "1", "500")
	}
	if text := order(founder, "sell", "1", "500"); !strings.Contains(text, "stall") && !strings.Contains(text, "غرفه") {
		t.Errorf("a fourth stall was not refused:\n%s", text)
	}

	// 5. The books agree: dues and listing fees in the ledger are the rows.
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	s := v.ShopCheck
	if s.KeeperLedger != s.KeeperRows || s.KeeperLedger != 48 {
		t.Errorf("keeper wages %d in the ledger, %d on the trades, want 48", s.KeeperLedger, s.KeeperRows)
	}
	if s.MarketDuesLedger != s.MarketDuesRows || s.ListingLedger != s.ListingRows || s.MarketDuesLedger == 0 || s.ListingLedger == 0 {
		t.Errorf("dues %d / %d, listing fees %d / %d", s.MarketDuesLedger, s.MarketDuesRows, s.ListingLedger, s.ListingRows)
	}
}

// The people who keep the stores are not also free for hire (rule 1c, one pool),
// but a settlement that already had its stores keeps all its labourers until the
// keeper rule's grace ends, and the market line says so.
func TestKeepersLeaveTheLabourPool(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	rules := handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(rules)
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	seedTreasury(t, pool, cityID, 60_000)
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'storehouse', 80, 3, 'complete', now(), now())`, newUUID(t), cityID); err != nil {
		t.Fatal(err)
	}
	head := asPlayer(meta, founder)
	mk := func(command string) envelope.Metadata {
		m := head
		m.Command = command
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	market := func() vpres.LaborMarketLine {
		t.Helper()
		resp, err := rrcm(mk("settlement.labor.board"))(village.LaborBoard(ctx, mk("settlement.labor.board")))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.LaborBoardView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v.Market
	}
	// settle a storage day with the granary and the storehouse kept
	e.clock.Advance(24*time.Hour + time.Minute)
	if _, err := rrcm(mk("settlement.materials"))(village.Materials(ctx, mk("settlement.materials"))); err != nil {
		t.Fatal(err)
	}
	m := market()
	if m.Reserved != 2 || m.Available != m.Pool-2 {
		t.Errorf("with no grace set the keepers should already be reserved from the pool: %+v", m)
	}
	// a grace that has not ended: the seats are counted, not taken, and the line says from when
	rules.GraceFrom, rules.GraceDays = e.clock.Now(), 14
	village.WithStorage(rules)
	m = market()
	if m.Reserved != 2 || m.Available != m.Pool || m.ReservedFrom == nil {
		t.Errorf("during the grace the labourers stay free and the date is told: %+v", m)
	}
	// after it the keepers are out of the pool
	e.clock.Advance(15 * 24 * time.Hour)
	m = market()
	if m.Available != m.Pool-2 || m.ReservedFrom != nil {
		t.Errorf("after the grace the two keepers are not free: %+v", m)
	}
}

// A busy construction day must not leave the stores unkept: the day's keepers are judged
// once, at the first look, and the permanent posts come before the day labour (live
// finding 2026-10-05: a city's room fell to the open yard under its stock).
func TestKeepersAreNotStarvedByBusyLabour(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	seedTreasury(t, pool, cityID, 60_000)
	// every NPC labourer of the settlement is on a shift right now
	var n int
	for i := 0; i < 40; i++ {
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_shifts (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed,
			game_action_id, started_at, finish_at, kind, worker_kind) VALUES ($1::uuid, $2::uuid, $3::uuid, NULL, 'working', 0, 0, '{}', '{}', $4::uuid, now(), now() + interval '1 hour', 'construction', 'npc')`,
			newUUID(t), cityID, newUUID(t), newUUID(t)); err != nil {
			t.Fatal(err)
		}
		n++
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_shifts WHERE settlement_id = $1::uuid`, cityID)
	})
	head := asPlayer(meta, founder)
	head.Command = "settlement.materials"
	head.RequestID = "req_" + randomToken(t, 16)
	head.IdempotencyKey = "it-" + randomToken(t, 16)
	e.clock.Advance(24*time.Hour + time.Minute)
	resp, err := village.Materials(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	var v vpres.MaterialsView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Stores) == 0 {
		t.Fatal("the founding kit has a granary")
	}
	for _, s := range v.Stores {
		if !s.Kept {
			t.Errorf("a store is unkept because the NPC labourers were all on shifts (%d of them): %+v", n, v.Stores)
		}
	}
}

// A new city has no free labourer to hire as keeper, yet its stock must fit its room
// from the first day: the residents keep the small granary by rota (communal room),
// so the room never collapses to the open yard.
func TestAFreshCityHoldsItsStockWithoutAKeeper(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	// no money in the treasury: no keeper can be paid
	// every NPC labourer of the settlement is on a shift right now
	var n int
	for i := 0; i < 40; i++ {
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_shifts (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed,
			game_action_id, started_at, finish_at, kind, worker_kind) VALUES ($1::uuid, $2::uuid, $3::uuid, NULL, 'working', 0, 0, '{}', '{}', $4::uuid, now(), now() + interval '1 hour', 'construction', 'npc')`,
			newUUID(t), cityID, newUUID(t), newUUID(t)); err != nil {
			t.Fatal(err)
		}
		n++
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_shifts WHERE settlement_id = $1::uuid`, cityID)
	})
	head := asPlayer(meta, founder)
	head.Command = "settlement.materials"
	head.RequestID = "req_" + randomToken(t, 16)
	head.IdempotencyKey = "it-" + randomToken(t, 16)
	e.clock.Advance(24*time.Hour + time.Minute)
	resp, err := village.Materials(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	var v vpres.MaterialsView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	if v.Used > v.Capacity {
		t.Errorf("a fresh city is over capacity on day one: %d / %d", v.Used, v.Capacity)
	}
	var food int64
	for _, c := range v.Classes {
		if c.Class == "food" {
			food = c.Capacity
		}
	}
	if food < 120 {
		t.Errorf("the granary gives no room without a keeper: food room %d, want at least 120 (base 20 + communal 100); n=%d", food, n)
	}
	for _, st := range v.Stores {
		if st.Kept || st.CommunalRoom == 0 {
			t.Errorf("a store must be unkept with a communal room here, no keeper can be paid: %+v", st)
		}
	}
}

// A city whose goods were stored under the old shared room (live finding 2026-10-05,
// «مارکو پلو»: timber, stone and wool sit in the open yard, 43 spaces over its 60). Until
// the grace ends the stores that stood before the classes rule lend their room to any
// class; after it the class is named over with what to build, and nothing is destroyed.
func TestOldSharedRoomLastsThroughTheGraceThenTheClassIsNamedOver(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	rules := handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 0}
	graceFrom := e.clock.Now().Add(48 * time.Hour) // the founding granary stood before the rule
	rules.GraceFrom, rules.GraceDays = graceFrom, 14
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(rules)
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	for item, qty := range map[string]int64{"timber": 53, "stone": 25, "wool": 25, "wheat": 40} {
		item, qty := item, qty
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Items().Move(ctx, application.ItemMove{
				ID: newUUID(t), Item: item, Qty: qty,
				ToOrg: application.SettlementOrg(cityID), ToHolding: application.HoldWarehouse,
				Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	view := func() vpres.MaterialsView {
		t.Helper()
		m := asPlayer(meta, founder)
		m.Command = "settlement.materials"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		resp, err := village.Materials(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.MaterialsView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	class := func(v vpres.MaterialsView, code string) vpres.StockClassLine {
		for _, c := range v.Classes {
			if c.Class == code {
				return c
			}
		}
		t.Fatalf("no %s class in %+v", code, v.Classes)
		return vpres.StockClassLine{}
	}
	v := view()
	if b := class(v, "bulk"); b.Used != 156 || b.Over != 0 || b.Borrowed != 96 {
		t.Errorf("during the grace the old shared room lends the bulk 96 spaces (timber and stone take 2 each): %+v", b)
	}
	if v.Over != 0 || v.Transition == nil {
		t.Errorf("during the grace nothing is over and the notice stands: over %d, transition %v", v.Over, v.Transition)
	}
	// after the grace
	e.clock.Advance(49*time.Hour + 15*24*time.Hour)
	v = view()
	b := class(v, "bulk")
	if b.Used != 156 || b.Over != 96 || b.Borrowed != 0 || len(b.Build) == 0 {
		t.Errorf("after the grace the bulk is 96 over and says what to build: %+v", b)
	}
	if v.Over != 101 || v.Transition != nil {
		t.Errorf("the total over is the sum of the full classes and the notice is gone: over %d, transition %v", v.Over, v.Transition)
	}
	if f := class(v, "food"); f.Used != 40 || f.Over != 0 {
		t.Errorf("the food still fits: %+v", f)
	}
}

// Every standing building says what work it does and why it is idle (roadmap 2.2 phase 1,
// rule 1c): a workplace nobody works at is idle with no_staff, a granary is a storage node,
// a building the game gives no work to says so.
func TestBuildingPanelCarriesItsWork(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	campID := newUUID(t)
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, campID, cityID); err != nil {
		t.Fatal(err)
	}
	work := func(id string) *vpres.WorkNode {
		t.Helper()
		m := asPlayer(meta, founder)
		m.Command = "settlement.building.view"
		m.RequestID = "req_" + randomToken(t, 16)
		resp, err := village.BuildingView(ctx, m, handlers.VillageBuildingViewRequest{BuildingID: id})
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.BuildingView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		if v.Work == nil {
			t.Fatalf("building %s has no work block", id)
		}
		return v.Work
	}
	camp := work(campID)
	if (camp.Kind != vpres.NodeKindProduction && camp.Kind != vpres.NodeKindExtraction) || camp.Status != vpres.NodeIdle || camp.Max == 0 || camp.Filled != 0 {
		t.Errorf("an unworked camp is an idle production node with posts: %+v", camp)
	}
	has := func(w *vpres.WorkNode, code string) bool {
		for _, r := range w.Reasons {
			if r.Code == code {
				return true
			}
		}
		return false
	}
	if !has(camp, vpres.NodeReasonNoStaff) {
		t.Errorf("an unworked camp says no_staff: %+v", camp.Reasons)
	}
	if len(camp.Outputs) == 0 || camp.ShiftSeconds == 0 {
		t.Errorf("the camp says what a shift gives and takes: %+v", camp)
	}
	rows, err := pool.Raw().Query(ctx, `SELECT id::text, type_code FROM settlement_buildings WHERE settlement_id = $1::uuid AND status = 'complete'`, cityID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ id, code string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.code); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		w := work(r.id)
		switch r.code {
		case "granary":
			if w.Kind != vpres.NodeKindStorage {
				t.Errorf("the granary is a storage node: %+v", w)
			}
		case "road":
			if w.Kind != vpres.NodeKindNone || !has(w, vpres.NodeReasonNoFunction) {
				t.Errorf("a road has no work and says so: %+v", w)
			}
		}
	}
}

// The live shape of «مارکو پلو» (2026-10-06): two granaries built before the classes rule, a
// barter post and a civic hall, no storehouse, stock timber 53, stone 25, wool 25, wheat 40, no
// research beyond a fresh founding, and the content read the way every service reads it, from
// the DATABASE (not from the files). The view must carry the classes (wheat in food against the
// granaries, wool in goods, timber and stone in bulk), the granaries as stores, and the old
// shared room during the grace: never one flat yard of 60.
func TestMarcoPoloShapeReadsItsClassesFromTheDatabaseContent(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	// the content exactly as a booted service reads it
	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	rules := handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000}
	graceFrom := e.clock.Now().Add(-3 * 24 * time.Hour) // the keeper rule began three days ago
	rules.GraceFrom, rules.GraceDays = graceFrom, 14
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: snap}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(rules)
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	// both granaries stood before the rule: the founding kit's and a second one
	if _, err := pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET completed_at = $2 WHERE settlement_id = $1::uuid AND status = 'complete'`,
		cityID, graceFrom.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'granary', 83, 3, 'complete', $3, $3)`, newUUID(t), cityID, graceFrom.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM village_storage_days WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, cityID)
			}
			if _, err := pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		purgeLedgerFor(t, pool, cityID)
	})
	for item, qty := range map[string]int64{"timber": 53, "stone": 25, "wool": 25, "wheat": 40} {
		item, qty := item, qty
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Items().Move(ctx, application.ItemMove{
				ID: newUUID(t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(cityID), ToHolding: application.HoldWarehouse,
				Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := asPlayer(meta, founder)
	m.Command = "settlement.materials"
	m.RequestID = "req_" + randomToken(t, 16)
	m.IdempotencyKey = "it-" + randomToken(t, 16)
	resp, err := village.Materials(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	var v vpres.MaterialsView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	byClass := map[string]vpres.StockClassLine{}
	for _, c := range v.Classes {
		byClass[c.Class] = c
	}
	for _, class := range []string{"bulk", "food", "goods"} {
		if _, ok := byClass[class]; !ok {
			t.Fatalf("the view has no %s class: the content read from the database has no storage classes? %+v", class, v.Classes)
		}
	}
	if f := byClass["food"]; f.Used != 40 || f.Capacity < 120 || f.Over != 0 {
		t.Errorf("the wheat is in the food class against the granaries (base 20 + at least their communal 100 each): %+v", f)
	}
	if g := byClass["goods"]; g.Used != 25 || g.Over != 0 {
		t.Errorf("the wool is in goods, which the old shared room covers during the grace: %+v", g)
	}
	if b := byClass["bulk"]; b.Used != 156 || b.Over != 0 || b.Borrowed == 0 {
		t.Errorf("timber and stone (2 spaces a unit) are 156 in bulk, covered by the old shared room during the grace: %+v", b)
	}
	if len(v.Stores) != 2 {
		t.Errorf("the two granaries are the stores: %+v", v.Stores)
	}
	if v.Transition == nil || v.Over != 0 {
		t.Errorf("the grace notice stands and nothing is over: transition %v over %d", v.Transition, v.Over)
	}
	// after the grace the bulk is over and the view says what to build
	e.clock.Advance(15 * 24 * time.Hour)
	m.RequestID, m.IdempotencyKey = "req_"+randomToken(t, 16), "it-"+randomToken(t, 16)
	resp, err = village.Materials(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	v = vpres.MaterialsView{}
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Classes {
		if c.Class == "bulk" && (c.Over != 96 || len(c.Build) == 0) {
			t.Errorf("after the grace bulk is 96 over (156 in 60) and names the storehouse: %+v", c)
		}
	}
}
