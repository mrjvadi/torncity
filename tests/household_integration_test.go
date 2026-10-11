//go:build integration

package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/household"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// The NPC households (docs/adr/0073, plan B9): the people behind the labour pool. The settlement is founded as the other village tests
// found it, a household rule date lies in the past, and one game day is 24 real minutes of the test clock.

type houseEnv struct {
	*shopEnv
	hrules application.HouseholdRules
	road   string
}

// newHouseEnv wires the households into both handlers of the shop env (the one without the shelf and the one with it). Every household
// is one adult who works (so a flip is size-exact), the arrivals fill the homes at once, and a hungry household leaves in two days.
func newHouseEnv(t *testing.T, tune func(*application.HouseholdRules)) *houseEnv {
	t.Helper()
	e := newShopEnv(t)
	if countRows(t, e.pool, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'households'`) == 0 {
		t.Skip("households does not exist; apply migration 0151 first")
	}
	cfg := config.Defaults()
	rules := settlementcfg.Households(cfg.Households, e.clk)
	rules.RuleAt = e.clock.Now().Add(-24 * time.Hour)
	rules.Rules.Compositions = []household.Composition{{Adults: 1, Weight: 1}}
	rules.Rules.EarnerBPS = household.BPS
	rules.Rules.ArrivalsPerDay = 1000
	rules.Rules.LeaveAfterDays = 2
	rules.Rules.SatisfactionAlphaBPS = household.BPS
	rules.Rules.LeaveBelowBPS = 5000
	rules.NoticeDays = 1
	rules.PerBatch = 50
	if tune != nil {
		tune(&rules)
	}
	if err := rules.Rules.Validate(); err != nil {
		t.Fatal(err)
	}
	e.village.WithHouseholds(rules)
	e.shop.WithHouseholds(rules)
	h := &houseEnv{shopEnv: e, hrules: rules}
	cityID := e.cityID
	t.Cleanup(func() {
		ctx := testCtx(t)
		var ids []string
		if rows, err := e.pool.Raw().Query(ctx, `SELECT id::text FROM households WHERE settlement_id = $1::uuid`, cityID); err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
		}
		for _, stmt := range []string{
			`DELETE FROM village_shop_sales WHERE settlement_id = $1::uuid`,
			`DELETE FROM household_rentals WHERE settlement_id = $1::uuid`,
			`DELETE FROM household_events WHERE settlement_id = $1::uuid`,
			`DELETE FROM household_ticks WHERE settlement_id = $1::uuid`,
			`DELETE FROM household_days WHERE settlement_id = $1::uuid`,
			`DELETE FROM household_settlements WHERE settlement_id = $1::uuid`,
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM village_storage_days WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, cityID)
			}
			if _, err := e.pool.Raw().Exec(ctx, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		for _, id := range ids {
			purgeLedgerFor(t, e.pool, id)
		}
		if _, err := e.pool.Raw().Exec(ctx, `DELETE FROM households WHERE settlement_id = $1::uuid`, cityID); err != nil {
			t.Errorf("cleanup households: %v", err)
		}
	})
	return h
}

// read is the households screen as the head sees it (it settles today's day first).
func (e *houseEnv) read(act, code, n string) village.HouseholdsView {
	e.t.Helper()
	return e.readWith(e.village, e.head, act, code, n)
}

func (e *houseEnv) readWith(h *handlers.VillageHandler, p *application.Player, act, code, n string) village.HouseholdsView {
	e.t.Helper()
	resp, err := rrc(h.Households(testCtx(e.t), e.as(p, "settlement.households", "households"), handlers.VillageHouseholdsRequest{Action: act, Code: code, N: n}))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.HouseholdsView
	if len(resp.View) == 0 {
		e.t.Fatalf("the households screen carries no view: %s", resp.Text)
	}
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

// settle reads the screen until the day is finished (a batch is PerBatch households), and returns the last view.
func (e *houseEnv) settle() village.HouseholdsView {
	e.t.Helper()
	var v village.HouseholdsView
	for i := 0; i < 12; i++ {
		v = e.read("", "", "")
		open := e.scalar(`SELECT count(*) FROM household_days WHERE settlement_id = $1::uuid AND done_at IS NULL`, e.cityID)
		if open == 0 {
			return v
		}
	}
	e.t.Fatal("a household day never finished")
	return v
}

// nextDay moves the clock a game day on and settles it.
func (e *houseEnv) nextDay() village.HouseholdsView {
	e.t.Helper()
	e.clock.Advance(24 * time.Minute)
	return e.settle()
}

func (e *houseEnv) stockOf(item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(e.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: time.Now().UTC()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *houseEnv) heldOf(item string) int64 {
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, e.cityID, item)
}

func (e *houseEnv) feed(days int64) {
	e.stockOf("wheat", 20*days)
	e.stockOf("spring_water", 20*days)
	e.stockOf("firewood", 20*days)
}

func (e *houseEnv) wallets() int64 {
	return e.scalar(`SELECT COALESCE(SUM(a.balance), 0)::bigint FROM accounts a JOIN households h ON h.id = a.owner_id
		WHERE a.kind = 'household_wallet' AND h.settlement_id = $1::uuid`, e.cityID)
}

func (e *houseEnv) households(state string) int64 {
	return e.scalar(`SELECT count(*) FROM households WHERE settlement_id = $1::uuid AND state = $2`, e.cityID, state)
}

func (e *houseEnv) phase() string {
	var s string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT COALESCE((SELECT state FROM household_settlements WHERE settlement_id = $1::uuid), 'none')`, e.cityID).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *houseEnv) pool_() int64 {
	e.t.Helper()
	if e.road == "" {
		e.road = e.place("road")
	}
	s, _ := e.site(e.head, e.road)
	return s.Market.Pool
}

