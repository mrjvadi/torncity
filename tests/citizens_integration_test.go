//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// TestCitizensFillUntakenOpenings: a company whose openings no player has
// taken is worked by the city's citizens, paid from its free money once per
// period; a player who is hired takes a citizen's place.
func TestCitizensFillUntakenOpenings(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := companyRegistry(t, pool)
	ctx := testCtx(t)

	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("ostmarch already has %d companies; run this test on a database without them", n)
	}

	owner := insertPlayer(t, pool)
	worker := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, owner.ID); purgeLedgerFor(t, pool, worker.ID) })
	t.Cleanup(func() { purgeWorkFor(t, pool, owner.ID); purgeWorkFor(t, pool, worker.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, owner.ID, worker.ID) })
	for _, p := range []*application.Player{owner, worker} {
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
			 WHERE id = $1::uuid`, p.ID, city.ID); err != nil {
			t.Fatal(err)
		}
	}
	grantCash(t, pool, owner.ID, 100_000)

	clock := time.Now().UTC()
	now := func() time.Time { return clock }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	rules := handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2, NameMin: 3, NameMax: 24, FoundingShares: 1000,
		InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5, PriceStepBPS: 1000, Limits: limits,
		Citizens: company.CitizenRules{ShiftsPerPeriod: 2, ProductivityBPS: 7000}, CitizenLabourShareBPS: 500}
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, rules, time.Hour, now)
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command, m.Language = p.TelegramUserID, command, "en"
		return m
	}

	if _, err := companies.Found(ctx, metaAs(owner, "company.found"), handlers.CompanyRequest{
		Type: "grocery", Method: "cash", Name: "Citizen Grocers"}); err != nil {
		t.Fatalf("Found: %v", err)
	}
	var companyID, code string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid`, owner.ID).
		Scan(&companyID, &code); err != nil {
		t.Fatalf("no company: %v", err)
	}
	// Two positions at 150 a shift, and money to pay them; no player applies.
	if _, err := companies.Post(ctx, metaAs(owner, "company.post"), handlers.CompanyRequest{
		Company: code, Career: "retail", Wage: "150"}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	var openingNo int64
	if err := pool.Raw().QueryRow(ctx, `UPDATE company_openings SET positions = 2 WHERE company_id = $1::uuid RETURNING no`,
		companyID).Scan(&openingNo); err != nil {
		t.Fatal(err)
	}
	if _, err := companies.Deposit(ctx, metaAs(owner, "company.deposit"), handlers.CompanyRequest{
		Company: code, Method: "cash", Amount: "20000"}); err != nil {
		t.Fatalf("Deposit: %v", err)
	}

	citizenWages := func() int64 {
		var sum int64
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(sum(e.amount), 0) FROM ledger_entries e
			JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'citizen_wage' AND a.kind = 'company_treasury' AND a.owner_id = $1::uuid`, companyID).
			Scan(&sum); err != nil {
			t.Fatal(err)
		}
		return -sum
	}
	settle := func() {
		t.Helper()
		var (
			periodNo int64
			actionID string
			nextAt   time.Time
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM company_markets WHERE city_id = $1::uuid`,
			city.ID).Scan(&periodNo, &actionID, &nextAt); err != nil {
			t.Fatalf("no settlement scheduled: %v", err)
		}
		clock = nextAt.Add(time.Second)
		payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: city.ID, PeriodNo: periodNo})
		req := handlers.CrimeScheduledRequest{ActionID: actionID, ReferenceType: application.CompanyMarketReference,
			ReferenceID: city.ID, Payload: payload}
		for range 2 { // delivered twice, settled once
			m := validMeta(t)
			m.TelegramUserID, m.Command = 0, "company.settle"
			if _, err := companies.Settle(context.Background(), m, req); err != nil {
				t.Fatalf("Settle: %v", err)
			}
		}
	}

	// Period 1: both positions are worked by citizens, 2 shifts each at 150.
	settle()
	if got := citizenWages(); got != 2*2*150 {
		t.Fatalf("citizen wages after period 1 = %d, want %d", got, 2*2*150)
	}
	var shifts, revenue int64
	if err := pool.Raw().QueryRow(ctx, `SELECT shifts, revenue FROM company_periods WHERE company_id = $1::uuid ORDER BY period_no DESC LIMIT 1`,
		companyID).Scan(&shifts, &revenue); err != nil {
		t.Fatal(err)
	}
	if shifts != 2 || revenue <= 0 {
		t.Fatalf("period 1 counted %d shifts with revenue %d; want 4 citizen shifts counting as 2, and sales", shifts, revenue)
	}

	// A player takes one position: period 2 has one citizen left.
	if _, err := companies.Apply(ctx, metaAs(worker, "company.apply"), handlers.CompanyRequest{No: strconvI(openingNo)}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var appNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_applications WHERE player_id = $1::uuid AND status = 'pending'`,
		worker.ID).Scan(&appNo); err != nil {
		t.Fatalf("no application: %v", err)
	}
	if _, err := companies.Decide(ctx, metaAs(owner, "company.decide"), handlers.CompanyRequest{No: strconvI(appNo), Verdict: "yes"}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	settle()
	if got := citizenWages(); got != 2*2*150+2*150 {
		t.Fatalf("citizen wages after period 2 = %d, want %d: one citizen made way for the player", got, 2*2*150+2*150)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM company_market_periods WHERE city_id = $1::uuid`, city.ID); n != 2 {
		t.Fatalf("settled periods = %d, want 2", n)
	}
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() || !v.Companies {
		t.Fatalf("ledger invariants broken: %+v", v)
	}
}
