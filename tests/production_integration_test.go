//go:build integration

// Integration test of the production economy (migration 0020,
// docs/adr/0021-production-economy.md): the owner's phone scenario through the
// real handlers, unit of work, ledger, item journal and policy resolver, on
// the game clock the game ships with.
//
//	A (a factory) researches semiconductors, microchips and battery
//	chemistry — each once, a double press starting one research and paying
//	once, a replayed completion adding nothing — sells licenses for
//	microchips and publishes battery chemistry;
//	B (a factory) cannot design a phone around a chipset until it buys the
//	license — paid once, company to company — and then designs, names and
//	finalises one;
//	M (a mine, B's owner's second company) buys fuel from the NPC supplier,
//	extracts silica, iron ore and crude oil, and lists them; B buys them;
//	B makes chipsets, battery cells and cases, is refused an order it is
//	short for with nothing taken, and manufactures phones — inputs consumed
//	exactly, output once, each phone a piece with its order as provenance;
//	B lists phones; C (a factory) buys one for the company and reverse
//	engineers it: the sample destroyed, a degraded copy of the design, and
//	no technology — C still cannot make a chipset, nor produce the copy
//	without buying the parts.
//
// The ledger's, the journal's and the production economy's invariants hold
// at the end, and everything the test made is removed.
package tests

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// productionCity is the city the scenario runs in: not the one the company
// test settles.
const productionCity = "fenwick_span"