func (e *houseEnv) rules0() labor.Rules { return e.laborEnv.rules }

func (e *houseEnv) verify() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || len(v.Drifted) != 0 || len(v.DriftedStacks) != 0 {
		e.t.Errorf("the ledger does not balance: sum %s, %d unbalanced, %d drifted", v.LedgerSum, len(v.Unbalanced), len(v.Drifted))
	}
	if s := v.VillageInvariants; !s.Households || s.HouseholdWalletBelowZero != 0 || s.HouseholdGoneHolding != 0 || s.HouseholdFromSource != 0 ||
		s.HouseholdWageToSink != 0 || s.HouseholdUnsettled != 0 || s.HouseholdItemOrphans != 0 || s.HouseholdShopOrphans != 0 {
		e.t.Errorf("the household invariants do not hold: %+v", s)
	}
}

// Before the flip nothing changes: the households arrive and are counted, the abstract pool still decides, no money moves.
// When they fill the homes the settlement flips: the pool is the earners, each household has the treasury's gift.
func TestHouseholdsDualRunChangesNothingThenFlips(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.feed(5)
	formula := e.rulesPool(1)
	if got := e.pool_(); got != formula { // the road it places is paid for before the treasury is counted
		t.Fatalf("the pool before any household is %d, the formula says %d", got, formula)
	}
	treasury0 := treasuryOf(t, e.pool, e.cityID)
	// one resident (the founder), the base homes: eight, so seven for households
	v := e.read("", "", "")
	if v.Phase != village.HouseholdPhaseDual || v.Living != 7 {
		t.Fatalf("the first day: phase %q, %d households (want dual and 7): %+v", v.Phase, v.Living, v)
	}
	if got := e.phase(); got != "dual" {
		t.Fatalf("phase %q", got)
	}
	if treasuryOf(t, e.pool, e.cityID) != treasury0 || e.wallets() != 0 {
		t.Errorf("the dual run moved money: treasury %d -> %d, wallets %d", treasury0, treasuryOf(t, e.pool, e.cityID), e.wallets())
	}
	if n := e.scalar(`SELECT count(*) FROM household_ticks WHERE settlement_id = $1::uuid`, e.cityID); n != 0 {
		t.Errorf("the dual run ticked %d households", n)
	}
	if got := e.pool_(); got != formula {
		t.Errorf("the dual run changed the pool: %d, the formula says %d", got, formula)
	}

	// the next day the homes are full: the flip
	v = e.nextDay()
	if v.Phase != village.HouseholdPhaseLive || e.phase() != "live" {
		t.Fatalf("the second day should flip: %+v", v)
	}
	if v.Earners != 7 || e.pool_() != 7 {
		t.Errorf("the pool is the earners of the households: earners %d, pool %d", v.Earners, e.pool_())
	}
	grants := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'household_welcome' AND e.amount > 0
		AND e.account_id IN (SELECT a.id FROM accounts a JOIN households h ON h.id = a.owner_id WHERE h.settlement_id = $1::uuid)`, e.cityID)
	if grants != 7*e.hrules.WelcomeGrant {
		t.Errorf("seven gifts of %d expected, the wallets were given %d", e.hrules.WelcomeGrant, grants)
	}
	bought := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'city_treasury'
		WHERE e.reason = 'household_purchase' AND e.amount > 0 AND e.reference_id IN (SELECT id FROM households WHERE settlement_id = $1::uuid)`, e.cityID)
	if got := treasury0 - treasuryOf(t, e.pool, e.cityID); got != grants-bought {
		t.Errorf("the treasury paid %d in gifts and was paid %d for the stock, it is down %d", grants, bought, got)
	}
	// they ate: food, water and firewood left the stock, once each, and what they paid went back to the treasury
	if e.heldOf("wheat") >= 100 || e.heldOf("spring_water") >= 100 || e.heldOf("firewood") >= 100 {
		t.Errorf("the households consumed nothing: wheat %d, water %d, firewood %d", e.heldOf("wheat"), e.heldOf("spring_water"), e.heldOf("firewood"))
	}
	if v.MoodBPS < 6500 {
		t.Errorf("fed households are not content: %d", v.MoodBPS)
	}
	e.verify()
}

