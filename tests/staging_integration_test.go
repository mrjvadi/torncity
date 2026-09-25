//go:build integration

// Integration tests of staged production (docs/adr/0021, section 14) and of
// defence licences (docs/adr/0022, section 2.14), through the real handlers,
// unit of work, ledger, item journal and policy resolver, on the game clock
// the game ships with (migration 0027 and the shipped content):
//
//	a new tech studio sees only basic designs — no phone — until it has
//	researched the electronics chain, and then the phone opens;
//	its next step leads it through a basic product: one tap buys the
//	missing inputs from the supplier, once; a quick order makes them;
//	the studio, standing high in technology, applies for a defence
//	contractor licence; the defence minister approves it once; it may
//	then research a controlled technology its owner could not before, and
//	its owner founds a defence company on it; the minister revokes the
//	licence with notice, and once the notice runs out the controlled
//	research is refused again;
//	a player with neither a rank nor a licensed company is refused a
//	defence company;
//	a soldier enlists, is refused a duty the empty defence fund cannot
//	pay, is paid one from it once it can, rises to captain, and founds a
//	defence company on the rank.
//
// The ledger's invariants hold at the end, and everything the tests made is
// removed.
package tests

import (
	"context"
	"strconv"
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
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// stagingCity is the city these tests found their companies in: one no
// other test uses.
const stagingCity = "kessmoor"

// buttons lists the callback data of a response's buttons.
func buttons(resp *presenter.Response) []string {
	if resp == nil || resp.Keyboard == nil {
		return nil
	}
	var out []string
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

// hasButton reports whether a response has a button whose data is want.
func hasButton(resp *presenter.Response, want string) bool {
	for _, b := range buttons(resp) {
		if b == want {
			return true
		}
	}
	return false
}

// anyButton reports whether a response has a button whose data contains s.
func anyButton(resp *presenter.Response, s string) bool {
	for _, b := range buttons(resp) {
		if strings.Contains(b, s) {
			return true
		}
	}
	return false
}

// requireLicences skips when migration 0027 or its content is missing.
func requireLicences(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.defence_licences') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("defence_licences does not exist; apply migration 0027 first")
	}
}

// stagingWorld is what both tests share: the city, the clock and the
// handlers.
type stagingWorld struct {
	t         *testing.T
	pool      *postgres.Pool
	city      *application.City
	home      string
	clock     *testClock
	uow       application.UnitOfWork
	companies *handlers.CompaniesHandler
	prod      *handlers.ProductionHandler
	forces    *handlers.MilitaryHandler
	jobs      *handlers.JobsHandler
	edu       *handlers.EducationHandler
	ledger    *postgres.LedgerRepository
}

func newStagingWorld(t *testing.T) *stagingWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireLicences(t, pool)
	home, far := requireMilitary(t, pool)
	registry := companyRegistry(t, pool)
	snap := registry.Current()
	if _, ok := snap.DefenceLicence(); !ok {
		t.Skip("the active content has no defence licence; run `admin content load`")
	}
	if _, ok := snap.ItemDef("led_torch"); !ok {
		t.Skip("the active content has no basic electronics; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, stagingCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", stagingCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", stagingCity, n)
	}
	// The home country's defence fund starts empty, and is left so.
	purgeMilitary(t, pool, []string{home, far}, nil)
	t.Cleanup(func() { purgeMilitary(t, pool, []string{home, far}, nil) })

	w := &stagingWorld{t: t, pool: pool, city: city, home: home, clock: &testClock{now: time.Now().UTC()},
		uow: postgres.NewUnitOfWork(pool, testDefaultLanguage), ledger: postgres.NewLedgerRepository(pool)}
	policy := postgres.NewPolicyReader(pool, w.clock.Now)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	scale := gametime.Scale(gameScale)
	w.companies = handlers.NewCompaniesHandler(w.uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), scale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 3,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits, MaxRunningOrders: 3, QuickUnits: 5}, time.Hour, w.clock.Now)
	w.prod = handlers.NewProductionHandler(w.uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.ProductionRules{
		MaxRunningOrders: 3, MaxDesigns: 20, MaxListings: 10, DesignMinSkill: 1, ReverseTime: 6 * time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits, QuickUnits: 5}, time.Hour, w.clock.Now)
	w.forces = handlers.NewMilitaryHandler(w.uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.MilitaryRules{
		Period: 24 * time.Hour, ReadinessLossBPS: 1000, ReadinessRecoveryBPS: 500, ReferenceRadarKM: 150,
		LicenceRevokeNotice: 24 * time.Hour}, time.Hour, w.clock.Now)
	w.jobs = handlers.NewJobsHandler(w.uow, workIDs{t}, nil, registry, cities, policy, scale, 5, time.Hour, w.clock.Now)
	w.edu = handlers.NewEducationHandler(w.uow, workIDs{t}, nil, registry, cities, scale, 5, time.Hour, w.clock.Now)
	return w
}