// purgeProductionOf removes everything of the production economy that
// hangs from the companies of owners: sales, listings, reverse engineering,
// orders, licenses, technologies, research, supplies, designs, their goods
// and journal, the scheduled actions, and the city's supplier stock.
func purgeProductionOf(t *testing.T, pool *postgres.Pool, cityID string, owners ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	owned, city := any(owners), any(cityID)
	for _, step := range []struct {
		sql string
		arg any
	}{
		{`CREATE TEMP TABLE purge_pco ON COMMIT DROP AS SELECT id FROM companies WHERE owner_player_id = ANY($1::uuid[])`, owned},
		{`CREATE TEMP TABLE purge_pdesign ON COMMIT DROP AS SELECT id FROM product_designs WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`CREATE TEMP TABLE purge_ppiece ON COMMIT DROP AS
		   SELECT id FROM item_pieces WHERE org_id IN (SELECT id FROM purge_pco) OR design_id IN (SELECT id FROM purge_pdesign)`, nil},
		{`CREATE TEMP TABLE purge_paction ON COMMIT DROP AS
		   SELECT game_action_id AS id FROM company_research WHERE company_id IN (SELECT id FROM purge_pco)
		   UNION SELECT game_action_id FROM production_orders WHERE company_id IN (SELECT id FROM purge_pco)
		   UNION SELECT game_action_id FROM reverse_jobs WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`, nil},
		{`ALTER TABLE company_sales DISABLE TRIGGER company_sales_append_only`, nil},
		{`ALTER TABLE technology_licenses DISABLE TRIGGER technology_licenses_append_only`, nil},
		{`ALTER TABLE supply_purchases DISABLE TRIGGER supply_purchases_append_only`, nil},
		{`DELETE FROM company_sales WHERE company_id IN (SELECT id FROM purge_pco) OR buyer_org_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM company_listings WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM reverse_jobs WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM production_orders WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM technology_licenses WHERE licensor_company_id IN (SELECT id FROM purge_pco)
		     OR licensee_company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM company_technologies WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM company_research WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM supply_purchases WHERE company_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM game_actions WHERE id IN (SELECT id FROM purge_paction)`, nil},
		{`DELETE FROM item_movements WHERE from_org IN (SELECT id FROM purge_pco) OR to_org IN (SELECT id FROM purge_pco)
		     OR piece_id IN (SELECT id FROM purge_ppiece)`, nil},
		{`DELETE FROM item_pieces WHERE id IN (SELECT id FROM purge_ppiece)`, nil},
		{`DELETE FROM org_stacks WHERE org_id IN (SELECT id FROM purge_pco)`, nil},
		{`DELETE FROM product_designs WHERE id IN (SELECT id FROM purge_pdesign) AND origin = 'reverse_engineered'`, nil},
		{`DELETE FROM product_designs WHERE id IN (SELECT id FROM purge_pdesign)`, nil},
		{`DELETE FROM shop_shelves WHERE city_id = $1::uuid AND shop_code LIKE 'supplier:%'`, city},
		{`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`, nil},
		{`ALTER TABLE company_sales ENABLE TRIGGER company_sales_append_only`, nil},
		{`ALTER TABLE technology_licenses ENABLE TRIGGER technology_licenses_append_only`, nil},
		{`ALTER TABLE supply_purchases ENABLE TRIGGER supply_purchases_append_only`, nil},
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

func TestPhoneScenarioEndToEnd(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.org_stacks') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("org_stacks does not exist; apply migration 0020 first")
	}
	registry := companyRegistry(t, pool)
	if len(registry.Current().Technologies()) == 0 {
		t.Skip("the active content has no technologies; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, productionCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", productionCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", productionCity, n)
	}

	ownerA, ownerB, ownerC := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	players := []*application.Player{ownerA, ownerB, ownerC}
	for _, p := range players {
		p := p
		t.Cleanup(func() { purgeLedgerFor(t, pool, p.ID); purgeWorkFor(t, pool, p.ID) })
	}
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, ownerA.ID, ownerB.ID, ownerC.ID) })
	t.Cleanup(func() { purgeProductionOf(t, pool, city.ID, ownerA.ID, ownerB.ID, ownerC.ID) })
	for _, p := range players {
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
			  WHERE id = $1::uuid`, p.ID, city.ID); err != nil {
			t.Fatal(err)
		}
		grantCash(t, pool, p.ID, 1_000_000)
	}
	// Engineering: A's owner can research; C's is a master at taking
	// things apart; B's is a designer.
	for p, level := range map[*application.Player]int{ownerA: 5, ownerB: 3, ownerC: 95} {
		if _, err := pool.Raw().Exec(ctx,
			`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at)
			 VALUES (gen_random_uuid(), $1::uuid, 'engineering', $2, 0, now())`, p.ID, level); err != nil {
			t.Fatal(err)
		}
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
		NameMin: 3, NameMax: 24, Limits: limits}, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)
	treasury := func(companyID string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), application.AccountCompanyTreasury, companyID)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
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
	ok := func(what string, resp *presenter.Response, err error, want ...string) *presenter.Response {
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
	stock := func(companyID, code string) int64 {
		var q int64
		_ = pool.Raw().QueryRow(testCtx(t), `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks
		  WHERE org_kind = 'company' AND org_id = $1::uuid AND item_code = $2 AND holding = 'warehouse'`, companyID, code).Scan(&q)
		return q
	}

	// Found the four companies and put money in them.
	type co struct{ id, code string }
	foundCo := func(p *application.Player, kind, name string) co {
		t.Helper()
		resp, err := companies.Found(ctx, metaAs(p, "company.found"), handlers.CompanyRequest{Type: kind, Method: "cash", Name: name})
		ok("found "+name, resp, err)
		var c co
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
			p.ID, name).Scan(&c.id, &c.code); err != nil {
			t.Fatalf("company %s was not founded: %v (%s)", name, err, resp.Text)
		}
		resp, err = companies.Deposit(ctx, metaAs(p, "company.deposit"), handlers.CompanyRequest{Company: c.code, Method: "cash",
			Amount: "300000"})
		ok("deposit into "+name, resp, err)
		return c
	}
	a := foundCo(ownerA, "factory", "Nilou Industries")
	b := foundCo(ownerB, "factory", "Kaveh Works")
	m := foundCo(ownerB, "mine", "Kaveh Quarry")
	c := foundCo(ownerC, "factory", "Sara Copies")

	// --- 1. Research: once, paid once, owned once. -----------------------
	research := func(p *application.Player, co co, tech string) {
		t.Helper()
		before := treasury(co.id)
		press := metaAs(p, "company.research")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := prod.Research(context.Background(), press, handlers.ProductionRequest{Company: co.code, Tech: tech})
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("research %s: %v", tech, err)
			}
		}
		var (
			id, action string
			cost       int64
			finish     time.Time
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, cost, finish_at FROM company_research
		  WHERE company_id = $1::uuid AND tech_code = $2 AND status = 'running'`, co.id, tech).Scan(&id, &action, &cost, &finish); err != nil {
			t.Fatalf("research %s did not start: %v", tech, err)
		}
		if got := before - treasury(co.id); got != cost {
			t.Fatalf("research %s cost the company %d, want %d once", tech, got, cost)
		}
		advance(finish.Sub(now()) + time.Second)
		for range 2 {
			if _, err := prod.Researched(ctx, scheduler("company.researched"), handlers.CrimeScheduledRequest{ActionID: action,
				ReferenceID: id}); err != nil {
				t.Fatalf("researched %s: %v", tech, err)
			}
		}
		if n := countRows(t, pool, `SELECT count(*) FROM company_technologies WHERE company_id = $1::uuid AND tech_code = $2`,
			co.id, tech); n != 1 {
			t.Fatalf("%s owned %d times, want once", tech, n)
		}
	}
	// Microchips need semiconductors first.
	resp, err := prod.Research(ctx, metaAs(ownerA, "company.research"), handlers.ProductionRequest{Company: a.code, Tech: "microchips"})
	ok("microchips before semiconductors", resp, err, "production.refused.prerequisite")
	research(ownerA, a, "semiconductors")
	research(ownerA, a, "microchips")
	research(ownerA, a, "batteries")
	// Researched once: a second research of the same technology is refused.
	resp, err = prod.Research(ctx, metaAs(ownerA, "company.research"), handlers.ProductionRequest{Company: a.code, Tech: "batteries"})
	ok("batteries again", resp, err, "production.refused.owned")

	// --- 2. Sharing: license microchips, publish battery chemistry. --------
	resp, err = prod.TechMode(ctx, metaAs(ownerA, "company.techmode"), handlers.ProductionRequest{Company: a.code,
		Tech: "microchips", Mode: "license", Price: "5000"})
	ok("license microchips", resp, err)
	resp, err = prod.TechMode(ctx, metaAs(ownerA, "company.techmode"), handlers.ProductionRequest{Company: a.code,
		Tech: "batteries", Mode: "published"})
	ok("publish batteries, unconfirmed", resp, err, "production.tech_publish_confirm")
	resp, err = prod.TechMode(ctx, metaAs(ownerA, "company.techmode"), handlers.ProductionRequest{Company: a.code,
		Tech: "batteries", Mode: "published", Confirm: "yes"})
	ok("publish batteries", resp, err, "production.tech_notice.published")
	resp, err = prod.TechMode(ctx, metaAs(ownerA, "company.techmode"), handlers.ProductionRequest{Company: a.code,
		Tech: "batteries", Mode: "private"})
	ok("unpublish batteries", resp, err, "production.refused.published")
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE payload->>'company_id' = $1 AND subject LIKE '%tech_published%'`, a.id); n != 1 {
		t.Errorf("tech_published events = %d, want 1 for the group line", n)
	}

	// --- 3. B cannot design with a chipset until it holds a license. -------
	resp, err = prod.DesignNew(ctx, metaAs(ownerB, "company.dnew"), handlers.ProductionRequest{Company: b.code, Item: "phone"})
	ok("new phone design", resp, err)
	var designNo int64
	var designID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM product_designs WHERE company_id = $1::uuid`, b.id).Scan(&designNo, &designID); err != nil {
		t.Fatal(err)
	}
	no := strconv.FormatInt(designNo, 10)
	resp, err = prod.DesignFill(ctx, metaAs(ownerB, "company.dfill"), handlers.ProductionRequest{No: no, Slot: "board", Component: "chipset"})
	ok("chipset without the technology", resp, err, "production.refused.tech_locked")
	aBefore, bBefore := treasury(a.id), treasury(b.id)
	licensePress := metaAs(ownerB, "company.license")
	for range 2 {
		resp, err = prod.License(ctx, licensePress, handlers.ProductionRequest{Company: b.code, Tech: "microchips", From: a.code,
			Confirm: "yes"})
		ok("buy the license", resp, err)
	}
	resp, err = prod.License(ctx, metaAs(ownerB, "company.license"), handlers.ProductionRequest{Company: b.code, Tech: "microchips",
		From: a.code, Confirm: "yes"})
	ok("buy the license again", resp, err, "production.refused.licensed")
	if got := treasury(a.id) - aBefore; got != 5000 {
		t.Fatalf("A received %d for the license, want 5000 once", got)
	}
	if got := bBefore - treasury(b.id); got != 5000 {
		t.Fatalf("B paid %d for the license, want 5000 once", got)
	}

	// --- 4. B designs the phone. ------------------------------------------
	for slot, comp := range map[string]string{"board": "chipset", "power": "cell", "shell": "plastic_case"} {
		resp, err = prod.DesignFill(ctx, metaAs(ownerB, "company.dfill"), handlers.ProductionRequest{No: no, Slot: slot, Component: comp})
		ok("fill "+slot, resp, err)
	}
	resp, err = prod.DesignFinal(ctx, metaAs(ownerB, "company.dfinal"), handlers.ProductionRequest{No: no})
	ok("finalise without a name", resp, err, "production.refused.no_name")
	resp, err = prod.DesignName(ctx, metaAs(ownerB, "company.dname"), handlers.ProductionRequest{No: no, Name: "Nil Mobile X"})
	ok("name the design", resp, err)
	resp, err = prod.DesignFinal(ctx, metaAs(ownerB, "company.dfinal"), handlers.ProductionRequest{No: no})
	ok("finalise the design", resp, err, "production.design_state.final")

	// --- 5. The mine: fuel from the supplier, raw materials extracted. -----
	resp, err = prod.Supply(ctx, metaAs(ownerB, "company.supply"), handlers.ProductionRequest{Company: m.code, Component: "diesel", Qty: "10"})
	ok("buy diesel", resp, err, "production.supply_bought")
	if got := stock(m.id, "diesel"); got != 10 {
		t.Fatalf("the mine holds %d diesel, want 10", got)
	}
	complete := func(orderID string) {
		t.Helper()
		var (
			action string
			finish time.Time
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, finish_at FROM production_orders WHERE id = $1::uuid`,
			orderID).Scan(&action, &finish); err != nil {
			t.Fatal(err)
		}
		if finish.After(now()) {
			advance(finish.Sub(now()) + time.Second)
		}
		for range 2 {
			if _, err := prod.Produced(ctx, scheduler("company.produced"), handlers.CrimeScheduledRequest{ActionID: action,
				ReferenceID: orderID}); err != nil {
				t.Fatalf("produced: %v", err)
			}
		}
	}
	produce := func(p *application.Player, co co, target string, qty int) string {
		t.Helper()
		resp, err := prod.Produce(ctx, metaAs(p, "company.produce"), handlers.ProductionRequest{Company: co.code, Target: target,
			Qty: strconv.Itoa(qty), Confirm: "yes"})
		ok("produce "+target, resp, err, "production.placed")
		var id string
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM production_orders WHERE company_id = $1::uuid
		  ORDER BY no DESC LIMIT 1`, co.id).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var mineOrders []string
	for _, target := range []string{"silica", "iron_ore", "crude_oil"} {
		mineOrders = append(mineOrders, produce(ownerB, m, target, 1))
	}
	if got := stock(m.id, "diesel"); got != 7 {
		t.Fatalf("the mine holds %d diesel after three batches, want 7", got)
	}
	for _, id := range mineOrders {
		complete(id)
	}
	if stock(m.id, "silica") != 10 || stock(m.id, "iron_ore") != 10 || stock(m.id, "crude_oil") != 8 {
		t.Fatalf("the mine extracted silica %d, iron ore %d, crude %d; want 10, 10, 8",
			stock(m.id, "silica"), stock(m.id, "iron_ore"), stock(m.id, "crude_oil"))
	}

	// --- 6. The mine lists its output; B buys it. -------------------------
	listingNo := func(companyID, code string) string {
		var n int64
		if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_listings WHERE company_id = $1::uuid AND item_code = $2
		  AND status = 'open'`, companyID, code).Scan(&n); err != nil {
			t.Fatalf("no listing of %s: %v", code, err)
		}
		return strconv.FormatInt(n, 10)
	}
	for code, qty := range map[string]string{"silica": "9", "iron_ore": "3", "crude_oil": "6"} {
		resp, err = prod.Sell(ctx, metaAs(ownerB, "company.sell"), handlers.ProductionRequest{Company: m.code, Target: code,
			Qty: qty, Price: "7"})
		ok("list "+code, resp, err, "production.listing_notice.listed")
		resp, err = prod.Buy(ctx, metaAs(ownerB, "company.buy"), handlers.ProductionRequest{No: listingNo(m.id, code), Qty: qty,
			Method: b.code})
		ok("B buys "+code, resp, err, "production.bought_company")
	}
	// Materials are sold to companies only.
	resp, err = prod.Sell(ctx, metaAs(ownerB, "company.sell"), handlers.ProductionRequest{Company: m.code, Target: "silica",
		Qty: "1", Price: "7"})
	ok("list one more silica", resp, err)
	resp, err = prod.Buy(ctx, metaAs(ownerC, "company.buy"), handlers.ProductionRequest{No: listingNo(m.id, "silica"), Qty: "1",
		Method: "cash"})
	ok("a player buys silica", resp, err, "production.refused.not_cleared")
	resp, err = prod.Supply(ctx, metaAs(ownerB, "company.supply"), handlers.ProductionRequest{Company: b.code, Component: "diesel", Qty: "3"})
	ok("B buys diesel", resp, err)
	resp, err = prod.Supply(ctx, metaAs(ownerB, "company.supply"), handlers.ProductionRequest{Company: b.code, Component: "lithium_salt", Qty: "6"})
	ok("B buys lithium", resp, err)

	// --- 7. B makes the parts, is refused a short order, makes phones. ----
	complete(produce(ownerB, b, "chipset", 3))
	complete(produce(ownerB, b, "cell", 3))
	complete(produce(ownerB, b, "plastic_case", 2))
	if stock(b.id, "chipset") != 3 || stock(b.id, "cell") != 3 || stock(b.id, "plastic_case") != 4 {
		t.Fatalf("B made chipsets %d, cells %d, cases %d; want 3, 3, 4",
			stock(b.id, "chipset"), stock(b.id, "cell"), stock(b.id, "plastic_case"))
	}
	target := "d" + no
	resp, err = prod.Produce(ctx, metaAs(ownerB, "company.produce"), handlers.ProductionRequest{Company: b.code, Target: target,
		Qty: "4", Confirm: "yes"})
	ok("four phones from three chipsets", resp, err, "production.refused.shortage", "production.shortage_line")
	if stock(b.id, "chipset") != 3 || stock(b.id, "cell") != 3 || stock(b.id, "plastic_case") != 4 {
		t.Fatal("a refused order took inputs: all or nothing")
	}
	phonesOrder := produce(ownerB, b, target, 3)
	if stock(b.id, "chipset") != 0 || stock(b.id, "cell") != 0 || stock(b.id, "plastic_case") != 1 {
		t.Fatalf("three phones consumed chipsets to %d, cells to %d, cases to %d; want 0, 0, 1",
			stock(b.id, "chipset"), stock(b.id, "cell"), stock(b.id, "plastic_case"))
	}
	complete(phonesOrder)
	if n := countRows(t, pool, `SELECT count(*) FROM item_pieces WHERE org_id = $1::uuid AND design_id = $2::uuid
	  AND origin = 'production' AND origin_ref = $3::uuid AND holding = 'warehouse'`, b.id, designID, phonesOrder); n != 3 {
		t.Fatalf("B holds %d phones of its order, want 3", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE payload->>'company_id' = $1 AND subject LIKE '%product_launched%'`, b.id); n != 1 {
		t.Errorf("product_launched events = %d, want 1", n)
	}

	// --- 8. B lists phones; C buys one for the company and takes it apart.
	resp, err = prod.Sell(ctx, metaAs(ownerB, "company.sell"), handlers.ProductionRequest{Company: b.code, Target: target,
		Qty: "3", Price: "1500"})
	ok("list phones", resp, err)
	phones := listingNo(b.id, "phone")
	var copyID string
	for attempt := 0; attempt < 3 && copyID == ""; attempt++ {
		bBefore, cBefore := treasury(b.id), treasury(c.id)
		resp, err = prod.Buy(ctx, metaAs(ownerC, "company.buy"), handlers.ProductionRequest{No: phones, Qty: "1", Method: c.code})
		ok("C buys a phone", resp, err, "production.bought_company")
		if got := cBefore - treasury(c.id); got != 1500 {
			t.Fatalf("C paid %d for a phone, want 1500", got)
		}
		if got := treasury(b.id) - bBefore; got <= 0 || got > 1500 {
			t.Fatalf("B received %d for a phone, want 1500 less the sales tax", got)
		}
		var serial string
		if err := pool.Raw().QueryRow(ctx, `SELECT serial FROM item_pieces WHERE org_id = $1::uuid AND holding = 'warehouse'
		  ORDER BY serial LIMIT 1`, c.id).Scan(&serial); err != nil {
			t.Fatalf("C holds no phone: %v", err)
		}
		resp, err = prod.Reverse(ctx, metaAs(ownerC, "company.reverse"), handlers.ProductionRequest{Company: c.code, Serial: serial})
		ok("take apart, unconfirmed", resp, err, "production.reverse_confirm")
		resp, err = prod.Reverse(ctx, metaAs(ownerC, "company.reverse"), handlers.ProductionRequest{Company: c.code, Serial: serial,
			Confirm: "yes"})
		ok("take apart", resp, err)
		var (
			jobID, action string
			finish        time.Time
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT j.id::text, j.game_action_id::text, j.finish_at FROM reverse_jobs j
		  JOIN item_pieces p ON p.id = j.piece_id WHERE p.serial = $1`, serial).Scan(&jobID, &action, &finish); err != nil {
			t.Fatalf("no reverse engineering: %v", err)
		}
		if n := countRows(t, pool, `SELECT count(*) FROM item_pieces WHERE serial = $1 AND holding = 'gone'`, serial); n != 1 {
			t.Fatal("the sample was not destroyed when the work started")
		}
		advance(finish.Sub(now()) + time.Second)
		for range 2 {
			if _, err := prod.Reversed(ctx, scheduler("company.reversed"), handlers.CrimeScheduledRequest{ActionID: action,
				ReferenceID: jobID}); err != nil {
				t.Fatalf("reversed: %v", err)
			}
		}
		_ = pool.Raw().QueryRow(ctx, `SELECT COALESCE(result_design_id::text, '') FROM reverse_jobs WHERE id = $1::uuid`, jobID).Scan(&copyID)
	}
	if copyID == "" {
		t.Fatal("three reverse engineerings by a master all failed")
	}
	var (
		origin, source    string
		loss, overhead    int64
		copyNo            int64
		copyOwner, status string
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT origin, source_design_id::text, quality_loss_bps, overhead_bps, no, company_id::text, status
	  FROM product_designs WHERE id = $1::uuid`, copyID).Scan(&origin, &source, &loss, &overhead, &copyNo, &copyOwner, &status); err != nil {
		t.Fatal(err)
	}
	if origin != "reverse_engineered" || source != designID || copyOwner != c.id || status != "final" || loss <= 0 || overhead <= 0 {
		t.Fatalf("the copy: origin %s, source %s, owner %s, status %s, loss %d, overhead %d", origin, source, copyOwner, status, loss, overhead)
	}
	// Never the technology.
	if n := countRows(t, pool, `SELECT count(*) FROM company_technologies WHERE company_id = $1::uuid`, c.id); n != 0 {
		t.Fatalf("reverse engineering gave C %d technologies", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM technology_licenses WHERE licensee_company_id = $1::uuid`, c.id); n != 0 {
		t.Fatal("reverse engineering gave C a license")
	}
	resp, err = prod.Produce(ctx, metaAs(ownerC, "company.produce"), handlers.ProductionRequest{Company: c.code, Target: "chipset",
		Qty: "1", Confirm: "yes"})
	ok("C makes a chipset", resp, err, "production.refused.tech_locked")
	resp, err = prod.Produce(ctx, metaAs(ownerC, "company.produce"), handlers.ProductionRequest{Company: c.code,
		Target: "d" + strconv.FormatInt(copyNo, 10), Qty: "1", Confirm: "yes"})
	ok("C makes the copy without parts", resp, err, "production.refused.shortage")

	// --- 9. The city's period: the mine sells the population what it holds.
	mineBefore := stock(m.id, "silica") + stock(m.id, "iron_ore") + stock(m.id, "crude_oil")
	var (
		periodNo int64
		settleID string
		nextAt   time.Time
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM company_markets WHERE city_id = $1::uuid`,
		city.ID).Scan(&periodNo, &settleID, &nextAt); err != nil {
		t.Fatalf("the city's settlement clock: %v", err)
	}
	if nextAt.After(now()) {
		advance(nextAt.Sub(now()) + time.Second)
	}
	payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: city.ID, PeriodNo: periodNo})
	if _, err := companies.Settle(ctx, scheduler("company.settle"), handlers.CrimeScheduledRequest{ActionID: settleID,
		ReferenceType: application.CompanyMarketReference, ReferenceID: city.ID, Payload: payload}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	var sold, stockUnits int64
	if err := pool.Raw().QueryRow(ctx, `SELECT sold_units, stock_units FROM company_periods WHERE company_id = $1::uuid`,
		m.id).Scan(&sold, &stockUnits); err != nil {
		t.Fatal(err)
	}
	mineAfter := stock(m.id, "silica") + stock(m.id, "iron_ore") + stock(m.id, "crude_oil")
	if sold <= 0 || sold > mineBefore || stockUnits != sold || mineBefore-mineAfter != sold {
		t.Fatalf("the mine sold %d (stock %d) of %d held, and holds %d after", sold, stockUnits, mineBefore, mineAfter)
	}

	// --- 10. The invariants hold. -----------------------------------------
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() {
		t.Fatalf("economy invariants broken: %+v", v)
	}
	if v.LicenseRows < 5000 || v.SaleRows <= 0 || v.SupplyRows <= 0 || v.ResearchRows <= 0 {
		t.Errorf("the verifier saw licenses %d, sales %d, supplies %d, research %d", v.LicenseRows, v.SaleRows, v.SupplyRows, v.ResearchRows)
	}
}
