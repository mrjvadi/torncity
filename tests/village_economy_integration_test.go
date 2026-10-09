//go:build integration

// Integration test of the village economy's first loop (ADR 0033 section 4.1,
// migration 0055): the owner's live problem - a village with a civic hall, a
// road and SUP in the treasury that cannot build because it has no timber and
// nothing to make it - is walked end to end: the refusal names the source, the
// village buys timber, builds a woodcutter's camp, a resident works a shift, the
// stock grows and the wage is paid, and `admin economy verify` stays green.
package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func stockOfItem(t *testing.T, pool *postgres.Pool, cityID, item string) int64 {
	t.Helper()
	var n int64
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`,
		cityID, item).Scan(&n); err != nil {
		t.Fatalf("reading the stock of %s: %v", item, err)
	}
	return n
}

func econButtonData(r *presenter.Response) string {
	if r == nil || r.Keyboard == nil {
		return ""
	}
	var b strings.Builder
	for _, row := range r.Keyboard.Rows {
		for _, btn := range row {
			b.WriteString(btn.CallbackData)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestVillageEconomyLoop(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)

	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatalf("loading the locale catalogue: %v", err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now)

	v0, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("migration 0052 is not applied")
	}

	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	// The scheduled actions go before the village does. The purchase and shift
	// rows stay, like the ledger and the item journal they are the record of:
	// the verifier compares the two, so deleting one side would break every
	// later test's verification.
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM game_actions WHERE reference_type = 'settlement_shift' AND reference_id IN (SELECT id FROM settlement_shifts WHERE settlement_id = $1::uuid)`,
			`DELETE FROM game_actions WHERE reference_type = 'settlement_building' AND reference_id IN (SELECT id FROM settlement_buildings WHERE settlement_id = $1::uuid)`,
		} {
			if _, err := pool.Raw().Exec(c, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	seedTreasury(t, pool, cityID, 60_000)
	head := asPlayer(meta, founder)
	mk := func(command, action string) envelope.Metadata {
		m := head
		m.Command, m.Action = command, action
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}

	// The live village: a civic hall, a road, the four founding knowledge items
	// and SUP - and no timber anywhere.
	if got := stockOfItem(t, pool, cityID, "timber"); got != 0 {
		t.Fatalf("a new village holds %d timber", got)
	}

	// 1. The catalogue lists what a village can start: the camp, no city-tier
	// building, and what needs timber says so.
	menu, err := rrcm(mk("settlement.build", "build"))(village.BuildMenu(ctx, mk("settlement.build", "build")))
	if err != nil {
		t.Fatal(err)
	}
	for _, city := range []string{"فرودگاه", "بندر", "بانک", "پادگان"} {
		if strings.Contains(menu.Text, city) {
			t.Errorf("a village's build menu lists %q, a bigger settlement's building:\n%s", city, menu.Text)
		}
	}
	if !strings.Contains(menu.Text, "هیزم‌شکنی") || !strings.Contains(econButtonData(menu), "settlement:build.lots:woodcutter_camp") {
		t.Errorf("the build menu does not offer the woodcutter's camp:\n%s\n%s", menu.Text, econButtonData(menu))
	}
	// Asking for a city building by code is refused, neutrally.
	if r, err := rrcm(mk("settlement.build.lots", "build.lots"))(village.Lots(ctx, mk("settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "airport"})); err != nil ||
		!strings.Contains(r.Text, "قابل") {
		t.Errorf("Lots(airport) = %+v %v, want a neutral refusal", r, err)
	}

	// 2. The housing block needs dressed masonry and a first house standing (no size label
	// lists it), then 10 timber: the refusal names where it comes from.
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at)
	    VALUES (gen_random_uuid(), $1::uuid, 'masonry_ii', 'researched', now()) ON CONFLICT DO NOTHING`, cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES (gen_random_uuid(), $1::uuid, 'cottage', 90, 90, 'complete', now(), now())`, cityID); err != nil {
		t.Fatal(err)
	}
	r, err := rrcm(mk("settlement.build.lots", "build.lots"))(village.Lots(ctx, mk("settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: "housing_block"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10", "الوار", "هیزم‌شکنی", "شهر مرکزی"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("the refusal for a housing block does not say %q:\n%s", want, r.Text)
		}
	}
	data := econButtonData(r)
	if !strings.Contains(data, "settlement:materials.buy:timber:10") || !strings.Contains(data, "settlement:build.lots:woodcutter_camp") {
		t.Errorf("the refusal has no button to the sources:\n%s", data)
	}

	// 3. Buy timber from Support with the treasury: a confirm first, nothing moves.
	treasury0 := treasuryOf(t, pool, cityID)
	buy := func(qty, confirm string) *presenter.Response {
		resp, err := rrcm(mk("settlement.materials.buy", "materials.buy"))(village.MaterialsBuy(ctx, mk("settlement.materials.buy", "materials.buy"),
			handlers.VillageMaterialRequest{Item: "timber", Qty: qty, Confirm: confirm}))
		if err != nil {
			t.Fatalf("MaterialsBuy(%s, %q): %v", qty, confirm, err)
		}
		return resp
	}
	ask := buy("10", "")
	if !strings.Contains(ask.Text, "180") || !strings.Contains(econButtonData(ask), "confirm") {
		t.Errorf("the purchase confirm does not show 180:\n%s", ask.Text)
	}
	if treasuryOf(t, pool, cityID) != treasury0 || stockOfItem(t, pool, cityID, "timber") != 0 {
		t.Fatal("asking to buy moved money or goods")
	}
	done := buy("10", screens.MaterialsConfirm)
	if !strings.Contains(done.Text, "خریداری") {
		t.Errorf("the purchase result:\n%s", done.Text)
	}
	if got := stockOfItem(t, pool, cityID, "timber"); got != 10 {
		t.Fatalf("stock after buying = %d, want 10", got)
	}
	if got := treasuryOf(t, pool, cityID); got != treasury0-180 {
		t.Fatalf("treasury after buying = %d, want %d (10 x 18)", got, treasury0-180)
	}
	// Room, budget and rights are enforced.
	// The founding kit's granary adds 300 of room; with it out of use the
	// base room is what is left, and 200 is past it.
	setGranary := func(status string) {
		if _, err := pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET status = $2,
		    completed_at = CASE WHEN $2 = 'complete' THEN now() END WHERE settlement_id = $1::uuid AND type_code = 'granary'`, cityID, status); err != nil {
			t.Fatal(err)
		}
	}
	setGranary("building")
	if r := buy("200", screens.MaterialsConfirm); !strings.Contains(r.Text, "جا ندارد") {
		t.Errorf("buying past the stock's room was not refused:\n%s", r.Text)
	}
	setGranary("complete")
	other := insertPlayer(t, pool)
	om := asPlayer(meta, other)
	om.Command, om.Action = "settlement.materials.buy", "materials.buy"
	om.IdempotencyKey = "it-" + randomToken(t, 16)
	if r, err := rrcm(om)(village.MaterialsBuy(ctx, om, handlers.VillageMaterialRequest{Item: "timber", Qty: "5", Confirm: screens.MaterialsConfirm})); err != nil ||
		!strings.Contains(r.Text, "منصب") {
		t.Errorf("a non-head bought timber: %+v %v", r, err)
	}
	if got := stockOfItem(t, pool, cityID, "timber"); got != 10 {
		t.Fatalf("refusals changed the stock: %d", got)
	}
	// A material the village may not buy is refused.
	if r, err := rrcm(mk("settlement.materials.buy", "materials.buy"))(village.MaterialsBuy(ctx, mk("settlement.materials.buy", "materials.buy"),
		handlers.VillageMaterialRequest{Item: "gemstone", Qty: "1", Confirm: screens.MaterialsConfirm})); err != nil || strings.Contains(r.Text, "خریداری") {
		t.Errorf("gemstone was bought: %+v %v", r, err)
	}

	// 4. A camp needs no timber: build it (the head's own lot choice), finish it.
	place := func(code string) string {
		t.Helper()
		lots, err := rrcm(mk("settlement.build.lots", "build.lots"))(village.Lots(ctx, mk("settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: code}))
		if err != nil {
			t.Fatal(err)
		}
		x, y, ok := firstFittingFreeLot(parseLotGrid(t, lots.View))
		if !ok {
			t.Fatalf("no lot fits %s:\n%s", code, lots.Text)
		}
		resp, err := rrcm(mk("settlement.build.place", "build.place"))(village.Place(ctx, mk("settlement.build.place", "build.place"),
			handlers.VillageBuildRequest{Code: code, Lot: screens.LotToken(x, y, false), Confirm: screens.VillageBuildConfirm}))
		if err != nil {
			t.Fatal(err)
		}
		var id string
		var finishAt time.Time
		if err := pool.Raw().QueryRow(ctx,
			`SELECT id::text, finish_at FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = $2 AND status = 'building'`,
			cityID, code).Scan(&id, &finishAt); err != nil {
			t.Fatalf("%s was not placed: %v\n%s", code, err, resp.Text)
		}
		e.clock.Advance(finishAt.Sub(e.clock.Now()) + time.Second)
		if _, err := rrcm(mk("settlement.built", "built"))(village.Built(ctx, mk("settlement.built", "built"), handlers.CrimeScheduledRequest{ReferenceID: id})); err != nil {
			t.Fatal(err)
		}
		return id
	}
	campID := place("woodcutter_camp")

	// 5. With the timber bought, the housing block builds: the deadlock is gone.
	place("housing_block")
	if got := stockOfItem(t, pool, cityID, "timber"); got != 0 {
		t.Fatalf("the housing block left %d timber, want 0 (it costs 10)", got)
	}

	// 6. A resident works a shift; the stock grows only when it ends.
	start := func(id string, m envelope.Metadata) *presenter.Response {
		m.Command, m.Action = "settlement.work", "work"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		resp, err := rrcm(m)(village.Work(ctx, m, handlers.VillageWorkRequest{ID: id}))
		if err != nil {
			t.Fatalf("Work(%s): %v", id, err)
		}
		return resp
	}
	cash0 := cashBalance(t, pool, application.AccountPlayerCash, founder.ID)
	treasury1 := treasuryOf(t, pool, cityID)
	started := start(campID, head)
	if !strings.Contains(started.Text, "شیفت") {
		t.Errorf("starting a shift:\n%s", started.Text)
	}
	if got := stockOfItem(t, pool, cityID, "timber"); got != 0 {
		t.Fatalf("a shift produced %d timber before it ended", got)
	}
	if r := start(campID, head); !strings.Contains(r.Text, "همین حالا") {
		t.Errorf("a second shift of the same resident was not refused:\n%s", r.Text)
	}
	// A player who does not live here cannot work.
	if r := start(campID, om); !strings.Contains(r.Text, "ساکن") {
		t.Errorf("a non-resident worked:\n%s", r.Text)
	}
	var shiftID, actionID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text FROM settlement_shifts WHERE settlement_id = $1::uuid AND status = 'working'`,
		cityID).Scan(&shiftID, &actionID); err != nil {
		t.Fatal(err)
	}
	// Not before its time.
	if _, err := rrcm(mk("settlement.worked", "worked"))(village.Worked(ctx, mk("settlement.worked", "worked"), handlers.CrimeScheduledRequest{ReferenceID: shiftID, ActionID: actionID})); err == nil {
		t.Error("a shift ended before its time")
	}
	e.clock.Advance(time.Hour + time.Second)
	for i := 0; i < 2; i++ { // twice: redelivery changes nothing
		if _, err := rrcm(mk("settlement.worked", "worked"))(village.Worked(ctx, mk("settlement.worked", "worked"), handlers.CrimeScheduledRequest{ReferenceID: shiftID, ActionID: actionID})); err != nil {
			t.Fatalf("Worked #%d: %v", i+1, err)
		}
	}
	if got := stockOfItem(t, pool, cityID, "timber"); got != 4 {
		t.Fatalf("stock after one shift = %d, want 4", got)
	}
	if got := cashBalance(t, pool, application.AccountPlayerCash, founder.ID) - cash0; got != 40 {
		t.Fatalf("the worker was paid %d, want 40", got)
	}
	if got := treasury1 - treasuryOf(t, pool, cityID); got != 40 {
		t.Fatalf("the treasury paid %d, want 40", got)
	}
	// Residency alone pays nothing: with no shift, the other resident earns nothing.
	if got := cashBalance(t, pool, application.AccountPlayerCash, other.ID); got != 0 {
		t.Errorf("a player with no shift holds %d", got)
	}

	// 7. The screens read the same numbers.
	stock, err := rrcm(mk("settlement.materials", "materials"))(village.Materials(ctx, mk("settlement.materials", "materials")))
	if err != nil || !strings.Contains(stock.Text, "الوار") {
		t.Errorf("the stock screen: %+v %v", stock, err)
	}
	work, err := rrcm(mk("settlement.work", "work"))(village.Work(ctx, mk("settlement.work", "work"), handlers.VillageWorkRequest{}))
	if err != nil || !strings.Contains(work.Text, "هیزم‌شکنی") {
		t.Errorf("the work screen: %+v %v", work, err)
	}

	// 8. The ledger, the item journal and the rows agree.
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i, i0 := v.VillageInvariants, v0.VillageInvariants
	drift := func(x postgres.VillageInvariants) [4]int64 {
		return [4]int64{x.MaterialLedger - x.MaterialRows, x.MaterialItems - x.MaterialItemRows, x.WageLedger - x.WageRows, x.ShiftItems - x.ShiftItemRows}
	}
	if drift(i) != drift(i0) || i.MaterialMismatched != i0.MaterialMismatched || i.WageMismatched != i0.WageMismatched {
		t.Errorf("the economy drifted: before %+v, after %+v", i0, i)
	}
	if i.MaterialRows-i0.MaterialRows != 180 || i.WageRows-i0.WageRows != 40 {
		t.Errorf("the rows changed by %d (material) and %d (wage), want 180 and 40", i.MaterialRows-i0.MaterialRows, i.WageRows-i0.WageRows)
	}

	// 9. A shift reserves room for its goods before it starts (audit F1): the
	// stock holds 4 timber (bulk 2 each) in the 60-space yard; fill it to the 11
	// spaces a shift needs (4 timber of 2, 2 firewood and a bark of 1) short of full.
	buy("19", screens.MaterialsConfirm)
	if got := stockOfItem(t, pool, cityID, "timber"); got != 23 {
		t.Fatalf("stock before the reservation check = %d, want 23", got)
	}
	cash1 := cashBalance(t, pool, application.AccountPlayerCash, founder.ID)
	if r := start(campID, head); !strings.Contains(r.Text, "شیفت") {
		t.Fatalf("a shift that fits was not started:\n%s", r.Text)
	}
	// The running shift's 4 timber hold the last room: nobody else can take it.
	if r := buy("1", screens.MaterialsConfirm); !strings.Contains(r.Text, "جا ندارد") {
		t.Errorf("a purchase took the room a running shift reserved:\n%s", r.Text)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text FROM settlement_shifts WHERE settlement_id = $1::uuid AND status = 'working'`,
		cityID).Scan(&shiftID, &actionID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour + time.Second)
	if _, err := rrcm(mk("settlement.worked", "worked"))(village.Worked(ctx, mk("settlement.worked", "worked"), handlers.CrimeScheduledRequest{ReferenceID: shiftID, ActionID: actionID})); err != nil {
		t.Fatal(err)
	}
	if got := stockOfItem(t, pool, cityID, "timber"); got != 27 {
		t.Fatalf("the reserved goods did not all arrive: stock %d, want 27", got)
	}
	if got := cashBalance(t, pool, application.AccountPlayerCash, founder.ID) - cash1; got != 40 {
		t.Fatalf("the worker was paid %d for a delivered shift, want 40", got)
	}
	// Full now: the next shift is refused, says how much room it needs, and
	// consumes and promises nothing.
	treasury2 := treasuryOf(t, pool, cityID)
	if r := start(campID, head); !strings.Contains(r.Text, "جا ندارد") || !strings.Contains(r.Text, "11") {
		t.Errorf("a shift into a full stock was not refused with the room it needs:\n%s", r.Text)
	}
	var working int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM settlement_shifts WHERE settlement_id = $1::uuid AND status = 'working'`, cityID).Scan(&working); err != nil || working != 0 {
		t.Errorf("a refused shift left %d running (%v)", working, err)
	}
	if got := treasuryOf(t, pool, cityID); got != treasury2 {
		t.Errorf("a refused shift moved the treasury by %d", got-treasury2)
	}
	v2, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if drift(v2.VillageInvariants) != drift(i0) || v2.VillageInvariants.WageMismatched != i0.WageMismatched {
		t.Errorf("the economy drifted after the reservation steps: before %+v, after %+v", i0, v2.VillageInvariants)
	}
}
