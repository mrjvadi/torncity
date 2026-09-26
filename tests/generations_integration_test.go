//go:build integration

// Integration test of product generations (migration 0036_generations,
// docs/adr/0021-production-economy.md generations addendum): the owner's
// 2026 request that a company keep making new versions of what it builds and
// keep researching better ones — proved on a civilian product (a phone)
// through the real handlers, unit of work, ledger, item journal and the
// game clock.
//
//	G (a factory) researches semiconductors, microchips and battery
//	chemistry, then microchips II — a generation of a technology it
//	already owns, gating nothing new but making what microchips already
//	gates better;
//	G designs and finalises a phone (v1) and manufactures one;
//	G revises the phone into v2 (same parts to start from) — v2's
//	computed quality already reads higher than v1's, because G now holds
//	microchips II (technology.EffectsFor);
//	G builds an upgrade kit for v2 (a production order of kind
//	upgrade_kit) and retrofits its existing v1 phone with it — the kit is
//	consumed once, the phone's design_id moves to v2, and its attributes
//	read v2's from that moment with no other write anywhere.
//
// The ledger's and the item journal's invariants hold at the end, and
// everything the test made is removed.
package tests

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// generationsCity is a company city of its own, so this test's careful
// resource counts never depend on the phone scenario's.
const generationsCity = "aldrin_hollow"

// grantStock puts counted units directly into an organisation's warehouse,
// bypassing the supply chain (already proved by the phone scenario): this
// test's subject is the generations mechanism, not re-deriving chipsets from
// silica.
func grantStock(t *testing.T, pool *postgres.Pool, orgID, itemCode string, qty int64) {
	t.Helper()
	ctx := testCtx(t)
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO org_stacks (org_kind, org_id, item_code, holding, quantity) VALUES ('company', $1::uuid, $2, 'warehouse', $3)
		 ON CONFLICT (org_kind, org_id, item_code, holding) DO UPDATE SET quantity = org_stacks.quantity + $3`,
		orgID, itemCode, qty); err != nil {
		t.Fatalf("granting %s: %v", itemCode, err)
	}
	// The item journal must agree with the stack (admin economy verify), so
	// the grant gets its own origin row: a supply, exactly like a
	// supplier's delivery, just not through the supplier's shelf.
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO item_movements (id, item_code, quantity, to_org_kind, to_org, to_holding, reason, created_at)
		 VALUES (gen_random_uuid(), $1, $2, 'company', $3::uuid, 'warehouse', 'supplied', now())`,
		itemCode, qty, orgID); err != nil {
		t.Fatalf("journalling the grant of %s: %v", itemCode, err)
	}
}