// player makes a player who lives in the city, at city hall, with cash, and
// removes everything of theirs at the end.
func (w *stagingWorld) player(cash int64) *application.Player {
	t := w.t
	p := insertPlayer(t, w.pool)
	t.Cleanup(func() { purgeLedgerFor(t, w.pool, p.ID); purgeWorkFor(t, w.pool, p.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, w.pool, w.city.ID, p.ID) })
	t.Cleanup(func() { purgeProductionOf(t, w.pool, w.city.ID, p.ID) })
	t.Cleanup(func() { purgeMilitary(t, w.pool, []string{w.home}, []string{p.ID}) })
	if _, err := w.pool.Raw().Exec(testCtx(t),
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
		  WHERE id = $1::uuid`, p.ID, w.city.ID); err != nil {
		t.Fatal(err)
	}
	if cash > 0 {
		grantCash(t, w.pool, p.ID, cash)
	}
	return p
}

// skill sets a player's level in a skill.
func (w *stagingWorld) skill(p *application.Player, skill string, level int) {
	if _, err := w.pool.Raw().Exec(testCtx(w.t),
		`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, $3, 0, now())
		 ON CONFLICT (player_id, skill_code) DO UPDATE SET level = EXCLUDED.level`, p.ID, skill, level); err != nil {
		w.t.Fatal(err)
	}
}

func (w *stagingWorld) meta(p *application.Player, command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	return m
}

func (w *stagingWorld) scheduler(command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.Command = 0, command
	return m
}

type stagingCo struct{ id, code string }

// found founds a company and puts money in it.
func (w *stagingWorld) found(p *application.Player, kind, name string, deposit string) stagingCo {
	t := w.t
	t.Helper()
	resp, err := w.companies.Found(testCtx(t), w.meta(p, "company.found"), handlers.CompanyRequest{Type: kind, Method: "cash", Name: name})
	said(t, "found "+name, resp, err)
	var c stagingCo
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
		p.ID, name).Scan(&c.id, &c.code); err != nil {
		t.Fatalf("company %s was not founded: %v (%s)", name, err, resp.Text)
	}
	if deposit != "" {
		resp, err = w.companies.Deposit(testCtx(t), w.meta(p, "company.deposit"), handlers.CompanyRequest{Company: c.code,
			Method: "cash", Amount: deposit})
		said(t, "deposit into "+name, resp, err)
	}
	return c
}

// research runs a research to its end.
func (w *stagingWorld) research(p *application.Player, c stagingCo, tech string) {
	t := w.t
	t.Helper()
	resp, err := w.prod.Research(testCtx(t), w.meta(p, "company.research"), handlers.ProductionRequest{Company: c.code, Tech: tech})
	said(t, "research "+tech, resp, err)
	var id, action string
	var finish time.Time
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT id::text, game_action_id::text, finish_at FROM company_research
	  WHERE company_id = $1::uuid AND tech_code = $2 AND status = 'running'`, c.id, tech).Scan(&id, &action, &finish); err != nil {
		t.Fatalf("research %s did not start: %v (%s)", tech, err, resp.Text)
	}
	w.clock.Advance(finish.Sub(w.clock.Now()) + time.Second)
	if _, err := w.prod.Researched(testCtx(t), w.scheduler("company.researched"), handlers.CrimeScheduledRequest{ActionID: action,
		ReferenceID: id}); err != nil {
		t.Fatalf("researched %s: %v", tech, err)
	}
}

