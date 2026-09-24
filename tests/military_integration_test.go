//go:build integration

// Integration tests of the armed forces and diplomacy (migration 0021,
// docs/adr/0022-military-and-diplomacy.md), through the real handlers, unit
// of work, ledger, item journal and policy resolver:
//
//	the operator seats a president, who appoints a defence minister and a
//	chief of the general staff in play, who appoints the air force's
//	commander; a defence-electronics company researches radar systems and
//	licenses them; an aerospace company researches stealth shaping,
//	radar-absorbent materials and jet propulsion, buys strategic materials,
//	makes the parts, designs, names and builds a stealth fighter and lists
//	it; the country's first defence period pays the cities' levy and the
//	defence appropriation once; nobody but the minister may buy it, and no
//	player or company can; the minister buys it — paid once from the
//	defence fund; the commander stations it, landing once; the next defence
//	period charges its upkeep once;
//
//	the president sanctions the other country: a market trade, a card
//	payment and a journey across the border are refused; lifted after its
//	least duration, all three go through;
//
//	a foreign minister proposes an alliance, accepted once.
//
// The ledger's, the journal's and the forces' invariants hold at the end,
// and everything the tests made is removed.
package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

const (
	homeCountryCode = "default_country"
	farCountryCode  = "vantor_federation"
	militaryCity    = "brennhaven"
)

// requireMilitary skips when migration 0021 or the military content is
// missing, and returns both countries' ids.
func requireMilitary(t *testing.T, pool *postgres.Pool) (home, far string) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.military_assets') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("military_assets does not exist; apply migration 0021 first")
	}
	for code, into := range map[string]*string{homeCountryCode: &home, farCountryCode: &far} {
		if err := pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM jurisdictions WHERE kind = 'country' AND code = $1`,
			code).Scan(into); err != nil {
			t.Skipf("the country %s is not loaded; run `admin content load`: %v", code, err)
		}
	}
	return home, far
}

// purgeMilitary removes everything the armed forces and diplomacy hold for
// the countries and the players: sanctions, treaties and their record,
// defence clocks and periods, purchases, moves, assets and the pieces the
// states hold, the state accounts with every ledger transaction that
// touched one (reversed on every balance), the scheduled actions, the
// events, and the seats the players hold.
func purgeMilitary(t *testing.T, pool *postgres.Pool, countries []string, players []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, step := range []struct {
		sql string
		arg any
	}{
		{`ALTER TABLE diplomacy_events DISABLE TRIGGER diplomacy_events_append_only`, nil},
		{`ALTER TABLE military_periods DISABLE TRIGGER military_periods_append_only`, nil},
		{`ALTER TABLE procurements DISABLE TRIGGER procurements_append_only`, nil},
		{`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`, nil},
		{`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`, nil},
		{`DELETE FROM diplomacy_events WHERE country_id = ANY($1::uuid[]) OR other_country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM sanctions WHERE imposer_id = ANY($1::uuid[]) OR target_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM treaties WHERE proposer_id = ANY($1::uuid[]) OR partner_id = ANY($1::uuid[])`, countries},
		{`CREATE TEMP TABLE purge_mpiece ON COMMIT DROP AS
		   SELECT id FROM item_pieces WHERE org_kind = 'state' AND org_id = ANY($1::uuid[])`, countries},
		{`CREATE TEMP TABLE purge_maction ON COMMIT DROP AS
		   SELECT action_id AS id FROM military_clocks WHERE country_id = ANY($1::uuid[]) AND action_id IS NOT NULL
		   UNION SELECT game_action_id FROM military_moves WHERE country_id = ANY($1::uuid[])
		   UNION SELECT id FROM game_actions WHERE action_type = 'military_period' AND reference_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM military_assets WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM military_moves WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM procurements WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM military_periods WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM military_clocks WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM game_actions WHERE id IN (SELECT id FROM purge_maction)`, nil},
		{`DELETE FROM item_movements WHERE from_org_kind = 'state' OR to_org_kind = 'state'
		     OR piece_id IN (SELECT id FROM purge_mpiece)`, nil},
		{`DELETE FROM item_pieces WHERE id IN (SELECT id FROM purge_mpiece)`, nil},
		{`CREATE TEMP TABLE purge_mtx ON COMMIT DROP AS
		   SELECT DISTINCT e.transaction_id FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		    WHERE a.kind IN ('state_treasury', 'defence_fund') AND a.owner_id = ANY($1::uuid[])`, countries},
		{`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_mtx) GROUP BY account_id) d
		  WHERE a.id = d.account_id`, nil},
		{`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_mtx)`, nil},
		{`DELETE FROM accounts WHERE kind IN ('state_treasury', 'defence_fund') AND owner_id = ANY($1::uuid[])`, countries},
		{`UPDATE offices SET holder_player_id = NULL, acquired_by = NULL, term_ends_at = NULL, since = now()
		   WHERE holder_player_id = ANY($1::uuid[])`, players},
		{`DELETE FROM outbox WHERE subject LIKE 'game.event.military.%' OR subject LIKE 'game.event.diplomacy.%'
		     OR subject LIKE 'game.event.governance.appointed%' OR subject LIKE 'game.event.governance.dismissed%'`, nil},
		{`ALTER TABLE diplomacy_events ENABLE TRIGGER diplomacy_events_append_only`, nil},
		{`ALTER TABLE military_periods ENABLE TRIGGER military_periods_append_only`, nil},
		{`ALTER TABLE procurements ENABLE TRIGGER procurements_append_only`, nil},
		{`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`, nil},
		{`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`, nil},
	} {
		var args []any
		if step.arg != nil {
			args = []any{step.arg}
		}
		if _, err := tx.Exec(ctx, step.sql, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(step.sql), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// testClock is a clock the test moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// said fails the test unless the response says every want.
func said(t *testing.T, what string, resp *presenter.Response, err error, want ...string) *presenter.Response {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if resp == nil {
		t.Fatalf("%s: no response", what)
	}
	for _, w := range want {
		if !strings.Contains(resp.Text, w) {
			t.Fatalf("%s: %q does not say %q", what, resp.Text, w)
		}
	}
	return resp
}

// seatAs seats a player in an office by the operator's path.
func seatAs(t *testing.T, uow application.UnitOfWork, office, jurisdictionID string, p *application.Player, now time.Time) {
	t.Helper()
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		_, _, err := application.AppointToOffice(ctx, tx, office, jurisdictionID, 1, p.ID, now)
		return err
	}); err != nil {
		t.Fatalf("seating %s: %v", office, err)
	}
}

// holder reads who holds an office's first seat in a jurisdiction.
func holder(t *testing.T, pool *postgres.Pool, office, jurisdictionID string) string {
	t.Helper()
	var id *string
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT holder_player_id::text FROM offices
	  WHERE office_code = $1 AND jurisdiction_id = $2::uuid AND seat = 1`, office, jurisdictionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id == nil {
		return ""
	}
	return *id
}