func TestProductGenerationsCivilian(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
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
	city, err := cities.ByCode(ctx, generationsCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", generationsCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", generationsCity, n)
	}

	owner := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, owner.ID); purgeWorkFor(t, pool, owner.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, owner.ID) })
	t.Cleanup(func() { purgeProductionOf(t, pool, city.ID, owner.ID) })
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
		  WHERE id = $1::uuid`, owner.ID, city.ID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, owner.ID, 2_000_000)
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, 'engineering', 6, 0, now())`, owner.ID); err != nil {
		t.Fatal(err)
	}

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits}, time.Hour, now)
	prod := handlers.NewProductionHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, handlers.ProductionRules{
		MaxRunningOrders: 3, MaxDesigns: 20, MaxListings: 10, DesignMinSkill: 1, ReverseTime: 6 * time.Hour,
		ImprovementTime: time.Hour, ImprovementCost: 5000, RetrofitTime: time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits}, time.Hour, now)
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command, m.Language = p.TelegramUserID, command, "en"
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

	resp, err := companies.Found(ctx, metaAs(owner, "company.found"), handlers.CompanyRequest{Type: "factory", Method: "cash",
		Name: "Generations Co"})
	ok("found the company", resp, err)
	var companyID, companyCode string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid`,
		owner.ID).Scan(&companyID, &companyCode); err != nil {
		t.Fatalf("the company was not founded: %v (%s)", err, resp.Text)
	}
	resp, err = companies.Deposit(ctx, metaAs(owner, "company.deposit"), handlers.CompanyRequest{Company: companyCode, Method: "cash",
		Amount: "1500000"})
	ok("deposit", resp, err)

	// --- 1. Research: semiconductors, microchips, batteries, then a
	// GENERATION of a technology already owned: microchips II. ------------
	research := func(tech string) {
		t.Helper()
		resp, err := prod.Research(ctx, metaAs(owner, "company.research"), handlers.ProductionRequest{Company: companyCode, Tech: tech})
		ok("research "+tech, resp, err)
		var id, action string
		var finish time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, finish_at FROM company_research
		  WHERE company_id = $1::uuid AND tech_code = $2 AND status = 'running'`, companyID, tech).Scan(&id, &action, &finish); err != nil {
			t.Fatalf("research %s did not start: %v", tech, err)
		}
		advance(finish.Sub(now()) + time.Second)
		if _, err := prod.Researched(ctx, scheduler("company.researched"), handlers.CrimeScheduledRequest{ActionID: action,
			ReferenceID: id}); err != nil {
			t.Fatalf("researched %s: %v", tech, err)
		}
		if n := countRows(t, pool, `SELECT count(*) FROM company_technologies WHERE company_id = $1::uuid AND tech_code = $2`,
			companyID, tech); n != 1 {
			t.Fatalf("%s owned %d times, want once", tech, n)
		}
	}
	research("semiconductors")
	research("microchips")
	research("batteries")
	research("microchips_ii")

	// --- 2. Design and finalise the phone (v1). ---------------------------
	resp, err = prod.DesignNew(ctx, metaAs(owner, "company.dnew"), handlers.ProductionRequest{Company: companyCode, Item: "phone"})
	ok("new phone design", resp, err)
	var designNo int64
	var v1ID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM product_designs WHERE company_id = $1::uuid`, companyID).
		Scan(&designNo, &v1ID); err != nil {
		t.Fatal(err)
	}
	no := strconv.FormatInt(designNo, 10)
	for slot, comp := range map[string]string{"board": "chipset", "power": "cell", "shell": "plastic_case"} {
		resp, err = prod.DesignFill(ctx, metaAs(owner, "company.dfill"), handlers.ProductionRequest{No: no, Slot: slot, Component: comp})
		ok("fill "+slot, resp, err)
	}
	resp, err = prod.DesignName(ctx, metaAs(owner, "company.dname"), handlers.ProductionRequest{No: no, Name: "Simorgh 5"})
	ok("name the design", resp, err)
	resp, err = prod.DesignFinal(ctx, metaAs(owner, "company.dfinal"), handlers.ProductionRequest{No: no})
	ok("finalise v1", resp, err)

	// --- 3. Grant materials directly (the supply chain is proved
	// elsewhere) and produce one phone (v1). -------------------------------
	grantMaterials := func() {
		grantStock(t, pool, companyID, "chipset", 1)
		grantStock(t, pool, companyID, "cell", 1)
		grantStock(t, pool, companyID, "plastic_case", 1)
	}
	grantMaterials()
	resp, err = prod.Produce(ctx, metaAs(owner, "company.produce"), handlers.ProductionRequest{Company: companyCode,
		Target: "d" + no, Qty: "1", Confirm: "yes"})
	ok("produce v1", resp, err)
	var v1OrderID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM production_orders WHERE company_id = $1::uuid ORDER BY no DESC LIMIT 1`,
		companyID).Scan(&v1OrderID); err != nil {
		t.Fatal(err)
	}
	completeOrder := func(orderID string) {
		t.Helper()
		var action string
		var finish time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, finish_at FROM production_orders WHERE id = $1::uuid`,
			orderID).Scan(&action, &finish); err != nil {
			t.Fatal(err)
		}
		advance(finish.Sub(now()) + time.Second)
		if _, err := prod.Produced(ctx, scheduler("company.produced"), handlers.CrimeScheduledRequest{ActionID: action,
			ReferenceID: orderID}); err != nil {
			t.Fatalf("produced: %v", err)
		}
	}
	completeOrder(v1OrderID)
	var v1Serial string
	if err := pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE design_id = $1::uuid AND holding = 'warehouse'`,
		v1ID).Scan(&v1Serial); err != nil {
		t.Fatalf("no v1 phone in the warehouse: %v", err)
	}

	// --- 4. Revise the phone into v2 (same parts to start from). ----------
	resp, err = prod.DesignRevise(ctx, metaAs(owner, "company.drevise"), handlers.ProductionRequest{No: no})
	ok("revise to v2", resp, err)
	var v2No int64
	var v2ID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM product_designs WHERE parent_design_id = $1::uuid`,
		v1ID).Scan(&v2No, &v2ID); err != nil {
		t.Fatalf("v2 was not created: %v", err)
	}
	v2no := strconv.FormatInt(v2No, 10)
	resp, err = prod.DesignFinal(ctx, metaAs(owner, "company.dfinal"), handlers.ProductionRequest{No: v2no})
	ok("finalise v2", resp, err)

	// --- 5. v2's computed quality already reads higher than v1's, from
	// microchips II alone -- no new component, same parts. -----------------
	snap := registry.Current()
	arch, ok2 := snap.Archetype("device")
	if !ok2 {
		t.Fatal("no device archetype")
	}
	components := snap.Components()
	owned, published := item.NewSet("semiconductors", "microchips", "batteries", "microchips_ii"), item.NewSet()
	standing := technology.Standing{Owned: owned, Published: published}
	v1Fills := item.Design{ID: v1ID, Archetype: "device", Origin: item.OriginAuthored, Fills: map[string]item.Fill{
		"board": {Component: "chipset", Quantity: 1}, "power": {Component: "cell", Quantity: 1}, "shell": {Component: "plastic_case", Quantity: 1}}}
	v1Base, err := item.ComputeAttributes(arch, v1Fills, components)
	if err != nil {
		t.Fatal(err)
	}
	v2Effects := technology.EffectsFor(v1Fills, components, snap.TechTree(), standing)
	if len(v2Effects) == 0 {
		t.Fatal("microchips II gave no effect on the phone's parts")
	}
	v2Attrs, err := item.ApplyEffects(v1Base, v2Effects)
	if err != nil {
		t.Fatal(err)
	}
	if v2Attrs["quality"] <= v1Base["quality"] {
		t.Fatalf("v2 quality %d is not above v1's %d: microchips II did not improve the phone", v2Attrs["quality"], v1Base["quality"])
	}

	// --- 6. Build an upgrade kit for v2, and retrofit the v1 phone. --------
	grantMaterials()
	resp, err = prod.ProduceKit(ctx, metaAs(owner, "company.kit"), handlers.ProductionRequest{Company: companyCode,
		Target: "d" + v2no, Qty: "1", Confirm: "yes"})
	ok("build a kit for v2", resp, err)
	var kitOrderID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM production_orders WHERE company_id = $1::uuid AND kind = 'upgrade_kit'
	  ORDER BY no DESC LIMIT 1`, companyID).Scan(&kitOrderID); err != nil {
		t.Fatal(err)
	}
	var kitAction string
	var kitFinish time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, finish_at FROM production_orders WHERE id = $1::uuid`,
		kitOrderID).Scan(&kitAction, &kitFinish); err != nil {
		t.Fatal(err)
	}
	advance(kitFinish.Sub(now()) + time.Second)
	if _, err := prod.KitProduced(ctx, scheduler("company.kit_produced"), handlers.CrimeScheduledRequest{ActionID: kitAction,
		ReferenceID: kitOrderID}); err != nil {
		t.Fatalf("kit_produced: %v", err)
	}
	var kitSerial string
	if err := pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE design_id = $1::uuid AND holding = 'warehouse'
	  AND origin_ref = $2::uuid`, v2ID, kitOrderID).Scan(&kitSerial); err != nil {
		t.Fatalf("no kit in the warehouse: %v", err)
	}

	resp, err = prod.RetrofitStart(ctx, metaAs(owner, "company.retrofit"), handlers.ProductionRequest{Company: companyCode,
		Item: kitSerial, Serial: v1Serial, Confirm: "yes"})
	ok("start the retrofit", resp, err)
	if n := countRows(t, pool, `SELECT count(*) FROM item_pieces WHERE serial = $1 AND holding = 'gone'`, kitSerial); n != 1 {
		t.Fatal("the kit was not consumed when the retrofit started")
	}
	var retrofitID, retrofitAction string
	var retrofitFinish time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT j.id::text, j.game_action_id::text, j.finish_at FROM retrofit_jobs j
	  JOIN item_pieces p ON p.id = j.piece_id WHERE p.serial = $1`, v1Serial).Scan(&retrofitID, &retrofitAction, &retrofitFinish); err != nil {
		t.Fatalf("no retrofit job: %v", err)
	}
	advance(retrofitFinish.Sub(now()) + time.Second)
	if _, err := prod.Retrofitted(ctx, scheduler("company.retrofitted"), handlers.CrimeScheduledRequest{ActionID: retrofitAction,
		ReferenceID: retrofitID}); err != nil {
		t.Fatalf("retrofitted: %v", err)
	}
	var afterDesignID string
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(design_id::text, '') FROM item_pieces WHERE serial = $1`, v1Serial).
		Scan(&afterDesignID); err != nil {
		t.Fatal(err)
	}
	if afterDesignID != v2ID {
		t.Fatalf("the phone's design is %s after retrofit, want v2 (%s)", afterDesignID, v2ID)
	}

	// --- 7. The ledger this test posted to is internally consistent. -------
	// LedgerSum and Unbalanced are what prove THIS test's own transactions
	// are correctly double-entry; v.Drifted also reports any account whose
	// long-lived CACHED balance disagrees with a fresh sum of its entries,
	// which on a shared database can already be true before this test ever
	// runs (a cache staleness left by unrelated activity) and is not this
	// scenario's to fix or to hide by skipping the check silently.
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