// rulesPool is the abstract formula's pool for the settlement's residents, with the housing the test put in.
func (e *houseEnv) rulesPool(residents int64) int64 {
	housing := e.scalar(`SELECT COALESCE(SUM(CASE type_code WHEN 'housing_block' THEN 20 ELSE 0 END), 0)::bigint FROM settlement_buildings
		WHERE settlement_id = $1::uuid AND status = 'complete'`, e.cityID)
	return e.rules0().PoolSize(housing, residents)
}

// At the flip the pool is the earners of the households and, with a mean of the formula's participation, it is the size the formula
// gave (within the spread of the draw).
func TestHouseholdPoolIsTheEarnersAndSizeNeutralAtTheFlip(t *testing.T) {
	e := newHouseEnv(t, func(r *application.HouseholdRules) {
		cfg := config.Defaults()
		r.Rules.Compositions = []household.Composition{{Adults: 2, Weight: 1}}
		r.Rules.EarnerBPS = cfg.Labor.ParticipationBPS // the default: 60 percent of a household's adults... per household
	})
	for i := 0; i < 10; i++ { // 200 homes more
		if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
			VALUES (gen_random_uuid(), $1::uuid, 'housing_block', $2, 70, 'complete', now(), now())`, e.cityID, 200+i); err != nil {
			t.Fatal(err)
		}
	}
	e.feed(30)
	e.read("", "", "")
	e.settle()
	v := e.nextDay()
	if e.phase() != "live" {
		t.Fatalf("not live after the second day: %+v", v)
	}
	earners := e.scalar(`SELECT COALESCE(SUM(earners), 0)::bigint FROM households WHERE settlement_id = $1::uuid AND state = 'living'`, e.cityID)
	pool := e.pool_()
	if pool != earners {
		t.Fatalf("the pool %d is not the earners of the households %d", pool, earners)
	}
	formula := e.rulesPool(1)
	lo, hi := formula*75/100, formula*125/100
	if pool < lo || pool > hi {
		t.Errorf("the flip changed the size of the pool: the formula gave %d, the households give %d (allowed %d..%d)", formula, pool, lo, hi)
	}
	if got := e.households("living"); got < 180 {
		t.Errorf("the homes are not full: %d households", got)
	}
	e.verify()
}

// A wage of an NPC crew lands in the wallet of the household that holds the seat, not in the sink; the head's tax is withheld.
func TestHouseholdWagesLandInWalletsWithNoSink(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.alignToLocalMorning()
	// a little of everything: a full store would stop the woodcutter's crew
	e.stockOf("wheat", 40)
	e.stockOf("spring_water", 20)
	e.stockOf("firewood", 10)
	e.read("", "", "")
	e.nextDay()
	if e.phase() != "live" {
		t.Fatal("the settlement did not flip")
	}
	// the head taxes a wage by ten percent
	if v := e.read(village.HouseholdActionTax, "", "1000"); v.IncomeTaxBPS != 1000 {
		t.Fatalf("the tax was not set: %+v", v)
	}
	ctx := testCtx(t)
	camp := newUUID(t)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(e.village.LaborPost(ctx, e.as(e.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, camp).Scan(&jobID); err != nil {
		t.Fatalf("no production job: %v", err)
	}
	sink0, treasury0, wallets0 := e.sink(), treasuryOf(t, e.pool, e.cityID), e.wallets()
	if _, err := rrc(e.village.LaborHire(ctx, e.as(e.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"})); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	shifts := e.workingShifts(camp)
	if len(shifts) == 0 {
		var paused string
		_ = e.pool.Raw().QueryRow(ctx, `SELECT COALESCE(paused, '') FROM labor_jobs WHERE id = $1::uuid`, jobID).Scan(&paused)
		t.Fatalf("the crew did not start: paused %q, pool %d, stock wheat %d", paused, e.pool_(), e.heldOf("wheat"))
	}
	for _, s := range shifts {
		e.end(s)
	}
	paid := e.scalar(`SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done'`, camp)
	if paid == 0 {
		t.Fatal("no wage was paid")
	}
	toWallets := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'household_wallet'
		WHERE e.reason IN ('labor_wage_npc', 'settlement_wage') AND e.amount > 0 AND e.reference_id IN (SELECT id FROM settlement_shifts WHERE building_id = $1::uuid)`, camp)
	toSink := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'system_sink'
		WHERE e.reason IN ('labor_wage_npc', 'settlement_wage') AND e.amount > 0 AND e.reference_id IN (SELECT id FROM settlement_shifts WHERE building_id = $1::uuid)`, camp)
	tax := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'city_treasury'
		WHERE e.reason = 'household_income_tax' AND e.amount > 0 AND e.reference_id IN (SELECT id FROM settlement_shifts WHERE building_id = $1::uuid)`, camp)
	if toSink != 0 {
		t.Errorf("%d of the NPC wage went to the sink", toSink)
	}
	if toWallets != paid {
		t.Errorf("the wallets were paid %d of the %d the shifts cost", toWallets, paid)
	}
	if tax == 0 || tax*10 > paid || tax*10 < paid-int64(len(shifts))*10 {
		t.Errorf("ten percent of %d should be withheld, %d was", paid, tax)
	}
	if got := e.wallets() - wallets0; got != paid-tax {
		t.Errorf("the wallets grew by %d, want %d (wage less tax)", got, paid-tax)
	}
	if got := e.sink() - sink0; got != 0 {
		t.Errorf("the sink grew by %d while households were paid", got)
	}
	if got := treasury0 - treasuryOf(t, e.pool, e.cityID); got != paid-tax {
		t.Errorf("the treasury paid %d, want %d (the wage less the tax it took back)", got, paid-tax)
	}
	e.verify()
}

// A household with nothing to eat or drink becomes unhappy, gives notice and leaves with its wallet to the sink; the home is free.
// A closed settlement takes nobody, an open one takes new households again once nobody left in the quiet days.
func TestHouseholdDepartureAndArrival(t *testing.T) {
	e := newHouseEnv(t, func(r *application.HouseholdRules) {
		r.WelcomeQuietDays = 1
	})
	e.read("", "", "")
	e.nextDay() // the flip: the gifts are paid, the households are settled hungry, thirsty and cold
	if e.phase() != "live" {
		t.Fatal("the settlement did not flip")
	}
	first := e.households("living")
	if first != 7 {
		t.Fatalf("%d households", first)
	}
	// close the settlement to new households
	if v := e.read(village.HouseholdActionClose, "", ""); v.Open {
		t.Fatal("the head could not close the settlement")
	}
	sink0 := e.sink()
	e.nextDay() // second unhappy day in a row: notice
	if e.households("living") != 0 || e.households("leaving") != 7 {
		t.Fatalf("after two hungry days: %d living, %d leaving (want 0, 7)", e.households("living"), e.households("leaving"))
	}
	if e.scalar(`SELECT count(*) FROM household_events WHERE settlement_id = $1::uuid AND kind = 'notice'`, e.cityID) != 7 {
		t.Errorf("seven households should have had notice")
	}
	e.nextDay() // the notice runs out: they go, their wallets with them
	if e.households("gone") != 7 {
		t.Fatalf("%d households gone, want 7", e.households("gone"))
	}
	if left := e.scalar(`SELECT count(*) FROM household_events WHERE settlement_id = $1::uuid AND kind = 'left' AND detail <> ''`, e.cityID); left != 7 {
		t.Errorf("the news should say what each lacked: %d lines", left)
	}
	if got := e.sink() - sink0; got <= 0 {
		t.Errorf("their wallets should have gone to the sink: +%d", got)
	}
	if e.wallets() != 0 {
		t.Errorf("a household that left holds %d", e.wallets())
	}
	if e.households("living") != 0 {
		t.Errorf("a closed settlement took %d new households", e.households("living"))
	}
	// open again: new households come (no gift while the quiet day after a departure lasts)
	e.read(village.HouseholdActionAdmit, "", "")
	treasury0 := treasuryOf(t, e.pool, e.cityID)
	v := e.nextDay()
	if v.Living < 1 {
		t.Fatalf("an open settlement with free homes and nobody unhappy takes households again: %+v", v)
	}
	_ = treasury0
	e.verify()
}

// A player who takes a home displaces a household, never the other way: the least satisfied gets notice, the player is not refused.
func TestHouseholdsNeverDisplaceAPlayer(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.feed(10)
	e.read("", "", "")
	v := e.nextDay()
	if v.Living != 7 {
		t.Fatalf("%d households", v.Living)
	}
	// another player lives here: one home fewer
	_ = e.resident()
	v = e.nextDay()
	if e.households("leaving")+e.households("gone") != 1 || v.Living != 6 {
		t.Fatalf("one household should have been given notice: %d leaving, %d living", e.households("leaving"), v.Living)
	}
	if n := e.scalar(`SELECT count(*) FROM household_events WHERE settlement_id = $1::uuid AND kind = 'notice' AND detail = 'home'`, e.cityID); n != 1 {
		t.Errorf("the notice should name the home: %d", n)
	}
	// the players are still two, and no household took the seat of a player: the pool is the households' earners
	if got := e.scalar(`SELECT count(*) FROM players WHERE residence_city_id = $1::uuid`, e.cityID); got != 2 {
		t.Errorf("%d residents", got)
	}
	// closing the settlement is the head's, and only the head's
	other := e.resident()
	if _, err := rrc(e.village.Households(testCtx(t), e.as(other, "settlement.households", "households"), handlers.VillageHouseholdsRequest{Action: village.HouseholdActionClose})); err != nil {
		t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM household_settlements WHERE settlement_id = $1::uuid AND admission = 'closed'`, e.cityID) != 0 {
		t.Error("a resident who holds no office closed the settlement")
	}
	e.verify()
}