func TestStealthFighterProcuredStationedAndKept(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	home, far := requireMilitary(t, pool)
	registry := companyRegistry(t, pool)
	if _, ok := registry.Current().Archetype("stealth_fighter"); !ok {
		t.Skip("the active content has no military goods; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, militaryCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", militaryCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", militaryCity, n)
	}

	president, minister, chief, commander := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	maker, radarMaker, stranger := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	players := []*application.Player{president, minister, chief, commander, maker, radarMaker, stranger}
	var ids []string
	for _, p := range players {
		p := p
		ids = append(ids, p.ID)
		t.Cleanup(func() { purgeLedgerFor(t, pool, p.ID); purgeWorkFor(t, pool, p.ID) })
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
			  WHERE id = $1::uuid`, p.ID, city.ID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, maker.ID, radarMaker.ID) })
	t.Cleanup(func() { purgeProductionOf(t, pool, city.ID, maker.ID, radarMaker.ID) })
	purgeMilitary(t, pool, []string{home, far}, nil)
	t.Cleanup(func() { purgeMilitary(t, pool, []string{home, far}, ids) })
	grantCash(t, pool, maker.ID, 2_500_000)
	grantCash(t, pool, radarMaker.ID, 600_000)
	grantCash(t, pool, stranger.ID, 100_000)
	for p, level := range map[*application.Player]int{maker: 7, radarMaker: 5} {
		if _, err := pool.Raw().Exec(ctx,
			`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at)
			 VALUES (gen_random_uuid(), $1::uuid, 'engineering', $2, 0, now())`, p.ID, level); err != nil {
			t.Fatal(err)
		}
	}

	clock := &testClock{now: time.Now().UTC()}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, clock.Now)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	scale := gametime.Scale(gameScale)
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), scale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits}, time.Hour, clock.Now)
	prod := handlers.NewProductionHandler(uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.ProductionRules{
		MaxRunningOrders: 5, MaxDesigns: 20, MaxListings: 10, DesignMinSkill: 1, ReverseTime: 6 * time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits}, time.Hour, clock.Now)
	forces := handlers.NewMilitaryHandler(uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.MilitaryRules{
		Period: 24 * time.Hour, ReadinessLossBPS: 1000, ReadinessRecoveryBPS: 500, ReferenceRadarKM: 150}, time.Hour, clock.Now)
	appoint := handlers.NewAppointmentHandler(uow, workIDs{t}, nil, postgres.NewPlayerSearchRepository(pool), time.Hour, clock.Now)
	ledger := postgres.NewLedgerRepository(pool)
	balance := func(kind application.AccountKind, owner string) int64 {
		t.Helper()
		acct, err := ledger.AccountFor(testCtx(t), kind, owner)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	scheduler := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command = 0, command
		return m
	}

	// --- 1. Offices: the operator seats a president, who appoints in play.
	resp, err := forces.Ministry(ctx, metaAs(stranger, "military.ministry"), handlers.MilitaryRequest{})
	said(t, "the ministry, before anyone holds office", resp, err)
	seatAs(t, uow, "president", home, president, clock.Now())
	resp, err = appoint.Appoint(ctx, metaAs(president, "gov.appoint"), handlers.AppointRequest{Office: "defence_minister",
		Place: homeCountryCode, To: minister.PublicCode})
	said(t, "propose the minister", resp, err, "gov.appoint.confirm")
	press := metaAs(president, "gov.seat")
	for range 2 {
		_, err = appoint.Seat(ctx, press, handlers.AppointRequest{Office: "defence_minister", Place: homeCountryCode,
			To: minister.PublicCode})
		if err != nil {
			t.Fatalf("seat the minister: %v", err)
		}
	}
	if holder(t, pool, "defence_minister", home) != minister.ID {
		t.Fatal("the defence minister was not seated")
	}
	resp, err = appoint.Seat(ctx, metaAs(minister, "gov.seat"), handlers.AppointRequest{Office: "chief_of_general_staff",
		Place: homeCountryCode, To: chief.PublicCode})
	said(t, "the minister appointing the chief", resp, err, "gov.appoint.refused.not_appointer")
	resp, err = appoint.Seat(ctx, metaAs(president, "gov.seat"), handlers.AppointRequest{Office: "chief_of_general_staff",
		Place: homeCountryCode, To: minister.PublicCode})
	said(t, "the minister as chief too", resp, err, "gov.refusal.incompatible")
	for who, office := range map[*application.Player]string{president: "chief_of_general_staff", chief: "air_force_commander"} {
		appointee := chief
		if office == "air_force_commander" {
			appointee = commander
		}
		resp, err = appoint.Seat(ctx, metaAs(who, "gov.seat"), handlers.AppointRequest{Office: office, Place: homeCountryCode,
			To: appointee.PublicCode})
		said(t, "appoint "+office, resp, err, "gov.appoint.done")
	}
	if holder(t, pool, "chief_of_general_staff", home) != chief.ID || holder(t, pool, "air_force_commander", home) != commander.ID {
		t.Fatal("the chief or the commander was not seated")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.governance.appointed.v1'
	  AND payload->>'player_id' = ANY($1::text[])`, []string{minister.ID, chief.ID, commander.ID}); n != 3 {
		t.Errorf("appointment events = %d, want 3 (one per seat, the double press once)", n)
	}

	// The defence clock: the ministry starts it.
	resp, err = forces.Ministry(ctx, metaAs(minister, "military.ministry"), handlers.MilitaryRequest{})
	said(t, "the ministry, as the minister", resp, err, "military.ministry.title")

	// --- 2. The defence industry. ------------------------------------------
	type co struct{ id, code string }
	found := func(p *application.Player, kind, name string, deposit string) co {
		t.Helper()
		resp, err := companies.Found(ctx, metaAs(p, "company.found"), handlers.CompanyRequest{Type: kind, Method: "cash", Name: name})
		said(t, "found "+name, resp, err)
		var c co
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
			p.ID, name).Scan(&c.id, &c.code); err != nil {
			t.Fatalf("company %s was not founded: %v (%s)", name, err, resp.Text)
		}
		resp, err = companies.Deposit(ctx, metaAs(p, "company.deposit"), handlers.CompanyRequest{Company: c.code, Method: "cash",
			Amount: deposit})
		said(t, "deposit into "+name, resp, err)
		return c
	}
	aero := found(maker, "aerospace", "Simorgh Aerospace", "2000000")
	radar := found(radarMaker, "defence_electronics", "Kaveh Radars", "400000")
	research := func(p *application.Player, c co, tech string) {
		t.Helper()
		resp, err := prod.Research(ctx, metaAs(p, "company.research"), handlers.ProductionRequest{Company: c.code, Tech: tech})
		said(t, "research "+tech, resp, err)
		var id, action string
		var finish time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, finish_at FROM company_research
		  WHERE company_id = $1::uuid AND tech_code = $2 AND status = 'running'`, c.id, tech).Scan(&id, &action, &finish); err != nil {
			t.Fatalf("research %s did not start: %v (%s)", tech, err, resp.Text)
		}
		clock.Advance(finish.Sub(clock.Now()) + time.Second)
		if _, err := prod.Researched(ctx, scheduler("company.researched"), handlers.CrimeScheduledRequest{ActionID: action,
			ReferenceID: id}); err != nil {
			t.Fatalf("researched %s: %v", tech, err)
		}
	}
	for _, tech := range []string{"aerospace_engineering", "jet_propulsion", "stealth_shaping", "radar_absorbent_materials"} {
		research(maker, aero, tech)
	}
	research(radarMaker, radar, "radar_systems")
	// A civilian factory could never license the radar; the aerospace
	// company buys a license.
	resp, err = prod.TechMode(ctx, metaAs(radarMaker, "company.techmode"), handlers.ProductionRequest{Company: radar.code,
		Tech: "radar_systems", Mode: "license", Price: "50000"})
	said(t, "license radar systems", resp, err)
	resp, err = prod.License(ctx, metaAs(maker, "company.license"), handlers.ProductionRequest{Company: aero.code,
		Tech: "radar_systems", From: radar.code, Confirm: "yes"})
	said(t, "buy the radar license", resp, err)
	if n := countRows(t, pool, `SELECT count(*) FROM technology_licenses WHERE licensee_company_id = $1::uuid`, aero.id); n != 1 {
		t.Fatalf("licenses = %d, want 1", n)
	}

	for comp, qty := range map[string]string{"titanium": "70", "carbon_fibre": "72", "rare_earths": "40"} {
		resp, err = prod.Supply(ctx, metaAs(maker, "company.supply"), handlers.ProductionRequest{Company: aero.code,
			Component: comp, Qty: qty})
		said(t, "buy "+comp, resp, err, "production.supply_bought")
	}
	build := func(target string) {
		t.Helper()
		resp, err := prod.Produce(ctx, metaAs(maker, "company.produce"), handlers.ProductionRequest{Company: aero.code,
			Target: target, Qty: "1", Confirm: "yes"})
		said(t, "produce "+target, resp, err, "production.placed")
		var id, action string
		var finish time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, finish_at FROM production_orders
		  WHERE company_id = $1::uuid ORDER BY no DESC LIMIT 1`, aero.id).Scan(&id, &action, &finish); err != nil {
			t.Fatal(err)
		}
		clock.Advance(finish.Sub(clock.Now()) + time.Second)
		if _, err := prod.Produced(ctx, scheduler("company.produced"), handlers.CrimeScheduledRequest{ActionID: action,
			ReferenceID: id}); err != nil {
			t.Fatalf("produced %s: %v", target, err)
		}
	}
	for _, part := range []string{"stealth_airframe", "ram_coating", "turbofan", "fire_control_radar"} {
		build(part)
	}
	resp, err = prod.DesignNew(ctx, metaAs(maker, "company.dnew"), handlers.ProductionRequest{Company: aero.code,
		Item: "stealth_fighter_jet"})
	said(t, "a new stealth fighter design", resp, err)
	var designNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM product_designs WHERE company_id = $1::uuid`, aero.id).Scan(&designNo); err != nil {
		t.Fatal(err)
	}
	no := itoa(designNo)
	for slot, comp := range map[string]string{"airframe": "stealth_airframe", "coating": "ram_coating", "engine": "turbofan",
		"radar": "fire_control_radar"} {
		resp, err = prod.DesignFill(ctx, metaAs(maker, "company.dfill"), handlers.ProductionRequest{No: no, Slot: slot, Component: comp})
		said(t, "fill "+slot, resp, err)
	}
	resp, err = prod.DesignName(ctx, metaAs(maker, "company.dname"), handlers.ProductionRequest{No: no, Name: "Simorgh 5"})
	said(t, "name the design", resp, err)
	resp, err = prod.DesignFinal(ctx, metaAs(maker, "company.dfinal"), handlers.ProductionRequest{No: no})
	said(t, "finalise the design", resp, err, "production.design_state.final")
	build("d" + no)
	resp, err = prod.Sell(ctx, metaAs(maker, "company.sell"), handlers.ProductionRequest{Company: aero.code, Target: "d" + no,
		Qty: "1", Price: "10000"})
	said(t, "list the fighter", resp, err)
	var listingNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_listings WHERE company_id = $1::uuid AND status = 'open'
	  AND item_code = 'stealth_fighter_jet'`, aero.id).Scan(&listingNo); err != nil {
		t.Fatalf("the fighter was not listed: %v", err)
	}
	listing := itoa(listingNo)
	// No player and no company may buy arms.
	resp, err = prod.Buy(ctx, metaAs(stranger, "company.buy"), handlers.ProductionRequest{No: listing, Qty: "1", Method: "cash"})
	said(t, "a player buying the fighter", resp, err, "production.refused.not_cleared")
	resp, err = prod.Buy(ctx, metaAs(radarMaker, "company.buy"), handlers.ProductionRequest{No: listing, Qty: "1", Method: radar.code})
	said(t, "a company buying the fighter", resp, err, "production.refused.not_cleared")

	// --- 3. The first defence period: the levy and the appropriation, once.
	period := func(wantNo int64) (levy, appropriation, due, paid int64) {
		t.Helper()
		var action string
		var next time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT action_id::text, next_at FROM military_clocks WHERE country_id = $1::uuid`,
			home).Scan(&action, &next); err != nil {
			t.Fatal(err)
		}
		if next.After(clock.Now()) {
			clock.Advance(next.Sub(clock.Now()) + time.Second)
		}
		var wg sync.WaitGroup
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := forces.Settle(context.Background(), scheduler("military.settle"), handlers.CrimeScheduledRequest{
					ActionID: action, ReferenceID: home,
					Payload: []byte(`{"country_id":"` + home + `","period_no":` + itoa(wantNo) + `}`)}); err != nil {
					t.Errorf("settle: %v", err)
				}
			}()
		}
		wg.Wait()
		if n := countRows(t, pool, `SELECT count(*) FROM military_periods WHERE country_id = $1::uuid AND period_no = $2`,
			home, wantNo); n != 1 {
			t.Fatalf("defence period %d settled %d times, want once", wantNo, n)
		}
		if err := pool.Raw().QueryRow(ctx, `SELECT levy, appropriation, upkeep_due, upkeep_paid FROM military_periods
		  WHERE country_id = $1::uuid AND period_no = $2`, home, wantNo).Scan(&levy, &appropriation, &due, &paid); err != nil {
			t.Fatal(err)
		}
		return
	}
	fundBefore := balance(application.AccountDefenceFund, home)
	levy, appropriation, _, _ := period(1)
	if levy <= 0 || appropriation <= 0 {
		t.Fatalf("the first period levied %d and appropriated %d; the companies' founding fees were revenue", levy, appropriation)
	}
	if got := balance(application.AccountDefenceFund, home) - fundBefore; got != appropriation {
		t.Fatalf("the defence fund grew by %d, want the appropriation %d once", got, appropriation)
	}
	if appropriation < 10_000 {
		t.Fatalf("the defence fund holds %d, less than the fighter's price", appropriation)
	}

	// --- 4. Procurement: only the minister, paid once. ---------------------
	resp, err = forces.ArmsBuy(ctx, metaAs(stranger, "military.buy"), handlers.MilitaryRequest{Country: homeCountryCode,
		No: listing, Qty: "1", Confirm: "yes"})
	said(t, "a stranger buying arms", resp, err, "military.refused.not_holder")
	resp, err = forces.Procure(ctx, metaAs(minister, "military.procure"), handlers.MilitaryRequest{Country: homeCountryCode})
	said(t, "the procurement list", resp, err, "military.procure.offer")
	fund, seller := balance(application.AccountDefenceFund, home), balance(application.AccountCompanyTreasury, aero.id)
	buy := metaAs(minister, "military.buy")
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := forces.ArmsBuy(context.Background(), buy, handlers.MilitaryRequest{Country: homeCountryCode, No: listing,
				Qty: "1", Confirm: "yes"}); err != nil {
				t.Errorf("buy: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := fund - balance(application.AccountDefenceFund, home); got != 10_000 {
		t.Fatalf("the defence fund paid %d, want 10000 once", got)
	}
	if got := balance(application.AccountCompanyTreasury, aero.id) - seller; got != 10_000 {
		t.Fatalf("the seller received %d, want 10000 once, untaxed", got)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM procurements WHERE country_id = $1::uuid`, home); n != 1 {
		t.Fatalf("procurements = %d, want 1", n)
	}
	var pieceID string
	if err := pool.Raw().QueryRow(ctx, `SELECT a.piece_id::text FROM military_assets a JOIN item_pieces p ON p.id = a.piece_id
	  WHERE a.country_id = $1::uuid AND a.class_code = 'stealth_fighter' AND p.org_kind = 'state' AND p.org_id = $1::uuid`,
		home).Scan(&pieceID); err != nil {
		t.Fatalf("the state holds no stealth fighter: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.military.procured.v1'`); n != 1 {
		t.Errorf("procured events = %d, want 1 for the groups' line", n)
	}
	resp, err = forces.Forces(ctx, metaAs(minister, "military.forces"), handlers.MilitaryRequest{Country: homeCountryCode})
	said(t, "the forces in full", resp, err, "military.forces.class_count")
	public := metaAs(stranger, "military.forces")
	resp, err = forces.Forces(ctx, public, handlers.MilitaryRequest{Country: homeCountryCode})
	said(t, "the forces in public", resp, err, "military.forces.class_band")

	// --- 5. Stationing: the commander, landing once. ----------------------
	resp, err = forces.Station(ctx, metaAs(minister, "military.station"), handlers.MilitaryRequest{Country: homeCountryCode,
		Target: "d" + no, City: "ostmarch", Qty: "1", Confirm: "yes"})
	said(t, "the minister commanding", resp, err, "military.refused.not_holder")
	station := metaAs(commander, "military.station")
	for range 2 {
		_, err = forces.Station(ctx, station, handlers.MilitaryRequest{Country: homeCountryCode, Target: "d" + no,
			City: "ostmarch", Qty: "1", Confirm: "yes"})
		if err != nil {
			t.Fatalf("station: %v", err)
		}
	}
	var moveID, moveAction string
	var arrives time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, arrives_at FROM military_moves
	  WHERE country_id = $1::uuid`, home).Scan(&moveID, &moveAction, &arrives); err != nil {
		t.Fatalf("no move: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM military_moves WHERE country_id = $1::uuid`, home); n != 1 {
		t.Fatalf("moves = %d, want 1", n)
	}
	clock.Advance(arrives.Sub(clock.Now()) + time.Second)
	for range 2 {
		if _, err := forces.Arrive(ctx, scheduler("military.arrive"), handlers.CrimeScheduledRequest{ActionID: moveAction,
			ReferenceID: moveID}); err != nil {
			t.Fatalf("arrive: %v", err)
		}
	}
	var garrison, status string
	if err := pool.Raw().QueryRow(ctx, `SELECT c.code, a.status FROM military_assets a JOIN cities c ON c.id = a.garrison_city_id
	  WHERE a.piece_id = $1::uuid`, pieceID).Scan(&garrison, &status); err != nil || garrison != "ostmarch" || status != "stationed" {
		t.Fatalf("the fighter stands in %q (%s), %v; want ostmarch, stationed", garrison, status, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.military.arrived.v1'`); n != 1 {
		t.Errorf("arrived events = %d, want 1", n)
	}

	// --- 6. The next period charges the upkeep, once. ---------------------
	fund = balance(application.AccountDefenceFund, home)
	_, appropriation2, due, paid := period(2)
	upkeep := registry.Current().MilitaryClasses()["stealth_fighter"].Upkeep
	if due != upkeep || paid != upkeep {
		t.Fatalf("the second period owed %d and paid %d, want the stealth fighter's upkeep %d", due, paid, upkeep)
	}
	if got := balance(application.AccountDefenceFund, home) - fund; got != appropriation2-upkeep {
		t.Fatalf("the fund moved by %d, want %d (appropriation less upkeep, once)", got, appropriation2-upkeep)
	}
	verifyLedger(t, pool)
}

