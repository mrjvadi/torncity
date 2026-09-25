//go:build integration

package tests

// Integration test of specialist recruitment (migration 0031,
// docs/adr/0027-specialist-recruitment.md): a company that has no engineer
// posts a campaign in the cities of its country, specialists apply at a
// check (delivered twice, evaluated once), the owner hires one (signing
// bonus and move paid once), the research lab's skill gate passes, the
// specialist is paid once per period, and — the market for engineers dried
// up — leaves underpaid after the content's periods.

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

// recruitCity is the city the scenario's company is in: one no other
// company test settles.
const recruitCity = "brennhaven"

// purgeRecruitOf removes everything of recruitment that hangs from the
// companies of owners — payments, specialists, candidates, fees, campaigns
// and their scheduled checks — and the pools the scenario counted. The money
// is the companies' ledger, which purgeCompaniesOf takes back.
func purgeRecruitOf(t *testing.T, pool *postgres.Pool, owners ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	owned := any(owners)
	for _, step := range []struct {
		sql string
		arg any
	}{
		{`CREATE TEMP TABLE purge_rco ON COMMIT DROP AS SELECT id FROM companies WHERE owner_player_id = ANY($1::uuid[])`, owned},
		{`CREATE TEMP TABLE purge_rcamp ON COMMIT DROP AS
		   SELECT id FROM recruit_campaigns WHERE company_id IN (SELECT id FROM purge_rco)`, nil},
		{`DELETE FROM npc_staff_payments WHERE company_id IN (SELECT id FROM purge_rco)`, nil},
		{`DELETE FROM npc_staff WHERE company_id IN (SELECT id FROM purge_rco)`, nil},
		{`DELETE FROM recruit_candidates WHERE company_id IN (SELECT id FROM purge_rco)`, nil},
		{`DELETE FROM recruit_ad_fees WHERE campaign_id IN (SELECT id FROM purge_rcamp)`, nil},
		{`UPDATE recruit_campaigns SET action_id = NULL WHERE id IN (SELECT id FROM purge_rcamp)`, nil},
		{`DELETE FROM game_actions WHERE reference_type = 'recruit_campaigns'
		     AND reference_id IN (SELECT id FROM purge_rcamp)`, nil},
		{`DELETE FROM recruit_campaigns WHERE id IN (SELECT id FROM purge_rcamp)`, nil},
		{`DELETE FROM specialist_pools`, nil},
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

func TestSpecialistRecruitmentEndToEnd(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.npc_staff') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("npc_staff does not exist; apply migration 0031 first")
	}
	registry := companyRegistry(t, pool)
	def, ok := registry.Current().Recruitment()
	if !ok {
		t.Skip("the active content has no recruitment; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, recruitCity)
	if err != nil {
		t.Skipf("the shipped city %s is not loaded: %v", recruitCity, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM companies WHERE city_id = $1::uuid AND status = 'active'`, city.ID); n != 0 {
		t.Skipf("%s already has %d companies; run this test on a database without them", recruitCity, n)
	}

	owner := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, owner.ID); purgeWorkFor(t, pool, owner.ID) })
	t.Cleanup(func() { purgeCompaniesOf(t, pool, city.ID, owner.ID) })
	t.Cleanup(func() { purgeProductionOf(t, pool, city.ID, owner.ID) })
	t.Cleanup(func() { purgeRecruitOf(t, pool, owner.ID) })
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid, place_code = 'city_hall', place_since = now()
		  WHERE id = $1::uuid`, owner.ID, city.ID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, pool, owner.ID, 2_000_000)

	clock := time.Now().UTC()
	now := func() time.Time { return clock }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	citizens := company.CitizenRules{ShiftsPerPeriod: 2, ProductivityBPS: 7000}
	companies := handlers.NewCompaniesHandler(uow, workIDs{t}, nil, registry, cities, policy,
		postgres.NewPlayerSearchRepository(pool), gameScale, handlers.CompanyRules{Period: 24 * time.Hour, MaxPerPlayer: 2,
			NameMin: 3, NameMax: 24, FoundingShares: 1000, InsolvencyPeriods: 3, NPCCityPeriodCap: 50_000, MaxOpenings: 5,
			PriceStepBPS: 1000, Limits: limits, Citizens: citizens, CitizenLabourShareBPS: 500}, time.Hour, now)
	prod := handlers.NewProductionHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, handlers.ProductionRules{
		MaxRunningOrders: 3, MaxDesigns: 20, MaxListings: 10, DesignMinSkill: 1, ReverseTime: 6 * time.Hour,
		NameMin: 3, NameMax: 24, Limits: limits, Citizens: citizens}, time.Hour, now)
	recruit := handlers.NewRecruitHandler(uow, workIDs{t}, nil, registry, cities, gameScale, handlers.RecruitRules{
		CheckEvery: 6 * time.Hour, Checks: 4, MaxCampaigns: 2, MaxPositions: 5, MaxCandidates: 6, MaxStaff: 10,
		Patience: 24 * time.Hour, Period: 24 * time.Hour, Limits: limits}, time.Hour, now)
	press := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command, m.Language = owner.TelegramUserID, command, "en"
		return m
	}
	scheduler := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command = 0, command
		return m
	}
	credited := func(reason string) int64 {
		var v int64
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = $1 AND amount > 0 AND reference_type IN ('recruit_campaigns', 'npc_staff')
			  AND reference_id IN (SELECT id FROM recruit_campaigns WHERE company_id = $2::uuid
			                       UNION SELECT id FROM npc_staff WHERE company_id = $2::uuid)`, reason, companyIDOf(t, pool, owner.ID)).
			Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	// A technology studio in Brennhaven, with money in its account.
	if _, err := companies.Found(ctx, press("company.found"), handlers.CompanyRequest{Type: "tech_studio", Method: "cash",
		Name: "Brenn Labs"}); err != nil {
		t.Fatalf("Found: %v", err)
	}
	companyID := companyIDOf(t, pool, owner.ID)
	var code string
	if err := pool.Raw().QueryRow(ctx, `SELECT code FROM companies WHERE id = $1::uuid`, companyID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if _, err := companies.Deposit(ctx, press("company.deposit"), handlers.CompanyRequest{Company: code, Method: "cash",
		Amount: "900000"}); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	researching := func() int {
		return countRows(t, pool, `SELECT count(*) FROM company_research WHERE company_id = $1::uuid`, companyID)
	}

	// --- 1. Nobody in the company is an engineer: the lab refuses. -------
	if _, err := prod.Research(ctx, press("company.research"), handlers.ProductionRequest{Company: code,
		Tech: "semiconductors"}); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if researching() != 0 {
		t.Fatal("research started with no engineer in the company")
	}

	// --- 2. A campaign for engineering 3, nationwide, with a package. ----
	if _, err := recruit.New(ctx, press("company.rnew"), handlers.RecruitRequest{Company: code, Skill: "engineering",
		Level: "3"}); err != nil {
		t.Fatalf("New: %v", err)
	}
	var campaignNo int64
	var campaignID string
	if err := pool.Raw().QueryRow(ctx, `SELECT no, id::text FROM recruit_campaigns WHERE company_id = $1::uuid AND status = 'draft'`,
		companyID).Scan(&campaignNo, &campaignID); err != nil {
		t.Fatalf("no draft: %v", err)
	}
	no := strconvI(campaignNo)
	for _, set := range []handlers.RecruitRequest{
		{No: no, Field: "scope", Value: "nation"},
		{No: no, Field: "salary", Value: "2"},
		{No: no, Field: "signing", Value: "1"},
		{No: no, Field: "relocation", Value: "2"},
	} {
		if _, err := recruit.Set(ctx, press("company.rset"), set); err != nil {
			t.Fatalf("Set %s: %v", set.Field, err)
		}
	}
	var cities_ []string
	var salary, signing, relocation int64
	if err := pool.Raw().QueryRow(ctx, `SELECT cities, salary, signing, relocation FROM recruit_campaigns WHERE id = $1::uuid`,
		campaignID).Scan(&cities_, &salary, &signing, &relocation); err != nil {
		t.Fatal(err)
	}
	if len(cities_) < 2 || salary < 1 || signing != salary || relocation != def.Presets.Relocation[2] {
		t.Fatalf("draft cities %v salary %d signing %d relocation %d", cities_, salary, signing, relocation)
	}
	// Posted twice by one press: the advertising is paid once, per city.
	post := press("company.rpost")
	for range 2 {
		if _, err := recruit.Post(ctx, post, handlers.RecruitRequest{No: no, Confirm: "yes"}); err != nil {
			t.Fatalf("Post: %v", err)
		}
	}
	if got, want := credited("recruitment_ad"), def.AdFee*int64(len(cities_)); got != want {
		t.Fatalf("advertising fees = %d, want %d (one per city)", got, want)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM recruit_ad_fees WHERE campaign_id = $1::uuid`, campaignID); n != len(cities_) {
		t.Fatalf("fee rows = %d, want %d", n, len(cities_))
	}

	// --- 3. The first check, delivered twice: candidates once. ----------
	check := func() int {
		t.Helper()
		var (
			action string
			nextAt time.Time
			done   int
		)
		if err := pool.Raw().QueryRow(ctx, `SELECT action_id::text, next_check_at, checks_done FROM recruit_campaigns
			WHERE id = $1::uuid`, campaignID).Scan(&action, &nextAt, &done); err != nil {
			t.Fatalf("no check scheduled: %v", err)
		}
		clock = nextAt.Add(time.Second)
		payload, _ := json.Marshal(handlers.RecruitCheckPayload{CampaignID: campaignID, CheckNo: done + 1})
		req := handlers.CrimeScheduledRequest{ActionID: action, ReferenceType: application.RecruitCampaignReference,
			ReferenceID: campaignID, Payload: payload}
		for range 2 {
			if _, err := recruit.Check(context.Background(), scheduler("company.rcheck"), req); err != nil {
				t.Fatalf("Check: %v", err)
			}
		}
		return countRows(t, pool, `SELECT count(*) FROM recruit_candidates WHERE campaign_id = $1::uuid`, campaignID)
	}
	found := check()
	for tries := 1; found == 0 && tries < 4; tries++ {
		found = check()
	}
	if found == 0 {
		t.Fatal("no specialist applied to a generous nationwide offer in four checks")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM (SELECT check_no, seq FROM recruit_candidates WHERE campaign_id = $1::uuid
		GROUP BY check_no, seq HAVING count(*) > 1) d`, campaignID); n != 0 {
		t.Fatalf("%d check slots were filled twice", n)
	}

	// --- 4. The owner hires one: the bonus and the move paid once. ------
	var (
		candNo   int64
		level    int
		moveCost int64
		homeID   string
	)
	if err := pool.Raw().QueryRow(ctx, `SELECT no, level, move_cost, home_city_id::text FROM recruit_candidates
		WHERE campaign_id = $1::uuid AND status = 'pending' ORDER BY no LIMIT 1`, campaignID).
		Scan(&candNo, &level, &moveCost, &homeID); err != nil {
		t.Fatalf("no pending candidate: %v", err)
	}
	t.Logf("%d candidates applied; hiring No. %d, engineering level %d, whose move costs %d", found, candNo, level, moveCost)
	if level < 3 {
		t.Fatalf("a candidate of level %d applied to a campaign for level 3", level)
	}
	var available int64
	if err := pool.Raw().QueryRow(ctx, `SELECT available FROM specialist_pools WHERE city_id = $1::uuid AND skill = 'engineering'
		AND level = $2`, homeID, level).Scan(&available); err != nil {
		t.Fatal(err)
	}
	hire := press("company.rdecide")
	for range 2 {
		if _, err := recruit.Decide(ctx, hire, handlers.RecruitRequest{No: strconvI(candNo), Verdict: "yes"}); err != nil {
			t.Fatalf("Decide: %v", err)
		}
	}
	// A second press of the same button finds them hired already.
	if _, err := recruit.Decide(ctx, press("company.rdecide"), handlers.RecruitRequest{No: strconvI(candNo), Verdict: "yes"}); err != nil {
		t.Fatalf("Decide again: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM npc_staff WHERE company_id = $1::uuid`, companyID); n != 1 {
		t.Fatalf("specialists = %d, want 1", n)
	}
	if got := credited("specialist_signing"); got != signing {
		t.Fatalf("signing bonuses = %d, want %d", got, signing)
	}
	if got, want := credited("specialist_relocation"), min(relocation, moveCost); got != want {
		t.Fatalf("moves paid = %d, want %d", got, want)
	}
	var after int64
	if err := pool.Raw().QueryRow(ctx, `SELECT available FROM specialist_pools WHERE city_id = $1::uuid AND skill = 'engineering'
		AND level = $2`, homeID, level).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != available-1 {
		t.Fatalf("the pool holds %d after the hire, want %d", after, available-1)
	}

	// --- 5. The lab's skill gate now passes. ----------------------------
	if _, err := prod.Research(ctx, press("company.research"), handlers.ProductionRequest{Company: code,
		Tech: "semiconductors"}); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if researching() != 1 {
		t.Fatal("research did not start with a specialist engineer in the company")
	}

	// --- 6. Paid once per period; underpaid in a dry market, poached. ---
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
		if nextAt.After(clock) {
			clock = nextAt.Add(time.Second)
		}
		payload, _ := json.Marshal(handlers.CompanyPeriodPayload{CityID: city.ID, PeriodNo: periodNo})
		req := handlers.CrimeScheduledRequest{ActionID: actionID, ReferenceType: application.CompanyMarketReference,
			ReferenceID: city.ID, Payload: payload}
		for range 2 {
			if _, err := companies.Settle(context.Background(), scheduler("company.settle"), req); err != nil {
				t.Fatalf("Settle: %v", err)
			}
		}
	}
	var staffID string
	var due int64
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, salary + housing FROM npc_staff WHERE company_id = $1::uuid`, companyID).
		Scan(&staffID, &due); err != nil {
		t.Fatal(err)
	}
	settle()
	if n := countRows(t, pool, `SELECT count(*) FROM npc_staff_payments WHERE staff_id = $1::uuid`, staffID); n != 1 {
		t.Fatalf("payments after one period = %d, want 1", n)
	}
	if got := credited("specialist_salary"); got != due {
		t.Fatalf("pay after one period = %d, want %d", got, due)
	}
	status := func() (string, string, int) {
		var st, reason string
		var run int
		if err := pool.Raw().QueryRow(ctx, `SELECT status, COALESCE(leave_reason, ''), underpaid_run FROM npc_staff
			WHERE id = $1::uuid`, staffID).Scan(&st, &reason, &run); err != nil {
			t.Fatal(err)
		}
		return st, reason, run
	}
	if st, _, run := status(); st != "active" || run != 0 {
		t.Fatalf("after a fairly paid period: %s, underpaid %d", st, run)
	}
	// Every engineer of their city at their level is hired away: the market
	// for the rest rises beyond what they are paid.
	dry := func() {
		if _, err := pool.Raw().Exec(ctx, `UPDATE specialist_pools SET available = 0, refilled_at = $3
			WHERE city_id = $1::uuid AND skill = 'engineering' AND level = $2`, homeID, level, clock.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	dry()
	settle()
	if st, _, run := status(); st != "active" || run != 1 {
		t.Fatalf("after an underpaid period: %s, underpaid %d; want active and 1", st, run)
	}
	dry()
	settle()
	if st, reason, _ := status(); st != "left" || reason != "poached" {
		t.Fatalf("after %d underpaid periods: %s (%s), want left, poached", def.Staff.UnderpaidPeriods, st, reason)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM npc_staff_payments WHERE staff_id = $1::uuid`, staffID); n != 3 {
		t.Fatalf("payments after three periods = %d, want 3", n)
	}
	settle()
	if n := countRows(t, pool, `SELECT count(*) FROM npc_staff_payments WHERE staff_id = $1::uuid`, staffID); n != 3 {
		t.Fatalf("a specialist who left was paid again: %d payments", n)
	}

	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() || !v.Recruit {
		t.Fatalf("ledger invariants broken: %+v", v.RecruitInvariants)
	}
}

// companyIDOf is the id of the company a player owns.
func companyIDOf(t *testing.T, pool *postgres.Pool, ownerID string) string {
	t.Helper()
	var id string
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM companies WHERE owner_player_id = $1::uuid
		ORDER BY founded_at DESC LIMIT 1`, ownerID).Scan(&id); err != nil {
		t.Fatalf("no company: %v", err)
	}
	return id
}