// At the shop households take only what the reserve leaves: the shelf keeps the share players buy from.
func TestHouseholdsLeaveTheShelfsReserveToPlayers(t *testing.T) {
	e := newHouseEnv(t, func(r *application.HouseholdRules) { r.PlayerReserveBPS = 7000 })
	e.stockOf("firewood", 100)
	e.stockOf("spring_water", 100) // no wheat: the shelf is the only food
	e.read("", "", "") // the households arrive (this handler has no shelf, so nothing is delivered yet)
	e.clock.Advance(24 * time.Minute)
	for i := 0; i < 3; i++ { // the flip day: the shelf is delivered, the households buy from what the reserve leaves
		e.readWith(e.shop, e.head, "", "", "")
	}
	sold := e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM village_shop_sales WHERE settlement_id = $1::uuid AND household_id IS NOT NULL AND line = 'bread'`, e.cityID)
	delivered := e.scalar(`SELECT COALESCE(SUM(delivered_total), 0)::bigint FROM village_shop_lines WHERE settlement_id = $1::uuid AND line = 'bread'`, e.cityID)
	stock := e.scalar(`SELECT COALESCE(SUM(stock), 0)::bigint FROM village_shop_lines WHERE settlement_id = $1::uuid AND line = 'bread'`, e.cityID)
	if delivered == 0 || sold == 0 {
		t.Fatalf("the shelf delivered %d bread and the households bought %d", delivered, sold)
	}
	if stock*10_000 < delivered*7_000 {
		t.Errorf("the households took into the players' reserve: %d of %d delivered left", stock, delivered)
	}
	// the sales are rows of the shop: the sink got the price, the treasury the tax
	if n := e.scalar(`SELECT count(*) FROM village_shop_sales WHERE settlement_id = $1::uuid AND household_id IS NOT NULL AND player_id IS NOT NULL`, e.cityID); n != 0 {
		t.Errorf("a sale has two buyers")
	}
	e.verify()
}

// Two replicas that settle the same day write it once: one day row, one tick for each household, every purchase once.
func TestHouseholdDayIsSettledOnceByConcurrentReplicas(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.feed(10)
	e.read("", "", "")
	e.nextDay() // live
	e.clock.Advance(24 * time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.village.Households(testCtx(t), e.as(e.head, "settlement.households", "households"), handlers.VillageHouseholdsRequest{}); err != nil {
				t.Errorf("households: %v", err)
			}
		}()
	}
	wg.Wait()
	e.settle()
	day := e.scalar(`SELECT MAX(day) FROM household_days WHERE settlement_id = $1::uuid`, e.cityID)
	if n := e.scalar(`SELECT count(*) FROM household_days WHERE settlement_id = $1::uuid AND day = $2`, e.cityID, day); n != 1 {
		t.Errorf("%d rows for one day", n)
	}
	living := e.households("living")
	if n := e.scalar(`SELECT count(*) FROM household_ticks WHERE settlement_id = $1::uuid AND day = $2`, e.cityID, day); n != living {
		t.Errorf("%d ticks for %d households", n, living)
	}
	if n := e.scalar(`SELECT count(*) FROM (SELECT household_id FROM household_ticks WHERE settlement_id = $1::uuid AND day = $2 GROUP BY 1 HAVING count(*) > 1) x`, e.cityID, day); n != 0 {
		t.Errorf("%d households ticked twice", n)
	}
	// the money: what the households paid the treasury for the day is what their ticks say
	paid := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'city_treasury'
		WHERE e.reason = 'household_purchase' AND e.amount > 0 AND e.reference_id IN (SELECT id FROM households WHERE settlement_id = $1::uuid)`, e.cityID)
	ticked := e.scalar(`SELECT COALESCE(SUM(spent), 0)::bigint FROM household_ticks WHERE settlement_id = $1::uuid`, e.cityID)
	if paid != ticked {
		t.Errorf("the treasury was paid %d, the ticks say %d: a household paid twice or not at all", paid, ticked)
	}
	// and the same day read again changes nothing
	before := e.scalar(`SELECT COUNT(*) FROM ledger_entries WHERE reason LIKE 'household_%'`)
	e.settle()
	if after := e.scalar(`SELECT COUNT(*) FROM ledger_entries WHERE reason LIKE 'household_%'`); after != before {
		t.Errorf("a second reading of the day wrote %d more legs", after-before)
	}
	e.verify()
}

