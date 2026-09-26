//go:build integration

// Integration tests for the operator's runtime switches
// (migrations/0041_runtime_switches): setting telegram_play through
// internal/operator.Ops — the same path cmd/admin's `switch set` and the
// panel's System > Switches page both call — writes the audited row a real
// Postgres holds, and internal/switches.Reader answers from a real Redis
// cache in front of it, exactly as the gateway reads it before publishing a
// player's command.
//
// What this file does NOT do is take down the shared INTEGRATION_REDIS_URL
// or INTEGRATION_DSN instances to prove the fail-open contract — that
// instance is shared with every other integration test that may be running
// alongside this one, and breaking it on purpose would be a bad neighbour.
// The fail-open contract itself (a cache or database failure must return the
// caller's default, never an error that blocks play) is instead proven with
// a Source and a Cache that fail on command, which is what internal/switches
// depends on through its own two small interfaces; cmd/gateway's
// TestRedirectedFailsOpenWhenTheSwitchIsUnavailable covers the same
// contract, one layer up, the same way.
package tests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/switches"
)

// switchSource adapts the database's read to switches.Source, exactly as
// cmd/gateway, cmd/admin and cmd/panel each define their own copy of it
// beside their own pool.
type switchSource struct{ ops *postgres.SwitchOps }

func (s switchSource) Get(ctx context.Context, key string) (string, bool, error) {
	return s.ops.Get(ctx, key)
}

// resetSwitch clears telegram_play back to "on" through the same audited
// path the test itself uses, so one test's ending state cannot leak into the
// next; operator_switches has no delete, only ever a new current value.
func resetSwitch(t *testing.T, ctx context.Context, ops operator.Ops, key, value string) {
	t.Helper()
	if _, err := ops.SetSwitch(ctx, key, value, operator.Actor{Name: "test-teardown", Reason: "reset after test"}); err != nil {
		t.Errorf("resetting %s to %q: %v", key, value, err)
	}
}

// TestSwitchSetIsAudited: setting telegram_play off writes the current row
// in operator_switches AND an audit_logs row naming the operator, the
// action and the reason — the same audit trail every other console change
// leaves, so `admin switch list` and the panel's history both find it.
func TestSwitchSetIsAudited(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool}
	t.Cleanup(func() { resetSwitch(t, testCtx(t), ops, switches.KeyTelegramPlay, switches.PlayOn) })

	actor := operator.Actor{Name: "integration-operator", Reason: "prove the audit trail"}
	st, err := ops.SetSwitch(ctx, switches.KeyTelegramPlay, switches.PlayOff, actor)
	if err != nil {
		t.Fatalf("SetSwitch: %v", err)
	}
	if st.Value != switches.PlayOff || st.ChangedBy != actor.Name {
		t.Fatalf("SetSwitch returned %+v", st)
	}

	// The current-value table: exactly the row SetSwitch reported.
	var value, changedBy, reason string
	err = pool.Raw().QueryRow(ctx, `SELECT value, changed_by, reason FROM operator_switches WHERE key = $1`,
		switches.KeyTelegramPlay).Scan(&value, &changedBy, &reason)
	if err != nil {
		t.Fatalf("reading operator_switches: %v", err)
	}
	if value != switches.PlayOff || changedBy != actor.Name || reason != actor.Reason {
		t.Fatalf("operator_switches row = (%q, %q, %q), want (%q, %q, %q)",
			value, changedBy, reason, switches.PlayOff, actor.Name, actor.Reason)
	}

	// The audit trail: the same generic table every operator action writes,
	// with a row for this change.
	var auditActor, auditAction, auditReason string
	var newValue []byte
	err = pool.Raw().QueryRow(ctx, `SELECT actor, action, reason, new_value FROM audit_logs
		WHERE target_type = 'operator_switches' AND action = 'switch.set'
		ORDER BY created_at DESC LIMIT 1`).Scan(&auditActor, &auditAction, &auditReason, &newValue)
	if err != nil {
		t.Fatalf("reading audit_logs: %v", err)
	}
	if auditActor != actor.Name || auditReason != actor.Reason {
		t.Fatalf("audit_logs row = (actor %q, reason %q), want (%q, %q)", auditActor, auditReason, actor.Name, actor.Reason)
	}
	if !strings.Contains(string(newValue), switches.KeyTelegramPlay) || !strings.Contains(string(newValue), switches.PlayOff) {
		t.Errorf("audit_logs.new_value = %s, want it to name the key and the value", newValue)
	}
}