func TestSanctionsBlockTradePaymentAndTravelUntilLifted(t *testing.T) {
	w := newGoodsWorld(t)
	pool := w.pool
	home, far := requireMilitary(t, pool)
	ctx := testCtx(t)
	calderis, err := w.cities.ByCode(ctx, "calderis")
	if err != nil {
		t.Skipf("calderis is not loaded: %v", err)
	}
	aldrin, err := w.cities.ByCode(ctx, "aldrin_hollow")
	if err != nil {
		t.Skipf("aldrin_hollow is not loaded: %v", err)
	}
	// The test's clock starts in the past, so a sanction bound on it also
	// binds for the handlers that read the wall clock.
	clock := &testClock{now: time.Now().UTC().Add(-3 * time.Hour)}
	w.now = clock.Now()
	scale := gametime.Scale(gameScale)
	president := insertPlayer(t, pool)
	seller := w.shopper(t, "bazaar", 1_000) // lives in the Federation: see below
	buyer := w.shopper(t, "bazaar", 1_000)
	traveller := crimePlayer(t, pool, aldrin.ID, clock.Now())
	grant(t, pool, application.AccountPlayerBank, buyer.ID, 5_000)
	grant(t, pool, application.AccountPlayerCash, traveller.ID, 5_000)
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET residence_city_id = $2::uuid WHERE id = $1::uuid`, seller.ID,
		calderis.ID); err != nil {
		t.Fatal(err)
	}
	ids := []string{president.ID, seller.ID, buyer.ID, traveller.ID}
	purgeMilitary(t, pool, []string{home, far}, nil)
	t.Cleanup(func() { purgeMilitary(t, pool, []string{home, far}, ids) })

	uow := w.uow
	shops := handlers.NewShopsHandler(uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale, &crimeDice{}, time.Hour, clock.Now)
	market := handlers.NewMarketHandler(uow, workIDs{t}, nil, w.registry, w.cities, w.policy, scale,
		handlers.MarketLimits{OrderTTL: 72 * time.Hour, MaxOpen: 20, MaxQuantity: 1000, MaxPrice: 1_000_000}, 10, time.Hour, clock.Now)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	payments := handlers.NewBankHandler(uow, workIDs{t}, nil, w.cities, w.policy, postgres.NewPlayerSearchRepository(pool), limits,
		time.Hour, clock.Now)
	travel := handlers.NewTravelHandler(uow, workIDs{t}, nil, w.cities, snapshotNetwork{w.registry.Current()}, w.policy,
		gameScale, 25, time.Hour, clock.Now)
	diplomacy := handlers.NewDiplomacyHandler(uow, workIDs{t}, nil, w.registry, handlers.DiplomacyRules{
		SanctionNotice: time.Hour, SanctionMinDuration: 24 * time.Hour, TreatyOfferTTL: 72 * time.Hour,
		EndedShownFor: 168 * time.Hour, HistoryPageSize: 8}, time.Hour, clock.Now)
	metaAs := func(p *application.Player, command string) envelope.Metadata { return w.meta(t, p, command) }

	if _, err := shops.Buy(ctx, metaAs(seller, "shop.buy"),
		handlers.ShopRequest{Shop: "grocery", Item: "bread", Qty: "2", Method: "cash", Nonce: w.nonce(t)}); err != nil {
		t.Fatal(err)
	}

	// --- The president sanctions the Federation, once. -------------------
	seatAs(t, uow, "president", home, president, clock.Now())
	resp, err := diplomacy.Impose(ctx, metaAs(buyer, "diplomacy.impose"), handlers.DiplomacyRequest{Target: farCountryCode,
		Mask: "25", Ground: "aggression", Confirm: "yes"})
	said(t, "a citizen imposing", resp, err, "diplomacy.refused.not_holder")
	press := metaAs(president, "diplomacy.impose")
	for range 2 {
		// trade (1) + travel (8) + financial (16)
		resp, err = diplomacy.Impose(ctx, press, handlers.DiplomacyRequest{Target: farCountryCode, Mask: "25", Ground: "aggression",
			Confirm: "yes"})
		said(t, "impose", resp, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM sanctions WHERE imposer_id = $1::uuid AND lifted_at IS NULL`, home); n != 1 {
		t.Fatalf("standing sanctions = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.diplomacy.sanction_imposed.v1'`); n != 1 {
		t.Errorf("sanction_imposed events = %d, want 1 for both countries' groups", n)
	}
	var sanctionNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM sanctions WHERE imposer_id = $1::uuid AND lifted_at IS NULL`, home).Scan(&sanctionNo); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour + time.Second)

	// --- Refused across the border. --------------------------------------
	resp, err = market.Order(ctx, metaAs(seller, "market.order"), handlers.MarketRequest{Side: "sell", Item: "bread", Qty: "1",
		Price: "40", Nonce: w.nonce(t)})
	said(t, "the Federation's seller lists", resp, err)
	resp, err = market.Order(ctx, metaAs(buyer, "market.order"), handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "1",
		Price: "40", Nonce: w.nonce(t), Method: "cash"})
	said(t, "the buyer across an embargo", resp, err, "market.embargoed")
	if n := countRows(t, pool, `SELECT count(*) FROM market_trades WHERE seller_id = $1::uuid`, seller.ID); n != 0 {
		t.Fatalf("trades across the embargo = %d, want none", n)
	}
	resp, err = payments.PaySend(ctx, metaAs(buyer, "bank.pay.send"), handlers.PayRequest{To: seller.PublicCode, Amount: "100",
		Method: "card", Nonce: "s1"})
	said(t, "a card payment across", resp, err, "diplomacy.blocked.financial")
	resp, err = travel.Options(ctx, metaAs(traveller, "travel.options"), handlers.TravelOptionsRequest{City: "calderis"})
	said(t, "a journey across", resp, err, "diplomacy.blocked.travel")
	// Lifting before its least duration is refused.
	resp, err = diplomacy.Lift(ctx, metaAs(president, "diplomacy.lift"), handlers.DiplomacyRequest{No: itoa(sanctionNo), Confirm: "yes"})
	said(t, "lift too soon", resp, err, "diplomacy.refused.too_soon")

	// --- Lifted: everything goes through. --------------------------------
	clock.Advance(24 * time.Hour)
	lift := metaAs(president, "diplomacy.lift")
	for range 2 {
		resp, err = diplomacy.Lift(ctx, lift, handlers.DiplomacyRequest{No: itoa(sanctionNo), Confirm: "yes"})
		said(t, "lift", resp, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM diplomacy_events WHERE kind = 'sanction_lifted' AND country_id = $1::uuid`, home); n != 1 {
		t.Fatalf("lift events = %d, want 1", n)
	}
	resp, err = market.Order(ctx, metaAs(buyer, "market.order"), handlers.MarketRequest{Side: "buy", Item: "bread", Qty: "1",
		Price: "40", Nonce: w.nonce(t), Method: "cash"})
	said(t, "the buyer after the lifting", resp, err)
	if n := countRows(t, pool, `SELECT count(*) FROM market_trades WHERE seller_id = $1::uuid`, seller.ID); n != 1 {
		t.Fatalf("trades after the lifting = %d, want 1", n)
	}
	sellerBank := cashBalance(t, pool, application.AccountPlayerBank, seller.ID)
	resp, err = payments.PaySend(ctx, metaAs(buyer, "bank.pay.send"), handlers.PayRequest{To: seller.PublicCode, Amount: "100",
		Method: "card", Nonce: "s2"})
	said(t, "a card payment after", resp, err)
	if got := cashBalance(t, pool, application.AccountPlayerBank, seller.ID) - sellerBank; got != 100 {
		t.Fatalf("the payee received %d by card, want 100", got)
	}
	resp, err = travel.Options(ctx, metaAs(traveller, "travel.options"), handlers.TravelOptionsRequest{City: "calderis"})
	said(t, "a journey after", resp, err)
	if strings.Contains(resp.Text, "diplomacy.blocked") {
		t.Fatalf("the route is still closed: %s", resp.Text)
	}
	verifyLedger(t, pool)
}

