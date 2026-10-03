//go:build integration

// The village shop end to end against PostgreSQL (docs/adr/0046 section 5,
// phase M2): the morning delivery happens once per game day however many
// replicas try, the shopkeeper's day is paid from the treasury, a purchase moves
// the shelf, the player's daily count, the money and the goods in one
// transaction and only where there is room, the head's levers bound the price,
// a village with no shopkeeper or no wage is frozen, the shop never buys back,
// and `admin economy verify` stays green.
package tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/vshop"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"path/filepath"
)

type shopEnv struct {
	*laborEnv
	shop  *handlers.VillageHandler
	rules handlers.ShopRules
	clk   gametime.Clock
}

func newShopEnv(t *testing.T) *shopEnv {
	t.Helper()
	l := newLaborEnv(t)
	if n := countRows(t, l.pool, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'village_shop_days'`); n == 0 {
		t.Skip("village_shop_days does not exist; apply migration 0109 first")
	}
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	// A game hour is a real minute; the epoch makes the clock read 07:00 on day 0 right now.
	clk := gametime.Clock{Epoch: l.clock.Now().Add(-7 * time.Minute), Scale: 60}
	rules := handlers.ShopRules{
		Rules: vshop.Rules{MarkupMinBPS: cfg.Merchant.MarkupMinBPS, MarkupMaxBPS: cfg.Merchant.MarkupMaxBPS, StockDays: cfg.Merchant.StockDays,
			FoodShareBPS: cfg.Merchant.FoodShareBPS, OtherShareBPS: cfg.Merchant.OtherShareBPS, PlayerDayFood: cfg.Merchant.PlayerDayFood,
			PlayerDayOther: cfg.Merchant.PlayerDayOther, SupplyValuePerResident: cfg.Merchant.SupplyValueFood, BuildingBoostBPS: cfg.Merchant.BuildingBoostBPS},
		RestockHour: int(cfg.Merchant.RestockHour), CapPresets: cfg.Merchant.CapPresets, BuyPresets: cfg.Merchant.BuyPresets,
		TaxPresets: cfg.Merchant.TaxPresets, TaxDefault: cfg.Merchant.TaxDefaultBPS, TaxMax: cfg.Merchant.TaxMaxBPS,
		NilUnitSup: cfg.Premium.NilUnitSup, NilExamples: cfg.Premium.NilExamples, OutputDays: int(cfg.Merchant.OutputDays),
		Carry: cfg.CarryRules(), Clock: clk,
	}
	uow := postgres.NewUnitOfWork(l.pool, testDefaultLanguage)
	shop := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, l.cache,
		postgres.NewCityRepository(l.pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, l.clock.Now).WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).WithShop(rules)
	e := &shopEnv{laborEnv: l, shop: shop, rules: rules, clk: clk}
	cityID := l.cityID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM village_shop_sales WHERE settlement_id = $1::uuid`,
			`DELETE FROM village_shop_player_day WHERE settlement_id = $1::uuid`,
			`DELETE FROM village_shop_terms WHERE settlement_id = $1::uuid`,
			`DELETE FROM village_shop_days WHERE settlement_id = $1::uuid`,
			`DELETE FROM village_shop_lines WHERE settlement_id = $1::uuid`,
		} {
			if _, err := l.pool.Raw().Exec(ctx, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	return e
}

func (e *shopEnv) buyer(t *testing.T, cash int64) *application.Player {
	t.Helper()
	p := e.resident()
	// Registered after the player, so the ledger is purged before the player goes.
	t.Cleanup(func() { purgeLedgerFor(t, e.pool, p.ID) })
	if cash > 0 {
		grant(t, e.pool, application.AccountPlayerCash, p.ID, cash)
	}
	return p
}

func (e *shopEnv) view(t *testing.T, p *application.Player) village.ShopView {
	t.Helper()
	resp, err := e.shop.Shop(testCtx(t), e.as(p, "settlement.shop", "shop"))
	if err != nil {
		t.Fatal(err)
	}
	var v village.ShopView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatalf("the shop view does not decode: %v (%s)", err, resp.Screen)
	}
	return v
}

func (e *shopEnv) lineOf(v village.ShopView, code string) (village.ShopLine, bool) {
	for _, l := range v.Lines {
		if l.Item.Code == code {
			return l, true
		}
	}
	return village.ShopLine{}, false
}

// buy asks for a purchase the two-step way and returns the answer's screen.
func (e *shopEnv) buy(t *testing.T, p *application.Player, item string, qty string, method string, nonce string) *presentation.Response {
	t.Helper()
	resp, err := e.shop.ShopBuy(testCtx(t), e.as(p, "settlement.shop.buy", "shop.buy"),
		handlers.VillageShopRequest{Item: item, Qty: qty, Method: method, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *shopEnv) day(t *testing.T) (outcome string, wage int64, rows int) {
	t.Helper()
	rows = countRows(t, e.pool, `SELECT count(*) FROM village_shop_days WHERE settlement_id = $1::uuid`, e.cityID)
	_ = e.pool.Raw().QueryRow(testCtx(t), `SELECT outcome, wage FROM village_shop_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`,
		e.cityID).Scan(&outcome, &wage)
	return outcome, wage, rows
}

func TestTheMorningDeliveryHappensOncePerGameDayWhoeverTries(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	treasury := cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID)

	// Five replicas look at the shop at once: one delivery, one wage.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
				s, err := tx.Settlements().ByID(ctx, e.cityID)
				if err != nil {
					return err
				}
				_, err = e.shop.SettleShopDay(ctx, tx, s, e.clock.Now())
				return err
			}); err != nil {
				t.Errorf("SettleShopDay: %v", err)
			}
		}()
	}
	wg.Wait()
	outcome, wage, rows := e.day(t)
	if rows != 1 || outcome != application.VillageShopDelivered || wage < 1 {
		t.Fatalf("days = %d, outcome %q, wage %d: want one delivery with a wage", rows, outcome, wage)
	}
	if got := treasury - cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID); got != wage {
		t.Errorf("the treasury paid %d, want exactly one wage of %d", got, wage)
	}
	if n := countRows(t, e.pool, `SELECT count(DISTINCT transaction_id) FROM ledger_entries WHERE reason = 'shopkeeper_wage' AND reference_id = $1::uuid`, e.cityID); n != 1 {
		t.Errorf("shopkeeper_wage transactions = %d, want 1", n)
	}

	// The founding stall's set is on the shelf, limited: nothing above the supply budget.
	p := e.buyer(t, 0)
	v := e.view(t, p)
	if v.Closed != village.ShopOpen {
		t.Fatalf("the shop is shut: %q", v.Closed)
	}
	for _, code := range []string{"bread", "water_bottle", "bag_pouch", "bag_sack"} {
		l, ok := e.lineOf(v, code)
		if !ok || l.Stock < 1 {
			t.Errorf("%s is not on the founding shelf (%+v)", code, l)
		}
	}
	if _, ok := e.lineOf(v, "bag_daypack"); ok {
		t.Error("the small rucksack needs the shop building but is on the shelf")
	}
	if len(v.Locked) == 0 {
		t.Error("no hint of what a shop building would add")
	}
	var value, budget int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT delivered_value, budget FROM village_shop_days WHERE settlement_id = $1::uuid`, e.cityID).Scan(&value, &budget); err != nil {
		t.Fatal(err)
	}
	if value > budget {
		t.Errorf("delivered value %d is over the supply budget %d", value, budget)
	}

	// The next morning: a new delivery, and the shelf cap trims what does not fit.
	e.clock.Advance(24 * time.Minute)
	e.view(t, p)
	if _, _, rows := e.day(t); rows != 2 {
		t.Fatalf("days after a second morning = %d, want 2", rows)
	}
	// a shelf holds two days: the third morning with nothing sold is turned away in part,
	// and what was turned away still counts as delivered (the shelf adds up)
	e.clock.Advance(24 * time.Minute)
	e.view(t, p)
	if _, _, rows := e.day(t); rows != 3 {
		t.Fatalf("days after a third morning = %d, want 3", rows)
	}
	var stock, delivered, trimmed, sold int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT stock, delivered_total, trimmed_total, sold_total FROM village_shop_lines WHERE settlement_id = $1::uuid AND line = 'bread'`,
		e.cityID).Scan(&stock, &delivered, &trimmed, &sold); err != nil {
		t.Fatal(err)
	}
	if trimmed < 1 {
		t.Errorf("bread: nothing was trimmed after three mornings with no sale (stock %d, delivered %d)", stock, delivered)
	}
	if stock != delivered-sold-trimmed || stock < 1 {
		t.Errorf("bread: stock %d, delivered %d, sold %d, trimmed %d: the shelf does not add up", stock, delivered, sold, trimmed)
	}
	verifyShopLedger(t, e.pool)
}