// produce places an order and runs it to its end.
func (w *stagingWorld) produce(p *application.Player, c stagingCo, target string, qty int) {
	t := w.t
	t.Helper()
	resp, err := w.prod.Produce(testCtx(t), w.meta(p, "company.produce"), handlers.ProductionRequest{Company: c.code, Target: target,
		Qty: strconv.Itoa(qty), Confirm: "yes"})
	said(t, "produce "+target, resp, err, "production.placed")
	var id, action string
	var finish time.Time
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT id::text, game_action_id::text, finish_at FROM production_orders
	  WHERE company_id = $1::uuid ORDER BY no DESC LIMIT 1`, c.id).Scan(&id, &action, &finish); err != nil {
		t.Fatal(err)
	}
	if finish.After(w.clock.Now()) {
		w.clock.Advance(finish.Sub(w.clock.Now()) + time.Second)
	}
	if _, err := w.prod.Produced(testCtx(t), w.scheduler("company.produced"), handlers.CrimeScheduledRequest{ActionID: action,
		ReferenceID: id}); err != nil {
		t.Fatalf("produced: %v", err)
	}
}

func (w *stagingWorld) stock(companyID, code string) int64 {
	var q int64
	_ = w.pool.Raw().QueryRow(testCtx(w.t), `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks
	  WHERE org_kind = 'company' AND org_id = $1::uuid AND item_code = $2 AND holding = 'warehouse'`, companyID, code).Scan(&q)
	return q
}

func (w *stagingWorld) verify() {
	v, err := postgres.NewEconomyAdmin(w.pool).VerifyLedger(testCtx(w.t), 20)
	if err != nil {
		w.t.Fatal(err)
	}
	if !v.OK() {
		w.t.Fatalf("economy invariants broken: %+v", v)
	}
}

func TestStagedProductionAndDefenceContractor(t *testing.T) {
	w := newStagingWorld(t)
	ctx := testCtx(t)
	owner, minister, civilian := w.player(2_000_000), w.player(0), w.player(400_000)
	w.skill(owner, "engineering", 5)
	studio := w.found(owner, "tech_studio", "Aria Tech", "600000")

	// --- 1. A new tech studio sees only basic designs: no phone. ----------
	resp, err := w.prod.Studio(ctx, w.meta(owner, "company.studio"), handlers.ProductionRequest{Company: studio.code})
	said(t, "a new studio", resp, err, "production.studio_later")
	if !hasButton(resp, "company:dnew:"+studio.code+":led_torch") || !hasButton(resp, "company:dnew:"+studio.code+":pocket_radio") {
		t.Fatalf("a new studio does not offer its basic goods: %v", buttons(resp))
	}
	if anyButton(resp, "phone") || strings.Contains(resp.Text, "production.studio_next") {
		t.Fatalf("a new studio already shows the phone: %q %v", resp.Text, buttons(resp))
	}
	resp, err = w.prod.DesignNew(ctx, w.meta(owner, "company.dnew"), handlers.ProductionRequest{Company: studio.code, Item: "phone"})
	said(t, "a phone on the first day", resp, err, "production.refused.tech_locked")
	// Nor does its lab show military technology, and it cannot research
	// one.
	resp, err = w.prod.Lab(ctx, w.meta(owner, "company.lab"), handlers.ProductionRequest{Company: studio.code})
	said(t, "a new studio's lab", resp, err)
	if anyButton(resp, "radar_systems") || !anyButton(resp, "semiconductors") || !anyButton(resp, "batteries") {
		t.Fatalf("a new studio's lab: %v", buttons(resp))
	}
	resp, err = w.prod.Research(ctx, w.meta(owner, "company.research"), handlers.ProductionRequest{Company: studio.code, Tech: "radar_systems"})
	said(t, "radar without a licence", resp, err, "production.refused.not_cleared")

	// --- 2. A basic product, step by step. --------------------------------
	resp, err = w.companies.Manage(ctx, w.meta(owner, "company.manage"), handlers.CompanyRequest{Company: studio.code})
	said(t, "the first next step", resp, err, "production.step.design_first")
	resp, err = w.prod.DesignNew(ctx, w.meta(owner, "company.dnew"), handlers.ProductionRequest{Company: studio.code, Item: "led_torch"})
	said(t, "a torch", resp, err)
	var torchNo int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no FROM product_designs WHERE company_id = $1::uuid AND item_code = 'led_torch'`,
		studio.id).Scan(&torchNo); err != nil {
		t.Fatal(err)
	}
	no := strconv.FormatInt(torchNo, 10)
	for _, step := range []func() (*presenter.Response, error){
		func() (*presenter.Response, error) {
			return w.prod.DesignFill(ctx, w.meta(owner, "company.dfill"), handlers.ProductionRequest{No: no, Slot: "board", Component: "circuit_board"})
		},
		func() (*presenter.Response, error) {
			return w.prod.DesignName(ctx, w.meta(owner, "company.dname"), handlers.ProductionRequest{No: no, Name: "Beacon"})
		},
		func() (*presenter.Response, error) {
			return w.prod.DesignFinal(ctx, w.meta(owner, "company.dfinal"), handlers.ProductionRequest{No: no})
		},
	} {
		resp, err := step()
		said(t, "the torch's design", resp, err)
	}
	torch := "d" + no
	// Its next step: the torch needs circuit boards the studio makes, and
	// the boards wire and resin the wholesaler sells — buy them in one tap.
	resp, err = w.prod.Warehouse(ctx, w.meta(owner, "company.warehouse"), handlers.ProductionRequest{Company: studio.code})
	said(t, "the warehouse's next step", resp, err, "production.step.supply")
	stockUp := "company:stockup:" + studio.code + ":circuit_board:3"
	if !hasButton(resp, stockUp) {
		t.Fatalf("the next step is not the one-tap purchase %s: %v", stockUp, buttons(resp))
	}
	// A quick order opens at the quick size, short, with the one tap.
	resp, err = w.prod.Produce(ctx, w.meta(owner, "company.produce"), handlers.ProductionRequest{Company: studio.code,
		Target: "circuit_board"})
	said(t, "a quick order of boards", resp, err, "production.plan_short")
	if !hasButton(resp, "company:stockup:"+studio.code+":circuit_board:5") {
		t.Fatalf("the short plan offers no one-tap purchase: %v", buttons(resp))
	}
	sinkBefore := countRows(t, w.pool, `SELECT count(*) FROM supply_purchases WHERE company_id = $1::uuid`, studio.id)
	tap := w.meta(owner, "company.stockup")
	for range 2 {
		resp, err = w.prod.StockUp(ctx, tap, handlers.ProductionRequest{Company: studio.code, Target: "circuit_board", Qty: "3"})
		said(t, "the one-tap purchase", resp, err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM supply_purchases WHERE company_id = $1::uuid`, studio.id) - sinkBefore; n != 2 {
		t.Fatalf("the one tap made %d purchases, want 2 (wire and resin), once", n)
	}
	if w.stock(studio.id, "copper_wire") != 6 || w.stock(studio.id, "resin") != 3 {
		t.Fatalf("the warehouse holds wire %d, resin %d; want 6 and 3", w.stock(studio.id, "copper_wire"), w.stock(studio.id, "resin"))
	}
	if !hasButton(resp, "company:produce:"+studio.code+":circuit_board:3:yes") {
		t.Fatalf("after the purchase the order is not one tap away: %v", buttons(resp))
	}
	w.produce(owner, studio, "circuit_board", 3)
	if got := w.stock(studio.id, "circuit_board"); got != 6 {
		t.Fatalf("three batches made %d boards, want 6", got)
	}
	resp, err = w.prod.Warehouse(ctx, w.meta(owner, "company.warehouse"), handlers.ProductionRequest{Company: studio.code})
	said(t, "the next step: make torches", resp, err, "production.step.produce")
	if !hasButton(resp, "company:produce:"+studio.code+":"+torch+":5:yes") {
		t.Fatalf("the quick order of torches is not one tap: %v", buttons(resp))
	}
	w.produce(owner, studio, torch, 5)
	if n := countRows(t, w.pool, `SELECT count(*) FROM item_pieces WHERE org_id = $1::uuid AND item_code = 'led_torch'
	  AND holding = 'warehouse'`, studio.id); n != 5 {
		t.Fatalf("five torches ordered, %d made", n)
	}
	resp, err = w.prod.Warehouse(ctx, w.meta(owner, "company.warehouse"), handlers.ProductionRequest{Company: studio.code})
	said(t, "the next step: sell them", resp, err, "production.step.sell")

	// --- 3. The electronics chain opens the phone. ------------------------
	w.research(owner, studio, "semiconductors")
	resp, err = w.prod.Studio(ctx, w.meta(owner, "company.studio"), handlers.ProductionRequest{Company: studio.code})
	said(t, "a studio one step from a phone", resp, err, "production.studio_next")
	if anyButton(resp, ":phone") {
		t.Fatalf("the phone opened before its technologies: %v", buttons(resp))
	}
	w.research(owner, studio, "microchips")
	w.research(owner, studio, "batteries")
	resp, err = w.prod.Studio(ctx, w.meta(owner, "company.studio"), handlers.ProductionRequest{Company: studio.code})
	said(t, "a studio with the chain", resp, err)
	if !hasButton(resp, "company:dnew:"+studio.code+":phone") {
		t.Fatalf("the phone did not open: %v", buttons(resp))
	}
	resp, err = w.prod.DesignNew(ctx, w.meta(owner, "company.dnew"), handlers.ProductionRequest{Company: studio.code, Item: "phone"})
	said(t, "a phone at last", resp, err)
	if n := countRows(t, w.pool, `SELECT count(*) FROM product_designs WHERE company_id = $1::uuid AND item_code = 'phone'`, studio.id); n != 1 {
		t.Fatalf("phone designs = %d, want 1", n)
	}

	// --- 4. A contractor licence, approved once. --------------------------
	seatAs(t, w.uow, "defence_minister", w.home, minister, w.clock.Now())
	resp, err = w.companies.Defence(ctx, w.meta(owner, "company.defence"), handlers.CompanyRequest{Company: studio.code})
	said(t, "the defence screen", resp, err, "defence.apply_how")
	apply := w.meta(owner, "company.defence")
	for range 2 {
		resp, err = w.companies.Defence(ctx, apply, handlers.CompanyRequest{Company: studio.code, Confirm: "yes"})
		said(t, "apply", resp, err)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM defence_licences WHERE company_id = $1::uuid AND status = 'pending'
	  AND kind = 'contractor'`, studio.id); n != 1 {
		t.Fatalf("applications = %d, want one", n)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.military.licence_applied.v1'
	  AND payload->>'player_id' = $1`, minister.ID); n != 1 {
		t.Errorf("the minister was told %d times, want once", n)
	}
	var licenceNo int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no FROM defence_licences WHERE company_id = $1::uuid`, studio.id).Scan(&licenceNo); err != nil {
		t.Fatal(err)
	}
	ln := strconv.FormatInt(licenceNo, 10)
	resp, err = w.forces.Licence(ctx, w.meta(civilian, "military.licence"), handlers.MilitaryRequest{No: ln, Verdict: "approve"})
	said(t, "a civilian approving", resp, err, "military.refused.not_holder")
	var wg sync.WaitGroup
	texts := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := w.forces.Licence(context.Background(), w.meta(minister, "military.licence"),
				handlers.MilitaryRequest{No: ln, Verdict: "approve"})
			if err != nil {
				t.Errorf("approve: %v", err)
				return
			}
			texts <- resp.Text
		}()
	}
	wg.Wait()
	close(texts)
	refused := 0
	for text := range texts {
		if strings.Contains(text, "military.refused.licence_state") {
			refused++
		}
	}
	if refused != 1 {
		t.Errorf("two presses of approve: %d refused, want the second one", refused)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM defence_licences WHERE no = $1 AND status = 'active' AND decided_by = $2::uuid
	  AND decided_office = 'defence_minister'`, licenceNo, minister.ID); n != 1 {
		t.Fatal("the licence was not approved by the minister")
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.military.licence_granted.v1'
	  AND payload->>'company_code' = $1`, studio.code); n != 1 {
		t.Errorf("grant announcements = %d, want 1", n)
	}

	// --- 5. What the licence opens. ---------------------------------------
	resp, err = w.prod.Research(ctx, w.meta(owner, "company.research"), handlers.ProductionRequest{Company: studio.code, Tech: "radar_systems"})
	said(t, "radar under a licence", resp, err)
	if n := countRows(t, w.pool, `SELECT count(*) FROM company_research WHERE company_id = $1::uuid AND tech_code = 'radar_systems'
	  AND status = 'running'`, studio.id); n != 1 {
		t.Fatalf("the licensed contractor could not research radar: %s", resp.Text)
	}
	// Its owner founds a defence company on it; a civilian cannot.
	resp, err = w.companies.Found(ctx, w.meta(civilian, "company.found"), handlers.CompanyRequest{Type: "aerospace", Method: "cash",
		Name: "Nobody Aero"})
	said(t, "a civilian founding arms", resp, err, "company.refused.defence")
	if n := countRows(t, w.pool, `SELECT count(*) FROM companies WHERE owner_player_id = $1::uuid`, civilian.ID); n != 0 {
		t.Fatal("an unlicensed player founded a defence company")
	}
	resp, err = w.companies.Type(ctx, w.meta(civilian, "company.type"), handlers.CompanyRequest{Type: "aerospace"})
	said(t, "the defence company's page, unlicensed", resp, err, "company.type_defence")
	aero := w.found(owner, "aerospace", "Aria Aerospace", "")
	if n := countRows(t, w.pool, `SELECT count(*) FROM defence_licences WHERE company_id = $1::uuid AND kind = 'manufacturer'
	  AND basis = 'contractor' AND status = 'active'`, aero.id); n != 1 {
		t.Fatal("the defence company was founded without its licence")
	}

	// --- 6. Revoked with notice. ------------------------------------------
	resp, err = w.forces.Licence(ctx, w.meta(minister, "military.licence"), handlers.MilitaryRequest{No: ln, Verdict: "revoke"})
	said(t, "revoke, unconfirmed", resp, err, "defence.revoke_confirm")
	resp, err = w.forces.Licence(ctx, w.meta(minister, "military.licence"), handlers.MilitaryRequest{No: ln, Verdict: "revoke",
		Confirm: "yes"})
	said(t, "revoke", resp, err, "defence.verdict.revoke")
	if n := countRows(t, w.pool, `SELECT count(*) FROM defence_licences WHERE no = $1 AND status = 'revoking'
	  AND effective_at > revoked_at`, licenceNo); n != 1 {
		t.Fatal("the licence was not revoked with notice")
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.military.licence_revoked.v1'
	  AND payload->>'company_code' = $1`, studio.code); n != 1 {
		t.Errorf("revocation announcements = %d, want 1", n)
	}
	w.clock.Advance(25 * time.Hour)
	resp, err = w.prod.Research(ctx, w.meta(owner, "company.research"), handlers.ProductionRequest{Company: studio.code,
		Tech: "missile_guidance"})
	said(t, "guidance after the notice", resp, err, "production.refused.not_cleared")

	w.verify()
}

func TestSoldierRisesAndFoundsADefenceCompany(t *testing.T) {
	w := newStagingWorld(t)
	ctx := testCtx(t)
	soldier := w.player(300_000)
	resp, err := w.companies.Found(ctx, w.meta(soldier, "company.found"), handlers.CompanyRequest{Type: "aerospace", Method: "cash",
		Name: "Simorgh Works"})
	said(t, "a civilian founding arms", resp, err, "company.refused.defence")

	// --- 1. Enlist. --------------------------------------------------------
	resp, err = w.jobs.Apply(ctx, w.meta(soldier, "job.apply"), handlers.JobRequest{Role: "armed_forces"})
	said(t, "enlist", resp, err)
	if n := countRows(t, w.pool, `SELECT count(*) FROM employments WHERE player_id = $1::uuid AND career_code = 'armed_forces'
	  AND ended_at IS NULL`, soldier.ID); n != 1 {
		t.Fatalf("enlisted %d times: %s", n, resp.Text)
	}
	standAtWorkplace(t, w.pool, companyRegistry(t, w.pool), soldier.ID, stagingCity, "armed_forces")

	// --- 2. Duty the fund cannot pay does not start; then it is paid. -----
	resp, err = w.jobs.Work(ctx, w.meta(soldier, "job.work"))
	said(t, "duty with an empty fund", resp, err, "job.army_cannot_pay")
	if n := countRows(t, w.pool, `SELECT count(*) FROM shift_sessions WHERE player_id = $1::uuid`, soldier.ID); n != 0 {
		t.Fatal("a duty the fund could not pay started")
	}
	fund, err := w.ledger.AccountFor(ctx, application.AccountDefenceFund, w.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ledger.Post(ctx, application.LedgerTransaction{Reason: application.ReasonAdminGrant,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-100_000)},
			{AccountID: fund.ID, Amount: money.FromMinor(100_000)},
		}}); err != nil {
		t.Fatal(err)
	}
	resp, err = w.jobs.Work(ctx, w.meta(soldier, "job.work"))
	said(t, "duty", resp, err)
	var sessionID string
	var endsAt time.Time
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, ends_at FROM shift_sessions WHERE player_id = $1::uuid AND status = 'working'`,
		soldier.ID).Scan(&sessionID, &endsAt); err != nil {
		t.Fatalf("the duty did not start: %v (%s)", err, resp.Text)
	}
	w.clock.Advance(endsAt.Sub(w.clock.Now()) + time.Second)
	finish := handlers.FinishShiftRequest{ActorID: soldier.ID, ReferenceType: "shift_sessions", ReferenceID: sessionID}
	for range 2 {
		if _, err := w.jobs.FinishShift(ctx, w.scheduler("job.finish_shift"), finish); err != nil {
			t.Fatalf("finish the duty: %v", err)
		}
	}
	var gross int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT gross FROM work_shifts WHERE id = $1::uuid`, sessionID).Scan(&gross); err != nil {
		t.Fatal(err)
	}
	after, err := w.ledger.AccountFor(ctx, application.AccountDefenceFund, w.home)
	if err != nil {
		t.Fatal(err)
	}
	if gross <= 0 || 100_000-after.Balance.Minor() != gross {
		t.Fatalf("the duty paid %d, the fund lost %d; want one wage from the fund", gross, 100_000-after.Balance.Minor())
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
	  WHERE e.reason = 'military_wage' AND e.reference_id = $1::uuid AND a.kind = 'defence_fund'`, sessionID); n != 1 {
		t.Fatalf("military wages paid for the duty = %d, want 1", n)
	}

	// --- 3. Rise to captain. ----------------------------------------------
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET level = 12 WHERE player_id = $1::uuid`, soldier.ID); err != nil {
		t.Fatal(err)
	}
	w.skill(soldier, "logistics", 4)
	w.skill(soldier, "management", 6)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'university', place_since = now() WHERE id = $1::uuid`,
		soldier.ID); err != nil {
		t.Fatal(err)
	}
	resp, err = w.edu.Enroll(ctx, w.meta(soldier, "education.enroll"), handlers.CourseRequest{Course: "officer_training", Method: "cash"})
	said(t, "officer training", resp, err)
	var enrollmentID string
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text FROM enrollments WHERE player_id = $1::uuid AND status = 'in_progress'`,
		soldier.ID).Scan(&enrollmentID); err != nil {
		t.Fatalf("no enrolment: %v (%s)", err, resp.Text)
	}
	w.clock.Advance(13 * time.Hour)
	if _, err := w.edu.Complete(ctx, w.scheduler("education.complete"), handlers.CompleteCourseRequest{ActorID: soldier.ID,
		ReferenceType: "enrollments", ReferenceID: enrollmentID}); err != nil {
		t.Fatal(err)
	}
	for tier := 1; tier <= 3; tier++ {
		if _, err := w.pool.Raw().Exec(ctx, `UPDATE employments SET performance = 100, shifts_in_tier = 50,
		  tier_since = $2 WHERE player_id = $1::uuid AND ended_at IS NULL`, soldier.ID, w.clock.Now().Add(-30*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		resp, err = w.jobs.Promote(ctx, w.meta(soldier, "job.promote"))
		said(t, "promotion", resp, err)
		if n := countRows(t, w.pool, `SELECT count(*) FROM employments WHERE player_id = $1::uuid AND ended_at IS NULL AND tier = $2`,
			soldier.ID, tier); n != 1 {
			t.Fatalf("promotion to tier %d did not happen: %s", tier, resp.Text)
		}
	}

	// --- 4. A captain founds a defence company. ---------------------------
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = now() WHERE id = $1::uuid`,
		soldier.ID); err != nil {
		t.Fatal(err)
	}
	works := w.found(soldier, "aerospace", "Simorgh Works", "")
	if n := countRows(t, w.pool, `SELECT count(*) FROM defence_licences WHERE company_id = $1::uuid AND kind = 'manufacturer'
	  AND basis = 'rank' AND status = 'active' AND applied_by = $2::uuid`, works.id, soldier.ID); n != 1 {
		t.Fatal("the captain's company holds no licence on the rank")
	}

	w.verify()
}
