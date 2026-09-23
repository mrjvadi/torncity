//go:build integration

// Integration tests for work and study (migration 0011): a job, its shifts
// and their wages, a course, its fee and its scheduled completion, all
// through the real handlers, the real unit of work, the real ledger and the
// real policy resolver.
//
// They need the active content to carry the shipped careers and courses:
// `admin content load` after migration 0011. Without them the test skips and
// says so, rather than loading content into a shared database itself.
//
// Cleaning up: work_shifts and certifications are append-only like the
// ledger, so the cleanup disables their guards in one transaction, removes
// exactly this test's rows and re-enables them, as purgeLedgerFor does.
package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// workRegistry builds the registry a game service would, from the active
// version, or skips when that version carries no careers.
func workRegistry(t *testing.T, pool *postgres.Pool) *content.Registry {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT to_regclass('public.career_definitions') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("career_definitions does not exist; apply migration 0011 first")
	}
	pack, err := postgres.NewContentStore(pool).LoadActive(testCtx(t))
	if err != nil {
		t.Skipf("no active content: %v", err)
	}
	if len(pack.Careers) == 0 || len(pack.Courses) == 0 {
		t.Skip("the active content has no careers or courses; run `admin content load`")
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		t.Fatal(err)
	}
	reg := content.NewRegistry()
	reg.Swap(snap)
	return reg
}

type workIDs struct{ t *testing.T }

func (g workIDs) NewID() string { return newUUID(g.t) }