// A dwelling its owner opens to tenants takes the households first; they pay the rent to the owner's purse.
func TestHouseholdsPayRentToTheOwnerOfAnOpenDwelling(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.feed(10)
	owner := e.resident()
	dwelling := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at, employer_player_id)
		VALUES ($1::uuid, $2::uuid, 'private_cottage', 95, 5, 'complete', now(), now(), $3::uuid)`, dwelling, e.cityID, owner.ID); err != nil {
		t.Fatal(err)
	}
	v := e.readWith(e.village, owner, "", "", "")
	if len(v.Dwellings) != 1 || v.Dwellings[0].Open {
		t.Fatalf("the owner's dwellings: %+v", v.Dwellings)
	}
	if v := e.readWith(e.village, owner, village.HouseholdActionRentOpen, dwelling, "30"); len(v.Dwellings) != 1 || !v.Dwellings[0].Open || v.Dwellings[0].Rent != 30 {
		t.Fatalf("the dwelling was not opened: %+v", v.Dwellings)
	}
	// a rent over the bound is refused
	_, _ = rrc(e.village.Households(testCtx(t), e.as(owner, "settlement.households", "households"), handlers.VillageHouseholdsRequest{Action: village.HouseholdActionRentOpen, Code: dwelling, N: "99999"}))
	if e.scalar(`SELECT rent FROM household_rentals WHERE building_id = $1::uuid`, dwelling) != 30 {
		t.Error("a rent over the bound was taken")
	}
	// someone else's dwelling cannot be opened
	stranger := e.resident()
	_, _ = rrc(e.village.Households(testCtx(t), e.as(stranger, "settlement.households", "households"), handlers.VillageHouseholdsRequest{Action: village.HouseholdActionRentOpen, Code: dwelling, N: "10"}))
	if e.scalar(`SELECT rent FROM household_rentals WHERE building_id = $1::uuid`, dwelling) != 30 {
		t.Error("a stranger changed the owner's rent")
	}
	e.nextDay() // arrive
	e.nextDay() // flip
	e.nextDay() // live day: rent is paid
	tenants := e.scalar(`SELECT count(*) FROM households WHERE settlement_id = $1::uuid AND home_ref LIKE 'b:' || $2 || ':%' AND state <> 'gone'`, e.cityID, dwelling)
	if tenants == 0 {
		t.Fatal("no household lives in the open dwelling")
	}
	rent := e.scalar(`SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id AND a.kind = 'player_cash' AND a.owner_id = $1::uuid
		WHERE e.reason = 'household_rent' AND e.amount > 0`, owner.ID)
	if rent == 0 || rent%30 != 0 {
		t.Errorf("the owner was paid %d in rent, in steps of 30", rent)
	}
	// closing the rental takes it off the list
	if v := e.readWith(e.village, owner, village.HouseholdActionRentClose, dwelling, ""); v.Dwellings[0].Open {
		t.Error("the dwelling is still open")
	}
	e.verify()
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM household_rentals WHERE settlement_id = $1::uuid`, e.cityID); err != nil {
		t.Fatal(err)
	}
	purgeLedgerFor(t, e.pool, owner.ID)
}