func TestABuyerPaysTheShelfLosesAndTheGoodsArrive(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	p := e.buyer(t, 5_000)
	v := e.view(t, p)
	bread, ok := e.lineOf(v, "bread")
	if !ok {
		t.Fatal("no bread on the shelf")
	}
	treasury := cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID)
	cash := cashBalance(t, e.pool, application.AccountPlayerCash, p.ID)

	// The first press is the checkout and changes nothing.
	resp := e.buy(t, p, "bread", "2", "", "")
	if resp.Screen != village.ScreenVillageShopCheckout {
		t.Fatalf("the first press answered %q, want the checkout", resp.Screen)
	}
	if got := cashBalance(t, e.pool, application.AccountPlayerCash, p.ID); got != cash {
		t.Fatalf("the checkout moved money: %d -> %d", cash, got)
	}
	var co village.ShopCheckoutView
	if err := presentation.DecodeView(resp.View, &co); err != nil {
		t.Fatal(err)
	}
	if co.Nonce == "" || co.Unit < bread.Reference || co.Unit*10_000 > bread.Reference*15_000 {
		t.Fatalf("checkout %+v: the price must be between the reference and 1.5 times it", co)
	}

	// The second press, twice (a double tap): one sale.
	for i := 0; i < 2; i++ {
		e.buy(t, p, "bread", "2", "cash", co.Nonce)
	}
	if got := (&goodsWorld{pool: e.pool}).held(t, p.ID, "bread", application.HoldCarried); got != 2 {
		t.Fatalf("bread carried = %d, want 2", got)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM village_shop_sales WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("sales = %d, want 1", n)
	}
	var unit, ref, total, tax int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT unit_price, reference_price, total, tax FROM village_shop_sales WHERE player_id = $1::uuid`, p.ID).
		Scan(&unit, &ref, &total, &tax); err != nil {
		t.Fatal(err)
	}
	if got := cash - cashBalance(t, e.pool, application.AccountPlayerCash, p.ID); got != total+tax {
		t.Errorf("cash spent %d, want %d + tax %d", got, total, tax)
	}
	if got := cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID) - treasury; got != tax {
		t.Errorf("the treasury got %d, want the tax %d", got, tax)
	}
	var stock, soldTotal int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT stock, sold_total FROM village_shop_lines WHERE settlement_id = $1::uuid AND line = 'bread'`, e.cityID).
		Scan(&stock, &soldTotal); err != nil {
		t.Fatal(err)
	}
	if stock != bread.Stock-2 || soldTotal != 2 {
		t.Errorf("the shelf: stock %d (was %d), sold %d", stock, bread.Stock, soldTotal)
	}

	// A player's daily cap: 3 loaves a day. One more is allowed, two more are not.
	if resp := e.buy(t, p, "bread", "1", "cash", e.nonceFor(t)); resp.Screen == village.ScreenVillageShopRefusal {
		t.Fatalf("the third loaf was refused: %s", string(resp.View))
	}
	resp = e.buy(t, p, "bread", "1", "cash", e.nonceFor(t))
	if resp.Screen != village.ScreenVillageShopRefusal || resp.Refusal == nil || resp.Refusal.Code != village.ShopRefusalCode(village.ShopRefusedCap) {
		t.Fatalf("the fourth loaf of the day: screen %q refusal %+v, want the daily cap", resp.Screen, resp.Refusal)
	}
	// The shop never buys back: no buy-back leg in the ledger, no way to ask for one.
	if n := countRows(t, e.pool, `SELECT count(*) FROM ledger_entries WHERE reason = 'shop_buyback' AND reference_type = 'village_shop_sales'`); n != 0 {
		t.Errorf("buy-back transactions = %d, want none", n)
	}
	// The next morning the cap resets.
	e.clock.Advance(24 * time.Minute)
	if resp := e.buy(t, p, "bread", "1", "cash", e.nonceFor(t)); resp.Screen == village.ScreenVillageShopRefusal {
		t.Errorf("after a new morning the bread was still refused: %s", string(resp.View))
	}
	verifyShopLedger(t, e.pool)
}

