//go:build integration

// Integration tests for the two durability fixes that meet in game_actions and
// the unit of work: abandoned claims are returned to the schedule, and a
// handler's phase 1 writes commit or roll back with the rest of its unit of
// work.
//
// The same rules as phase1_integration_test.go apply. Every fixture is this
// file's own and is removed again; the reaper is global by nature, so its
// fixtures carry claim times in pastEpoch, where nothing but this file lives,
// and it is always given a cutoff only they satisfy.
//
// Required environment:
//
//	INTEGRATION_DSN  postgres://user:pass@host:port/db?sslmode=disable
package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// seedClaimed inserts n actions of actionType that are running and were
// claimed at claimedAt, and returns their ids.
func seedClaimed(t *testing.T, pool *postgres.Pool, actionType string, claimedAt time.Time, n int) []string {
	t.Helper()

	ctx := testCtx(t)
	rows, err := pool.Raw().Query(ctx,
		`INSERT INTO game_actions (id, action_type, actor_type, payload, status, retry_count, started_at, finish_at, claimed_at)
		 SELECT gen_random_uuid(), $1, 'system', '{}'::jsonb, 'running', 0, $2::timestamptz, $2::timestamptz, $2::timestamptz
		   FROM generate_series(1, $3)
		 RETURNING id::text`,
		actionType, claimedAt, n)
	if err != nil {
		t.Fatalf("seeding claimed actions: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a seeded id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading seeded ids: %v", err)
	}
	return ids
}

// actionState reads what the reaper changes on one row.
type actionState struct {
	status     string
	retryCount int
	claimedAt  *time.Time
}

func readAction(t *testing.T, pool *postgres.Pool, id string) actionState {
	t.Helper()

	var s actionState
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT status, retry_count, claimed_at FROM game_actions WHERE id = $1::uuid`, id).
		Scan(&s.status, &s.retryCount, &s.claimedAt); err != nil {
		t.Fatalf("reading action %s: %v", id, err)
	}
	return s
}

// ---------------------------------------------------------------------------
// the claim records its time
// ---------------------------------------------------------------------------

// The reaper can only return what the claim dated. A claim that left
// claimed_at NULL would make every running row invisible to it.
func TestDueStampsTheClaimTime(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	actionType := "itest_stamp_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	id := seedAction(t, pool, actionType, pastEpoch)

	claimNow := pastEpoch.Add(time.Minute)
	due, err := postgres.NewGameActionRepository(pool).Due(ctx, claimNow, 10)
	if err != nil {
		t.Fatalf("claiming: %v", err)
	}
	found := false
	for _, a := range due {
		if a.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("the seeded action was not claimed; got %d rows", len(due))
	}

	got := readAction(t, pool, id)
	if got.status != "running" {
		t.Errorf("status = %q, want running", got.status)
	}
	if got.claimedAt == nil || !got.claimedAt.Equal(claimNow) {
		t.Errorf("claimed_at = %v, want %v", got.claimedAt, claimNow)
	}
}

// ---------------------------------------------------------------------------
// the reaper
// ---------------------------------------------------------------------------

func TestReclaimStaleReturnsAbandonedClaims(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	actionType := "itest_reap_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	repo := postgres.NewGameActionRepository(pool)
	cutoff := pastEpoch.Add(time.Hour)

	stale := seedClaimed(t, pool, actionType, pastEpoch, 3)
	fresh := seedClaimed(t, pool, actionType, cutoff.Add(time.Minute), 2)

	t.Run("a held row is stepped over, not waited on", func(t *testing.T) {
		// Hold the first stale row the way a scheduler completing it would.
		tx, err := pool.Raw().Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := tx.Exec(ctx, `SELECT 1 FROM game_actions WHERE id = $1::uuid FOR UPDATE`, stale[0]); err != nil {
			t.Fatalf("locking a row: %v", err)
		}

		reapCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		n, err := repo.ReclaimStale(reapCtx, cutoff, 100)
		if err != nil {
			t.Fatalf("reaping while a row is held: %v", err)
		}
		if n != len(stale)-1 {
			t.Errorf("reaped %d rows, want %d (every stale row but the held one)", n, len(stale)-1)
		}
		if got := readAction(t, pool, stale[0]).status; got != "running" {
			t.Errorf("the held row was reaped anyway: status %q", got)
		}
		_ = tx.Rollback(ctx)
	})

	t.Run("the released row is reaped on the next pass", func(t *testing.T) {
		n, err := repo.ReclaimStale(ctx, cutoff, 100)
		if err != nil {
			t.Fatalf("reaping: %v", err)
		}
		if n != 1 {
			t.Errorf("reaped %d rows, want 1", n)
		}
	})

	t.Run("every stale row is back in the schedule, counted once", func(t *testing.T) {
		for _, id := range stale {
			got := readAction(t, pool, id)
			if got.status != "scheduled" {
				t.Errorf("stale %s: status = %q, want scheduled", id, got.status)
			}
			if got.retryCount != 1 {
				t.Errorf("stale %s: retry_count = %d, want 1", id, got.retryCount)
			}
			if got.claimedAt != nil {
				t.Errorf("stale %s: claimed_at = %v, want NULL", id, *got.claimedAt)
			}
		}
	})

	t.Run("a claim inside the lease is left alone", func(t *testing.T) {
		for _, id := range fresh {
			got := readAction(t, pool, id)
			if got.status != "running" || got.retryCount != 0 || got.claimedAt == nil {
				t.Errorf("fresh %s was touched: %+v", id, got)
			}
		}
	})

	t.Run("the limit bounds one pass", func(t *testing.T) {
		more := seedClaimed(t, pool, actionType, pastEpoch, 5)
		n, err := repo.ReclaimStale(ctx, cutoff, 2)
		if err != nil {
			t.Fatalf("reaping: %v", err)
		}
		if n != 2 {
			t.Errorf("reaped %d rows with a limit of 2", n)
		}
		left := countRows(t, pool,
			`SELECT count(*) FROM game_actions WHERE action_type = $1 AND status = 'running' AND claimed_at < $2`,
			actionType, cutoff)
		if left != len(more)-2 {
			t.Errorf("%d stale rows left, want %d", left, len(more)-2)
		}
	})
}

// Two schedulers reap on every tick. If their passes overlapped, one abandoned
// claim would be counted as two attempts and noisy_attempts would escalate
// early; the CTE's SKIP LOCKED is what keeps them disjoint.
func TestReclaimStaleIsDisjoint(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	actionType := "itest_reap_race_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	const total = 200
	seedClaimed(t, pool, actionType, pastEpoch, total)

	repo := postgres.NewGameActionRepository(pool)
	cutoff := pastEpoch.Add(time.Hour)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		moved int
		errs  []error
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := repo.ReclaimStale(ctx, cutoff, 25)
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				moved += n
				mu.Unlock()
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	for _, err := range errs {
		t.Errorf("a concurrent reaper failed: %v", err)
	}
	if moved != total {
		t.Errorf("the reapers reported %d rows between them, want %d", moved, total)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM game_actions WHERE action_type = $1 AND (status <> 'scheduled' OR retry_count <> 1)`,
		actionType); n != 0 {
		t.Errorf("%d rows were not returned exactly once", n)
	}
}