func TestTreatyProposedAndAcceptedOnce(t *testing.T) {
	pool := requirePostgres(t)
	home, far := requireMilitary(t, pool)
	registry := companyRegistry(t, pool)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	ostmarch, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skip(err)
	}
	calderis, err := cities.ByCode(ctx, "calderis")
	if err != nil {
		t.Skip(err)
	}
	minister, president := insertPlayer(t, pool), insertPlayer(t, pool)
	for p, city := range map[*application.Player]string{minister: ostmarch.ID, president: calderis.ID} {
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid WHERE id = $1::uuid`,
			p.ID, city); err != nil {
			t.Fatal(err)
		}
	}
	purgeMilitary(t, pool, []string{home, far}, nil)
	t.Cleanup(func() { purgeMilitary(t, pool, []string{home, far}, []string{minister.ID, president.ID}) })
	clock := &testClock{now: time.Now().UTC()}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	diplomacy := handlers.NewDiplomacyHandler(uow, workIDs{t}, nil, registry, handlers.DiplomacyRules{
		SanctionNotice: time.Hour, SanctionMinDuration: 24 * time.Hour, TreatyOfferTTL: 72 * time.Hour,
		EndedShownFor: 168 * time.Hour, HistoryPageSize: 8}, time.Hour, clock.Now)
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	// The Commonwealth's foreign minister; the Federation's president acts
	// for its vacant foreign ministry.
	seatAs(t, uow, "foreign_minister", home, minister, clock.Now())
	seatAs(t, uow, "president", far, president, clock.Now())

	press := metaAs(minister, "diplomacy.propose")
	for range 2 {
		resp, err := diplomacy.Propose(ctx, press, handlers.DiplomacyRequest{Target: farCountryCode, Kind: "alliance", Confirm: "yes"})
		said(t, "propose", resp, err)
	}
	var no int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM treaties WHERE proposer_id = $1::uuid AND partner_id = $2::uuid`,
		home, far).Scan(&no); err != nil {
		t.Fatalf("no proposal: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM treaties WHERE proposer_id = $1::uuid`, home); n != 1 {
		t.Fatalf("proposals = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.diplomacy.treaty_proposed.v1'
	  AND payload->>'player_id' = $1`, president.ID); n != 1 {
		t.Errorf("proposal notices to the Federation's acting minister = %d, want 1", n)
	}
	// The proposer may not answer its own proposal.
	resp, err := diplomacy.Answer(ctx, metaAs(minister, "diplomacy.answer"), handlers.DiplomacyRequest{No: itoa(no), Verdict: "accept"})
	said(t, "the proposer accepting", resp, err, "diplomacy.refused.not_holder")
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := diplomacy.Answer(context.Background(), metaAs(president, "diplomacy.answer"),
				handlers.DiplomacyRequest{No: itoa(no), Verdict: "accept"}); err != nil {
				t.Errorf("accept: %v", err)
			}
		}()
	}
	wg.Wait()
	var status string
	if err := pool.Raw().QueryRow(ctx, `SELECT status FROM treaties WHERE no = $1`, no).Scan(&status); err != nil || status != "active" {
		t.Fatalf("the treaty is %q (%v), want active", status, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM diplomacy_events WHERE kind = 'treaty_signed'`); n != 1 {
		t.Fatalf("signed events = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.diplomacy.treaty_signed.v1'`); n != 1 {
		t.Errorf("treaty_signed announcements = %d, want 1", n)
	}
	resp, err = diplomacy.Answer(ctx, metaAs(president, "diplomacy.answer"), handlers.DiplomacyRequest{No: itoa(no), Verdict: "decline"})
	said(t, "declining after accepting", resp, err, "diplomacy.refused.state")
}
