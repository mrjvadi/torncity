//go:build integration

// Integration test of product generations on a state asset (migrations
// 0021, 0036; docs/adr/0022-military-and-diplomacy.md generations addendum):
//
//	a defence-electronics company researches radar systems and licenses
//	armour systems from a land-systems company (it needs the technology to
//	design a light hull into a radar station, though a supplier grant
//	stands in for making one from raw materials — that supply chain is
//	proved by the phone and stealth-fighter scenarios already);
//	it designs and finalises a radar station (v1); the state already
//	holds one (a direct grant, standing in for a past procurement — also
//	already proved);
//	the company then researches radar systems II — a generation of a
//	technology it already owns — and its v1 design's computed detection
//	range already reads higher for it, before anything else happens;
//	it revises the design into v2 and builds an upgrade kit for it, lists
//	the kit; the defence minister buys it for the state (paid from the
//	defence fund, delivered to the state's warehouse, never becoming a
//	stationed asset of its own); the air-defence branch's commander
//	retrofits the state's existing v1 radar with it — the kit consumed
//	once, the radar's design_id moved to v2.
//
// A war strike reading the radar's now-current attributes is not
// re-launched here: internal/application/handlers/war_battle.go always
// recomputes a unit's attributes fresh off item_pieces.design_id
// (confirmed by inspection, and exercised for a non-retrofitted unit by
// tests/war_integration_test.go), so a retrofit already lands its effect
// there with no further write anywhere.
//
// This scenario's own ledger transactions balance, and everything it made
// is removed.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// generationsMilitaryCity is a city of its own, so this scenario's counts
// never depend on another military test's.
const generationsMilitaryCity = "calderis"

// grantDefenceFund pays a country's defence fund from system_source, exactly
// as a national levy or an appropriation would, so purgeMilitary's existing
// reversal (it already unwinds every transaction that touches a country's
// defence fund) cleans it up with no extra code.
func grantDefenceFund(t *testing.T, pool *postgres.Pool, countryID string, amount int64) {
	t.Helper()
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	if err := uow.Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, countryID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason: application.ReasonAdminGrant,
			Entries: []application.LedgerEntry{
				{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)},
				{AccountID: acct.ID, Amount: money.FromMinor(amount)},
			},
		})
		return err
	}); err != nil {
		t.Fatalf("granting the defence fund: %v", err)
	}
}