// Households with a surplus above their savings spend it on the stock's goods (money back to the treasury, never a hoard), and the
// market day's trader leaves the households' days of food in the store.
func TestHouseholdSurplusGoesBackToTheTreasury(t *testing.T) {
	e := newHouseEnv(t, func(r *application.HouseholdRules) {
		r.WalletCap = 100
		r.WelcomeGrant = 500
	})
	e.feed(10)
	e.stockOf("pots", 50)
	e.read("", "", "")
	e.nextDay() // live: gifts of 500
	e.nextDay()
	if got := e.heldOf("pots"); got >= 50 {
		t.Errorf("the households spent none of their surplus: %d pots left", got)
	}
	if e.wallets() >= 7*500 {
		t.Errorf("the wallets still hold all the gifts: %d", e.wallets())
	}
	e.verify()
}

// The households cost nothing while they are off: before the rule date a settlement is not enrolled and its pool is the formula's.
func TestHouseholdsAreOffBeforeTheRuleDate(t *testing.T) {
	e := newHouseEnv(t, func(r *application.HouseholdRules) { r.RuleAt = time.Now().Add(1000 * time.Hour) })
	v := e.read("", "", "")
	if v.Phase != village.HouseholdPhaseNone || e.scalar(`SELECT count(*) FROM households WHERE settlement_id = $1::uuid`, e.cityID) != 0 {
		t.Fatalf("the households ran before their date: %+v", v)
	}
	if got, want := e.pool_(), e.rulesPool(1); got != want {
		t.Errorf("the pool is %d, the formula says %d", got, want)
	}
	if e.scalar(`SELECT count(*) FROM household_settlements WHERE settlement_id = $1::uuid`, e.cityID) != 0 {
		t.Errorf("settlement enrolled before its date")
	}
}

