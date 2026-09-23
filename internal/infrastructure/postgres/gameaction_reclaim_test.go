package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The reaper runs on every scheduler tick, in every scheduler. Like the claim,
// its safety against a second reaper is a property of the statement's shape:
// a standalone locking select would let two reapers return the same row and
// count one abandoned claim as two attempts.
func TestReclaimStaleHoldsItsLocks(t *testing.T) {
	sql := normalize(reclaimStaleSQL)

	if !strings.HasPrefix(sql, "WITH stale AS ( SELECT id FROM game_actions") {
		t.Errorf("the locking select is not wrapped in the returning update:\n%s", sql)
	}
	for _, want := range []string{
		"FOR UPDATE SKIP LOCKED",
		"LIMIT $2",
		"UPDATE game_actions a",
		"SET status = 'scheduled'",
		"retry_count = a.retry_count + 1",
		"claimed_at = NULL",
		"claimed_by = NULL",
		"FROM stale s WHERE a.id = s.id",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the reclaim statement is missing %q:\n%s", want, sql)
		}
	}
}

// The reclaim's predicate and order are written to match
// game_actions_claimed_idx exactly, so the reaper walks the front of a partial
// index that holds only rows in flight. Drift turns every tick into a scan of
// the whole history.
func TestReclaimStaleMatchesTheClaimIndex(t *testing.T) {
	const path = "../../../migrations/0004_game_action_claims.up.sql"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	migration := normalize(string(raw))

	if !strings.Contains(migration, "CREATE INDEX game_actions_claimed_idx ON game_actions (claimed_at) WHERE status = 'running'") {
		t.Fatalf("game_actions_claimed_idx is no longer a partial index on (claimed_at) WHERE status = 'running'")
	}
	if !strings.Contains(normalize(reclaimStaleSQL), "WHERE status = 'running' AND claimed_at < $1 ORDER BY claimed_at") {
		t.Errorf("the reclaim predicate and order no longer match game_actions_claimed_idx:\n%s", normalize(reclaimStaleSQL))
	}
}

// The claim is what gives the reaper something to measure. A claim that did
// not stamp claimed_at would leave every running row invisible to the reaper,
// which is the stranded-player bug in a new place.
func TestClaimDueStampsTheClaimTime(t *testing.T) {
	sql := normalize(claimDueSQL)
	if !strings.Contains(sql, "SET status = 'running', claimed_at = $1") {
		t.Errorf("the due claim does not record when it claimed:\n%s", sql)
	}
}

func TestReclaimStaleReportsRowsMoved(t *testing.T) {
	q := &fakeQuerier{execTag: pgconn.NewCommandTag("UPDATE 3")}
	r := &GameActionRepository{q: q}
	cutoff := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	n, err := r.ReclaimStale(context.Background(), cutoff, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("reported %d rows, want 3", n)
	}
	got := q.last()
	if got.sql != reclaimStaleSQL {
		t.Errorf("sent a statement other than reclaimStaleSQL")
	}
	if len(got.args) != 2 || got.args[0] != cutoff || got.args[1] != 50 {
		t.Errorf("sent args %v, want [%v 50]", got.args, cutoff)
	}
}

func TestReclaimStaleRefusesAMeaninglessCall(t *testing.T) {
	r := &GameActionRepository{q: &fakeQuerier{}}
	cutoff := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	if _, err := r.ReclaimStale(context.Background(), cutoff, 0); err == nil {
		t.Error("a zero limit was accepted")
	}
	// A zero cutoff matches nothing, so a reaper wired with one would never
	// reap and never say so.
	if _, err := r.ReclaimStale(context.Background(), time.Time{}, 10); err == nil {
		t.Error("a zero cutoff was accepted")
	}
}

func TestReclaimStaleWrapsDriverErrors(t *testing.T) {
	cause := errors.New("connection reset")
	r := &GameActionRepository{q: &fakeQuerier{execErr: cause}}

	n, err := r.ReclaimStale(context.Background(), time.Now(), 10)
	if !errors.Is(err, cause) {
		t.Fatalf("got %v, want the driver error in the chain", err)
	}
	if n != 0 {
		t.Errorf("reported %d rows on failure, want 0", n)
	}
}