// TestSwitchCacheTTLPropagation: a change is not necessarily instant — it
// takes effect within the cache's TTL, not on the next read regardless of
// how recently the previous one was cached. Off is read once (filling the
// cache); flipping back to on does not change what a read answers until the
// cached entry's TTL has passed, at which point it does.
func TestSwitchCacheTTLPropagation(t *testing.T) {
	pool := requirePostgres(t)
	rdb := requireRedis(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool}
	t.Cleanup(func() { resetSwitch(t, testCtx(t), ops, switches.KeyTelegramPlay, switches.PlayOn) })

	const ttl = 2 * time.Second
	reader := &switches.Reader{Source: switchSource{postgres.NewSwitchOps(pool)}, Cache: infraredis.NewSwitchCache(rdb), TTL: ttl}

	actor := operator.Actor{Name: "integration-operator", Reason: "prove the cache TTL"}
	if _, err := ops.SetSwitch(ctx, switches.KeyTelegramPlay, switches.PlayOff, actor); err != nil {
		t.Fatalf("SetSwitch(off): %v", err)
	}

	// First read: a cache miss, straight from the database, and it fills
	// the cache for ttl.
	v, age, err := reader.Get(ctx, switches.KeyTelegramPlay, switches.PlayOn)
	if err != nil || v != switches.PlayOff {
		t.Fatalf("Get after setting off = (%q, %v), want (%q, nil)", v, err, switches.PlayOff)
	}
	if age != 0 {
		t.Errorf("a fresh database read reported a cache age of %s, want 0", age)
	}

	// The switch is turned back on directly, bypassing the cache (exactly
	// as another gateway instance's cached copy would not see this yet).
	if _, err := ops.SetSwitch(ctx, switches.KeyTelegramPlay, switches.PlayOn, actor); err != nil {
		t.Fatalf("SetSwitch(on): %v", err)
	}

	// Within the TTL, the cached "off" still answers: the change has not
	// taken effect everywhere yet, by design.
	v, _, err = reader.Get(ctx, switches.KeyTelegramPlay, switches.PlayOn)
	if err != nil || v != switches.PlayOff {
		t.Fatalf("Get within the TTL = (%q, %v), want the stale cached %q", v, err, switches.PlayOff)
	}

	// Past the TTL, the change has taken effect: switch on resumes play.
	time.Sleep(ttl + 500*time.Millisecond)
	v, _, err = reader.Get(ctx, switches.KeyTelegramPlay, switches.PlayOn)
	if err != nil || v != switches.PlayOn {
		t.Fatalf("Get after the TTL = (%q, %v), want %q: the switch did not take effect within its TTL", v, err, switches.PlayOn)
	}
}

// TestSwitchReaderFailsOpen: when neither the cache nor the database can
// answer, Get returns the caller's default rather than an error that would
// block a player's command — a broken switch must not take the game down.
// See the package doc above for why this does not touch the shared
// integration Redis or Postgres to simulate the outage.
func TestSwitchReaderFailsOpen(t *testing.T) {
	reader := &switches.Reader{Source: alwaysFailingSource{}, Cache: alwaysFailingCache{}, TTL: time.Second}

	v, age, err := reader.Get(context.Background(), switches.KeyTelegramPlay, switches.PlayOn)
	if v != switches.PlayOn {
		t.Fatalf("Get with everything down returned %q, want the default %q", v, switches.PlayOn)
	}
	if err == nil {
		t.Error("Get with everything down reported no error to log")
	}
	if age != 0 {
		t.Errorf("age = %s, want 0 for a failed read", age)
	}
	if switches.Blocked(v, false) || switches.Blocked(v, true) {
		t.Error("the default value the reader failed open to would still block play")
	}
}

type alwaysFailingSource struct{}

func (alwaysFailingSource) Get(context.Context, string) (string, bool, error) {
	return "", false, errors.New("the database is unreachable")
}

type alwaysFailingCache struct{}

func (alwaysFailingCache) Get(context.Context, string) (string, time.Time, bool, error) {
	return "", time.Time{}, false, errors.New("redis is unreachable")
}

func (alwaysFailingCache) Set(context.Context, string, string, time.Time, time.Duration) error {
	return errors.New("redis is unreachable")
}
