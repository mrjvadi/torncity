//go:build integration

// Integration tests for the phase 1 repositories: the world a player moves
// through, their stats and skills, the durable schedule and the social graph.
//
// The split is the same one integration_test.go describes. A unit test can
// pin the text of a statement and the mapping of an error; it cannot prove
// that FOR UPDATE SKIP LOCKED really holds a lock inside a CTE, that a partial
// unique index really serialises two inserts, or that two statements really
// commit together. Those belong to PostgreSQL, so they are asserted against a
// real one here.
//
// Everything in this file creates its own fixtures and removes them again.
// Nothing asserts on a table count it did not establish itself, because this
// database is shared: another process may be loading content into it while
// these run. Where a statement would otherwise reach rows this file did not
// write — the scheduler's claim is global by nature — the fixtures are placed
// far in the past and the query is given an instant that only they satisfy.
//
// Required environment:
//
//	INTEGRATION_DSN  postgres://user:pass@host:port/db?sslmode=disable
package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// ---------------------------------------------------------------------------
// phase 1 fixtures
// ---------------------------------------------------------------------------

// pastEpoch is where every scheduled fixture in this file lives.
//
// GameActionRepository.Due claims the oldest due rows in the whole table, so a
// test that seeded rows at "now" could claim another process's work and would
// mutate data it does not own. Seeding far in the past and asking for rows due
// just after that instant makes the claim reach this file's rows and nothing
// else, while proving exactly the same thing about the locks.
var pastEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