func (e *shopEnv) nonceFor(t *testing.T) string { return (&goodsWorld{}).nonce(t) }

func TestNoRoomNoBuy(t *testing.T) {
	e := newShopEnv(t)
	p := e.buyer(t, 5_000)
	e.view(t, p) // the morning delivery
	// Seven loaves in a pack of eight: one place left, a sack needs one and a second does not fit.
	(&goodsWorld{uow: e.uowOf(), now: e.clock.Now()}).giveBread(t, p.ID, 7)
	before := cashBalance(t, e.pool, application.AccountPlayerCash, p.ID)
	resp := e.buy(t, p, "bag_sack", "2", "cash", e.nonceFor(t))
	if resp.Screen != village.ScreenVillageShopRefusal || resp.Refusal == nil || resp.Refusal.Code != village.ShopRefusalCode(village.ShopRefusedNoSpace) {
		t.Fatalf("a sack too many: screen %q refusal %+v, want no room", resp.Screen, resp.Refusal)
	}
	if got := cashBalance(t, e.pool, application.AccountPlayerCash, p.ID); got != before {
		t.Errorf("a refused sale moved money: %d -> %d", before, got)
	}
	if n := countRows(t, e.pool, `SELECT count(*) FROM village_shop_sales WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Errorf("a sale was recorded for a refusal: %d", n)
	}
	// One sack fits (1 room) and, worn, it opens twelve more.
	if resp := e.buy(t, p, "bag_sack", "1", "cash", e.nonceFor(t)); resp.Screen == village.ScreenVillageShopRefusal {
		t.Fatalf("the one sack that fits was refused: %s", string(resp.View))
	}
}

func (e *shopEnv) uowOf() application.UnitOfWork {
	return postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
}

func TestAFrozenShopSellsNothing(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	p := e.buyer(t, 5_000)
	// The treasury cannot pay the shopkeeper: he stays home and the shop is shut that day.
	var bal int64 = cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID)
	spendTreasury(t, e.pool, e.cityID, bal)
	v := e.view(t, p)
	if v.Closed != village.ShopUnpaid {
		t.Fatalf("with an empty treasury the shop is %q, want unpaid", v.Closed)
	}
	if outcome, wage, _ := e.day(t); outcome != application.VillageShopUnpaid || wage != 0 {
		t.Fatalf("the day was recorded as %q with wage %d", outcome, wage)
	}
	resp := e.buy(t, p, "bread", "1", "cash", e.nonceFor(t))
	if resp.Screen != village.ScreenVillageShopRefusal || resp.Refusal == nil || resp.Refusal.Code != village.ShopRefusalCode(village.ShopRefusedClosed) {
		t.Fatalf("a purchase from a shut shop: screen %q refusal %+v", resp.Screen, resp.Refusal)
	}
	// Fund the treasury: the next morning the shop is open again.
	seedTreasury(t, e.pool, e.cityID, 5_000)
	e.clock.Advance(24 * time.Minute)
	if v := e.view(t, p); v.Closed != village.ShopOpen {
		t.Fatalf("after funds and a new morning the shop is still %q", v.Closed)
	}

	// Nobody to keep it: more people living there than homes, so the labour pool is empty.
	for i := 0; i < 20; i++ {
		e.resident()
	}
	e.clock.Advance(24 * time.Minute)
	if v := e.view(t, p); v.Closed != village.ShopNoShopkeeper {
		t.Fatalf("a village with nobody to hire has its shop %q, want no_shopkeeper", v.Closed)
	}
	var wage int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT wage FROM village_shop_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, e.cityID).Scan(&wage); err != nil || wage != 0 {
		t.Errorf("a frozen day paid %d (%v), want 0", wage, err)
	}
	verifyShopLedger(t, e.pool)
}

func spendTreasury(t *testing.T, pool *postgres.Pool, cityID string, amount int64) {
	t.Helper()
	if amount <= 0 {
		return
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonMaintenance, CreatedAt: time.Now().UTC(),
			Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: moneyFromMinor(-amount)},
				{AccountID: application.SystemSinkAccountID, Amount: moneyFromMinor(amount)},
			}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTheHeadsLeversBoundThePriceAndOnlyTheHeadMovesThem(t *testing.T) {
	e := newShopEnv(t)
	p := e.buyer(t, 5_000)
	v := e.view(t, p)
	bread, _ := e.lineOf(v, "bread")
	if bread.Price != bread.Reference*11_000/10_000 {
		t.Fatalf("the usual price is %d, want the reference %d plus 10 %%", bread.Price, bread.Reference)
	}
	// A resident who is not the head cannot move the ceiling.
	resp, err := e.shop.ShopCap(testCtx(t), e.as(p, "settlement.shop.cap", "shop.cap"), handlers.VillageShopRequest{BPS: "10000"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Screen != village.ScreenVillageRefusal {
		t.Fatalf("a resident setting the cap got %q, want a refusal", resp.Screen)
	}
	// The head holds it down to the reference; refused outside the bounds.
	if _, err := e.shop.ShopCap(testCtx(t), e.as(e.head, "settlement.shop.cap", "shop.cap"), handlers.VillageShopRequest{BPS: "10000"}); err != nil {
		t.Fatal(err)
	}
	if v := e.view(t, p); func() int64 { l, _ := e.lineOf(v, "bread"); return l.Price }() != bread.Reference {
		t.Fatalf("with the ceiling at 1.0x the bread costs %d, want the reference %d", func() int64 { l, _ := e.lineOf(v, "bread"); return l.Price }(), bread.Reference)
	}
	if resp, err := e.shop.ShopCap(testCtx(t), e.as(e.head, "settlement.shop.cap", "shop.cap"), handlers.VillageShopRequest{BPS: "9000"}); err != nil ||
		resp.Refusal == nil || resp.Refusal.Code != village.ShopRefusalCode(village.ShopRefusedCapRange) {
		t.Fatalf("a ceiling under the reference price: %+v %v", resp, err)
	}
	if resp, err := e.shop.ShopTax(testCtx(t), e.as(e.head, "settlement.shop.tax", "shop.tax"), handlers.VillageShopRequest{BPS: "9999"}); err != nil ||
		resp.Refusal == nil {
		t.Fatalf("a tax over the bound: %+v %v", resp, err)
	}
	if _, err := e.shop.ShopTax(testCtx(t), e.as(e.head, "settlement.shop.tax", "shop.tax"), handlers.VillageShopRequest{BPS: "500"}); err != nil {
		t.Fatal(err)
	}
	if v := e.view(t, p); v.TaxBPS != 500 || v.PriceCapBPS != 10_000 {
		t.Errorf("terms = tax %d cap %d, want 500 and 10000", v.TaxBPS, v.PriceCapBPS)
	}
}

func TestAShopBuildingMendsABag(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	p := e.buyer(t, 5_000)
	e.view(t, p)
	granter := handlers.NewBagGranter(e.uowOf(), workIDs{t}, staticContentSource{snap: loadTestContent(t)}, e.rules.Carry, e.clk, e.clock.Now)
	if _, err := granter.GrantOne(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE item_pieces SET uses_left = 10 WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, p.ID); err != nil {
		t.Fatal(err)
	}
	var serial string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, p.ID).Scan(&serial); err != nil {
		t.Fatal(err)
	}
	// Without a shop building there is no mending counter.
	resp, err := e.shop.ShopRepair(ctx, e.as(p, "settlement.shop.repair", "shop.repair"), handlers.VillageShopRequest{Item: serial})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Refusal == nil || resp.Refusal.Code != village.ShopRefusalCode(village.ShopRefusedNoBuilding) {
		t.Fatalf("mending without a shop building: %+v", resp.Refusal)
	}
	// Stand a shop building (the founding research is the barter ring) and mend.
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'general_store', 4, 4, 'complete', $3, $3)`, newUUID(t), e.cityID, e.clock.Now()); err != nil {
		t.Skipf("cannot stand a shop building on lot 4,4: %v", err)
	}
	cash := cashBalance(t, e.pool, application.AccountPlayerCash, p.ID)
	for i := 0; i < 2; i++ { // a double press mends once
		if _, err := e.shop.ShopRepair(ctx, e.as(p, "settlement.shop.repair", "shop.repair"), handlers.VillageShopRequest{Item: serial}); err != nil {
			t.Fatal(err)
		}
	}
	var uses int
	if err := e.pool.Raw().QueryRow(ctx, `SELECT uses_left FROM item_pieces WHERE serial = $1`, serial).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if uses != 40 {
		t.Errorf("the mended sack has %d points, want a whole 40", uses)
	}
	// 30 of 40 missing: three quarters of a quarter of the 80 price.
	if got := cash - cashBalance(t, e.pool, application.AccountPlayerCash, p.ID); got != 15 {
		t.Errorf("mending cost %d, want 15", got)
	}
	if n := countRows(t, e.pool, `SELECT count(DISTINCT transaction_id) FROM ledger_entries WHERE reason = 'bag_repair' AND reference_type = 'bag_repairs'`); n < 1 {
		t.Error("no bag_repair transaction")
	}
	// A shop building carries the small rucksack and a bigger delivery.
	e.clock.Advance(24 * time.Minute)
	if v := e.view(t, p); !v.Building {
		t.Error("the view does not know the shop building stands")
	} else if _, ok := e.lineOf(v, "bag_daypack"); !ok {
		t.Error("a village with a shop building does not carry the small rucksack")
	}
	verifyShopLedger(t, e.pool)
}