// The reaper runs every tick, so it is held to the same rule as the claim: it
// reaches its rows through the partial index and never reads the history.
func TestReclaimUsesThePartialIndex(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	actionType := "itest_reap_plan_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	// ~2000 rows of every status, mostly closed out: the shape of a real
	// table, where the claims in flight are a sliver of the history.
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO game_actions (id, action_type, actor_type, payload, status, retry_count, started_at, finish_at, completed_at, claimed_at)
		 SELECT gen_random_uuid(), $1, 'system', '{}'::jsonb,
		        CASE WHEN i % 40 = 0 THEN 'running'
		             WHEN i % 10 = 0 THEN 'scheduled'
		             WHEN i % 3 = 0 THEN 'failed'
		             ELSE 'completed' END,
		        0, $2::timestamptz, $2::timestamptz + (i || ' milliseconds')::interval,
		        CASE WHEN i % 10 = 0 THEN NULL ELSE $2::timestamptz + (i || ' milliseconds')::interval END,
		        CASE WHEN i % 40 = 0 THEN $2::timestamptz + (i || ' milliseconds')::interval END
		   FROM generate_series(1, 2000) AS i`,
		actionType, pastEpoch); err != nil {
		t.Fatalf("seeding actions: %v", err)
	}
	if _, err := pool.Raw().Exec(ctx, `ANALYZE game_actions`); err != nil {
		t.Fatalf("analyzing game_actions: %v", err)
	}

	// EXPLAIN without ANALYZE: the reap is an UPDATE, and this test must not
	// move the rows it is only asking about.
	rows, err := pool.Raw().Query(ctx, "EXPLAIN "+postgres.ReclaimStatement(), pastEpoch.Add(time.Hour), 50)
	if err != nil {
		t.Fatalf("explaining the reap: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatalf("scanning a plan line: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the plan: %v", err)
	}

	got := plan.String()
	t.Logf("EXPLAIN of the reap:\n%s", got)

	// Scoped to the CTE for the reason TestDueUsesThePartialIndex gives: the
	// UPDATE's join back to the chosen ids is a separate, size-dependent
	// decision.
	reapPlan := planSubtree(t, got, "CTE stale")
	if !strings.Contains(reapPlan, "Index Scan using game_actions_claimed_idx on game_actions") {
		t.Errorf("the reap does not reach its rows through game_actions_claimed_idx:\n%s", reapPlan)
	}
	if strings.Contains(reapPlan, "Seq Scan") {
		t.Errorf("the reap falls back to a sequential scan:\n%s", reapPlan)
	}
	if !strings.Contains(reapPlan, "LockRows") {
		t.Errorf("the reap does not lock the rows it selects:\n%s", reapPlan)
	}
}

// ---------------------------------------------------------------------------
// the unit of work carries the phase 1 writes
// ---------------------------------------------------------------------------

// TestUnitOfWorkRollsBackAnArrival is the database half of the XP-on-retry fix.
//
// The handler test proves the handler routes every write through Tx. This
// proves Tx is a real transaction for them: TravelRepository.Complete opens
// its own inner transaction, and on the unit of work's Tx that must be a
// savepoint of the outer one, not a second connection that commits on its
// own. A failure after the arrival must leave the journey in transit and the
// player where they were.
func TestUnitOfWorkRollsBackAnArrival(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)
	from := seedCity(t, pool, "origin")
	to := seedCity(t, pool, "destination")

	actionType := "itest_uow_arrive_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
		player.ID, from.ID); err != nil {
		t.Fatalf("placing the player: %v", err)
	}

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	journeyID := newUUID(t)
	actionID := newUUID(t)
	now := time.Now().UTC()

	// Departure, entirely through the unit of work: schedule, journey, stats.
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID: actionID, ActionType: actionType, ActorType: "player", ActorID: player.ID,
			ReferenceType: "travel", ReferenceID: journeyID,
			StartedAt: now, FinishAt: pastEpoch,
		}); err != nil {
			return err
		}
		if err := tx.Travels().Start(ctx, application.Travel{
			ID: journeyID, PlayerID: player.ID, FromCityID: from.ID, ToCityID: to.ID,
			GameActionID: actionID, DepartedAt: now, ArrivesAt: now.Add(time.Hour),
		}); err != nil {
			return err
		}
		_, err := tx.Stats().EnsureDefaults(ctx, player.ID, application.Stats{
			PlayerID: player.ID, Level: 1, Health: 100, MaxHealth: 100, Energy: 100, MaxEnergy: 100, UpdatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("departing: %v", err)
	}

	playerCity := func() string {
		var city string
		if err := pool.Raw().QueryRow(testCtx(t), `SELECT city_id::text FROM players WHERE id = $1::uuid`, player.ID).Scan(&city); err != nil {
			t.Fatalf("reading the player's city: %v", err)
		}
		return city
	}
	xp := func() int64 {
		s, err := postgres.NewStatsRepository(pool).Get(testCtx(t), player.ID)
		if err != nil {
			t.Fatalf("reading stats: %v", err)
		}
		return s.XP
	}

	arrive := func(failAfter bool) error {
		return uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			if err := tx.Travels().Complete(ctx, journeyID); err != nil {
				return err
			}
			s, err := tx.Stats().Get(ctx, player.ID)
			if err != nil {
				return err
			}
			s.XP += 25
			if err := tx.Stats().Save(ctx, *s); err != nil {
				return err
			}
			if failAfter {
				return errInjectedIntegration
			}
			return nil
		})
	}

	if err := arrive(true); !errors.Is(err, errInjectedIntegration) {
		t.Fatalf("got %v, want the injected failure", err)
	}
	if _, err := postgres.NewTravelRepository(pool).Active(ctx, player.ID); err != nil {
		t.Fatalf("after a failed unit of work the journey must still be active, got %v", err)
	}
	if got := playerCity(); got != from.ID {
		t.Errorf("a rolled-back arrival moved the player to %s", got)
	}
	if got := xp(); got != 0 {
		t.Errorf("a rolled-back arrival kept %d xp", got)
	}

	if err := arrive(false); err != nil {
		t.Fatalf("retried arrival: %v", err)
	}
	if got := playerCity(); got != to.ID {
		t.Errorf("after the retry the player is in %s, want %s", got, to.ID)
	}
	if got := xp(); got != 25 {
		t.Errorf("after the retry xp = %d, want 25", got)
	}
}

var errInjectedIntegration = errors.New("injected failure after the arrival")