// seedCity inserts one city and removes it when the test ends.
//
// Cities are content and arrive through the loader, so there is no Create on
// the repository; the row is written directly. content_version_id is left
// NULL, which 0003_content permits and which keeps these rows outside any
// version an operator might be loading at the same moment.
func seedCity(t *testing.T, pool *postgres.Pool, label string) application.City {
	t.Helper()

	ctx := testCtx(t)
	c := application.City{
		ID:           newUUID(t),
		Code:         "IT_" + strings.ToUpper(randomToken(t, 10)),
		Name:         "integration " + label,
		TaxRateBPS:   250,
		CostOfLiving: 1000,
		Population:   0,
	}

	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6)`,
		c.ID, c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving, c.Population); err != nil {
		t.Fatalf("seeding city %s: %v", c.Code, err)
	}

	// The cleanup releases every reference to this city before dropping it,
	// so it works whatever order the test's other cleanups run in: t.Cleanup
	// is last-registered-first, and players.city_id and travels both point
	// here with ON DELETE RESTRICT.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, stmt := range []string{
			`DELETE FROM travels WHERE from_city_id = $1::uuid OR to_city_id = $1::uuid`,
			`UPDATE players SET city_id = NULL WHERE city_id = $1::uuid`,
			`DELETE FROM cities WHERE id = $1::uuid`,
		} {
			if _, err := pool.Raw().Exec(cleanupCtx, stmt, c.ID); err != nil {
				t.Errorf("cleaning up city %s (%q): %v", c.Code, stmt, err)
			}
		}
	})

	return c
}

// seedAction schedules one action of actionType, due at finishAt.
//
// Cleanup is by action_type, which every caller makes unique with a random
// token, so a run removes exactly its own rows and never another process's.
func seedAction(t *testing.T, pool *postgres.Pool, actionType string, finishAt time.Time) string {
	t.Helper()

	ctx := testCtx(t)
	id := newUUID(t)

	if err := postgres.NewGameActionRepository(pool).Schedule(ctx, application.GameAction{
		ID:         id,
		ActionType: actionType,
		ActorType:  "system",
		FinishAt:   finishAt,
		StartedAt:  finishAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("scheduling a %s action: %v", actionType, err)
	}

	return id
}

// cleanupActions removes every action of this type, and any travel hanging
// from one, when the test ends. Travels first: travels.game_action_id is a
// foreign key.
func cleanupActions(t *testing.T, pool *postgres.Pool, actionType string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		if _, err := pool.Raw().Exec(ctx,
			`DELETE FROM travels WHERE game_action_id IN (SELECT id FROM game_actions WHERE action_type = $1)`,
			actionType); err != nil {
			t.Errorf("cleaning up travels for %s: %v", actionType, err)
		}
		if _, err := pool.Raw().Exec(ctx,
			`DELETE FROM game_actions WHERE action_type = $1`, actionType); err != nil {
			t.Errorf("cleaning up %s actions: %v", actionType, err)
		}
	})
}

// cleanupStats, cleanupSkills and cleanupFriendships remove the rows a player
// accumulates. They must be registered AFTER insertPlayer, because t.Cleanup
// runs last-registered-first and players cannot be deleted while anything
// still references them.
func cleanupPlayerRows(t *testing.T, pool *postgres.Pool, playerIDs ...string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()

		for _, id := range playerIDs {
			for _, stmt := range []string{
				`DELETE FROM friendships WHERE player_id = $1::uuid OR friend_player_id = $1::uuid`,
				`DELETE FROM player_skills WHERE player_id = $1::uuid`,
				`DELETE FROM player_stats WHERE player_id = $1::uuid`,
				`DELETE FROM travels WHERE player_id = $1::uuid`,
			} {
				if _, err := pool.Raw().Exec(ctx, stmt, id); err != nil {
					t.Errorf("cleanup %q for player %s: %v", stmt, id, err)
				}
			}
		}
	})
}

// countRows answers a targeted question, never "how big is this table".
func countRows(t *testing.T, pool *postgres.Pool, query string, args ...any) int {
	t.Helper()

	ctx := testCtx(t)
	var n int
	if err := pool.Raw().QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("counting rows (%s): %v", query, err)
	}
	return n
}

// ---------------------------------------------------------------------------
// 1. the scheduler's claim
// ---------------------------------------------------------------------------

// TestGameActionDueIsDisjoint is the test this file exists for.
//
// GameActionRepository.Due folds a locking select into a claiming update, and
// every promise the scheduler makes rests on that being real: if the locks
// were not genuinely held across the statement, two schedulers would receive
// the same batch and the same player would be landed twice, the same salary
// paid twice, the same production run banked twice. No unit test can check it
// — the behaviour belongs to PostgreSQL's lock manager.
//
// It is checked from both sides, like the outbox claim above it:
//
//   - honoured: rows this test holds locked are stepped over, not waited on;
//   - held: two concurrent claims come back with no id in common.
func TestGameActionDueIsDisjoint(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	const batch = 60
	const total = batch * 2

	actionType := "itest_due_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	seeded := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		id := seedAction(t, pool, actionType, pastEpoch.Add(time.Duration(i)*time.Millisecond))
		seeded[id] = true
	}

	// Only this test's rows are due at this instant, so a claim cannot reach
	// another process's work.
	due := pastEpoch.Add(time.Hour)
	repo := postgres.NewGameActionRepository(pool)

	t.Run("a held lock is skipped, not waited on", func(t *testing.T) {
		conn, err := pool.Raw().Acquire(ctx)
		if err != nil {
			t.Fatalf("acquiring a connection: %v", err)
		}
		defer conn.Release()

		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("beginning the blocking transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

		// FOR UPDATE without SKIP LOCKED on purpose: this side must genuinely
		// hold the rows so the repository's side has something real to skip.
		rows, err := tx.Query(ctx,
			`SELECT id::text FROM game_actions
			  WHERE action_type = $1 AND status = 'scheduled'
			  ORDER BY finish_at LIMIT $2 FOR UPDATE`,
			actionType, batch)
		if err != nil {
			t.Fatalf("locking the first batch: %v", err)
		}
		locked := make(map[string]bool, batch)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scanning a locked id: %v", err)
			}
			locked[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("reading locked ids: %v", err)
		}
		if len(locked) != batch {
			t.Fatalf("locked %d rows, want %d", len(locked), batch)
		}

		// Without SKIP LOCKED this call blocks until the deferred rollback and
		// the test times out instead of failing.
		claimed, err := repo.Due(ctx, due, batch)
		if err != nil {
			t.Fatalf("Due while rows are locked: %v", err)
		}
		if len(claimed) != batch {
			t.Fatalf("claimed %d actions, want %d", len(claimed), batch)
		}
		for _, a := range claimed {
			if locked[a.ID] {
				t.Fatalf("Due returned action %s which another transaction holds locked", a.ID)
			}
			if !seeded[a.ID] {
				t.Fatalf("Due returned action %s which this test did not schedule", a.ID)
			}
			// The claim is a status change: a claimed row must leave the set
			// of candidates immediately, or the next poll a second later hands
			// it out again.
			if a.Status != "running" {
				t.Errorf("claimed action %s came back with status %q, want running", a.ID, a.Status)
			}
		}
	})

	t.Run("two concurrent claims are disjoint", func(t *testing.T) {
		// The previous subtest claimed half the rows; put them back so both
		// goroutines here have work to overlap on.
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE game_actions SET status = 'scheduled' WHERE action_type = $1 AND status = 'running'`,
			actionType); err != nil {
			t.Fatalf("resetting the seeded actions: %v", err)
		}

		var (
			ready sync.WaitGroup
			done  sync.WaitGroup
			start = make(chan struct{})

			batches [2][]application.GameAction
			errs    [2]error
		)

		ready.Add(2)
		done.Add(2)
		for i := range batches {
			go func(i int) {
				defer done.Done()
				ready.Done()
				<-start
				batches[i], errs[i] = repo.Due(ctx, due, batch)
			}(i)
		}

		ready.Wait()
		close(start)
		done.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("claim %d failed: %v", i, err)
			}
		}

		first := make(map[string]bool, len(batches[0]))
		for _, a := range batches[0] {
			first[a.ID] = true
		}

		var shared []string
		for _, a := range batches[1] {
			if first[a.ID] {
				shared = append(shared, a.ID)
			}
		}
		if len(shared) != 0 {
			t.Fatalf("the two concurrent claims share %d action(s) (%v); the claim is not holding its locks "+
				"(batch sizes %d and %d)", len(shared), shared, len(batches[0]), len(batches[1]))
		}
		if len(batches[0]) == 0 || len(batches[1]) == 0 {
			t.Fatalf("one claim came back empty (sizes %d and %d); the two statements did not overlap, "+
				"so this run proved nothing about the locks", len(batches[0]), len(batches[1]))
		}
	})
}

// ---------------------------------------------------------------------------
// 2. the claim's plan
// ---------------------------------------------------------------------------

