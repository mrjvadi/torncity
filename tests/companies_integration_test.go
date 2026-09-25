//go:build integration

// Integration tests for player companies (migration 0019,
// docs/adr/0020-companies.md), through the real handlers, the real unit of
// work, the real ledger and the real policy resolver, on the game clock the
// game ships with: a company founded at city hall (a double press founds
// one), an opening posted, a player hired, a shift that cannot start while
// the company cannot pay it, the same shift paid from the company's account
// once it can, one city period settled once — NPC revenue bounded by the
// city's budget, the sales tax, the upkeep — and the owner taking profit out,
// taxed. The ledger's and the companies' invariants hold at the end.
//
// It needs the active content to carry the shipped companies: `admin content
// load` after migration 0019. It skips on a database where the test city
// already has companies, rather than settling a real city's period.
package tests

import (
	"context"
	"encoding/json"
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
)

// companyRegistry is the registry with the shipped company content, or a
// skip.
func companyRegistry(t *testing.T, pool *postgres.Pool) *content.Registry {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.companies') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("companies does not exist; apply migration 0019 first")
	}
	reg := workRegistry(t, pool)
	if len(reg.Current().CompanyTypes()) == 0 {
		t.Skip("the active content has no company types; run `admin content load`")
	}
	return reg
}

// purgeCompaniesOf removes every company the players own, with everything
// that hangs from them — their staff's jobs, shifts and payroll, their
// openings, applications and settled periods, their treasuries and every
// ledger transaction that touched one or the players — and the city's
// settlement clock.
func purgeCompaniesOf(t *testing.T, pool *postgres.Pool, cityID string, owners ...string) {
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
		{`CREATE TEMP TABLE purge_co ON COMMIT DROP AS SELECT id FROM companies WHERE owner_player_id = ANY($1::uuid[])`, owned},
		{`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`, nil},
		{`ALTER TABLE work_shifts DISABLE TRIGGER work_shifts_append_only`, nil},
		{`ALTER TABLE company_periods DISABLE TRIGGER company_periods_append_only`, nil},
		{`ALTER TABLE company_market_periods DISABLE TRIGGER company_market_periods_append_only`, nil},
		// Every transaction of the companies and of the players together:
		// a wage and the income tax withheld from it must go back as one.
		{`CREATE TEMP TABLE purge_ctx ON COMMIT DROP AS
		   SELECT DISTINCT e.transaction_id FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		    WHERE (a.kind = 'company_treasury' AND a.owner_id IN (SELECT id FROM purge_co))
		       OR a.owner_id = ANY($1::uuid[])`, owned},
		{`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries
		          WHERE transaction_id IN (SELECT transaction_id FROM purge_ctx) GROUP BY account_id) d
		  WHERE a.id = d.account_id`, nil},
		{`DELETE FROM ledger_entries WHERE transaction_id IN (SELECT transaction_id FROM purge_ctx)`, nil},
		{`DELETE FROM accounts WHERE kind = 'company_treasury' AND owner_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM work_shifts WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM shift_sessions WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM employments WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM company_applications WHERE company_id IN (SELECT id FROM purge_co) OR player_id = ANY($1::uuid[])`, owned},
		{`DELETE FROM company_openings WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM company_periods WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM company_shareholders WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM outbox WHERE payload->>'company_id' IN (SELECT id::text FROM purge_co)`, nil},
		{`DELETE FROM outbox WHERE payload->>'company_code' IN (SELECT code FROM companies WHERE id IN (SELECT id FROM purge_co))`, nil},
		{`DELETE FROM defence_licences WHERE company_id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM companies WHERE id IN (SELECT id FROM purge_co)`, nil},
		{`DELETE FROM company_market_periods WHERE city_id = $1::uuid`, city},
		{`UPDATE company_markets SET action_id = NULL, next_at = NULL WHERE city_id = $1::uuid`, city},
		{`DELETE FROM game_actions WHERE action_type = 'company_period' AND reference_id = $1::uuid`, city},
		{`DELETE FROM company_markets WHERE city_id = $1::uuid`, city},
		{`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`, nil},
		{`ALTER TABLE work_shifts ENABLE TRIGGER work_shifts_append_only`, nil},
		{`ALTER TABLE company_periods ENABLE TRIGGER company_periods_append_only`, nil},
		{`ALTER TABLE company_market_periods ENABLE TRIGGER company_market_periods_append_only`, nil},
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

func TestCompanyEndToEnd(t *testing.T) {
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
			`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid WHERE id = $1::uuid`, p.ID, city.ID); err != nil {
			t.Fatal(err)
		}
	}
	// The owner stands at city hall, where companies are registered.
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_hall', place_since = now() WHERE id = $1::uuid`,
		owner.ID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, owner.ID, 100_000)

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	setClock := func(at time.Time) { clockMu.Lock(); clock = at; clockMu.Unlock() }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	rules := handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2, NameMin: 3, NameMax: 24, FoundingShares: 1000,
		InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5, PriceStepBPS: 1000, Limits: limits}
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, rules, time.Hour, now)
	jobs := handlers.NewJobsHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, 5, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)
	balance := func(kind application.AccountKind, ownerID string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), kind, ownerID)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = p.TelegramUserID
		m.Command = command
		m.Language = "en"
		return m
	}
	treasuryBefore := balance(application.AccountCityTreasury, city.ID)

	// A name that impersonates the city is refused, and nothing is paid.
	resp, err := companies.Found(ctx, metaAs(owner, "company.found"), handlers.CompanyRequest{
		Type: "grocery", Method: "cash", Name: "City Hall Foods"})
	if err != nil || resp == nil || !strings.Contains(resp.Text, "company.refused.name_reserved") {
		t.Fatalf("a reserved name = %v, %v; want the refusal", resp, err)
	}

	// Found: the same press delivered twice, at once, founds one company
	// and pays one fee.
	found := metaAs(owner, "company.found")
	req := handlers.CompanyRequest{Type: "grocery", Method: "cash", Name: "Kaveh  Bakery"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := companies.Found(context.Background(), found, req)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Found: %v", err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE owner_player_id = $1::uuid`, owner.ID); n != 1 {
		t.Fatalf("companies founded = %d, want exactly 1", n)
	}
	var (
		companyID, code, name string
		fee                   int64
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code, name, registration_fee FROM companies WHERE owner_player_id = $1::uuid`,
		owner.ID).Scan(&companyID, &code, &name, &fee); err != nil {
		t.Fatal(err)
	}
	if name != "Kaveh Bakery" || fee <= 0 {
		t.Fatalf("company %q with fee %d; want the cleaned name and a fee", name, fee)
	}
	if got := balance(application.AccountPlayerCash, owner.ID); got != 100_000-fee {
		t.Fatalf("owner's cash = %d, want %d: one fee", got, 100_000-fee)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != fee {
		t.Fatalf("treasury grew by %d, want the fee %d", got, fee)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM company_shareholders WHERE company_id = $1::uuid AND player_id = $2::uuid AND shares = 1000`,
		companyID, owner.ID); n != 1 {
		t.Fatal("the founder does not hold every share")
	}
	var (
		periodNo int64
		actionID string
		nextAt   time.Time
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM company_markets WHERE city_id = $1::uuid`,
		city.ID).Scan(&periodNo, &actionID, &nextAt); err != nil {
		t.Fatalf("the city's settlement clock did not start: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'company_id' = $2`,
		subjects.Event("company", "founded"), companyID); n != 1 {
		t.Errorf("founded events = %d, want 1 for the group line", n)
	}
	// The same name again in the city is taken.
	resp, err = companies.Found(ctx, metaAs(owner, "company.found"), handlers.CompanyRequest{
		Type: "restaurant", Method: "cash", Name: "kaveh bakery."})
	if err != nil || resp == nil || !strings.Contains(resp.Text, "company.refused.name_taken") {
		t.Fatalf("a taken name = %v, %v; want the refusal", resp, err)
	}

	// An opening, and the worker applies to it and is accepted.
	if _, err := companies.Post(ctx, metaAs(owner, "company.post"), handlers.CompanyRequest{
		Company: code, Career: "retail", Wage: "150"}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	var openingNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_openings WHERE company_id = $1::uuid`, companyID).Scan(&openingNo); err != nil {
		t.Fatalf("no opening: %v", err)
	}
	list, err := jobs.List(ctx, metaAs(worker, "job.list"), handlers.PageRequest{})
	if err != nil || !strings.Contains(list.Text, "company.job_line") {
		t.Fatalf("job openings = %v, %v; want the company's opening beside the base employer's", list, err)
	}
	no := strconvI(openingNo)
	if _, err := companies.Apply(ctx, metaAs(worker, "company.apply"), handlers.CompanyRequest{No: no}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var appNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM company_applications WHERE player_id = $1::uuid AND status = 'pending'`,
		worker.ID).Scan(&appNo); err != nil {
		t.Fatalf("no pending application: %v", err)
	}
	decide := metaAs(owner, "company.decide")
	for range 2 { // a second press of the same button decides nothing more
		if _, err := companies.Decide(ctx, decide, handlers.CompanyRequest{No: strconvI(appNo), Verdict: "yes"}); err != nil {
			t.Fatalf("Decide: %v", err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM employments WHERE player_id = $1::uuid AND company_id = $2::uuid AND ended_at IS NULL AND rate = 150`,
		worker.ID, companyID); n != 1 {
		t.Fatalf("company jobs = %d, want the worker hired once at 150", n)
	}
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'bazaar', place_since = now() WHERE id = $1::uuid`, worker.ID); err != nil {
		t.Fatal(err)
	}

	// The company holds nothing: the shift cannot start, and costs nothing.
	resp, err = jobs.Work(ctx, metaAs(worker, "job.work"))
	if err != nil || resp == nil || !strings.Contains(resp.Text, "company.refused.cannot_pay") {
		t.Fatalf("a shift at a company that cannot pay = %v, %v; want the refusal", resp, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM shift_sessions WHERE player_id = $1::uuid`, worker.ID); n != 0 {
		t.Fatal("a shift started that the company could not pay")
	}

	// The owner puts money in; now the shift starts and sets its wage aside.
	if _, err := companies.Deposit(ctx, metaAs(owner, "company.deposit"), handlers.CompanyRequest{
		Company: code, Method: "cash", Amount: "20000"}); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	companyBalance := func() int64 { return balance(application.AccountCompanyTreasury, companyID) }
	if got := companyBalance(); got != 20_000 {
		t.Fatalf("company balance = %d, want 20000", got)
	}
	if _, err := jobs.Work(ctx, metaAs(worker, "job.work")); err != nil {
		t.Fatalf("Work: %v", err)
	}
	var (
		sessionID string
		reserved  int64
		endsAt    time.Time
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, wage_reserved, ends_at FROM shift_sessions
		WHERE player_id = $1::uuid AND company_id = $2::uuid AND status = 'working'`, worker.ID, companyID).
		Scan(&sessionID, &reserved, &endsAt); err != nil {
		t.Fatalf("no shift working for the company: %v", err)
	}
	if reserved != 150 {
		t.Fatalf("reserved = %d, want the wage 150", reserved)
	}
	// The reserved wage is out of reach of a withdrawal.
	resp, err = companies.Withdraw(ctx, metaAs(owner, "company.withdraw"), handlers.CompanyRequest{Company: code, Amount: "19900"})
	if err != nil || resp == nil || !strings.Contains(resp.Text, "company.refused.not_enough") {
		t.Fatalf("withdrawing promised wages = %v, %v; want the refusal", resp, err)
	}

	// The shift ends: its wage comes from the company, once.
	setClock(endsAt.Add(time.Second))
	finish := handlers.FinishShiftRequest{ActorID: worker.ID, ReferenceType: "shift_sessions", ReferenceID: sessionID}
	for range 2 {
		m := metaAs(worker, "job.finish_shift")
		m.TelegramUserID = 0
		if _, err := jobs.FinishShift(ctx, m, finish); err != nil {
			t.Fatalf("FinishShift: %v", err)
		}
	}
	if got := companyBalance(); got != 20_000-150 {
		t.Fatalf("company balance after the shift = %d, want %d", got, 20_000-150)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		WHERE e.reason = 'company_wage' AND a.kind = 'company_treasury' AND a.owner_id = $1::uuid`, companyID); n != 1 {
		t.Fatalf("company wage legs = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM work_shifts WHERE id = $1::uuid AND company_id = $2::uuid AND gross = 150`,
		sessionID, companyID); n != 1 {
		t.Fatal("the payroll row does not name the company")
	}

	// The period ends: settled once however often it is delivered.
	setClock(nextAt.Add(time.Second))
	payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: city.ID, PeriodNo: periodNo})
	settle := handlers.CrimeScheduledRequest{ActionID: actionID, ReferenceType: application.CompanyMarketReference,
		ReferenceID: city.ID, Payload: payload}
	treasuryMid := balance(application.AccountCityTreasury, city.ID)
	serrs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := validMeta(t)
			m.TelegramUserID, m.Command = 0, "company.settle"
			_, err := companies.Settle(context.Background(), m, settle)
			serrs <- err
		}()
	}
	wg.Wait()
	close(serrs)
	for err := range serrs {
		if err != nil {
			t.Fatalf("concurrent Settle: %v", err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM company_market_periods WHERE city_id = $1::uuid`, city.ID); n != 1 {
		t.Fatalf("settled periods = %d, want exactly 1", n)
	}
	var (
		budget, paid                              int64
		revenue, salesTax, upkeepPaid, shifts, cp int64
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT budget, paid FROM company_market_periods WHERE city_id = $1::uuid`, city.ID).
		Scan(&budget, &paid); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT revenue, sales_tax, upkeep_paid, shifts, capacity_units FROM company_periods
		WHERE company_id = $1::uuid`, companyID).Scan(&revenue, &salesTax, &upkeepPaid, &shifts, &cp); err != nil {
		t.Fatal(err)
	}
	if paid <= 0 || paid > budget || revenue != paid || shifts != 1 {
		t.Fatalf("period paid %d of budget %d, company revenue %d with %d shifts; want revenue from the one shift, within budget",
			paid, budget, revenue, shifts)
	}
	if revenue > cp*12 {
		t.Fatalf("revenue %d beyond capacity %d units at the unit price", revenue, cp)
	}
	if upkeepPaid != 500 {
		t.Fatalf("upkeep paid = %d, want the grocery's 500", upkeepPaid)
	}
	want := int64(20_000-150) + revenue - salesTax - upkeepPaid
	if got := companyBalance(); got != want {
		t.Fatalf("company balance after the period = %d, want %d", got, want)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryMid; got != salesTax {
		t.Fatalf("treasury grew by %d over the period, want the sales tax %d", got, salesTax)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM company_markets WHERE city_id = $1::uuid AND period_no = $2 AND action_id IS NOT NULL`,
		city.ID, periodNo+1); n != 1 {
		t.Fatal("the next period was not scheduled")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'company_id' = $2`,
		subjects.Event("company", "period_settled"), companyID); n != 1 {
		t.Errorf("period reports = %d, want 1", n)
	}

	// The owner takes profit out: the corporate tax to the city, the rest
	// to their bank.
	taxLever, err := policy.Get(ctx, city.JurisdictionID, handlers.LeverCorporateTax)
	if err != nil {
		t.Fatal(err)
	}
	before := companyBalance()
	treasuryBefore = balance(application.AccountCityTreasury, city.ID)
	if _, err := companies.Withdraw(ctx, metaAs(owner, "company.withdraw"), handlers.CompanyRequest{Company: code, Amount: "1000"}); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	tax := 1000 * taxLever.Value / 10_000
	if got := balance(application.AccountPlayerBank, owner.ID); got != 1000-tax {
		t.Fatalf("owner's bank = %d, want %d", got, 1000-tax)
	}
	if got := before - companyBalance(); got != 1000 {
		t.Fatalf("company paid out %d, want 1000", got)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != tax {
		t.Fatalf("treasury took %d of corporate tax, want %d", got, tax)
	}

	// Every invariant holds, the companies' among them.
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() || !v.Companies {
		t.Fatalf("economy invariants broken: %+v", v)
	}
}

func strconvI(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestCompanyInsolvencyAndClosing: a company nobody funds or staffs earns
// only its unstaffed trade, cannot pay its upkeep, owes the rest, and is
// dissolved after the configured periods in a row in debt — the city's
// clock then stops. A second company its owner closes pays its money out,
// the corporate tax taken. Nothing is ever overdrawn.
func TestCompanyInsolvencyAndClosing(t *testing.T) {
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
	t.Cleanup(func() { purgeLedgerFor(t, pool, owner.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, owner.ID) })
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid,
		place_code = 'city_hall', place_since = now() WHERE id = $1::uuid`, owner.ID, city.ID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, owner.ID, 200_000)

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	setClock := func(at time.Time) { clockMu.Lock(); clock = at; clockMu.Unlock() }
	limits, _ := bank.NewLimits(1, 1_000_000_000)
	rules := handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2, NameMin: 3, NameMax: 24, FoundingShares: 1000,
		InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5, PriceStepBPS: 1000, Limits: limits}
	policy := postgres.NewPolicyReader(pool, nil)
	companies := handlers.NewCompaniesHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil, registry,
		cities, policy, postgres.NewPlayerSearchRepository(pool), gameScale, rules, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)
	balance := func(kind application.AccountKind, ownerID string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), kind, ownerID)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	metaFor := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command, m.Language = owner.TelegramUserID, command, "en"
		return m
	}
	found := func(name string) (id, code string) {
		if _, err := companies.Found(ctx, metaFor("company.found"), handlers.CompanyRequest{
			Type: "grocery", Method: "cash", Name: name}); err != nil {
			t.Fatalf("Found: %v", err)
		}
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, code FROM companies WHERE owner_player_id = $1::uuid AND name = $2`,
			owner.ID, name).Scan(&id, &code); err != nil {
			t.Fatalf("no company %q: %v", name, err)
		}
		return id, code
	}
	settleNext := func() {
		var (
			no       int64
			actionID string
			next     time.Time
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT period_no, action_id::text, next_at FROM company_markets WHERE city_id = $1::uuid`,
			city.ID).Scan(&no, &actionID, &next); err != nil {
			t.Fatalf("no settlement scheduled: %v", err)
		}
		setClock(next.Add(time.Second))
		payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: city.ID, PeriodNo: no})
		m := validMeta(t)
		m.TelegramUserID, m.Command = 0, "company.settle"
		if _, err := companies.Settle(ctx, m, handlers.CrimeScheduledRequest{ActionID: actionID,
			ReferenceType: application.CompanyMarketReference, ReferenceID: city.ID, Payload: payload}); err != nil {
			t.Fatalf("Settle: %v", err)
		}
	}

	idle, _ := found("Idle Grocers")
	for period := 1; period <= 3; period++ {
		settleNext()
		var (
			debt    int64
			arrears int
			status  string
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT debt, arrears, status FROM companies WHERE id = $1::uuid`, idle).
			Scan(&debt, &arrears, &status); err != nil {
			t.Fatal(err)
		}
		if got := balance(application.AccountCompanyTreasury, idle); got < 0 {
			t.Fatalf("period %d: the company is overdrawn: %d", period, got)
		}
		switch {
		case period < 3 && (status != "active" || debt <= 0 || arrears != period):
			t.Fatalf("period %d: status %s, debt %d, arrears %d; want active and owing for %d periods", period, status, debt, arrears, period)
		case period == 3 && status != "dissolved":
			t.Fatalf("after 3 periods in debt the company is %s, want dissolved", status)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE id = $1::uuid AND close_reason = 'insolvent'`, idle); n != 1 {
		t.Fatal("the company was not dissolved for its debt")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM company_markets WHERE city_id = $1::uuid AND action_id IS NULL`, city.ID); n != 1 {
		t.Fatal("the city's clock kept running with no company left")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'company_id' = $2`,
		subjects.Event("company", "closed"), idle); n != 1 {
		t.Errorf("closed events = %d, want 1 for the group line", n)
	}

	// A company its owner closes: its money comes out, the tax to the city.
	closing, code := found("Closing Grocers")
	if _, err := companies.Deposit(ctx, metaFor("company.deposit"), handlers.CompanyRequest{
		Company: code, Method: "cash", Amount: "5000"}); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	taxLever, err := policy.Get(ctx, city.JurisdictionID, handlers.LeverCorporateTax)
	if err != nil {
		t.Fatal(err)
	}
	bankBefore := balance(application.AccountPlayerBank, owner.ID)
	confirm := metaFor("company.close")
	for range 2 { // the confirming press twice closes once
		if _, err := companies.Close(ctx, confirm, handlers.CompanyRequest{Company: code, Confirm: "yes"}); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	tax := 5000 * taxLever.Value / 10_000
	if got := balance(application.AccountPlayerBank, owner.ID) - bankBefore; got != 5000-tax {
		t.Fatalf("owner received %d on closing, want %d", got, 5000-tax)
	}
	if got := balance(application.AccountCompanyTreasury, closing); got != 0 {
		t.Fatalf("a closed company holds %d", got)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE id = $1::uuid AND status = 'dissolved' AND close_reason = 'closed'`,
		closing); n != 1 {
		t.Fatal("the company is not closed")
	}
	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() {
		t.Fatalf("economy invariants broken: %+v", v)
	}
}
