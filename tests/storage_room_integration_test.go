//go:build integration

// Room everywhere goods come in (storage and market audit P1): no room, no buy;
// what arrives with no room waits in the holding slot and is claimed; «انبار من»
// keeps goods at home. Against PostgreSQL, on the bag world and the founding world.
package tests

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// moveGrant puts n units of a good in one of the player's holdings from a
// recorded origin (a test grant), bypassing every room check.
func (w *goodsWorld) moveGrant(t *testing.T, playerID, item string, n int64, holding string) {
	t.Helper()
	if err := w.uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: item, Qty: n, To: playerID,
			ToHolding: holding, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: w.now})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNoRoomNoBuyAndTheHoldingSlot(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	cfg := config.Defaults()
	clock := w.clock
	scale := gametime.Scale(gameScale)
	shops := handlers.NewShopsHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, w.clockFunc()).
		WithCarry(cfg.CarryRules(), clock)
	bag := w.bag
	base := cfg.CarryRules().Base // hands and pockets, no bag worn

	p := w.shopper(t, "bazaar", 100_000)
	buy := func(qty string) string {
		t.Helper()
		r, err := rrcm(w.meta(t, p, "shop.buy"))(shops.Buy(ctx, w.meta(t, p, "shop.buy"),
			handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: qty, Method: "cash", Nonce: w.nonce(t)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}

	// Past the room: refused before the checkout, with the room it lacks, and
	// no money moves.
	cash := w.purse(t, application.AccountPlayerCash, p.ID)
	if text := buy("9"); !strings.Contains(text, "جا ندارید") {
		t.Errorf("a purchase past the room was not refused for room:\n%s", text)
	}
	if got := w.purse(t, application.AccountPlayerCash, p.ID); got != cash {
		t.Errorf("a refused purchase moved %d", cash-got)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 0 {
		t.Errorf("a refused purchase left %d bread", got)
	}
	// What fits is sold.
	if text := buy("1"); strings.Contains(text, "جا ندارید") {
		t.Errorf("a purchase that fits was refused:\n%s", text)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != 1 {
		t.Fatalf("bread carried = %d, want 1", got)
	}

	// Fill the pack to the brim, then a reward goes to the holding slot, not
	// the bags: it is never lost and never over-fills the pack.
	fill := base - 1 // one loaf already carried; bread takes one space each
	w.giveBread(t, p.ID, fill)
	if v := w.inventory(t, p); v.Carry.Used != base || len(v.Claims) != 0 {
		t.Fatalf("carry used = %d (want %d), claims = %d", v.Carry.Used, base, len(v.Claims))
	}
	w.moveGrant(t, p.ID, "bread", 3, application.HoldClaim)
	v := w.inventory(t, p)
	if len(v.Claims) != 1 || v.Claims[0].Qty != 3 {
		t.Fatalf("the holding slot shows %+v, want 3 bread", v.Claims)
	}
	if v.Carry.Used != base {
		t.Errorf("the holding slot took room in the bags: used %d", v.Carry.Used)
	}

	// Claiming with a full pack is refused for room, and nothing moves.
	claim := func(qty string) string {
		t.Helper()
		r, err := rrcm(w.meta(t, p, "inventory.claim"))(bag.Claim(ctx, w.meta(t, p, "inventory.claim"),
			handlers.StorageRequest{Item: "bread", Qty: qty, Nonce: w.nonce(t)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	if text := claim("3"); !strings.Contains(text, "جا ندارید") {
		t.Errorf("a claim into a full pack was not refused:\n%s", text)
	}
	if got := w.held(t, p.ID, "bread", application.HoldClaim); got != 3 {
		t.Fatalf("bread in the slot = %d after a refused claim, want 3", got)
	}
	// Make room for two: a claim takes as many as fit, the rest keeps waiting.
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: 2, From: p.ID,
			FromHolding: application.HoldCarried, Reason: application.ItemDropped, At: w.now})
	}); err != nil {
		t.Fatal(err)
	}
	claim("3")
	if got := w.held(t, p.ID, "bread", application.HoldClaim); got != 1 {
		t.Errorf("bread left in the slot = %d, want 1", got)
	}
	if got := w.held(t, p.ID, "bread", application.HoldCarried); got != base {
		t.Errorf("bread carried = %d, want the full pack %d", got, base)
	}
	// Nothing appeared or vanished: the journal and the stacks agree.
	verifyLedger(t, w.pool)
}

func TestHomeStoreNeedsAStoringBuilding(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	cfg := config.Defaults()
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-time.Hour), Scale: gameScale}
	inv := handlers.NewInventoryHandler(uow, workIDs{t}, nil, staticContentSource{snap: loadTestContent(t)}, postgres.NewCityRepository(pool),
		gametime.Scale(gameScale), crimeRules().Nerve, 10, time.Hour, e.clock.Now).WithCarry(cfg.CarryRules(), clock)

	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	head := asPlayer(meta, founder)
	mk := func(command string) envelope.Metadata {
		m := head
		m.Command, m.Language = command, "en"
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	give := func(n int64, holding string) {
		t.Helper()
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: n, To: founder.ID,
				ToHolding: holding, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: e.clock.Now()})
		}); err != nil {
			t.Fatal(err)
		}
	}
	held := func(holding string) int64 {
		var n int64
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(sum(quantity), 0) FROM item_stacks WHERE player_id = $1::uuid AND item_code = 'bread' AND holding = $2`,
			founder.ID, holding).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	store := func(qty string) string {
		t.Helper()
		r, err := rrcm(mk("inventory.store"))(inv.Store(ctx, mk("inventory.store"), handlers.StorageRequest{Item: "bread", Qty: qty, Nonce: randomToken(t, 8)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	give(5, application.HoldCarried)

	// No storing building: nowhere to keep it.
	if text := store("5"); !strings.Contains(text, "ندارید") {
		t.Errorf("storing with no shed or house was not refused:\n%s", text)
	}
	if held(application.HoldHome) != 0 {
		t.Fatal("goods went into a store that does not exist")
	}

	// A finished shed on the founder's lot (150 space): the store opens.
	buildingID := newUUID(t)
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'private_shed', 90, 90, 'complete', now(), now())`, buildingID, cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
	    VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 0, now())`, buildingID, cityID, founder.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_, _ = pool.Raw().Exec(c, `DELETE FROM settlement_private_buildings WHERE building_id = $1::uuid`, buildingID)
	})
	store("3")
	if held(application.HoldHome) != 3 || held(application.HoldCarried) != 2 {
		t.Fatalf("home %d, carried %d after storing 3 of 5; want 3 and 2", held(application.HoldHome), held(application.HoldCarried))
	}
	// The screen shows the store and its space.
	resp, err := rrcm(mk("inventory.show"))(inv.Show(ctx, mk("inventory.show"), handlers.PageRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var v plife.InventoryView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	if v.Home == nil || v.Home.Capacity != 150 || v.Home.Used != 3 || !v.Home.Here {
		t.Errorf("the home store view = %+v, want 3 of 150, here", v.Home)
	}
	// Fetch brings it back.
	if r, err := rrcm(mk("inventory.fetch"))(inv.Fetch(ctx, mk("inventory.fetch"), handlers.StorageRequest{Item: "bread", Qty: "10", Nonce: randomToken(t, 8)})); err != nil {
		t.Fatal(err)
	} else if strings.Contains(r.Text, "جا ندارید") {
		t.Errorf("fetch was refused for room:\n%s", r.Text)
	}
	if held(application.HoldHome) != 0 || held(application.HoldCarried) != 5 {
		t.Errorf("home %d, carried %d after fetching; want 0 and 5", held(application.HoldHome), held(application.HoldCarried))
	}
}

// A bid keeps the room of its goods from the moment it is placed, so a fill
// never fails for space and a second bid cannot take the same room.
func TestABidReservesItsRoom(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	cfg := config.Defaults()
	scale := gametime.Scale(gameScale)
	market := handlers.NewMarketHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale,
		handlers.MarketLimits{OrderTTL: time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, w.clockFunc()).
		WithCarry(cfg.CarryRules(), w.clock)
	base := cfg.CarryRules().Base
	p := w.shopper(t, "bazaar", 100_000)
	bid := func(qty int64) string {
		t.Helper()
		m := w.meta(t, p, "market.order")
		r, err := rrcm(m)(market.Order(ctx, m, handlers.MarketRequest{Side: "buy", Item: "bread", Qty: strconv.FormatInt(qty, 10), Price: "50",
			Method: "cash", Nonce: w.nonce(t)}))
		if err != nil {
			t.Fatal(err)
		}
		return r.Text
	}
	cash := w.purse(t, application.AccountPlayerCash, p.ID)
	if text := bid(base + 1); !strings.Contains(text, "جا ندارید") {
		t.Errorf("a bid past the room was not refused:\n%s", text)
	}
	if w.purse(t, application.AccountPlayerCash, p.ID) != cash {
		t.Error("a refused bid moved money")
	}
	if text := bid(base); strings.Contains(text, "جا ندارید") {
		t.Errorf("a bid that fits was refused:\n%s", text)
	}
	if text := bid(1); !strings.Contains(text, "جا ندارید") {
		t.Errorf("a second bid took the room the first one reserved:\n%s", text)
	}
	if v := w.inventory(t, p); v.Carry.Reserved != base {
		t.Errorf("reserved = %d, want %d", v.Carry.Reserved, base)
	}
}
