//go:build integration

// Bags end to end against PostgreSQL (docs/adr/0046 section 4, phase M1): the
// free sack of the rollout is given once and worn, the space it gives is
// counted, a bag wears by the game day only when it is carried at least half
// full, a torn bag gives half its room, and a bag that leaves its wearer is no
// longer worn.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// bagWorld is the goods world plus the carry rules and a clock the test moves:
// a game hour is a real minute, so a game day is 24 real minutes.
type bagWorld struct {
	*goodsWorld
	clock   gametime.Clock
	granter *handlers.BagGranter
	bag     *handlers.InventoryHandler
}

func newBagWorld(t *testing.T) *bagWorld {
	t.Helper()
	w := newGoodsWorld(t)
	var ready bool
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.player_bags') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("player_bags does not exist; apply migration 0108 first")
	}
	if def, ok := w.registry.Current().ItemDef(handlers.StartingBagItem); !ok || def.Bag == nil {
		t.Skip("the active content has no bags; run `admin content load`")
	}
	cfg := config.Defaults()
	clock := gametime.Clock{Epoch: w.now.Add(-time.Hour), Scale: gameScale}
	scale := gametime.Scale(gameScale)
	return &bagWorld{
		goodsWorld: w, clock: clock,
		granter: handlers.NewBagGranter(w.uow, workIDs{t}, w.registry, cfg.CarryRules(), clock, w.clockFunc()),
		bag: handlers.NewInventoryHandler(w.uow, workIDs{t}, nil, w.registry, w.cities, scale, crimeRules().Nerve, 10, time.Hour, w.clockFunc()).
			WithCarry(cfg.CarryRules(), clock),
	}
}

func (w *goodsWorld) clockFunc() func() time.Time { return func() time.Time { return w.now } }

// giveBread puts n loaves in a player's pack from a recorded origin.
func (w *goodsWorld) giveBread(t *testing.T, playerID string, n int64) {
	t.Helper()
	if err := w.uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: "bread", Qty: n, To: playerID,
			ToHolding: application.HoldCarried, Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: w.now})
	}); err != nil {
		t.Fatal(err)
	}
}

// inventory reads the bag screen's view.
func (w *bagWorld) inventory(t *testing.T, p *application.Player) plife.InventoryView {
	t.Helper()
	resp, err := w.bag.Show(testCtx(t), w.meta(t, p, "inventory.show"), handlers.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var v plife.InventoryView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatalf("the inventory view does not decode: %v", err)
	}
	return v
}

func (w *bagWorld) sackUses(t *testing.T, playerID string) int {
	t.Helper()
	var uses int
	if err := w.pool.Raw().QueryRow(testCtx(t),
		`SELECT uses_left FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, playerID).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	return uses
}

func TestTheStartingSackIsGivenOnceAndWorn(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	p := w.shopper(t, "bazaar", 0)

	// Run twice: the second finds the player's fence written.
	for i, want := range []bool{true, false} {
		gave, err := w.granter.GrantOne(ctx, p.ID)
		if err != nil {
			t.Fatalf("GrantOne #%d: %v", i+1, err)
		}
		if gave != want {
			t.Fatalf("GrantOne #%d gave %v, want %v", i+1, gave, want)
		}
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, p.ID); n != 1 {
		t.Fatalf("sacks = %d, want exactly 1", n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM starting_bag_grants WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("grant rows = %d, want 1", n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM player_bags WHERE player_id = $1::uuid AND slot = 'back'`, p.ID); n != 1 {
		t.Fatalf("worn on the back = %d, want the sack", n)
	}
	// A recorded origin: the verifier counts the piece among those that came from a grant.
	if n := countRows(t, w.pool, `SELECT count(*) FROM item_movements m JOIN item_pieces p ON p.id = m.piece_id
	   WHERE p.owner_id = $1::uuid AND m.reason = 'grant' AND m.from_player IS NULL`, p.ID); n != 1 {
		t.Fatalf("grant movements = %d, want 1", n)
	}

	// Nobody loses room at the rollout: 8 in hands and pockets, 12 in the sack.
	v := w.inventory(t, p)
	if v.Carry.Capacity != 20 || v.Carry.Base != 8 {
		t.Fatalf("capacity = %d (base %d), want 20 (8)", v.Carry.Capacity, v.Carry.Base)
	}
	if len(v.Bags) != 2 || v.Bags[1].Slot != "back" || v.Bags[1].Bag == nil || v.Bags[1].Bag.Item.Code != "bag_sack" || v.Bags[0].Bag != nil {
		t.Fatalf("slots = %+v", v.Bags)
	}
	verifyLedger(t, w.pool)
}

