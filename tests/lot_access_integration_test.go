//go:build integration

// Integration test of lot access (docs/adr/0043, migration 0100): a lot is
// sold only when a road can reach it. An enclosed lot is refused with its
// reason, a lot that needs a road is bought with the road included (paid by
// the buyer, laid at once, reserved as right-of-way so nothing can landlock
// or tear it down), and a lot that lost its road can be refunded by the owner
// from the treasury; `admin economy verify` proves every coin balances.
package tests

import (
	"context"
	"fmt"
	"os"
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

func TestLotAccess(t *testing.T) {
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

	e.h.WithFoundingGrant(10_000)
	metaA, head := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	// LOTACCESS_KEEP=1 stops after the lot is cut off and leaves the rows, so the operator report
	// (`admin settlement landlocked`) can be run against a real landlocked lot.
	keep := os.Getenv("LOTACCESS_KEEP") != ""
	if !keep {
		t.Cleanup(func() { purgeLotAccessFootprint(t, pool, cityID) })
	}

	rules := handlers.CitizenRules{
		LotPrice: 400, LotPriceMin: 100, LotPriceMax: 5000, PermitFee: 100, PermitFeeMax: 1000,
		TaxBPS: 200, TaxBPSMax: 500, TaxPeriod: 24 * time.Hour, MaterialMarkupBPS: 12_000,
		MaxLotsPerPlayer: 6, PrivateShareMaxBPS: 10_000, HomeRestCooldown: 6 * time.Hour, HomeRestHealth: 10, HomeRestHappiness: 5,
		CrossingLotCost: 60, MaxCrossing: 2,
	}
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, nil, staticContentSource{snap: snap}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000, AutoRoadCost: 10,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250}).WithCitizenRules(rules)

	support := cityIDByCode(t, pool, "support")
	resident, neighbour := insertPlayer(t, pool), insertPlayer(t, pool)
	for _, p := range []*application.Player{resident, neighbour} {
		placePlayer(t, pool, p.ID, support, "")
	}
	client := func(p *application.Player, command string) envelope.Metadata {
		m := clientMeta(asPlayer(metaA, p), command, command)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	for _, p := range []*application.Player{resident, neighbour} {
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

	type cell struct {
		x, y, roads, crossings int
		state, access          string
		cost                   int64
	}
	land := func(p *application.Player) [][]cell {
		m := client(p, "settlement.land")
		r, err := rrm(m)(village.Land(ctx, m))
		if err != nil {
			t.Fatal(err)
		}
		var out [][]cell
		rows, ok := viewOf(t, r)["rows"].([]any)
		if !ok {
			t.Fatalf("the land answers %q: %s", r.Screen, r.Text)
		}
		for _, row := range rows {
			var line []cell
			for _, c := range row.([]any) {
				mc := c.(map[string]any)
				cl := cell{x: int(mc["x"].(float64)), y: int(mc["y"].(float64)), state: mc["state"].(string)}
				if a, ok := mc["access"].(string); ok {
					cl.access = a
				}
				if f, ok := mc["cost"].(float64); ok {
					cl.cost = int64(f)
				}
				if f, ok := mc["roads"].(float64); ok {
					cl.roads = int(f)
				}
				if f, ok := mc["crossings"].(float64); ok {
					cl.crossings = int(f)
				}
				line = append(line, cl)
			}
			out = append(out, line)
		}
		return out
	}
	dump := func(g [][]cell) string {
		var b strings.Builder
		for _, row := range g {
			for _, c := range row {
				b.WriteString(fmt.Sprintf("%-9s", c.state[:min(len(c.state), 4)]+":"+c.access[:min(len(c.access), 4)]))
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	buy := func(p *application.Player, lot, confirm string) *presenter.Response {
		m := client(p, "settlement.lot.buy")
		r, err := rrm(m)(village.BuyLot(ctx, m, handlers.VillageLotRequest{Lot: lot, Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	token := func(c cell) string { return screens.LotToken(c.x, c.y, false) }
	t.Logf("the village's land:\n%s", dump(land(resident)))

	// ---- the land screen says how each free lot is served ------------------
	var farLot *cell
	for _, row := range land(resident) {
		for i := range row {
			c := row[i]
			if c.state == screens.LandFree && c.access == "needs_road" && (farLot == nil || c.roads > farLot.roads) {
				farLot = &c
			}
		}
	}
	if farLot == nil {
		t.Skip("every free lot of the test village already touches the road")
	}
	if farLot.cost != int64(farLot.roads)*10 {
		t.Fatalf("a lot needing %d lots of road quotes %d, want %d", farLot.roads, farLot.cost, farLot.roads*10)
	}

	// ---- 1. an enclosed lot is refused, with its reason ---------------------
	// the lot's four sides are other residents' land (rows written straight, the
	// way a legacy village could have them): no road can reach it.
	var enclosedCell *cell
	for _, row := range land(resident) {
		for i := range row {
			c := row[i]
			if c.state == screens.LandFree && c.access == "needs_road" && (c.x != farLot.x || c.y != farLot.y) {
				enclosedCell = &c
			}
		}
	}
	if enclosedCell == nil {
		t.Skip("the test village has no second lot needing a road")
	}
	var fakeRows []string
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := enclosedCell.x+d[0], enclosedCell.y+d[1]
		if ny < 0 || ny >= 5 || nx < 0 || nx >= 5 {
			continue
		}
		g := land(resident)
		if g[ny][nx].state != screens.LandFree {
			continue
		}
		id := (workIDs{t}).NewID()
		if _, err := pool.Raw().Exec(ctx, `
			INSERT INTO settlement_lots (id, settlement_id, lot_x, lot_y, tenure, owner_id, price, ledger_transaction_id, acquired_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, 'freehold', $5::uuid, 400, $6::uuid, now())`,
			id, cityID, nx, ny, neighbour.ID, (workIDs{t}).NewID()); err != nil {
			t.Fatal(err)
		}
		fakeRows = append(fakeRows, id)
	}
	enclosedTok := token(*enclosedCell)
	ask := buy(resident, enclosedTok, "")
	if !strings.Contains(ask.Text, "citizen.access.none") {
		t.Fatalf("the quote for an enclosed lot does not say no road can reach it: %s", ask.Text)
	}
	if ask.Screen != screens.ScreenLotBuyConfirm {
		t.Fatalf("an enclosed lot answers %q, want the buy popup with the reason", ask.Screen)
	}
	for _, a := range ask.Actions {
		if a.Role == "confirm" {
			t.Fatalf("an enclosed lot offers a confirm action: %+v", a)
		}
	}
	cash0, treasury0 := cashOf(resident), treasuryOf(t, pool, cityID)
	if r := buy(resident, enclosedTok, screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen.refusal.citizen_no_access") {
		t.Fatalf("an enclosed lot was sold: %s", r.Text)
	}
	if cashOf(resident) != cash0 || treasuryOf(t, pool, cityID) != treasury0 ||
		count(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid AND owner_id = $2::uuid`, cityID, resident.ID) != 0 {
		t.Fatal("a refused sale moved money or recorded a lot")
	}
	for _, id := range fakeRows {
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM settlement_lots WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
	}

	// ---- 2. buying with the road included ----------------------------------
	roads0 := count(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'road' AND status = 'complete'`, cityID)
	farTok := token(*farLot)
	ask = buy(resident, farTok, "")
	if !strings.Contains(ask.Text, "citizen.access.needs_road") && !strings.Contains(ask.Text, "citizen.access.needs_bridge") {
		t.Fatalf("the quote does not state the road: %s", ask.Text)
	}
	if got := viewOf(t, ask)["total"].(float64); int64(got) != 400+farLot.cost {
		t.Fatalf("the quote totals %v, want the price and the road, %d", got, 400+farLot.cost)
	}
	if cashOf(resident) != cash0 {
		t.Fatal("asking moved money")
	}
	done := buy(resident, farTok, screens.ResidenceConfirm)
	if !strings.Contains(done.Text, "citizen.buy.done_title") {
		t.Fatalf("buying with the road: %s", done.Text)
	}
	if got, want := cashOf(resident), cash0-400-farLot.cost; got != want {
		t.Fatalf("the buyer has %d, want %d: the road is on the buyer", got, want)
	}
	if got := treasuryOf(t, pool, cityID); got != treasury0+400 {
		t.Fatalf("the treasury has %d, want %d: only the price lands there", got, treasury0+400)
	}
	if got := count(`SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'road' AND status = 'complete'`, cityID) - roads0; got != farLot.roads {
		t.Fatalf("%d road lots laid, want %d", got, farLot.roads)
	}
	if got := count(`SELECT count(*) FROM settlement_road_reserve WHERE settlement_id = $1::uuid AND kind = 'corridor' AND serves_x = $2 AND serves_y = $3`,
		cityID, farLot.x, farLot.y); got != farLot.roads {
		t.Fatalf("%d corridor lots reserved, want %d", got, farLot.roads)
	}
	if count(`SELECT count(*) FROM settlement_lot_connections WHERE settlement_id = $1::uuid AND fee = $2 AND origin = 'buy'`, cityID, farLot.cost) != 1 {
		t.Fatal("the connection is not in the journal")
	}
	// The lot touches the road now: nothing more to lay, and the road shows as one.
	for _, row := range land(resident) {
		for _, c := range row {
			if c.x == farLot.x && c.y == farLot.y && c.access != "road" {
				t.Fatalf("the bought lot is still %q", c.access)
			}
		}
	}
	// A corridor lot is road right-of-way: not for sale, and its road is not torn down.
	var corridor [2]int
	if err := pool.Raw().QueryRow(ctx, `SELECT lot_x, lot_y FROM settlement_road_reserve WHERE settlement_id = $1::uuid AND kind = 'corridor' ORDER BY lot_y, lot_x LIMIT 1`, cityID).Scan(&corridor[0], &corridor[1]); err != nil {
		t.Fatal(err)
	}
	if r := buy(neighbour, screens.LotToken(corridor[0], corridor[1], false), screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen.refusal.citizen_lot_reserved") &&
		!strings.Contains(r.Text, "village.refusal.occupied") {
		t.Fatalf("a corridor lot was sold: %s", r.Text)
	}
	var roadID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'road' AND lot_x = $2 AND lot_y = $3`,
		cityID, corridor[0], corridor[1]).Scan(&roadID); err != nil {
		t.Fatal(err)
	}
	headM := asPlayer(metaA, head)
	headM.Command, headM.IdempotencyKey = "settlement.build.demolish", "it-"+randomToken(t, 16)
	if r, err := rrm(headM)(village.Demolish(ctx, headM, handlers.VillageBuildingRequest{ID: roadID})); err != nil || !strings.Contains(r.Text, "village.refusal.road_reserved") {
		t.Fatalf("the head tore down someone's way in: %+v %v", r, err)
	}
	// The owner can build: there is a road, so no "no road" dead end.
	pm := client(resident, "settlement.private.place")
	bill, err := rrm(pm)(village.PrivatePlace(ctx, pm, handlers.VillagePrivateRequest{Code: "private_cottage", Lot: farTok}))
	if err != nil || !strings.Contains(bill.Text, "citizen.confirm.title") {
		t.Fatalf("building on a lot with its road: %+v %v", bill, err)
	}

	// ---- 3. a lot that lost its road: the owner may take the money back -----
	// (a lot sold before the rule, or cut off since: the road and its reserve are
	// taken away straight in the database, the way the legacy village stands)
	var lost *cell
	for _, row := range land(resident) {
		for i := range row {
			c := row[i]
			if c.state == screens.LandFree && c.access == "road" {
				lost = &c
			}
		}
	}
	if lost == nil {
		t.Skip("no second lot on the road to sell and cut off")
	}
	lostTok := token(*lost)
	if r := buy(resident, lostTok, screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen.buy.done_title") {
		t.Fatalf("buying the second lot: %s", r.Text)
	}
	// cut it off: every road lot beside it goes, with the reserve rows
	if _, err := pool.Raw().Exec(ctx, `
		UPDATE settlement_buildings SET status = 'demolished', demolished_at = now()
		 WHERE settlement_id = $1::uuid AND type_code = 'road'
		   AND abs(lot_x - $2) + abs(lot_y - $3) = 1`, cityID, lost.x, lost.y); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `DELETE FROM settlement_road_reserve WHERE settlement_id = $1::uuid AND abs(lot_x - $2) + abs(lot_y - $3) = 1`, cityID, lost.x, lost.y); err != nil {
		t.Fatal(err)
	}
	if keep {
		return
	}
	am := client(resident, "settlement.lot.access")
	access, err := rrm(am)(village.LotAccess(ctx, am, handlers.VillageAccessRequest{Lot: lostTok}))
	if err != nil || access.Screen != screens.ScreenLotAccess {
		t.Fatalf("the lot's access: %+v %v", access, err)
	}
	roles := map[string]bool{}
	for _, a := range access.Actions {
		roles[a.ID] = true
	}
	if !roles["lot.refund"] || (!roles["lot.connect"] && !roles["lot.carve"]) {
		t.Fatalf("a cut-off lot must offer the road and the refund, offers %v", roles)
	}
	rm := func(option, confirm string) *presenter.Response {
		m := client(resident, "settlement.lot.repair")
		r, err := rrm(m)(village.RepairLot(ctx, m, handlers.VillageRepairRequest{Lot: lostTok, Option: option, Confirm: confirm}))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cash1, treasury1 := cashOf(resident), treasuryOf(t, pool, cityID)
	if r := rm("refund", ""); r.Screen != screens.ScreenLotAccess {
		t.Fatalf("asking about a refund changes things: %s", r.Screen)
	}
	if cashOf(resident) != cash1 {
		t.Fatal("asking moved money")
	}
	if r := rm("refund", screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen.access.done.refund") {
		t.Fatalf("the refund: %s", r.Text)
	}
	if got := cashOf(resident); got != cash1+400 {
		t.Fatalf("the owner has %d, want the price back: %d", got, cash1+400)
	}
	if got := treasuryOf(t, pool, cityID); got != treasury1-400 {
		t.Fatalf("the treasury has %d, want %d", got, treasury1-400)
	}
	if count(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND released_at IS NULL`, cityID, lost.x, lost.y) != 0 {
		t.Fatal("the refunded lot is still held")
	}
	if count(`SELECT count(*) FROM settlement_lots WHERE settlement_id = $1::uuid AND lot_x = $2 AND lot_y = $3 AND release_kind = 'refund' AND refund_amount = 400`, cityID, lost.x, lost.y) != 1 {
		t.Fatal("the refund is not on the lot's row")
	}
	// The same confirm again (a stale button): the lot is not the viewer's, nothing is paid twice.
	if r := rm("refund", screens.ResidenceConfirm); !strings.Contains(r.Text, "citizen.refusal.citizen_not_owner") || cashOf(resident) != cash1+400 {
		t.Fatalf("a refund repeated: %s", r.Text)
	}

	// ---- the ledger and the rows agree --------------------------------------
	v, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	c := v.CitizenInvariants
	if c.RoadLedger-c.RoadRows != v0.CitizenInvariants.RoadLedger-v0.CitizenInvariants.RoadRows || c.RoadMismatched != 0 ||
		c.RefundLedger-c.RefundRows != v0.CitizenInvariants.RefundLedger-v0.CitizenInvariants.RefundRows || c.RefundMismatched != 0 ||
		c.LotSaleMismatched != 0 || c.CitizenPaidToPlayer != 0 {
		t.Errorf("lot access's ledger and rows drifted apart: before %+v, after %+v", v0.CitizenInvariants, c)
	}
	if c.RoadLedger == v0.CitizenInvariants.RoadLedger || c.RefundLedger == v0.CitizenInvariants.RefundLedger {
		t.Errorf("the road fee and the refund never reached the ledger: %+v", c)
	}
}

// purgeLotAccessFootprint takes back what the lot access test wrote: the
// road and refund ledger transactions with their journal rows, then the
// citizen loop's own footprint.
func purgeLotAccessFootprint(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`CREATE TEMP TABLE purge_la ON COMMIT DROP AS
		   SELECT DISTINCT e.transaction_id FROM ledger_entries e
		    WHERE (e.reason = 'settlement_lot_road' AND e.reference_id IN (SELECT id FROM settlement_lot_connections WHERE settlement_id = $1::uuid))
		       OR (e.reason = 'settlement_lot_refund' AND e.reference_id IN (SELECT id FROM settlement_lots WHERE settlement_id = $1::uuid))`,
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_la) GROUP BY account_id) d
		  WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_la)`,
		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
		`DELETE FROM settlement_lot_connections WHERE settlement_id = $1::uuid`,
		`DELETE FROM settlement_road_reserve WHERE settlement_id = $1::uuid`,
	} {
		var err error
		if strings.Contains(stmt, "$1") {
			_, err = tx.Exec(ctx, stmt, cityID)
		} else {
			_, err = tx.Exec(ctx, stmt)
		}
		if err != nil {
			t.Errorf("cleanup %.40q: %v", stmt, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
		return
	}
	purgeCitizenFootprint(t, pool, cityID)
}