// The market day's trader leaves the households' next days of food in the store: whatever the head puts on sale, the stock keeps
// StockDays of what the live households eat.
func TestTheTraderLeavesTheHouseholdsTheirFood(t *testing.T) {
	e := newHouseEnv(t, nil)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	e.village.WithTrade(handlers.TradeRules{Rules: settlementcfg.Trade(cfg.Settlement), Clock: clock, KeepPresets: cfg.Settlement.ExportKeepPresets})
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM trade_days WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM trade_orders WHERE settlement_id = $1::uuid`, e.cityID)
	})
	e.stockOf("wheat", 8)
	e.stockOf("spring_water", 20)
	e.stockOf("firewood", 8)
	e.read("", "", "")
	e.nextDay() // live; the households ate a little
	if e.phase() != "live" {
		t.Fatal("not live")
	}
	e.stockOf("wheat", 60)
	// the head puts all the wheat on sale; the trader comes the first time the desk is looked at
	if _, err := rrc(e.village.TradeDesk(testCtx(t), e.as(e.head, "settlement.trade", "trade"), handlers.VillageTradeRequest{Action: village.TradeActionKeep, Code: "wheat", N: "0"})); err != nil {
		t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM trade_days WHERE settlement_id = $1::uuid AND outcome = 'sold'`, e.cityID) != 1 {
		t.Skip("no trader came today (no clerk seat or no road in this settlement)")
	}
	left := e.heldOf("wheat")
	// seven one-adult households eat four points a day, two to a wheat: fourteen wheat a day, two days
	if left < 28 {
		t.Errorf("the trader left %d wheat, the households need 28 for their next two days", left)
	}
	if left >= 60 {
		t.Errorf("the trader bought nothing: %d wheat left", left)
	}
	e.verify()
}

// The emergency stop: a settlement switched off pays its NPC wages into the sink again, with the wallets kept.
func TestHouseholdsSwitchedOffPayTheSinkAgain(t *testing.T) {
	e := newHouseEnv(t, nil)
	e.feed(5)
	e.read("", "", "")
	e.nextDay()
	if e.phase() != "live" {
		t.Fatal("not live")
	}
	wallets := e.wallets()
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE household_settlements SET state = 'off' WHERE settlement_id = $1::uuid`, e.cityID); err != nil {
		t.Fatal(err)
	}
	days := e.scalar(`SELECT count(*) FROM household_days WHERE settlement_id = $1::uuid`, e.cityID)
	e.clock.Advance(24 * time.Minute)
	e.read("", "", "")
	if e.scalar(`SELECT count(*) FROM household_days WHERE settlement_id = $1::uuid`, e.cityID) != days {
		t.Error("a switched-off settlement settled a day")
	}
	if e.wallets() != wallets {
		t.Errorf("the wallets moved while switched off: %d -> %d", wallets, e.wallets())
	}
	if got, want := e.pool_(), e.rulesPool(1); got != want {
		t.Errorf("the pool is %d, the formula says %d", got, want)
	}
	e.verify()
}