// purgeWorkFor removes every job, shift, enrolment, certificate, scheduled
// course and event of one player.
func purgeWorkFor(t *testing.T, pool *postgres.Pool, playerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, stmt := range []string{
		`ALTER TABLE work_shifts DISABLE TRIGGER work_shifts_append_only`,
		`ALTER TABLE certifications DISABLE TRIGGER certifications_append_only`,
		`DELETE FROM work_shifts WHERE player_id = $1::uuid`,
		`DELETE FROM certifications WHERE player_id = $1::uuid`,
		`DELETE FROM enrollments WHERE player_id = $1::uuid`,
		`DELETE FROM employments WHERE player_id = $1::uuid`,
		`DELETE FROM game_actions WHERE actor_id = $1::uuid`,
		`DELETE FROM outbox WHERE payload->>'player_id' = $1`,
		`DELETE FROM player_skills WHERE player_id = $1::uuid`,
		`DELETE FROM player_stats WHERE player_id = $1::uuid`,
		`ALTER TABLE work_shifts ENABLE TRIGGER work_shifts_append_only`,
		`ALTER TABLE certifications ENABLE TRIGGER certifications_append_only`,
	} {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = []any{playerID}
		}
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(stmt), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// TestWorkAndStudyEndToEnd runs a career and a course through the real stack
// and then checks the ledger's invariants.
func TestWorkAndStudyEndToEnd(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := workRegistry(t, pool)
	ctx := testCtx(t)

	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}

	p := insertPlayer(t, pool)
	t.Cleanup(func() { purgeLedgerFor(t, pool, p.ID) })
	t.Cleanup(func() { purgeWorkFor(t, pool, p.ID) })
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid WHERE id = $1::uuid`,
		p.ID, city.ID); err != nil {
		t.Fatal(err)
	}

	clock := time.Now().UTC()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, nil)
	jobs := handlers.NewJobsHandler(uow, workIDs{t}, nil, registry, cities, policy, 5, time.Hour, now)
	edu := handlers.NewEducationHandler(uow, workIDs{t}, nil, registry, cities, 5, time.Hour, now)
	ledger := postgres.NewLedgerRepository(pool)

	metaFor := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = p.TelegramUserID
		m.Command = command
		m.Language = "en"
		return m
	}
	balance := func(kind application.AccountKind, owner string) int64 {
		acct, err := ledger.AccountFor(testCtx(t), kind, owner)
		if err != nil {
			t.Fatal(err)
		}
		return acct.Balance.Minor()
	}
	treasuryBefore := balance(application.AccountCityTreasury, city.ID)

	// The labour law in force, read the way the handler reads it.
	wage, err := policy.Get(ctx, city.JurisdictionID, "city.minimum_wage")
	if err != nil {
		t.Fatalf("the labour levers are not loaded: %v", err)
	}
	taxRate, err := policy.Get(ctx, city.JurisdictionID, "city.income_tax")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := jobs.Apply(ctx, metaFor("job.apply"), handlers.JobRequest{Role: "retail"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM employments WHERE player_id = $1::uuid AND ended_at IS NULL`, p.ID); n != 1 {
		t.Fatalf("current jobs = %d, want 1", n)
	}

	// Three shifts, two of them at once: the job row's lock serialises them,
	// so each sees the energy and fatigue history the one before it left.
	if _, err := jobs.Work(ctx, metaFor("job.work")); err != nil {
		t.Fatalf("Work: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := jobs.Work(context.Background(), metaFor("job.work"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Work: %v", err)
		}
	}

	gross := max(int64(120), wage.Value)
	tax := gross * taxRate.Value / 10_000
	if n := countRows(t, pool, `SELECT count(*) FROM work_shifts WHERE player_id = $1::uuid AND gross = $2 AND tax = $3`,
		p.ID, gross, tax); n != 3 {
		t.Errorf("shifts paying %d with %d tax = %d, want 3", gross, tax, n)
	}
	if got := balance(application.AccountPlayerCash, p.ID); got != 3*(gross-tax) {
		t.Errorf("cash = %d, want 3 x %d", got, gross-tax)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != 3*tax {
		t.Errorf("treasury grew by %d, want %d", got, 3*tax)
	}
	var energy int
	if err := pool.Raw().QueryRow(ctx, `SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if energy != 100-3*15 {
		t.Errorf("energy = %d, want %d: a concurrent shift was lost or double-spent", energy, 100-3*15)
	}

	// A content load that would remove the career the player works in is
	// refused, and writes nothing.
	pack, err := content.Load("../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	var kept []content.CareerDef
	for _, c := range pack.Careers {
		if c.Code != "retail" {
			kept = append(kept, c)
		}
	}
	pack.Careers = kept
	if _, err := postgres.NewContentStore(pool).Apply(ctx, pack, postgres.ApplyRequest{Actor: "integration", Reason: "must be refused"}); !errors.Is(err, postgres.ErrJobContentInUse) {
		t.Fatalf("Apply without retail = %v, want ErrJobContentInUse", err)
	}

	// Study: the fee, the schedule, then the completion.
	grant := money.FromMinor(1000)
	negGrant, _ := grant.Neg()
	cash, err := ledger.AccountFor(ctx, application.AccountPlayerCash, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Post(ctx, application.LedgerTransaction{
		Reason: application.ReasonAdminGrant,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: negGrant},
			{AccountID: cash.ID, Amount: grant},
		},
	}); err != nil {
		t.Fatal(err)
	}
	before := balance(application.AccountPlayerCash, p.ID)
	if _, err := edu.Enroll(ctx, metaFor("education.enroll"), handlers.CourseRequest{Course: "first_aid"}); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got := before - balance(application.AccountPlayerCash, p.ID); got != 600 {
		t.Errorf("fee charged = %d, want 600", got)
	}
	var enrollmentID, actionType string
	var finishAt time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT e.id::text, a.action_type, a.finish_at FROM enrollments e JOIN game_actions a ON a.id = e.game_action_id
		  WHERE e.player_id = $1::uuid AND e.status = 'in_progress'`, p.ID).Scan(&enrollmentID, &actionType, &finishAt); err != nil {
		t.Fatalf("reading the enrolment: %v", err)
	}
	if actionType != application.EducationActionType || finishAt.Sub(now()) < 2*time.Hour-time.Second {
		t.Errorf("scheduled %q at %s, want education two hours out", actionType, finishAt)
	}

	clockMu.Lock()
	clock = clock.Add(3 * time.Hour)
	clockMu.Unlock()
	req := handlers.CompleteCourseRequest{ActorID: p.ID, ReferenceType: "enrollments", ReferenceID: enrollmentID}
	for i := 0; i < 2; i++ {
		m := metaFor("education.complete")
		if _, err := edu.Complete(ctx, m, req); err != nil {
			t.Fatalf("Complete #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'first_aid'`, p.ID); n != 1 {
		t.Errorf("certificates = %d, want exactly 1 after two deliveries", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'medicine' AND xp = 150`, p.ID); n != 1 {
		t.Errorf("medicine xp not granted exactly once")
	}

	if _, err := jobs.Quit(ctx, metaFor("job.quit"), handlers.QuitRequest{Confirm: "yes"}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM employments WHERE player_id = $1::uuid AND end_reason = 'resigned'`, p.ID); n != 1 {
		t.Errorf("resigned jobs = %d, want 1", n)
	}

	v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK() {
		t.Errorf("ledger invariants broken: sum %s, unbalanced %v, drifted %v", v.LedgerSum, v.Unbalanced, v.Drifted)
	}
}
