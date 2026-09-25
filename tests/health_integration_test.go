//go:build integration

// Integration tests of health, hospitals and medicine (migration 0023,
// docs/adr/0023-health-missions-factions.md), through the real handlers,
// unit of work, ledger and item journal, on the game clock the game ships
// with.
//
//	A pharmaceutical lab buys herb extract and starch, designs a bandage,
//	makes a batch and lists it; a pharmacy buys from the lab and lists the
//	bandages to players; a player buys one and uses it, and is better for
//	it.
//
//	A clinic buys bandages from the lab, sets its price and opens. A thief
//	fails a pickpocketing, is hurt and admitted to the city hospital: crime
//	waits. They are treated at the clinic — paid once for a double press,
//	one bandage used, the stay cut short — a second treatment is refused,
//	the stale discharge does nothing, the new one discharges them once, and
//	their health comes back at rest on the game clock.
package tests

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// healthCity is where the health scenarios run: a city no other test
// founds companies in.
const healthCity = "vantor_reach"

// healthWorld is one scenario's city, clock and handlers.
type healthWorld struct {
	t        *testing.T
	pool     *postgres.Pool
	registry *content.Registry
	city     *application.City
	uow      application.UnitOfWork
	ledger   *postgres.LedgerRepository

	mu    sync.Mutex
	clock time.Time

	companies *handlers.CompaniesHandler
	prod      *handlers.ProductionHandler
}

func (w *healthWorld) now() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.clock }

func (w *healthWorld) advance(d time.Duration) { w.mu.Lock(); w.clock = w.clock.Add(d); w.mu.Unlock() }

func newHealthWorld(t *testing.T) *healthWorld {
	t.Helper()
	pool := requirePostgres(t)
	requireLedger(t, pool)
	requireStageE(t, pool)
	registry := companyRegistry(t, pool)
	snap := registry.Current()
	if _, ok := snap.Health(); !ok {
		t.Skip("the active content has no health; run `admin content load`")
	}
	if _, _, ok := snap.CompanyType("pharma"); !ok {
		t.Skip("the active content has no pharmaceutical labs; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, healthCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", healthCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", healthCity, n)
	}
	w := &healthWorld{t: t, pool: pool, registry: registry, city: city, clock: time.Now().UTC(),
		uow: postgres.NewUnitOfWork(pool, testDefaultLanguage), ledger: postgres.NewLedgerRepository(pool)}
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	w.companies = handlers.NewCompaniesHandler(w.uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits}, time.Hour, w.now)
	w.prod = handlers.NewProductionHandler(w.uow, workIDs{t}, nil, registry, cities, policy, gameScale, handlers.ProductionRules{
		MaxRunningOrders: 3, MaxDesigns: 20, MaxListings: 10, DesignMinSkill: 1, ReverseTime: 6 * time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits}, time.Hour, w.now)
	return w
}

// resident is a player of the city standing at city hall, with cash.
func (w *healthWorld) resident(cash int64) *application.Player {
	w.t.Helper()
	p := crimePlayer(w.t, w.pool, w.city.ID, w.now())
	w.t.Cleanup(func() { purgeWorkFor(w.t, w.pool, p.ID) })
	if _, err := w.pool.Raw().Exec(testCtx(w.t),
		`UPDATE players SET place_code = 'city_hall', place_since = now(), language = 'en' WHERE id = $1::uuid`, p.ID); err != nil {
		w.t.Fatal(err)
	}
	p.Language = "en"
	if cash > 0 {
		grantCash(w.t, w.pool, p.ID, cash)
	}
	return p
}

// skill sets a player's level of a skill.
func (w *healthWorld) skill(p *application.Player, code string, level int) {
	w.t.Helper()
	if _, err := w.pool.Raw().Exec(testCtx(w.t),
		`INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at) VALUES (gen_random_uuid(), $1::uuid, $2, $3, 0, now())
		 ON CONFLICT (player_id, skill_code) DO UPDATE SET level = EXCLUDED.level`, p.ID, code, level); err != nil {
		w.t.Fatal(err)
	}
}

func (w *healthWorld) meta(p *application.Player, command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
	m.IdempotencyKey = "it-" + randomToken(w.t, 16)
	return m
}

func (w *healthWorld) scheduler(command string) envelope.Metadata {
	m := validMeta(w.t)
	m.TelegramUserID, m.Command = 0, command
	return m
}