var (
	_ = errors.New
	_ envelope.Metadata
)

func moneyFromMinor(n int64) money.Amount { return money.FromMinor(n) }

// verifyShopLedger runs the verifier and holds the money, the goods and the
// shop's own invariants to account. (The village treasury's founding grants are
// left to their own tests: a test village is funded by a plain grant.)
func verifyShopLedger(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(testCtx(t), 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || len(v.Drifted) != 0 || len(v.DriftedStacks) != 0 || v.OrphanPieces != 0 {
		t.Errorf("the ledger or the item journal drifted: sum %s unbalanced %v drifted %v stacks %v orphans %d",
			v.LedgerSum, v.Unbalanced, v.Drifted, v.DriftedStacks, v.OrphanPieces)
	}
	if !v.Shop {
		t.Fatal("the verifier does not know the village shop")
	}
	if s := v.ShopCheck; !s.Holds() {
		t.Errorf("the shop's invariants do not hold: %+v", s)
	}
}

// The village's own tick (the literacy tick every settlement already has) settles
// the morning delivery too, so a village nobody looks at is still stocked.
func TestTheVillageTickDeliversTheMorning(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	if _, err := e.shop.Taught(ctx, e.as(e.head, "settlement.taught", "taught"), handlers.CrimeScheduledRequest{ReferenceID: e.cityID}); err != nil {
		t.Fatalf("Taught: %v", err)
	}
	if outcome, wage, rows := e.day(t); rows != 1 || outcome != application.VillageShopDelivered || wage < 1 {
		t.Fatalf("after the tick: %d days, %q, wage %d: want one delivery", rows, outcome, wage)
	}
	// A second tick the same morning delivers nothing more.
	if _, err := e.shop.Taught(ctx, e.as(e.head, "settlement.taught", "taught"), handlers.CrimeScheduledRequest{ReferenceID: e.cityID}); err != nil {
		t.Fatalf("Taught again: %v", err)
	}
	if _, _, rows := e.day(t); rows != 1 {
		t.Errorf("days after a second tick = %d, want 1", rows)
	}
	verifyShopLedger(t, e.pool)
}

// The money panel is a reading: it quotes the neutral currency in Nil, reads the
// treasury from the ledger and the basket from the shop, says what the currency
// does not have yet, and moves no money.
func TestTheMoneyPanelQuotesNilAndMovesNothing(t *testing.T) {
	e := newShopEnv(t)
	ctx := testCtx(t)
	p := e.buyer(t, 0)
	e.view(t, p) // the morning delivery, so the shelf has prices
	entries := func() int {
		return countRows(t, e.pool, `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id WHERE a.owner_id = $1::uuid`, e.cityID)
	}
	before := entries()
	resp, err := e.shop.Money(ctx, e.as(p, "settlement.money", "money"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Screen != village.ScreenVillageMoney {
		t.Fatalf("screen %q: %s", resp.Screen, string(resp.View))
	}
	var v village.MoneyView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	if v.NilUnitSup != 100 || v.NilPerUnitMicro != 10_000 {
		t.Errorf("the quote is %d micro-Nil per unit with nil_unit_sup %d, want 10000 and 100", v.NilPerUnitMicro, v.NilUnitSup)
	}
	for _, ex := range v.Examples {
		if want := ex.Amount * 10_000; ex.NilMicro != want {
			t.Errorf("%d SUP quoted as %d micro-Nil, want %d", ex.Amount, ex.NilMicro, want)
		}
	}
	if want := cashBalance(t, e.pool, application.AccountCityTreasury, e.cityID); v.Treasury != want {
		t.Errorf("the panel's treasury %d is not the ledger's %d", v.Treasury, want)
	}
	if v.TreasuryNilMicro != v.Treasury*10_000 {
		t.Errorf("the treasury in Nil: %d", v.TreasuryNilMicro)
	}
	if v.Market != village.MoneyNone || v.Reserve != village.MoneyNone || v.Currency.Issued {
		t.Errorf("the panel claims a market or a reserve that does not exist: %+v", v)
	}
	if len(v.Basket) == 0 || v.IndexBPS < 10_000 || v.IndexBPS > 15_000 || v.CoverBPS < 1 {
		t.Errorf("basket %d lines, index %d, cover %d: the shop prices must read between the reference and 1.5 times it", len(v.Basket), v.IndexBPS, v.CoverBPS)
	}
	if after := entries(); after != before {
		t.Errorf("reading the panel moved money: %d ledger entries became %d", before, after)
	}
	verifyShopLedger(t, e.pool)
}
