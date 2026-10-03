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
	if _, c := room(v, "food"); c != 20 {
		t.Errorf("food room with an unkept granary = %d, want the base 20", c)
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
	if s.MarketDuesLedger != s.MarketDuesRows || s.ListingLedger != s.ListingRows || s.MarketDuesLedger == 0 || s.ListingLedger == 0 {
		t.Errorf("dues %d / %d, listing fees %d / %d", s.MarketDuesLedger, s.MarketDuesRows, s.ListingLedger, s.ListingRows)
	}
}