func (w *healthWorld) ok(what string, resp *presenter.Response, err error, want ...string) *presenter.Response {
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
	if resp == nil {
		w.t.Fatalf("%s: no response", what)
	}
	for _, s := range want {
		if !strings.Contains(resp.Text, s) {
			w.t.Fatalf("%s: %q does not say %q", what, resp.Text, s)
		}
	}
	return resp
}

// stock is what a company holds of a good in its warehouse.
func (w *healthWorld) stock(companyID, code string) int64 {
	var q int64
	_ = w.pool.Raw().QueryRow(testCtx(w.t), `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks
	  WHERE org_kind = 'company' AND org_id = $1::uuid AND item_code = $2 AND holding = 'warehouse'`, companyID, code).Scan(&q)
	return q
}

func (w *healthWorld) balance(kind application.AccountKind, owner string) int64 {
	acct, err := w.ledger.AccountFor(testCtx(w.t), kind, owner)
	if err != nil {
		w.t.Fatal(err)
	}
	return acct.Balance.Minor()
}

type healthCo struct{ id, code string }

// found founds a company of a kind and puts money in it.
func (w *healthWorld) found(p *application.Player, kind, name string) healthCo {
	w.t.Helper()
	ctx := testCtx(w.t)
	resp, err := w.companies.Found(ctx, w.meta(p, "company.found"), handlers.CompanyRequest{Type: kind, Method: "cash", Name: name})
	w.ok("found "+name, resp, err)
	var c healthCo
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
		p.ID, name).Scan(&c.id, &c.code); err != nil {
		w.t.Fatalf("company %s was not founded: %v (%s)", name, err, resp.Text)
	}
	resp, err = w.companies.Deposit(ctx, w.meta(p, "company.deposit"), handlers.CompanyRequest{Company: c.code, Method: "cash",
		Amount: "200000"})
	w.ok("deposit into "+name, resp, err)
	return c
}

// listingNo is the open listing of a good of a company.
func (w *healthWorld) listingNo(companyID, code string) string {
	w.t.Helper()
	var n int64
	if err := w.pool.Raw().QueryRow(testCtx(w.t), `SELECT no FROM company_listings WHERE company_id = $1::uuid AND item_code = $2
	  AND status = 'open' ORDER BY no DESC LIMIT 1`, companyID, code).Scan(&n); err != nil {
		w.t.Fatalf("no listing of %s: %v", code, err)
	}
	return strconv.FormatInt(n, 10)
}

// medicineLab founds a pharmaceutical lab for owner, which buys its
// ingredients, designs a bandage, makes a batch of them and lists qty at
// price. It returns the lab.
func (w *healthWorld) medicineLab(owner *application.Player, qty int64, price string) healthCo {
	w.t.Helper()
	ctx := testCtx(w.t)
	w.skill(owner, "medicine", 10)
	lab := w.found(owner, "pharma", "Shafa Labs")
	for _, comp := range []string{"herb_extract", "starch"} {
		resp, err := w.prod.Supply(ctx, w.meta(owner, "company.supply"), handlers.ProductionRequest{Company: lab.code,
			Component: comp, Qty: "400"})
		w.ok("buy "+comp, resp, err, "production.supply_bought")
	}
	resp, err := w.prod.DesignNew(ctx, w.meta(owner, "company.dnew"), handlers.ProductionRequest{Company: lab.code, Item: "bandage"})
	w.ok("new bandage design", resp, err)
	var designNo int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT no FROM product_designs WHERE company_id = $1::uuid`, lab.id).Scan(&designNo); err != nil {
		w.fail(err, resp)
	}
	no := strconv.FormatInt(designNo, 10)
	for slot, comp := range map[string]string{"active": "herb_extract", "carrier": "starch"} {
		resp, err = w.prod.DesignFill(ctx, w.meta(owner, "company.dfill"), handlers.ProductionRequest{No: no, Slot: slot, Component: comp})
		w.ok("fill "+slot, resp, err)
	}
	resp, err = w.prod.DesignName(ctx, w.meta(owner, "company.dname"), handlers.ProductionRequest{No: no, Name: "Shafa Band"})
	w.ok("name the design", resp, err)
	resp, err = w.prod.DesignFinal(ctx, w.meta(owner, "company.dfinal"), handlers.ProductionRequest{No: no})
	w.ok("finalise the design", resp, err, "production.design_state.final")
	for w.stock(lab.id, "bandage") < qty {
		resp, err = w.prod.Produce(ctx, w.meta(owner, "company.produce"), handlers.ProductionRequest{Company: lab.code,
			Target: "d" + no, Qty: "5", Confirm: "yes"})
		w.ok("make bandages", resp, err, "production.placed")
		var (
			id, action string
			finish     time.Time
		)
		if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, finish_at FROM production_orders
		  WHERE company_id = $1::uuid ORDER BY no DESC LIMIT 1`, lab.id).Scan(&id, &action, &finish); err != nil {
			w.t.Fatal(err)
		}
		w.advance(finish.Sub(w.now()) + time.Second)
		for range 2 {
			if _, err := w.prod.Produced(ctx, w.scheduler("company.produced"), handlers.CrimeScheduledRequest{ActionID: action,
				ReferenceID: id}); err != nil {
				w.t.Fatalf("produced: %v", err)
			}
		}
	}
	resp, err = w.prod.Sell(ctx, w.meta(owner, "company.sell"), handlers.ProductionRequest{Company: lab.code, Target: "d" + no,
		Qty: strconv.FormatInt(qty, 10), Price: price})
	w.ok("list bandages", resp, err, "production.listing_notice.listed")
	return lab
}