// TestDueUsesThePartialIndex asserts the project's standing requirement that
// no scheduler tick may scan the whole table.
//
// game_actions_due_idx is partial on status = 'scheduled', so it stays roughly
// the size of the pending backlog however many millions of completed rows pile
// up behind it. That only helps if the planner actually uses it, and the
// planner's choice depends on the statement text matching the index's
// predicate and order exactly — which is a thing that drifts silently during a
// refactor. The server is therefore asked directly.
func TestDueUsesThePartialIndex(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	actionType := "itest_plan_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	// ~2000 rows, mostly closed out, which is the shape this index is for: the
	// backlog is small and the history behind it is not.
	const (
		closed    = 1800
		scheduled = 200
	)
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO game_actions (id, action_type, actor_type, payload, status, retry_count, started_at, finish_at, completed_at)
		 SELECT gen_random_uuid(), $1, 'system', '{}'::jsonb,
		        CASE WHEN i % 3 = 0 THEN 'failed' ELSE 'completed' END,
		        0, $2::timestamptz, $2::timestamptz + (i || ' milliseconds')::interval,
		        $2::timestamptz + (i || ' milliseconds')::interval
		   FROM generate_series(1, $3) AS i`,
		actionType, pastEpoch, closed); err != nil {
		t.Fatalf("seeding closed actions: %v", err)
	}
	if _, err := pool.Raw().Exec(ctx,
		`INSERT INTO game_actions (id, action_type, actor_type, payload, status, retry_count, started_at, finish_at)
		 SELECT gen_random_uuid(), $1, 'system', '{}'::jsonb, 'scheduled',
		        0, $2::timestamptz, $2::timestamptz + (i || ' milliseconds')::interval
		   FROM generate_series(1, $3) AS i`,
		actionType, pastEpoch, scheduled); err != nil {
		t.Fatalf("seeding scheduled actions: %v", err)
	}

	// Without statistics the planner is guessing, and a plan produced from a
	// guess says nothing about the one production would get.
	if _, err := pool.Raw().Exec(ctx, `ANALYZE game_actions`); err != nil {
		t.Fatalf("analyzing game_actions: %v", err)
	}

	// The real statement, not a copy: see postgres.DueClaimStatement.
	rows, err := pool.Raw().Query(ctx,
		"EXPLAIN (ANALYZE, BUFFERS) "+postgres.DueClaimStatement(),
		pastEpoch.Add(time.Hour), 50)
	if err != nil {
		t.Fatalf("explaining the claim: %v", err)
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
	t.Logf("EXPLAIN (ANALYZE, BUFFERS) of the due claim:\n%s", got)

	// The assertion is scoped to the CTE, which is the part that decides WHICH
	// rows the tick reads. The UPDATE's own join back to the claimed ids is a
	// separate decision: with a fixture this size the planner hashes the fifty
	// claimed ids against a sequential read of the table because that is
	// genuinely cheaper than fifty primary key lookups, and it stops doing so
	// as the table grows. Asserting on the whole plan would therefore be
	// asserting on the size of the fixture, not on the statement.
	claimPlan := planSubtree(t, got, "CTE claimed")

	if !strings.Contains(claimPlan, "game_actions_due_idx") {
		t.Errorf("the claim is not answered by game_actions_due_idx:\n%s", claimPlan)
	}
	if !strings.Contains(claimPlan, "Index Scan using game_actions_due_idx on game_actions") {
		t.Errorf("the claim does not reach its rows through an index scan:\n%s", claimPlan)
	}
	// The whole point of the partial index is that the tick never visits a
	// completed row. A sequential scan here means every tick reads the entire
	// history behind the backlog.
	if strings.Contains(claimPlan, "Seq Scan") {
		t.Errorf("the claim falls back to a sequential scan:\n%s", claimPlan)
	}
	// SKIP LOCKED is what LockRows carries out. Without it, a second scheduler
	// would wait on the first one's rows instead of taking the next ones.
	if !strings.Contains(claimPlan, "LockRows") {
		t.Errorf("the claim does not lock the rows it selects:\n%s", claimPlan)
	}
}

// planSubtree returns the block of an EXPLAIN plan under the first line
// containing header, using the indentation the planner prints.
//
// It exists so an assertion can name one node of a plan rather than the whole
// text: the claim's own scan is the thing under test, and a node somewhere
// else in the same plan must not be able to satisfy or break it.
func planSubtree(t *testing.T, plan, header string) string {
	t.Helper()

	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

	lines := strings.Split(plan, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, header) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("the plan has no %q node:\n%s", header, plan)
	}

	out := []string{lines[start]}
	base := indent(lines[start])
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if indent(line) <= base {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// 3. travel
// ---------------------------------------------------------------------------

// TestTravelStartRefusesASecondJourney proves the invariant a player is in at
// most one place at a time.
//
// Start makes no "is this player already travelling?" check, deliberately: two
// taps arrive as two concurrent requests, both would read "no active travel"
// under READ COMMITTED, and both would insert. Only
// travels_one_active_per_player_idx can serialise them, and only a real server
// has one.
func TestTravelStartRefusesASecondJourney(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)

	from := seedCity(t, pool, "origin")
	to := seedCity(t, pool, "destination")

	actionType := "itest_travel_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	repo := postgres.NewTravelRepository(pool)

	// Two journeys with distinct identities in every respect except the
	// player: if the index were missing, nothing else would refuse them.
	travels := [2]application.Travel{}
	for i := range travels {
		travels[i] = application.Travel{
			ID:           newUUID(t),
			PlayerID:     player.ID,
			FromCityID:   from.ID,
			ToCityID:     to.ID,
			Cost:         0,
			GameActionID: seedAction(t, pool, actionType, pastEpoch.Add(time.Duration(i)*time.Second)),
			DepartedAt:   time.Now().UTC(),
			ArrivesAt:    time.Now().UTC().Add(time.Hour),
		}
	}

	_, errs := runConcurrently(t, 2, func(i int) (bool, error) {
		err := repo.Start(ctx, travels[i])
		return err == nil, err
	})

	var started, refused int
	for i, err := range errs {
		switch {
		case err == nil:
			started++
		case err == application.ErrAlreadyTravelling:
			// Identity, not errors.Is: the shared errors package matches by
			// code, so errors.Is would also accept any other Conflict and
			// would pass on a mapping that sent the player to the wrong
			// screen.
			refused++
		default:
			t.Fatalf("start %d failed with an unmapped error: %v", i, err)
		}
	}

	if started != 1 || refused != 1 {
		t.Fatalf("%d journeys started and %d were refused, want exactly one of each", started, refused)
	}

	// Exactly one in-transit row, and Active agrees with it.
	if n := countRows(t, pool,
		`SELECT count(*) FROM travels WHERE player_id = $1::uuid AND status = 'in_transit'`, player.ID); n != 1 {
		t.Fatalf("the player has %d journeys in progress, want 1", n)
	}
	active, err := repo.Active(ctx, player.ID)
	if err != nil {
		t.Fatalf("Active after a successful start: %v", err)
	}
	if active.ID != travels[0].ID && active.ID != travels[1].ID {
		t.Errorf("Active returned travel %s, which neither start proposed", active.ID)
	}

	// A player may travel any number of times in sequence: the index is
	// partial on in_transit, so cancelling releases the slot.
	if err := repo.Cancel(ctx, active.ID); err != nil {
		t.Fatalf("cancelling the active journey: %v", err)
	}
	next := travels[0]
	next.ID = newUUID(t)
	next.GameActionID = seedAction(t, pool, actionType, pastEpoch.Add(10*time.Second))
	if err := repo.Start(ctx, next); err != nil {
		t.Fatalf("starting a journey after cancelling the previous one: %v", err)
	}
}

// TestTravelCompleteIsAtomic proves that arrival is one event.
//
// Run as two statements, a process that dies between them strands the player:
// the travel says arrived while players.city_id still names the city they
// left, Active returns ErrNoActiveTravel so nothing will ever finish the move,
// and an arrived travel is indistinguishable from one that completed
// correctly, so no repair pass could find it.
func TestTravelCompleteIsAtomic(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)

	from := seedCity(t, pool, "origin")
	to := seedCity(t, pool, "destination")

	actionType := "itest_arrive_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)

	repo := postgres.NewTravelRepository(pool)

	// The player starts in the origin city, which is what the move has to
	// change.
	if _, err := pool.Raw().Exec(ctx,
		`UPDATE players SET city_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
		player.ID, from.ID); err != nil {
		t.Fatalf("placing the player in the origin city: %v", err)
	}

	newJourney := func() application.Travel {
		return application.Travel{
			ID:           newUUID(t),
			PlayerID:     player.ID,
			FromCityID:   from.ID,
			ToCityID:     to.ID,
			GameActionID: seedAction(t, pool, actionType, pastEpoch),
			DepartedAt:   time.Now().UTC(),
			ArrivesAt:    time.Now().UTC().Add(time.Hour),
		}
	}

	playerCity := func() string {
		ctx := testCtx(t)
		var city *string
		if err := pool.Raw().QueryRow(ctx, `SELECT city_id::text FROM players WHERE id = $1::uuid`, player.ID).Scan(&city); err != nil {
			t.Fatalf("reading the player's city: %v", err)
		}
		if city == nil {
			return ""
		}
		return *city
	}
	travelStatus := func(id string) string {
		ctx := testCtx(t)
		var status string
		if err := pool.Raw().QueryRow(ctx, `SELECT status FROM travels WHERE id = $1::uuid`, id).Scan(&status); err != nil {
			t.Fatalf("reading the travel's status: %v", err)
		}
		return status
	}

	t.Run("both halves land together", func(t *testing.T) {
		journey := newJourney()
		if err := repo.Start(ctx, journey); err != nil {
			t.Fatalf("starting the journey: %v", err)
		}

		if err := repo.Complete(ctx, journey.ID); err != nil {
			t.Fatalf("completing the journey: %v", err)
		}

		if got := travelStatus(journey.ID); got != "arrived" {
			t.Errorf("travel status = %q, want arrived", got)
		}
		if got := playerCity(); got != to.ID {
			t.Errorf("the player is in city %s, want the destination %s", got, to.ID)
		}
		// The journey is over, so the player may start another one.
		if _, err := repo.Active(ctx, player.ID); err != application.ErrNoActiveTravel {
			t.Errorf("Active after arrival = %v, want application.ErrNoActiveTravel", err)
		}
	})

	t.Run("a replayed completion changes nothing", func(t *testing.T) {
		// Put the player back where the previous subtest found them, so this
		// one starts from a known place.
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
			player.ID, from.ID); err != nil {
			t.Fatalf("resetting the player's city: %v", err)
		}

		journey := newJourney()
		if err := repo.Start(ctx, journey); err != nil {
			t.Fatalf("starting the journey: %v", err)
		}
		if err := repo.Complete(ctx, journey.ID); err != nil {
			t.Fatalf("completing the journey: %v", err)
		}
		// The player has since moved on; a late worker completing the same
		// travel again must not drag them back.
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
			player.ID, from.ID); err != nil {
			t.Fatalf("moving the player on: %v", err)
		}

		if err := repo.Complete(ctx, journey.ID); err != application.ErrNoActiveTravel {
			t.Fatalf("a replayed Complete = %v, want application.ErrNoActiveTravel", err)
		}
		if got := playerCity(); got != from.ID {
			t.Errorf("a replayed Complete moved the player to %s", got)
		}
	})

	t.Run("a failure leaves neither the travel nor the player changed", func(t *testing.T) {
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET city_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`,
			player.ID, from.ID); err != nil {
			t.Fatalf("resetting the player's city: %v", err)
		}

		journey := newJourney()
		if err := repo.Start(ctx, journey); err != nil {
			t.Fatalf("starting the journey: %v", err)
		}

		// The failure is provoked where it matters: between the two
		// statements. Another transaction holds the player's row, so the move
		// blocks; a deadline on the caller's context then aborts the
		// transaction after the travel has already been marked arrived inside
		// it. If the two statements were not one transaction, this is exactly
		// the moment a player would be stranded.
		conn, err := pool.Raw().Acquire(ctx)
		if err != nil {
			t.Fatalf("acquiring a connection: %v", err)
		}
		defer conn.Release()

		blocker, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("beginning the blocking transaction: %v", err)
		}
		defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()

		var lockedID string
		if err := blocker.QueryRow(ctx,
			`SELECT id::text FROM players WHERE id = $1::uuid FOR UPDATE`, player.ID).Scan(&lockedID); err != nil {
			t.Fatalf("locking the player's row: %v", err)
		}

		shortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		err = repo.Complete(shortCtx, journey.ID)
		if err == nil {
			t.Fatal("Complete reported success although the player's row was locked against it")
		}
		if err == application.ErrNoActiveTravel {
			t.Fatalf("Complete reported a missing journey rather than the failure it hit: %v", err)
		}
		t.Logf("Complete failed as arranged: %v", err)

		// Release the lock before reading, so the assertions below are not
		// waiting on this test's own transaction.
		_ = blocker.Rollback(context.WithoutCancel(ctx))

		// Neither half survived.
		if got := travelStatus(journey.ID); got != "in_transit" {
			t.Errorf("travel status = %q after a failed arrival, want in_transit", got)
		}
		if got := playerCity(); got != from.ID {
			t.Errorf("the player moved to %s despite the arrival failing", got)
		}
		// And the journey is still there to be completed properly.
		if _, err := repo.Active(ctx, player.ID); err != nil {
			t.Errorf("Active after a failed arrival = %v, want the journey still in progress", err)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. stats
// ---------------------------------------------------------------------------

// TestStatsEnsureDefaultsIsRaceSafe proves the contract StatsRepository
// declares: first contact from two directions at once produces one row, and
// the caller that lost the race carries the surviving row's numbers onward
// rather than the defaults it proposed.
//
// The mechanism is ON CONFLICT DO UPDATE taking a row lock, which only a real
// database does. DO NOTHING would not block, and the loser's read-back would
// intermittently find nothing on exactly the race this is meant to survive.
func TestStatsEnsureDefaultsIsRaceSafe(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)

	repo := postgres.NewStatsRepository(pool)

	const callers = 8
	// Every caller proposes different numbers, so a result that mixed two
	// rows together would be visible rather than accidentally identical.
	proposals := make([]application.Stats, callers)
	for i := range proposals {
		proposals[i] = application.Stats{
			Level:      1 + i,
			XP:         int64(100 * i),
			Health:     100 + i,
			MaxHealth:  100 + i,
			Energy:     50 + i,
			MaxEnergy:  50 + i,
			Happiness:  60 + i,
			Stamina:    70 + i,
			Reputation: i,
			UpdatedAt:  time.Now().UTC(),
		}
	}

	results := make([]*application.Stats, callers)
	_, errs := runConcurrently(t, callers, func(i int) (bool, error) {
		s, err := repo.EnsureDefaults(ctx, player.ID, proposals[i])
		results[i] = s
		return err == nil, err
	})

	for i, err := range errs {
		if err != nil {
			t.Fatalf("EnsureDefaults %d failed: %v", i, err)
		}
	}

	// player_stats has player_id as its primary key, so this is the schema
	// speaking; the assertion is that no caller saw an error getting there.
	if n := countRows(t, pool, `SELECT count(*) FROM player_stats WHERE player_id = $1::uuid`, player.ID); n != 1 {
		t.Fatalf("the player has %d stats rows, want 1", n)
	}

	first := results[0]
	if first == nil {
		t.Fatal("EnsureDefaults returned no stats")
	}
	for i, got := range results {
		if got == nil {
			t.Fatalf("caller %d received no stats", i)
		}
		if got.PlayerID != player.ID {
			t.Errorf("caller %d received stats for player %s", i, got.PlayerID)
		}
		if got.Level != first.Level || got.XP != first.XP || got.Health != first.Health ||
			got.Energy != first.Energy || got.Happiness != first.Happiness ||
			got.Stamina != first.Stamina || got.Reputation != first.Reputation {
			t.Errorf("caller %d received %+v, but caller 0 received %+v; the losers did not read the surviving row",
				i, *got, *first)
		}
	}

	// The winning row must be one of the proposals, not a blend of several.
	matched := false
	for _, p := range proposals {
		if first.Level == p.Level && first.XP == p.XP && first.Health == p.Health {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("the surviving row %+v matches no single caller's proposal", *first)
	}

	// A second EnsureDefaults must not reset a live player. This is what the
	// self-assignment in the conflict action buys.
	if err := repo.Save(ctx, application.Stats{
		PlayerID: player.ID, Level: 42, XP: 9999, Health: 7, MaxHealth: 200,
		Energy: 3, MaxEnergy: 90, Happiness: 11, Stamina: 12, Reputation: 13,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("saving live stats: %v", err)
	}
	again, err := repo.EnsureDefaults(ctx, player.ID, proposals[0])
	if err != nil {
		t.Fatalf("EnsureDefaults on an existing row: %v", err)
	}
	if again.Level != 42 || again.Health != 7 || again.XP != 9999 {
		t.Errorf("EnsureDefaults overwrote a live player with starting values: %+v", *again)
	}
}

// ---------------------------------------------------------------------------
// 5. skills
// ---------------------------------------------------------------------------

// TestSkillUpsertRespectsTheUniqueKey proves that two concurrent training
// commands cannot split one skill into two half-progressed rows.
//
// player_skills_player_skill_key is the only thing that can decide it: each
// caller mints its own surrogate id, so nothing in Go would collide.
func TestSkillUpsertRespectsTheUniqueKey(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)

	repo := postgres.NewSkillRepository(pool)
	code := "itest_" + randomToken(t, 10)

	const callers = 8
	levels := make([]int, callers)
	for i := range levels {
		levels[i] = i + 1
	}

	_, errs := runConcurrently(t, callers, func(i int) (bool, error) {
		err := repo.Upsert(ctx, application.Skill{
			PlayerID:  player.ID,
			Code:      code,
			Level:     levels[i],
			XP:        int64(levels[i]) * 100,
			UpdatedAt: time.Now().UTC(),
		})
		return err == nil, err
	})

	for i, err := range errs {
		if err != nil {
			t.Fatalf("upsert %d failed: %v", i, err)
		}
	}

	if n := countRows(t, pool,
		`SELECT count(*) FROM player_skills WHERE player_id = $1::uuid AND skill_code = $2`,
		player.ID, code); n != 1 {
		t.Fatalf("the player holds %d rows for skill %s, want 1", n, code)
	}

	got, err := repo.Get(ctx, player.ID, code)
	if err != nil {
		t.Fatalf("reading the skill back: %v", err)
	}
	// One caller won outright; the row must be that caller's, not a mixture.
	if got.XP != int64(got.Level)*100 {
		t.Errorf("the surviving skill is a blend of two writers: level %d with %d xp", got.Level, got.XP)
	}
	if got.Level < 1 || got.Level > callers {
		t.Errorf("the surviving level %d was proposed by nobody", got.Level)
	}

	// The surrogate id must survive a repeated upsert, or a skill would change
	// identity every time it was trained.
	var idBefore string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text FROM player_skills WHERE player_id = $1::uuid AND skill_code = $2`,
		player.ID, code).Scan(&idBefore); err != nil {
		t.Fatalf("reading the skill's id: %v", err)
	}
	if err := repo.Upsert(ctx, application.Skill{
		PlayerID: player.ID, Code: code, Level: 99, XP: 1234, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("re-upserting the skill: %v", err)
	}
	var idAfter string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text FROM player_skills WHERE player_id = $1::uuid AND skill_code = $2`,
		player.ID, code).Scan(&idAfter); err != nil {
		t.Fatalf("reading the skill's id again: %v", err)
	}
	if idAfter != idBefore {
		t.Errorf("the skill changed identity on a repeated upsert: %s -> %s", idBefore, idAfter)
	}

	// And the caller's stated progress must win, unlike EnsureDefaults.
	after, err := repo.Get(ctx, player.ID, code)
	if err != nil {
		t.Fatalf("reading the skill after the second upsert: %v", err)
	}
	if after.Level != 99 || after.XP != 1234 {
		t.Errorf("the second upsert did not apply: %+v", *after)
	}
}

// ---------------------------------------------------------------------------
// 6. friendships
// ---------------------------------------------------------------------------

// TestFriendshipAcceptWritesBothDirections proves that a friendship is never
// half-finished.
//
// Split into two statements, a crash between them leaves the requester's list
// showing an accepted friend while the accepter's shows nothing, the
// accepter's screen still offering an "accept" button for a request that is no
// longer pending, and nothing in the schema marking the pair as broken for a
// repair pass to find.
func TestFriendshipAcceptWritesBothDirections(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	requester := insertPlayer(t, pool)
	accepter := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, requester.ID, accepter.ID)

	repo := postgres.NewFriendshipRepository(pool)

	edgeStatus := func(from, to string) string {
		ctx := testCtx(t)
		var status string
		err := pool.Raw().QueryRow(ctx,
			`SELECT status FROM friendships WHERE player_id = $1::uuid AND friend_player_id = $2::uuid`,
			from, to).Scan(&status)
		if err != nil {
			return ""
		}
		return status
	}

	t.Run("the self edge surfaces as a sentinel", func(t *testing.T) {
		// friendships_no_self_check rejects it at the lowest possible level;
		// what is asserted here is that the check violation is translated
		// rather than reaching a player as a driver error.
		if err := repo.Request(ctx, requester.ID, requester.ID); err != postgres.ErrSelfFriendship {
			t.Errorf("Request(self) = %v, want postgres.ErrSelfFriendship", err)
		}
		if err := repo.Block(ctx, requester.ID, requester.ID); err != postgres.ErrSelfFriendship {
			t.Errorf("Block(self) = %v, want postgres.ErrSelfFriendship", err)
		}
		if n := countRows(t, pool,
			`SELECT count(*) FROM friendships WHERE player_id = friend_player_id`); n != 0 {
			t.Errorf("the database holds %d self edges", n)
		}
	})

	t.Run("accepting without a request is refused", func(t *testing.T) {
		if err := repo.Accept(ctx, accepter.ID, requester.ID); err != application.ErrNotFriends {
			t.Errorf("Accept without a request = %v, want application.ErrNotFriends", err)
		}
		if n := countRows(t, pool,
			`SELECT count(*) FROM friendships WHERE player_id IN ($1::uuid, $2::uuid)`,
			requester.ID, accepter.ID); n != 0 {
			t.Errorf("a refused Accept wrote %d row(s)", n)
		}
	})

	t.Run("one request writes one edge", func(t *testing.T) {
		if err := repo.Request(ctx, requester.ID, accepter.ID); err != nil {
			t.Fatalf("requesting: %v", err)
		}
		if got := edgeStatus(requester.ID, accepter.ID); got != "pending" {
			t.Fatalf("the requester's edge is %q, want pending", got)
		}
		// The other player's edge is theirs to create by accepting, which is
		// the whole reason the request state exists.
		if got := edgeStatus(accepter.ID, requester.ID); got != "" {
			t.Errorf("requesting also wrote the other player's edge as %q", got)
		}
		// Pressing the button again is ordinary.
		if err := repo.Request(ctx, requester.ID, accepter.ID); err != nil {
			t.Errorf("re-sending a pending request = %v, want success", err)
		}
	})

	t.Run("a failure leaves the request pending", func(t *testing.T) {
		// The reverse edge is created by the second statement. Holding a row
		// it must touch makes that statement block, and a deadline on the
		// caller aborts the transaction after the first statement has already
		// flipped the request to accepted inside it. If the two writes were
		// not one transaction, this is the moment a friendship would end up
		// belonging to only one of the two people.
		if err := repo.Block(ctx, accepter.ID, requester.ID); err != nil {
			t.Fatalf("pre-creating the accepter's edge: %v", err)
		}

		conn, err := pool.Raw().Acquire(ctx)
		if err != nil {
			t.Fatalf("acquiring a connection: %v", err)
		}
		defer conn.Release()

		blocker, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("beginning the blocking transaction: %v", err)
		}
		defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()

		var lockedID string
		if err := blocker.QueryRow(ctx,
			`SELECT id::text FROM friendships WHERE player_id = $1::uuid AND friend_player_id = $2::uuid FOR UPDATE`,
			accepter.ID, requester.ID).Scan(&lockedID); err != nil {
			t.Fatalf("locking the accepter's edge: %v", err)
		}

		shortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := repo.Accept(shortCtx, accepter.ID, requester.ID); err == nil {
			t.Fatal("Accept reported success although its second write was blocked")
		} else {
			t.Logf("Accept failed as arranged: %v", err)
		}

		_ = blocker.Rollback(context.WithoutCancel(ctx))

		if got := edgeStatus(requester.ID, accepter.ID); got != "pending" {
			t.Errorf("the request is %q after a failed Accept, want it still pending", got)
		}

		// Put the accepter's edge back to where the next subtest expects it.
		if err := repo.Remove(ctx, accepter.ID, requester.ID); err != nil {
			t.Fatalf("removing the pre-created edge: %v", err)
		}
	})

	t.Run("accepting writes both directions", func(t *testing.T) {
		if err := repo.Accept(ctx, accepter.ID, requester.ID); err != nil {
			t.Fatalf("accepting: %v", err)
		}

		if got := edgeStatus(requester.ID, accepter.ID); got != "accepted" {
			t.Errorf("the requester's edge is %q, want accepted", got)
		}
		if got := edgeStatus(accepter.ID, requester.ID); got != "accepted" {
			t.Errorf("the accepter's edge is %q, want accepted", got)
		}

		// A repeated request must not downgrade an accepted friendship.
		if err := repo.Request(ctx, requester.ID, accepter.ID); err != application.ErrAlreadyFriends {
			t.Errorf("requesting an accepted friend = %v, want application.ErrAlreadyFriends", err)
		}
		if got := edgeStatus(requester.ID, accepter.ID); got != "accepted" {
			t.Errorf("a repeated request downgraded the friendship to %q", got)
		}

		// Both edges are listed from their owner's side only.
		edges, err := repo.List(ctx, requester.ID)
		if err != nil {
			t.Fatalf("listing the requester's edges: %v", err)
		}
		if len(edges) != 1 || edges[0].FriendPlayerID != accepter.ID {
			t.Errorf("the requester's list is %+v, want exactly the one edge they own", edges)
		}
	})

	t.Run("removal is one-directional", func(t *testing.T) {
		if err := repo.Remove(ctx, requester.ID, accepter.ID); err != nil {
			t.Fatalf("removing: %v", err)
		}
		if got := edgeStatus(requester.ID, accepter.ID); got != "" {
			t.Errorf("the remover's edge survives as %q", got)
		}
		// The other player's row is theirs; rewriting it through an ordinary
		// command is exactly what this must not do.
		if got := edgeStatus(accepter.ID, requester.ID); got != "accepted" {
			t.Errorf("removing one edge also changed the other player's, now %q", got)
		}
		// Removing what is no longer there is stale state worth reporting.
		if err := repo.Remove(ctx, requester.ID, accepter.ID); err != application.ErrNotFriends {
			t.Errorf("a repeated Remove = %v, want application.ErrNotFriends", err)
		}
	})
}

// ---------------------------------------------------------------------------
// 7. the read-only repositories, against rows this file wrote
// ---------------------------------------------------------------------------

// The city and search repositories are read-only, so what is worth proving
// about them on a live server is that their statements match the schema and
// that their predicates hold. Neither assertion may depend on a table count:
// another process may be loading content into these very tables.
func TestCityAndSearchAgainstOwnFixtures(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	city := seedCity(t, pool, "lookup")
	cities := postgres.NewCityRepository(pool)

	t.Run("a city is found by both keys", func(t *testing.T) {
		byCode, err := cities.ByCode(ctx, city.Code)
		if err != nil {
			t.Fatalf("ByCode(%s): %v", city.Code, err)
		}
		if byCode.ID != city.ID || byCode.Name != city.Name || byCode.TaxRateBPS != city.TaxRateBPS {
			t.Errorf("ByCode returned %+v, want the seeded %+v", *byCode, city)
		}

		byID, err := cities.ByID(ctx, city.ID)
		if err != nil {
			t.Fatalf("ByID(%s): %v", city.ID, err)
		}
		if *byID != *byCode {
			t.Errorf("ByID and ByCode disagree: %+v vs %+v", *byID, *byCode)
		}
	})

	t.Run("a malformed identifier is a miss, not a fault", func(t *testing.T) {
		// The value reaches the repository from a callback payload, so a
		// string that is not uuid text names no city, exactly like a
		// well-formed uuid no row carries.
		if _, err := cities.ByID(ctx, "not-a-uuid"); err != application.ErrCityNotFound {
			t.Errorf("ByID(non-uuid) = %v, want application.ErrCityNotFound", err)
		}
		if _, err := cities.ByID(ctx, newUUID(t)); err != application.ErrCityNotFound {
			t.Errorf("ByID(unknown) = %v, want application.ErrCityNotFound", err)
		}
		// The lookup by code is exact, never case-folded.
		if _, err := cities.ByCode(ctx, strings.ToLower(city.Code)); err != application.ErrCityNotFound {
			t.Errorf("ByCode folds case, so two spellings resolve to one row")
		}
	})

	t.Run("the listing contains the seeded city and is ordered by code", func(t *testing.T) {
		all, err := cities.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		found := false
		listed := make([]string, 0, len(all))
		inList := make(map[string]bool, len(all))
		for _, c := range all {
			if c.ID == city.ID {
				found = true
			}
			listed = append(listed, c.Code)
			inList[c.Code] = true
		}
		if !found {
			t.Fatalf("the seeded city %s is missing from the listing", city.Code)
		}

		// The order is compared against the server's own ORDER BY rather than
		// recomputed in Go. Sorting text is the database's collation's job,
		// and it is not byte order: under the collation this database runs,
		// "fenwick_span" sorts before "IT_CITY", which a Go comparison of the
		// two strings would call out of order.
		rows, err := pool.Raw().Query(ctx, `SELECT code FROM cities ORDER BY code`)
		if err != nil {
			t.Fatalf("reading the server's ordering: %v", err)
		}
		var expected []string
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				rows.Close()
				t.Fatalf("scanning a code: %v", err)
			}
			// Another process may load content between the two queries, so
			// only the codes present in both are compared.
			if inList[code] {
				expected = append(expected, code)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("reading codes: %v", err)
		}

		fromServer := make(map[string]bool, len(expected))
		for _, code := range expected {
			fromServer[code] = true
		}
		var got []string
		for _, code := range listed {
			if fromServer[code] {
				got = append(got, code)
			}
		}

		if strings.Join(got, "\x00") != strings.Join(expected, "\x00") {
			t.Errorf("the listing is not in the server's code order:\ngot  %v\nwant %v", got, expected)
		}
	})

	t.Run("search finds an active player and never an inactive one", func(t *testing.T) {
		active := insertPlayer(t, pool)
		cleanupPlayerRows(t, pool, active.ID)
		banned := insertPlayer(t, pool)
		cleanupPlayerRows(t, pool, banned.ID)

		// A display name nothing else in the database can share.
		name := "Integration " + randomToken(t, 16)
		for _, id := range []string{active.ID, banned.ID} {
			if _, err := pool.Raw().Exec(ctx,
				`UPDATE players SET display_name = $2 WHERE id = $1::uuid`, id, name); err != nil {
				t.Fatalf("naming the test player: %v", err)
			}
		}
		if _, err := pool.Raw().Exec(ctx,
			`UPDATE players SET status = 'banned' WHERE id = $1::uuid`, banned.ID); err != nil {
			t.Fatalf("banning the second player: %v", err)
		}

		search := postgres.NewPlayerSearchRepository(pool)
		got, err := search.Search(ctx, name, 10, 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(got) != 1 || got[0].ID != active.ID {
			t.Fatalf("Search returned %d player(s), want only the active one", len(got))
		}

		// A banned account surfacing here would confirm a ban to anyone who
		// looked and would offer a friend request it can never answer.
		for _, p := range got {
			if p.ID == banned.ID {
				t.Error("Search returned a banned account")
			}
		}

		// A wildcard typed by a player is a literal, not a request for
		// everyone.
		wild, err := search.Search(ctx, "%", 10, 0)
		if err != nil {
			t.Fatalf("Search(%%): %v", err)
		}
		for _, p := range wild {
			if p.ID == active.ID {
				t.Error("a literal percent sign matched every player")
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 8. the sentinels, against the server that produces them
// ---------------------------------------------------------------------------

// Every "not found" a phase 1 repository can report, checked against a real
// empty result rather than a canned pgx.ErrNoRows.
func TestPhase1MissingRowsMapToSentinels(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	player := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, player.ID)

	unknown := newUUID(t)

	if _, err := postgres.NewStatsRepository(pool).Get(ctx, player.ID); err != postgres.ErrStatsNotFound {
		t.Errorf("Get stats for a player with no row = %v, want postgres.ErrStatsNotFound", err)
	}
	if _, err := postgres.NewSkillRepository(pool).Get(ctx, player.ID, "nothing"); err != application.ErrSkillNotFound {
		t.Errorf("Get an untrained skill = %v, want application.ErrSkillNotFound", err)
	}
	if _, err := postgres.NewTravelRepository(pool).Active(ctx, player.ID); err != application.ErrNoActiveTravel {
		t.Errorf("Active for a player at home = %v, want application.ErrNoActiveTravel", err)
	}
	if err := postgres.NewTravelRepository(pool).Cancel(ctx, unknown); err != application.ErrNoActiveTravel {
		t.Errorf("Cancel of an unknown journey = %v, want application.ErrNoActiveTravel", err)
	}

	actions := postgres.NewGameActionRepository(pool)
	if err := actions.Complete(ctx, unknown); err != postgres.ErrGameActionNotFound {
		t.Errorf("Complete of an unknown action = %v, want postgres.ErrGameActionNotFound", err)
	}
	if err := actions.Fail(ctx, unknown, "boom"); err != postgres.ErrGameActionNotFound {
		t.Errorf("Fail of an unknown action = %v, want postgres.ErrGameActionNotFound", err)
	}

	// A closed action stays closed: a late worker cannot resurrect it, and the
	// first completion keeps its timestamp.
	actionType := "itest_closed_" + randomToken(t, 12)
	cleanupActions(t, pool, actionType)
	id := seedAction(t, pool, actionType, pastEpoch)

	if err := actions.Complete(ctx, id); err != nil {
		t.Fatalf("completing a scheduled action: %v", err)
	}
	if err := actions.Complete(ctx, id); err != postgres.ErrGameActionNotFound {
		t.Errorf("a replayed Complete = %v, want postgres.ErrGameActionNotFound", err)
	}
	if err := actions.Fail(ctx, id, "too late"); err != postgres.ErrGameActionNotFound {
		t.Errorf("failing a completed action = %v, want postgres.ErrGameActionNotFound", err)
	}

	// Failing an action records the reason inside the payload, because
	// game_actions has no column of its own for one.
	failing := seedAction(t, pool, actionType, pastEpoch)
	reason := "boundary " + randomToken(t, 8)
	if err := actions.Fail(ctx, failing, reason); err != nil {
		t.Fatalf("failing an action: %v", err)
	}
	var payload string
	if err := pool.Raw().QueryRow(ctx,
		`SELECT payload::text FROM game_actions WHERE id = $1::uuid`, failing).Scan(&payload); err != nil {
		t.Fatalf("reading the failed action's payload: %v", err)
	}
	if !strings.Contains(payload, reason) {
		t.Errorf("the failure reason was not recorded: %s", payload)
	}
	if n := countRows(t, pool,
		`SELECT retry_count FROM game_actions WHERE id = $1::uuid`, failing); n != 1 {
		t.Errorf("retry_count = %d after one failure, want 1", n)
	}
}
