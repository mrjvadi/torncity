//go:build integration

// Integration tests for work and study (migrations 0011 and 0012): a job, a
// timed shift and its wage, a course, its fee and its scheduled completion,
// all through the real handlers, the real unit of work, the real ledger and
// the real policy resolver, on the game clock the game ships with.
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
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
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

// gameScale is the game clock the game ships with (config game.time_scale).
const gameScale = 60

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
		`DELETE FROM shift_sessions WHERE player_id = $1::uuid`,
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
	jobs := handlers.NewJobsHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, 5, time.Hour, now)
	edu := handlers.NewEducationHandler(uow, workIDs{t}, nil, registry, cities, gameScale, 5, time.Hour, now)
	travel := handlers.NewTravelHandler(uow, workIDs{t}, nil, cities, snapshotNetwork{registry.Current()},
		policy, gameScale, 25, time.Hour, now)
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
	// A shift is worked at the workplace (places.yml work_categories); the
	// walk there from elsewhere is places_integration_test.go's.
	standAtWorkplace(t, pool, registry, p.ID, "ostmarch", "retail")

	// Three presses of "work" at once, then a fourth: exactly one shift
	// starts and its energy is charged once. The job row's lock serialises
	// the presses and the partial unique index backs it up.
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
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
	resp, err := jobs.Work(ctx, metaFor("job.work"))
	if err != nil || resp == nil || !strings.Contains(resp.Text, "job.at_work") {
		t.Fatalf("a start while working = %v, %v; want the at-work refusal", resp, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM shift_sessions WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Fatalf("shifts started = %d, want exactly 1", n)
	}
	var energy int
	if err := pool.Raw().QueryRow(ctx, `SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID).Scan(&energy); err != nil {
		t.Fatal(err)
	}
	if energy != 100-15 {
		t.Errorf("energy = %d, want %d: a refused start charged", energy, 100-15)
	}
	var (
		sessionID, status string
		startedAt, endsAt time.Time
		dueAt             time.Time
	)
	if err := pool.Raw().QueryRow(ctx,
		`SELECT s.id::text, s.status, s.started_at, s.ends_at, a.finish_at
		   FROM shift_sessions s JOIN game_actions a ON a.id = s.game_action_id
		  WHERE s.player_id = $1::uuid AND a.action_type = $2`, p.ID, application.ShiftActionType).
		Scan(&sessionID, &status, &startedAt, &endsAt, &dueAt); err != nil {
		t.Fatalf("reading the shift: %v", err)
	}
	// The retail entry shift is 4 game hours: 4 real minutes at 60.
	if status != application.ShiftWorking || endsAt.Sub(startedAt) != 4*time.Minute || !dueAt.Equal(endsAt) {
		t.Errorf("shift %s from %s to %s, due %s; want working for 4 real minutes", status, startedAt, endsAt, dueAt)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM work_shifts WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Fatalf("payroll rows = %d before the shift ended, want 0", n)
	}

	// Nobody leaves town in the middle of a shift.
	if _, err := travel.Start(ctx, metaFor("travel.start"), handlers.StartTravelRequest{
		City: "brennhaven", Mode: "bus", Max: "1000000",
	}); !errors.Is(err, application.ErrShiftInProgress) {
		t.Fatalf("travel while working = %v, want ErrShiftInProgress", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM travels WHERE player_id = $1::uuid`, p.ID); n != 0 {
		t.Fatalf("journeys = %d, want none while working", n)
	}

	// The scheduler ends the shift; its dispatch is delivered three times
	// (two at once, then a replay under a new request id). One wage.
	clockMu.Lock()
	clock = endsAt.Add(time.Second)
	clockMu.Unlock()
	finish := handlers.FinishShiftRequest{ActorID: p.ID, ReferenceType: "shift_sessions", ReferenceID: sessionID}
	finishErrs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := metaFor("job.finish_shift")
			m.TelegramUserID = 0
			_, err := jobs.FinishShift(context.Background(), m, finish)
			finishErrs <- err
		}()
	}
	wg.Wait()
	close(finishErrs)
	for err := range finishErrs {
		if err != nil {
			t.Fatalf("concurrent FinishShift: %v", err)
		}
	}
	replay := metaFor("job.finish_shift")
	replay.TelegramUserID = 0
	if resp, err := jobs.FinishShift(ctx, replay, finish); err != nil || resp != nil {
		t.Fatalf("replayed FinishShift = %v, %v; want nothing", resp, err)
	}

	gross := max(int64(120), wage.Value)
	tax := gross * taxRate.Value / 10_000
	if n := countRows(t, pool, `SELECT count(*) FROM work_shifts WHERE player_id = $1::uuid AND id = $2::uuid AND gross = $3 AND tax = $4`,
		p.ID, sessionID, gross, tax); n != 1 {
		t.Errorf("payroll rows for the shift paying %d with %d tax = %d, want exactly 1", gross, tax, n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM work_shifts WHERE player_id = $1::uuid`, p.ID); n != 1 {
		t.Errorf("payroll rows = %d, want 1", n)
	}
	if got := balance(application.AccountPlayerCash, p.ID); got != gross-tax {
		t.Errorf("cash = %d, want one wage %d", got, gross-tax)
	}
	if got := balance(application.AccountCityTreasury, city.ID) - treasuryBefore; got != tax {
		t.Errorf("treasury grew by %d, want %d", got, tax)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM shift_sessions WHERE id = $1::uuid AND status = 'completed'`, sessionID); n != 1 {
		t.Error("the shift was not marked completed")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload::text LIKE '%' || $2 || '%'`,
		subjects.Event("job", "shift_worked"), p.ID); n != 1 {
		t.Errorf("shift_worked events = %d, want 1 for the one notice", n)
	}

	// Free again: the shift no longer stands in the way of a journey.
	if _, err := travel.Start(ctx, metaFor("travel.start"), handlers.StartTravelRequest{City: "brennhaven"}); errors.Is(err, application.ErrShiftInProgress) {
		t.Fatalf("travel after the shift = %v, want the shift no longer in the way", err)
	}

	// A content load that would remove the career the player works in is
	// refused, and writes nothing.
	pack, err := content.Load("../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	var (
		kept    []content.CareerDef
		dropped string
	)
	for _, c := range pack.Careers {
		if c.Code != "retail" {
			kept = append(kept, c)
		} else {
			dropped = c.Category
		}
	}
	pack.Careers = kept
	// Keep the rest of the pack consistent without retail — a venue that
	// places workers of its category would otherwise be refused first — so
	// the refusal seen is the one about the job in use.
	for i := range pack.Venues {
		var cats []string
		for _, c := range pack.Venues[i].WorkCategories {
			if c != dropped {
				cats = append(cats, c)
			}
		}
		pack.Venues[i].WorkCategories = cats
	}
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
	// A course is taught at its institution's place: the player walks to
	// the university quarter first (places.yml).
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'university', place_since = now() WHERE id = $1::uuid`,
		p.ID); err != nil {
		t.Fatal(err)
	}
	before := balance(application.AccountPlayerCash, p.ID)
	if _, err := edu.Enroll(ctx, metaFor("education.enroll"), handlers.CourseRequest{Course: "first_aid", Method: "cash"}); err != nil {
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
	// first_aid is a 2-hour course: two real minutes on the game clock.
	if wait := finishAt.Sub(now()); actionType != application.EducationActionType ||
		wait < 2*time.Minute-time.Second || wait > 2*time.Minute+time.Second {
		t.Errorf("scheduled %q at %s, want education two real minutes out", actionType, finishAt)
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
