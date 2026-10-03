//go:build integration

// Integration test of the storage and market fixes (docs/research/2026-10-03-
// storage-market-audit.md, P0): a founded settlement without a market post has
// no market (F4) and its Economy hub does not list one (F6).
package tests

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
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