func TestAPageOfPlayersGetsTheirSackAndOnlyOnce(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	a, b := w.shopper(t, "bazaar", 0), w.shopper(t, "bazaar", 0)
	if _, err := w.granter.GrantOne(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// A run over everybody (page of 1) gives b one and leaves a alone.
	for i := 0; i < 2; i++ {
		if _, err := w.granter.GrantAll(ctx, 1, 0, nil); err != nil {
			t.Fatalf("GrantAll #%d: %v", i+1, err)
		}
	}
	for _, p := range []*application.Player{a, b} {
		if n := countRows(t, w.pool, `SELECT count(*) FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, p.ID); n != 1 {
			t.Errorf("player %s has %d sacks, want 1", p.ID, n)
		}
	}
}

func TestABagWearsByTheGameDayOnlyWhenHalfFull(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	full, light := w.shopper(t, "bazaar", 0), w.shopper(t, "bazaar", 0)
	for _, p := range []*application.Player{full, light} {
		if _, err := w.granter.GrantOne(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	w.giveBread(t, full.ID, 12)  // 12 of 20: past half
	w.giveBread(t, light.ID, 3) // 3 of 20: under half
	start := w.sackUses(t, full.ID)
	if start != 40 {
		t.Fatalf("a new sack has %d wear points, want 40", start)
	}

	w.now = w.now.Add(3 * 24 * time.Minute) // three game days
	for _, p := range []*application.Player{full, light} {
		w.inventory(t, p)
		w.inventory(t, p) // reading again charges nothing more
	}
	if got := w.sackUses(t, full.ID); got != 37 {
		t.Errorf("the full pack's sack has %d points left, want 37 (one a day for three days)", got)
	}
	if got := w.sackUses(t, light.ID); got != 40 {
		t.Errorf("the light pack's sack has %d points left, want 40 (under half full, no wear)", got)
	}

	// Torn: the sack gives half its room, 6 of 12.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE item_pieces SET uses_left = 0 WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, full.ID); err != nil {
		t.Fatal(err)
	}
	v := w.inventory(t, full)
	if v.Carry.Capacity != 14 || v.Bags[1].Bag == nil || !v.Bags[1].Bag.Torn || v.Bags[1].Bag.Space != 6 {
		t.Errorf("a torn sack: capacity %d, slot %+v", v.Carry.Capacity, v.Bags[1].Bag)
	}
}

func TestTakingABagOffAndPuttingItOnAgain(t *testing.T) {
	w := newBagWorld(t)
	ctx := testCtx(t)
	p := w.shopper(t, "bazaar", 0)
	if _, err := w.granter.GrantOne(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var serial string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'bag_sack'`, p.ID).Scan(&serial); err != nil {
		t.Fatal(err)
	}

	if _, err := w.bag.TakeOff(ctx, w.meta(t, p, "inventory.bag.off"), handlers.BagRequest{Slot: "back"}); err != nil {
		t.Fatal(err)
	}
	if v := w.inventory(t, p); v.Carry.Capacity != 8 || v.Bags[1].Bag != nil {
		t.Fatalf("hands only: capacity %d, back %+v", v.Carry.Capacity, v.Bags[1].Bag)
	}
	// The sack in the pack takes one room of its own.
	if v := w.inventory(t, p); v.Carry.Used != 1 {
		t.Errorf("a sack in the pack takes %d room, want 1", v.Carry.Used)
	}
	for i := 0; i < 2; i++ { // twice: nothing more to do the second time
		if _, err := w.bag.Wear(ctx, w.meta(t, p, "inventory.bag.wear"), handlers.BagRequest{Item: serial}); err != nil {
			t.Fatalf("Wear #%d: %v", i+1, err)
		}
	}
	if v := w.inventory(t, p); v.Carry.Capacity != 20 || v.Carry.Used != 0 {
		t.Fatalf("worn again: capacity %d used %d", v.Carry.Capacity, v.Carry.Used)
	}

	// A bag that leaves its wearer (here: set aside in escrow) is not worn any more.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE item_pieces SET holding = 'escrow' WHERE serial = $1`, serial); err != nil {
		t.Fatal(err)
	}
	if v := w.inventory(t, p); v.Carry.Capacity != 8 {
		t.Errorf("a bag in escrow still gives room: capacity %d", v.Carry.Capacity)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM player_bags WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Errorf("worn rows = %d, want 0 once the piece left", n)
	}
}