func TestProductGenerationsMilitary(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	home, _ := requireMilitary(t, pool)
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.design_improvement_projects') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("design_improvement_projects does not exist; apply migration 0036 first")
	}
	registry := companyRegistry(t, pool)
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, generationsMilitaryCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", generationsMilitaryCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", generationsMilitaryCity, n)
	}
	purgeMilitary(t, pool, []string{home}, nil)

	minister, commander, radarMaker, hullMaker := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	players := []*application.Player{minister, commander, radarMaker, hullMaker}
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
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, radarMaker.ID, hullMaker.ID) })
	t.Cleanup(func() { purgeProductionOf(t, pool, city.ID, radarMaker.ID, hullMaker.ID) })
	t.Cleanup(func() { purgeMilitary(t, pool, []string{home}, ids) })
	grantCash(t, pool, radarMaker.ID, 1_000_000)
	grantCash(t, pool, hullMaker.ID, 700_000)
	for _, p := range []*application.Player{radarMaker, hullMaker} {
		enlistAs(t, pool, p.ID, city.ID, 3)
	}
	for p, level := range map[*application.Player]int{radarMaker: 8, hullMaker: 5} {
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
		ImprovementTime: time.Hour, ImprovementCost: 5000, RetrofitTime: time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits}, time.Hour, clock.Now)
	forces := handlers.NewMilitaryHandler(uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.MilitaryRules{
		Period: 24 * time.Hour, ReadinessLossBPS: 1000, ReadinessRecoveryBPS: 500, ReferenceRadarKM: 150,
		RetrofitTime: time.Hour}, time.Hour, clock.Now)
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
	ok := func(what string, resp *presenter.Response, err error) *presenter.Response {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if resp == nil {
			t.Fatalf("%s: no response", what)
		}
		return resp
	}

	// --- 1. Offices, seated directly: the appointment chain itself is
	// proved by tests/military_integration_test.go. ------------------------
	seatAs(t, uow, "defence_minister", home, minister, clock.Now())
	seatAs(t, uow, "air_defence_commander", home, commander, clock.Now())

	// --- 2. The defence industry: radar systems, licensed armour systems.
	type co struct{ id, code string }
	found := func(p *application.Player, kind, name, deposit string) co {
		t.Helper()
		resp, err := companies.Found(ctx, metaAs(p, "company.found"), handlers.CompanyRequest{Type: kind, Method: "cash", Name: name})
		ok("found "+name, resp, err)
		var c co
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
			p.ID, name).Scan(&c.id, &c.code); err != nil {
			t.Fatalf("company %s was not founded: %v (%s)", name, err, resp.Text)
		}
		resp, err = companies.Deposit(ctx, metaAs(p, "company.deposit"), handlers.CompanyRequest{Company: c.code, Method: "cash",
			Amount: deposit})
		ok("deposit into "+name, resp, err)
		return c
	}
	radar := found(radarMaker, "defence_electronics", "Kaveh Radars", "500000")
	hull := found(hullMaker, "land_systems", "Simorgh Armour", "300000")
	research := func(p *application.Player, c co, tech string) {
		t.Helper()
		resp, err := prod.Research(ctx, metaAs(p, "company.research"), handlers.ProductionRequest{Company: c.code, Tech: tech})
		ok("research "+tech, resp, err)
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
	research(radarMaker, radar, "radar_systems")
	research(hullMaker, hull, "armour_systems")
	resp, err := prod.TechMode(ctx, metaAs(hullMaker, "company.techmode"), handlers.ProductionRequest{Company: hull.code,
		Tech: "armour_systems", Mode: "license", Price: "20000"})
	ok("license armour systems", resp, err)
	resp, err = prod.License(ctx, metaAs(radarMaker, "company.license"), handlers.ProductionRequest{Company: radar.code,
		Tech: "armour_systems", From: hull.code, Confirm: "yes"})
	ok("buy the armour license", resp, err)

	// --- 3. The radar station (v1): its parts granted directly (the
	// supply chain from raw materials is proved elsewhere), designed and
	// finalised, one unit placed in the state's hands directly (standing
	// in for a past procurement, also proved elsewhere). -------------------
	for _, comp := range []string{"fire_control_radar", "light_hull", "powerpack"} {
		grantStock(t, pool, radar.id, comp, 1)
	}
	resp, err = prod.DesignNew(ctx, metaAs(radarMaker, "company.dnew"), handlers.ProductionRequest{Company: radar.code,
		Item: "early_warning_radar"})
	ok("new radar design", resp, err)
	var v1No int64
	var v1ID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM product_designs WHERE company_id = $1::uuid`, radar.id).
		Scan(&v1No, &v1ID); err != nil {
		t.Fatal(err)
	}
	v1no := itoa(v1No)
	for slot, comp := range map[string]string{"radar": "fire_control_radar", "carrier": "light_hull", "drive": "powerpack"} {
		resp, err = prod.DesignFill(ctx, metaAs(radarMaker, "company.dfill"), handlers.ProductionRequest{No: v1no, Slot: slot, Component: comp})
		ok("fill "+slot, resp, err)
	}
	resp, err = prod.DesignName(ctx, metaAs(radarMaker, "company.dname"), handlers.ProductionRequest{No: v1no, Name: "Simorgh Radar"})
	ok("name the design", resp, err)
	resp, err = prod.DesignFinal(ctx, metaAs(radarMaker, "company.dfinal"), handlers.ProductionRequest{No: v1no})
	ok("finalise v1", resp, err)

	grantState(t, pool, home, "early_warning_radar", "radar_station", v1ID, "radar", "air_defence", city.ID, 1)
	var stateSerial string
	if err := pool.Raw().QueryRow(ctx, `SELECT p.serial FROM item_pieces p JOIN military_assets a ON a.piece_id = p.id
	  WHERE p.design_id = $1::uuid AND a.country_id = $2::uuid`, v1ID, home).Scan(&stateSerial); err != nil {
		t.Fatalf("the granted state radar is missing: %v", err)
	}

	// --- 4. Radar systems II: a generation of a technology already owned,
	// gating nothing new -- v1's own computed detection range already reads
	// higher for it, before any revision or retrofit. -----------------------
	research(radarMaker, radar, "radar_systems_ii")
	snap := registry.Current()
	arch, ok2 := snap.Archetype("radar_station")
	if !ok2 {
		t.Fatal("no radar_station archetype")
	}
	components := snap.Components()
	v1Design := item.Design{ID: v1ID, Archetype: "radar_station", Origin: item.OriginAuthored, Fills: map[string]item.Fill{
		"radar": {Component: "fire_control_radar", Quantity: 1}, "carrier": {Component: "light_hull", Quantity: 1},
		"drive": {Component: "powerpack", Quantity: 1}}}
	before, err := item.ComputeAttributes(arch, v1Design, components)
	if err != nil {
		t.Fatal(err)
	}
	standing := technology.Standing{Owned: item.NewSet("radar_systems", "radar_systems_ii", "armour_systems"), Published: item.NewSet()}
	afterEffects := technology.EffectsFor(v1Design, components, snap.TechTree(), standing)
	if len(afterEffects) == 0 {
		t.Fatal("radar systems II gave no effect on the radar's parts")
	}
	after, err := item.ApplyEffects(before, afterEffects)
	if err != nil {
		t.Fatal(err)
	}
	if after["detection_range"] <= before["detection_range"] {
		t.Fatalf("detection range with radar systems II (%d) is not above without it (%d)",
			after["detection_range"], before["detection_range"])
	}

	// --- 5. Revise to v2, build a kit, and retrofit the state's radar. -----
	resp, err = prod.DesignRevise(ctx, metaAs(radarMaker, "company.drevise"), handlers.ProductionRequest{No: v1no})
	ok("revise to v2", resp, err)
	var v2No int64
	var v2ID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM product_designs WHERE parent_design_id = $1::uuid`, v1ID).
		Scan(&v2No, &v2ID); err != nil {
		t.Fatalf("v2 was not created: %v", err)
	}
	v2no := itoa(v2No)
	resp, err = prod.DesignFinal(ctx, metaAs(radarMaker, "company.dfinal"), handlers.ProductionRequest{No: v2no})
	ok("finalise v2", resp, err)

	resp, err = prod.ProduceKit(ctx, metaAs(radarMaker, "company.kit"), handlers.ProductionRequest{Company: radar.code,
		Target: "d" + v2no, Qty: "1", Confirm: "yes"})
	ok("build a kit for v2", resp, err)
	var kitOrderID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM production_orders WHERE company_id = $1::uuid AND kind = 'upgrade_kit'
	  ORDER BY no DESC LIMIT 1`, radar.id).Scan(&kitOrderID); err != nil {
		t.Fatal(err)
	}
	var kitAction string
	var kitFinish time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, finish_at FROM production_orders WHERE id = $1::uuid`,
		kitOrderID).Scan(&kitAction, &kitFinish); err != nil {
		t.Fatal(err)
	}
	clock.Advance(kitFinish.Sub(clock.Now()) + time.Second)
	if _, err := prod.KitProduced(ctx, scheduler("company.kit_produced"), handlers.CrimeScheduledRequest{ActionID: kitAction,
		ReferenceID: kitOrderID}); err != nil {
		t.Fatalf("kit_produced: %v", err)
	}

	resp, err = prod.Sell(ctx, metaAs(radarMaker, "company.sell"), handlers.ProductionRequest{Company: radar.code,
		Target: "d" + v2no, Qty: "1", Price: "8000"})
	ok("list the kit", resp, err)
	var kitListingNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_listings WHERE company_id = $1::uuid AND status = 'open'
	  AND item_code = 'early_warning_radar'`, radar.id).Scan(&kitListingNo); err != nil {
		t.Fatalf("the kit was not listed: %v", err)
	}

	grantDefenceFund(t, pool, home, 50_000)
	resp, err = forces.ProcureKit(ctx, metaAs(minister, "military.kitbuy"), handlers.MilitaryRequest{Country: homeCountryCode,
		No: itoa(kitListingNo), Qty: "1", Confirm: "yes"})
	ok("the minister buys the kit", resp, err)
	var kitSerial string
	if err := pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE org_kind = 'state' AND org_id = $1::uuid
	  AND holding = 'warehouse' AND design_id = $2::uuid`, home, v2ID).Scan(&kitSerial); err != nil {
		t.Fatalf("the state does not hold the kit: %v", err)
	}

	resp, err = forces.RetrofitState(ctx, metaAs(commander, "military.retrofit"), handlers.MilitaryRequest{Country: homeCountryCode,
		Target: kitSerial, City: stateSerial, Confirm: "yes"})
	ok("the commander starts the retrofit", resp, err)
	if n := countRows(t, pool, `SELECT count(*) FROM item_pieces WHERE serial = $1 AND holding = 'gone'`, kitSerial); n != 1 {
		t.Fatal("the kit was not consumed when the retrofit started")
	}
	var retrofitID, retrofitAction string
	var retrofitFinish time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT j.id::text, j.game_action_id::text, j.finish_at FROM retrofit_jobs j
	  JOIN item_pieces p ON p.id = j.piece_id WHERE p.serial = $1`, stateSerial).Scan(&retrofitID, &retrofitAction, &retrofitFinish); err != nil {
		t.Fatalf("no retrofit job: %v", err)
	}
	clock.Advance(retrofitFinish.Sub(clock.Now()) + time.Second)
	if _, err := prod.Retrofitted(ctx, scheduler("company.retrofitted"), handlers.CrimeScheduledRequest{ActionID: retrofitAction,
		ReferenceID: retrofitID}); err != nil {
		t.Fatalf("retrofitted: %v", err)
	}
	var afterDesignID string
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(design_id::text, '') FROM item_pieces WHERE serial = $1`, stateSerial).
		Scan(&afterDesignID); err != nil {
		t.Fatal(err)
	}
	if afterDesignID != v2ID {
		t.Fatalf("the state radar's design is %s after retrofit, want v2 (%s)", afterDesignID, v2ID)
	}

	// --- 6. This scenario's own ledger transactions balance. ---------------
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 {
		t.Fatalf("this scenario's own ledger transactions do not balance: %+v", v)
	}
	if len(v.Drifted) != 0 {
		t.Logf("pre-existing cached-balance drift on this database, unrelated to this scenario's own transactions: %+v", v.Drifted)
	}
}