func (w *healthWorld) fail(err error, resp *presenter.Response) {
	w.t.Helper()
	text := ""
	if resp != nil {
		text = resp.Text
	}
	w.t.Fatalf("%v (%s)", err, text)
}

func TestMedicineFromLabThroughPharmacyToPlayer(t *testing.T) {
	w := newHealthWorld(t)
	ctx := testCtx(t)
	chemist, pharmacist, buyer := w.resident(500_000), w.resident(500_000), w.resident(5_000)
	t.Cleanup(func() { purgeCompaniesOf(t, w.pool, w.city.ID, chemist.ID, pharmacist.ID) })
	t.Cleanup(func() { purgeProductionOf(t, w.pool, w.city.ID, chemist.ID, pharmacist.ID) })
	t.Cleanup(func() { purgeStageE(t, w.pool, chemist.ID, pharmacist.ID, buyer.ID) })

	// The lab makes and lists bandages.
	lab := w.medicineLab(chemist, 6, "90")

	// The pharmacy buys four for the shop, paid once from its treasury.
	pharmacy := w.found(pharmacist, "pharmacy", "Darou Khaneh")
	before := w.balance(application.AccountCompanyTreasury, pharmacy.id)
	press := w.meta(pharmacist, "company.buy")
	for range 2 {
		resp, err := w.prod.Buy(ctx, press, handlers.ProductionRequest{No: w.listingNo(lab.id, "bandage"), Qty: "4",
			Method: pharmacy.code})
		w.ok("the pharmacy buys bandages", resp, err)
	}
	if got := w.stock(pharmacy.id, "bandage"); got != 4 {
		t.Fatalf("the pharmacy holds %d bandages, want 4 once", got)
	}
	if got := before - w.balance(application.AccountCompanyTreasury, pharmacy.id); got != 4*90 {
		t.Fatalf("the pharmacy paid %d, want %d once", got, 4*90)
	}

	// It lists them to players; a player buys one with cash.
	resp, err := w.prod.Sell(ctx, w.meta(pharmacist, "company.sell"), handlers.ProductionRequest{Company: pharmacy.code,
		Target: "bandage", Qty: "4", Price: "150"})
	w.ok("the pharmacy lists bandages", resp, err, "production.listing_notice.listed")
	cash := w.balance(application.AccountPlayerCash, buyer.ID)
	resp, err = w.prod.Buy(ctx, w.meta(buyer, "company.buy"), handlers.ProductionRequest{No: w.listingNo(pharmacy.id, "bandage"),
		Qty: "1", Method: "cash"})
	w.ok("a player buys a bandage", resp, err)
	if got := cash - w.balance(application.AccountPlayerCash, buyer.ID); got != 150 {
		t.Fatalf("the player paid %d, want 150", got)
	}
	var held int64
	_ = w.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks
	  WHERE player_id = $1::uuid AND item_code = 'bandage'`, buyer.ID).Scan(&held)
	if held != 1 {
		t.Fatalf("the player holds %d bandages, want 1", held)
	}

	// Hurt a little, the player uses it and is better for it.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET health = 50 WHERE player_id = $1::uuid`, buyer.ID); err != nil {
		t.Fatal(err)
	}
	bag := handlers.NewInventoryHandler(w.uow, workIDs{t}, nil, w.registry, postgres.NewCityRepository(w.pool), gameScale,
		crimeRules().Nerve, 10, time.Hour, w.now)
	if _, err := bag.Use(ctx, w.meta(buyer, "inventory.use"), handlers.ItemRequest{Item: "bandage", Nonce: randomToken(t, 12)}); err != nil {
		t.Fatalf("use a bandage: %v", err)
	}
	var hp int
	if err := w.pool.Raw().QueryRow(ctx, `SELECT health FROM player_stats WHERE player_id = $1::uuid`, buyer.ID).Scan(&hp); err != nil {
		t.Fatal(err)
	}
	if hp != 60 {
		t.Fatalf("after a bandage the player's health is %d, want 60", hp)
	}
	_ = w.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks
	  WHERE player_id = $1::uuid AND item_code = 'bandage'`, buyer.ID).Scan(&held)
	if held != 0 {
		t.Fatalf("the used bandage is still held (%d)", held)
	}
	verifyLedger(t, w.pool)
}

func TestInjuryTreatedAtClinicAndRecovered(t *testing.T) {
	w := newHealthWorld(t)
	ctx := testCtx(t)
	snap := w.registry.Current()
	pick, ok := snap.CrimeDef("pickpocketing")
	if !ok || pick.Failure.Injury == nil {
		t.Skip("the active content's pickpocketing does not hurt; run `admin content load`")
	}
	restoreNPCProceeds(t, w.pool)
	chemist, doctor, patient := w.resident(500_000), w.resident(500_000), w.resident(20_000)
	t.Cleanup(func() { purgeCompaniesOf(t, w.pool, w.city.ID, chemist.ID, doctor.ID) })
	t.Cleanup(func() { purgeProductionOf(t, w.pool, w.city.ID, chemist.ID, doctor.ID) })
	t.Cleanup(func() { purgeStageE(t, w.pool, chemist.ID, doctor.ID, patient.ID) })

	// The clinic stocks three bandages from the lab, prices a treatment and
	// opens.
	lab := w.medicineLab(chemist, 5, "90")
	w.skill(doctor, "medicine", 10)
	clinic := w.found(doctor, "clinic", "Darman Clinic")
	resp, err := w.prod.Buy(ctx, w.meta(doctor, "company.buy"), handlers.ProductionRequest{No: w.listingNo(lab.id, "bandage"),
		Qty: "3", Method: clinic.code})
	w.ok("the clinic buys bandages", resp, err)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := bank.NewLimits(1, 1_000_000_000)
	cities := postgres.NewCityRepository(w.pool)
	hosp := handlers.NewHealthHandler(w.uow, workIDs{t}, catalog, w.registry, cities, gameScale, limits, time.Hour, w.now)
	resp, err = hosp.Price(ctx, w.meta(doctor, "health.price"), handlers.HealthRequest{Company: clinic.code, Price: "2000"})
	w.ok("price a treatment", resp, err)
	resp, err = hosp.Open(ctx, w.meta(doctor, "health.open"), handlers.HealthRequest{Company: clinic.code, On: "on"})
	w.ok("open the clinic", resp, err)

	// The thief, weak already, fails a pickpocketing and is hurt: every
	// roll the worst (an NPC mark, a failure, an escape), and an id the
	// injury rolls on.
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET health = 20 WHERE player_id = $1::uuid`, patient.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'bazaar' WHERE id = $1::uuid`, patient.ID); err != nil {
		t.Fatal(err)
	}
	dice := &crimeDice{}
	crimes := handlers.NewCrimeHandler(w.uow, hurtingIDs{t, pick.Failure.Injury.Injury()}, catalog, w.registry, cities,
		postgres.NewPolicyReader(w.pool, nil), gameScale, dice, crimeRules(), time.Hour, w.now)
	dice.script(9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999)
	resp, err = crimes.Commit(ctx, w.meta(patient, "crime.commit"), handlers.CrimeCommitRequest{Crime: "pickpocketing"})
	w.ok("a failed pickpocketing", resp, err)
	var (
		stayID, action, place string
		endsAt                time.Time
		healthIn              int
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, ends_at, health_in FROM hospital_stays
	  WHERE player_id = $1::uuid AND status = 'admitted'`, patient.ID).Scan(&stayID, &action, &endsAt, &healthIn); err != nil {
		t.Fatalf("the hurt thief was not admitted: %v (%s)", err, resp.Text)
	}
	if healthIn < 1 || healthIn >= 20 {
		t.Errorf("admitted with health %d, want 1..19", healthIn)
	}
	_ = w.pool.Raw().QueryRow(ctx, `SELECT COALESCE(place_code, '') FROM players WHERE id = $1::uuid`, patient.ID).Scan(&place)
	if place != "hospital" {
		t.Errorf("the patient lies at %q, want the hospital", place)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'stay_id' = $2`,
		subjects.Event("health", "hospitalised"), stayID); n != 1 {
		t.Errorf("health.hospitalised events = %d, want 1", n)
	}
	// In hospital, crime waits.
	resp, err = crimes.Commit(ctx, w.meta(patient, "crime.commit"), handlers.CrimeCommitRequest{Crime: "pickpocketing"})
	if !errors.Is(err, application.ErrHospitalised) && (resp == nil || !strings.Contains(resp.Text, "hospital")) {
		t.Errorf("a crime from a hospital bed = %v, %v; want it refused", resp, err)
	}
	resp, err = hosp.Hospital(ctx, w.meta(patient, "health.hospital"))
	w.ok("the hospital screen", resp, err, "You are in hospital", "Darman Clinic")

	// Treated at the clinic: a double press pays once and uses one bandage.
	cash := w.balance(application.AccountPlayerCash, patient.ID)
	till := w.balance(application.AccountCompanyTreasury, clinic.id)
	press := w.meta(patient, "health.treat")
	for range 2 {
		resp, err = hosp.Treat(ctx, press, handlers.HealthRequest{Provider: clinic.code, Method: "cash"})
		w.ok("treated at the clinic", resp, err)
	}
	if got := cash - w.balance(application.AccountPlayerCash, patient.ID); got != 2000 {
		t.Fatalf("the patient paid %d, want 2000 once", got)
	}
	if got := w.balance(application.AccountCompanyTreasury, clinic.id) - till; got != 2000 {
		t.Fatalf("the clinic earned %d, want 2000 once", got)
	}
	if got := w.stock(clinic.id, "bandage"); got != 2 {
		t.Fatalf("the clinic holds %d bandages after one treatment, want 2", got)
	}
	var (
		newAction string
		newEnds   time.Time
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT game_action_id::text, ends_at FROM hospital_stays WHERE id = $1::uuid`, stayID).
		Scan(&newAction, &newEnds); err != nil {
		t.Fatal(err)
	}
	if !newEnds.Before(endsAt) || newAction == action {
		t.Fatalf("the treatment did not shorten the stay: ends %s (was %s), action %s (was %s)", newEnds, endsAt, newAction, action)
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM hospital_treatments WHERE stay_id = $1::uuid AND provider = 'clinic'
	  AND medicine_units = 1 AND price = 2000`, stayID); n != 1 {
		t.Fatalf("treatments = %d, want one at the clinic", n)
	}
	resp, err = hosp.Treat(ctx, w.meta(patient, "health.treat"), handlers.HealthRequest{Provider: "city", Method: "cash"})
	w.ok("a second treatment", resp, err, "already treated")

	// The discharge the treatment replaced does nothing; the new one
	// discharges once.
	w.advance(newEnds.Sub(w.now()) + time.Second)
	for _, a := range []string{action, newAction, newAction} {
		if _, err := hosp.Discharge(ctx, w.scheduler("health.discharge"), handlers.HealthScheduledRequest{ActionID: a,
			ActorID: patient.ID, ReferenceID: stayID}); err != nil {
			t.Fatalf("discharge: %v", err)
		}
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM hospital_stays WHERE id = $1::uuid AND status = 'discharged'`, stayID); n != 1 {
		t.Fatal("the stay was not discharged")
	}
	if n := countRows(t, w.pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'stay_id' = $2`,
		subjects.Event("health", "discharged"), stayID); n != 1 {
		t.Errorf("health.discharged events = %d, want 1", n)
	}
	resp, err = hosp.Hospital(ctx, w.meta(patient, "health.hospital"))
	w.ok("health at discharge", resp, err, "Health: 60 of 100", "not in hospital")

	// At rest the game clock gives health back: five game hours, 4 an hour.
	w.advance(5 * time.Hour / gameScale)
	resp, err = hosp.Hospital(ctx, w.meta(patient, "health.hospital"))
	w.ok("health after rest", resp, err, "Health: 80 of 100")
	verifyLedger(t, w.pool)
}
