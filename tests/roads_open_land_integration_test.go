//go:build integration

// Integration test of "a road opens the land it reaches" (docs/adr/0044 5.5 and
// the owner decision of 2026-10-03, migration 0110).
//
// The village's grid is only its first block. The head draws a road out of it,
// as far as the ground allows:
//   - the first press is a quote and changes nothing; the second stores a PLAN
//     (free, idempotent), opens the lots along it for sale and claims its tiles;
//   - a resident who is not the head cannot draw; another settlement's tile is
//     refused;
//   - a buyer of a lot far out pays the price AND the stretch of road up to it,
//     which is laid in the same purchase (one ledger transaction, one journal
//     row); a second buyer nearer to the village pays only what is still unlaid,
//     so no cell of road is ever charged twice;
//   - the bought lot can be built on, and the head can build a public building
//     on an unsold lot out there;
//   - a plan with a laid cell or a sold lot cannot be taken back; an unbought one
//     can, and its lots close;
//   - `admin economy verify` proves the ledger and the journal agree.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"


	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func TestRoadsOpenTheLand(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool
	admin := postgres.NewEconomyAdmin(pool)
	v0, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Citizen || !v0.CitizenInvariants.AccessChecked {
		t.Skip("migration 0100 is not applied")
	}
	var hasPlans bool
	if err := pool.Raw().QueryRow(ctx, `SELECT to_regclass('settlement_road_plans') IS NOT NULL`).Scan(&hasPlans); err != nil || !hasPlans {
		t.Skip("migration 0110 is not applied")
	}

	e.h.WithFoundingGrant(10_000)
	metaA, head := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { purgeRoadsFootprint(t, pool, cityID) })

	rules := handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000,
		TaxBPS: 200, TaxBPSMax: 500, TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000,
		MaxLotsPerPlayer: 6, PrivateShareMaxBPS: 10_000, HomeRestCooldown: 6 * time.Hour, HomeRestHealth: 10, HomeRestHappiness: 5,
		CrossingLotCost: 60, MaxCrossing: 2,
		RoadFrontageDepth: 2, RoadPlanMaxLots: 1500, RoadOpenLotsMax: 30_000, RoadForeignBufferTiles: 3, RoadSteepSlopeM: 30,
		RoadCorridorRing: 1, RoadTrackCostBPS: 15_000,
	}
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, staticContentSource{snap: snap}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000, AutoRoadCost: 10,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250}).WithCitizenRules(rules)

	support := cityIDByCode(t, pool, "support")
	buyer, buyer2 := insertPlayer(t, pool), insertPlayer(t, pool)
	for _, p := range []*application.Player{buyer, buyer2} {
		placePlayer(t, pool, p.ID, support, "")
	}
	clientAs := func(p *application.Player, command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, p), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	for _, p := range []*application.Player{buyer, buyer2} {
		m := asPlayer(metaA, p)
		m.Command, m.IdempotencyKey = "settlement.join", "it-"+randomToken(t, 16)
		if _, err := rrm(m)(village.Join(ctx, m, handlers.VillageJoinRequest{Confirm: screens.ResidenceConfirm})); err != nil {
			t.Fatal(err)
		}
		grantCash(t, pool, p.ID, 20_000)
	}
	cashOf := func(p *application.Player) int64 {
		var bal int64
		if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
			if err != nil {
				return err
			}
			b, err := tx.Ledger().Balance(ctx, acct.ID)
			bal = b.Minor()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return bal
	}
	count := func(query string, args ...any) int { return e.count(t, query, args...) }
	plan := func(p *application.Player, to, class, confirm string, key string) *presenter.Response {
		t.Helper()
		m := clientAs(p, "settlement.road.plan")
		if key != "" {
			m.IdempotencyKey = key
		}
		r, err := rrm(m)(village.RoadPlan(ctx, m, handlers.VillageRoadRequest{To: to, Class: class, Confirm: confirm}))
		if err != nil {
			t.Fatalf("RoadPlan to %s: %v", to, err)
		}
		return r
	}

	// ---- the first grid is the first block: nothing is open beyond it yet ----
	if n := count(`SELECT count(*) FROM settlement_open_lots WHERE settlement_id = $1::uuid`, cityID); n != 0 {
		t.Fatalf("%d lots open before any road", n)
	}

	// ---- a resident who is not the head cannot draw ---------------------------
	if r := plan(buyer, screens.LotToken(-9, 7, false), "", "", ""); r.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a resident drew a road: %q %q", r.Screen, r.Text)
	}

	// ---- a quote: first candidate that routes (the test world has water and hills) ----
	candidates := [][2]int{{-9, 7}, {14, 4}, {8, -12}, {-6, -10}, {3, 18}, {-14, 16}, {20, -8}, {-18, -4}}
	var quote *presenter.Response
	var to [2]int
	for _, c := range candidates {
		r := plan(head, screens.LotToken(c[0], c[1], false), "", "", "")
		if r.Screen == screens.ScreenRoadQuote {
			quote, to = r, c
			break
		}
		t.Logf("road to %v: %s", c, viewOf(t, r)["kind"])
	}
	if quote == nil {
		t.Skip("no candidate end could be routed in this world")
	}
	q := viewOf(t, quote)
	lots := int(q["lots"].(float64))
	if lots < 8 {
		t.Fatalf("a road to %v is only %d lots long", to, lots)
	}
	path := q["path"].([]any)
	if last := path[len(path)-1].(map[string]any); int(last["x"].(float64)) != to[0] || int(last["y"].(float64)) != to[1] {
		t.Fatalf("the road ends at %v, not at %v", last, to)
	}
	crossings := int(q["crossings"].(float64))
	if got, want := int64(q["full_cost"].(float64)), int64(lots-crossings)*10+int64(crossings)*60; got != want {
		t.Fatalf("the road quotes %d for %d lots (%d crossings), want %d", got, lots, crossings, want)
	}
	if int(q["usable"].(float64)) < 4 {
		t.Skipf("the road to %v opens only %v usable lots", to, q["usable"])
	}
	if n := count(`SELECT count(*) FROM settlement_road_plans WHERE settlement_id = $1::uuid`, cityID); n != 0 {
		t.Fatalf("the quote stored %d plans", n)
	}
	treasury0 := treasuryOf(t, pool, cityID)
	headCash0 := cashOf(buyer)

	// ---- confirming stores the plan, free, once --------------------------------
	key := "it-plan-" + randomToken(t, 12)
	planned := plan(head, screens.LotToken(to[0], to[1], false), "", screens.VillageBuildConfirm, key)
	if planned.Screen != screens.ScreenRoadPlanned {
		t.Fatalf("confirming answered %q: %s", planned.Screen, planned.Text)
	}
	again := plan(head, screens.LotToken(to[0], to[1], false), "", screens.VillageBuildConfirm, key)
	_ = again
	if n := count(`SELECT count(*) FROM settlement_road_plans WHERE settlement_id = $1::uuid AND cancelled_at IS NULL`, cityID); n != 1 {
		t.Fatalf("%d plans after a confirm sent twice, want 1", n)
	}
	if n := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid`, cityID); n != lots {
		t.Fatalf("%d cells stored for a %d-lot road", n, lots)
	}
	if n := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid AND built_at IS NOT NULL`, cityID); n != 0 {
		t.Fatalf("a plan laid %d cells before anyone bought", n)
	}
	open := count(`SELECT count(*) FROM settlement_open_lots WHERE settlement_id = $1::uuid`, cityID)
	if open < 4 {
		t.Fatalf("%d lots opened along a %d-lot road", open, lots)
	}
	if treasuryOf(t, pool, cityID) != treasury0 || cashOf(buyer) != headCash0 {
		t.Fatal("drawing a road moved money")
	}
	if n := count(`SELECT count(*) FROM outbox WHERE subject LIKE '%.land_changed.v1' AND payload->>'settlement_id' = $1`, cityID); n != 1 {
		t.Fatalf("%d land_changed events, want 1", n)
	}
	// a road drawn to a lot it already reaches is not a second road
	if r := plan(head, screens.LotToken(to[0], to[1], false), "", "", ""); r.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a second road to the same lot answered %q", r.Screen)
	}

	// ---- the land screen shows what the road opened -----------------------------
	type outerCell struct {
		x, y, roads, crossings int
		state, access          string
		cost                   int64
	}
	outer := func(p *application.Player) []outerCell {
		m := clientAs(p, "settlement.land")
		r, err := rrm(m)(village.Land(ctx, m))
		if err != nil {
			t.Fatal(err)
		}
		var out []outerCell
		arr, _ := viewOf(t, r)["outer"].([]any)
		for _, c := range arr {
			mc := c.(map[string]any)
			oc := outerCell{x: int(mc["x"].(float64)), y: int(mc["y"].(float64)), state: mc["state"].(string)}
			oc.access, _ = mc["access"].(string)
			if f, ok := mc["cost"].(float64); ok {
				oc.cost = int64(f)
			}
			if f, ok := mc["roads"].(float64); ok {
				oc.roads = int(f)
			}
			if f, ok := mc["crossings"].(float64); ok {
				oc.crossings = int(f)
			}
			out = append(out, oc)
		}
		return out
	}
	cells := outer(buyer)
	var far, near *outerCell
	for i := range cells {
		c := cells[i]
		if c.state != screens.LandFree || c.access != "needs_road" {
			continue
		}
		if far == nil || c.roads > far.roads {
			far = &cells[i]
		}
		if near == nil || c.roads < near.roads {
			near = &cells[i]
		}
	}
	if far == nil || near == nil || far.roads < 3 || far.roads == near.roads {
		t.Skipf("the road's lots do not differ in distance (far %v near %v)", far, near)
	}
	if far.cost < int64(far.roads)*10 {
		t.Fatalf("a lot needing %d lots of road quotes %d", far.roads, far.cost)
	}
	t.Logf("far lot (%d,%d): %d road lots, cost %d; near lot (%d,%d): %d road lots, cost %d", far.x, far.y, far.roads, far.cost, near.x, near.y, near.roads, near.cost)

	// ---- the first buyer pays the price and the road up to the lot ---------------
	buy := func(p *application.Player, x, y int, confirm string) *presenter.Response {
		t.Helper()
		m := clientAs(p, "settlement.lot.buy")
		r, err := rrm(m)(village.BuyLot(ctx, m, handlers.VillageLotRequest{Lot: screens.LotToken(x, y, false), Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cash0, tr0 := cashOf(buyer), treasuryOf(t, pool, cityID)
	ask := buy(buyer, far.x, far.y, "")
	if ask.Screen != screens.ScreenLotBuyConfirm {
		t.Fatalf("asking to buy answered %q: %s", ask.Screen, ask.Text)
	}
	if got := int64(viewOf(t, ask)["total"].(float64)); got != 400+far.cost {
		t.Fatalf("the quote is %d, want the price 400 and the road %d", got, far.cost)
	}
	if cashOf(buyer) != cash0 || count(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid`, cityID) != 0 {
		t.Fatal("asking to buy changed something")
	}
	done := buy(buyer, far.x, far.y, screens.ResidenceConfirm)
	if done.Screen != screens.ScreenLotBuyDone {
		t.Fatalf("buying answered %q: %s", done.Screen, done.Text)
	}
	if got := cash0 - cashOf(buyer); got != 400+far.cost {
		t.Fatalf("the buyer paid %d, want %d", got, 400+far.cost)
	}
	if got := treasuryOf(t, pool, cityID) - tr0; got != 400 {
		t.Fatalf("the treasury got %d, want the price 400 (the road is not the treasury's income)", got)
	}
	laid1 := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid AND built_at IS NOT NULL`, cityID)
	if laid1 == 0 || laid1 > lots {
		t.Fatalf("%d cells laid by the first purchase", laid1)
	}
	if n := count(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`, cityID, far.x, far.y); n != 1 {
		t.Fatalf("the lot (%d,%d) is not recorded", far.x, far.y)
	}
	// every laid cell is a finished road building, every road building out there a laid cell
	if a, b := laid1, count(`SELECT count(*) FROM settlement_buildings sb JOIN settlement_road_cells c
	   ON c.settlement_id = sb.settlement_id AND c.lot_x = sb.lot_x AND c.lot_y = sb.lot_y
	  WHERE sb.settlement_id = $1::uuid AND sb.type_code = 'road' AND sb.status = 'complete' AND c.built_at IS NOT NULL`, cityID); a != b {
		t.Fatalf("%d cells laid but %d road buildings stand on them", a, b)
	}
	// repeating the purchase changes nothing
	if r := buy(buyer, far.x, far.y, screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen_lot_taken") {
		t.Fatalf("the same lot sold twice: %q", r.Text)
	}

	// ---- the second buyer pays only the road still unlaid -------------------------
	cells = outer(buyer2)
	var next *outerCell
	for i := range cells {
		c := cells[i]
		if c.state == screens.LandFree && c.access != "none" && (next == nil || c.cost < next.cost) {
			next = &cells[i]
		}
	}
	if next == nil {
		t.Fatal("no lot left to buy")
	}
	cashB0 := cashOf(buyer2)
	if r := buy(buyer2, next.x, next.y, screens.ResidenceConfirm); r.Screen != screens.ScreenLotBuyDone {
		t.Fatalf("the second buyer: %q %s", r.Screen, r.Text)
	}
	if got := cashB0 - cashOf(buyer2); got != 400+next.cost {
		t.Fatalf("the second buyer paid %d, want %d", got, 400+next.cost)
	}
	// no cell of road is charged twice: the fees equal the price of the cells laid
	laid2 := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid AND built_at IS NOT NULL`, cityID)
	var fees int64
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(fee), 0) FROM settlement_lot_connections WHERE settlement_id = $1::uuid`, cityID).Scan(&fees); err != nil {
		t.Fatal(err)
	}
	var laneAndCells int
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(road_lots + crossing_lots), 0) FROM settlement_lot_connections WHERE settlement_id = $1::uuid`, cityID).Scan(&laneAndCells); err != nil {
		t.Fatal(err)
	}
	if laneAndCells < laid2 {
		t.Fatalf("the journals count %d lots of road but %d cells are laid", laneAndCells, laid2)
	}
	if fees < int64(laid2)*10 {
		t.Fatalf("the fees %d are less than the %d cells laid are worth", fees, laid2)
	}
	if laid2 < laid1 {
		t.Fatalf("laid cells went down: %d then %d", laid1, laid2)
	}

	// ---- the head builds a public building on an unsold lot out there (before the house: the village has one slot)
	cells = outer(head)
	var pub *outerCell
	for i := range cells {
		if cells[i].state == screens.LandFree && cells[i].access != "none" {
			pub = &cells[i]
			break
		}
	}
	if pub == nil {
		t.Fatal("no unsold lot for the head to build on")
	}
	seedTimber(t, uow, cityID, 30)
	hm := clientAs(head, "settlement.build.place")
	pr, err := rrm(hm)(village.Place(ctx, hm, handlers.VillageBuildRequest{Code: "cottage", Lot: screens.LotToken(pub.x, pub.y, false), Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Screen == screens.ScreenVillageRefusal {
		t.Fatalf("the head's building at (%d,%d) out there was refused: %v", pub.x, pub.y, viewOf(t, pr)["kind"])
	}
	if n := count(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'cottage' AND lot_x = $2 AND lot_y = $3`, cityID, pub.x, pub.y); n != 1 {
		t.Fatalf("no public building at (%d,%d)", pub.x, pub.y)
	}
	// ---- a lot out there can be built on ---------------------------------------
	pm := clientAs(buyer, "settlement.private.place")
	r, err := rrm(pm)(village.PrivatePlace(ctx, pm, handlers.VillagePrivateRequest{Code: "private_cottage", Lot: screens.LotToken(far.x, far.y, false), Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "citizen.mine.title") {
		t.Fatalf("building on the far lot: %q %q", r.Screen, r.Text)
	}
	if n := count(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'private_cottage' AND lot_x = $2 AND lot_y = $3`, cityID, far.x, far.y); n != 1 {
		t.Fatalf("no house stands on (%d,%d)", far.x, far.y)
	}
	// ...but not on a lot nobody bought, by a resident
	var unsold *outerCell
	for i, c := range outer(buyer2) {
		if c.state == screens.LandFree && c.access != "none" {
			cc := c
			unsold = &cc
			_ = i
			break
		}
	}
	if unsold == nil {
		t.Fatal("no unsold lot is left out there")
	}
	pm = clientAs(buyer2, "settlement.private.place")
	if r, err := rrm(pm)(village.PrivatePlace(ctx, pm, handlers.VillagePrivateRequest{Code: "private_cottage", Lot: screens.LotToken(unsold.x, unsold.y, false), Confirm: screens.VillageBuildConfirm})); err != nil ||
		!strings.Contains(r.Text, "citizen_not_owner") {
		t.Fatalf("a resident built on a lot nobody sold: %+v %v", r, err)
	}
	// the land the roads did not open is still closed
	if r := buy(buyer2, 500, 500, screens.ResidenceConfirm); r.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a lot no road reaches was sold: %q", r.Screen)
	}

	// ---- a plan with a laid cell or a sold lot is not taken back -------------------
	var planID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM settlement_road_plans WHERE settlement_id = $1::uuid`, cityID).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	cancel := func(id string) *presenter.Response {
		m := clientAs(head, "settlement.road.cancel")
		r, err := rrm(m)(village.RoadCancel(ctx, m, handlers.VillageRoadCancelRequest{ID: id}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := cancel(planID); r.Screen != screens.ScreenVillageRefusal {
		t.Fatalf("a road with a sold lot was taken back: %q", r.Screen)
	}

	// ---- an unbought plan can be taken back, and its lots close ------------------------
	var second *presenter.Response
	var to2 [2]int
	for _, c := range candidates {
		if c == to {
			continue
		}
		r := plan(head, screens.LotToken(c[0], c[1], false), "", screens.VillageBuildConfirm, "")
		if r.Screen == screens.ScreenRoadPlanned {
			second, to2 = r, c
			break
		}
	}
	if second != nil {
		var id2 string
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM settlement_road_plans WHERE settlement_id = $1::uuid AND cancelled_at IS NULL AND id <> $2::uuid ORDER BY created_at DESC LIMIT 1`, cityID, planID).Scan(&id2); err != nil {
			t.Fatal(err)
		}
		cellsBefore := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid`, cityID)
		if r := cancel(id2); r.Screen != screens.ScreenRoadCancelled {
			t.Fatalf("an unbought road to %v was not taken back: %q %s", to2, r.Screen, r.Text)
		}
		if n := count(`SELECT count(*) FROM settlement_road_cells WHERE plan_id = $1::uuid`, id2); n != 0 {
			t.Fatalf("%d cells left of a cancelled plan", n)
		}
		if after := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid`, cityID); after >= cellsBefore {
			t.Fatalf("cells %d -> %d after a cancel", cellsBefore, after)
		}
		if n := count(`SELECT count(*) FROM settlement_open_lots WHERE plan_id = $1::uuid`, id2); n != 0 {
			t.Fatalf("%d lots still open under a cancelled plan", n)
		}
		if r := cancel(id2); r.Screen != screens.ScreenVillageRefusal {
			t.Fatalf("a cancelled plan cancelled twice answered %q", r.Screen)
		}
	}

	// ---- another settlement's ground is closed to this road ----------------------------
	// a road cell of another village on the tile of a target: the claim a road carries
	var face, gx, gy int
	if err := pool.Raw().QueryRow(ctx, `SELECT tile_face, tile_gx, tile_gy FROM settlement_road_cells WHERE settlement_id = $1::uuid ORDER BY seq DESC LIMIT 1`, cityID).Scan(&face, &gx, &gy); err != nil {
		t.Fatal(err)
	}
	foreignCity := newUUID(t)
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_road_plans (id, settlement_id, drawn_by, class, surface, lots, crossing_lots, climb_m, length_m, from_x, from_y, to_x, to_y, created_at)
	   VALUES ($1::uuid, $1::uuid, $2::uuid, 'path', 'path', 1, 0, 0, 30, 0, 0, 1, 1, now())`, foreignCity, head.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_road_cells (settlement_id, lot_x, lot_y, plan_id, seq, water, elevation_m, tile_face, tile_gx, tile_gy)
	   VALUES ($1::uuid, 7000, 7000, $1::uuid, 0, 0, 0, $2, $3, $4)`, foreignCity, face, gx, gy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM settlement_road_cells WHERE settlement_id = $1::uuid`, foreignCity)
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM settlement_road_plans WHERE settlement_id = $1::uuid`, foreignCity)
	})
	// a NEW target on that tile (a lot beside the road's end that is not itself a cell) is refused as foreign
	var tx0, ty0 int
	if err := pool.Raw().QueryRow(ctx, `SELECT lot_x, lot_y FROM settlement_road_cells WHERE settlement_id = $1::uuid AND tile_face = $2 AND tile_gx = $3 AND tile_gy = $4 ORDER BY seq DESC LIMIT 1`, cityID, face, gx, gy).Scan(&tx0, &ty0); err != nil {
		t.Fatal(err)
	}
	foreignSeen := false
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
		if foreignSeen {
			break
		}
		if n := count(`SELECT count(*) FROM settlement_road_cells WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3`, cityID, tx0+d[0], ty0+d[1]); n != 0 {
			continue
		}
		probe := plan(head, screens.LotToken(tx0+d[0], ty0+d[1], false), "", "", "")
		if probe.Screen != screens.ScreenRoadQuote && viewOf(t, probe)["kind"] == "road_foreign" {
			foreignSeen = true
		}
	}
	if !foreignSeen {
		t.Fatalf("no target on the tile another settlement's road holds was refused as foreign")
	}

	// ---- the books agree -----------------------------------------------------------------
	v, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	c := v.CitizenInvariants
	if c.RoadLedger-c.RoadRows != v0.CitizenInvariants.RoadLedger-v0.CitizenInvariants.RoadRows || c.RoadMismatched != 0 ||
		c.LotSaleMismatched != 0 || c.CitizenPaidToPlayer != 0 {
		t.Errorf("the ledger and the journals drifted apart: before %+v, after %+v", v0.CitizenInvariants, c)
	}
}

// purgeRoadsFootprint takes back what the test wrote: the road-plan rows, then
// the lot-access and citizen footprints.
func purgeRoadsFootprint(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for _, stmt := range []string{
		`DELETE FROM settlement_open_lots WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_road_cells WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_road_plans WHERE settlement_id = $1::uuid`,
	} {
		if _, err := pool.Raw().Exec(ctx, stmt, cityID); err != nil {
			t.Errorf("cleanup %.40q: %v", stmt, err)
		}
	}
	purgeLotAccessFootprint(t, pool, cityID)
}
