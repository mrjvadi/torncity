//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// The console's changes against the real schema: each acts, leaves its
// audit row, and refuses what the state does not allow.

func panelActor(reason string) operator.Actor {
	return operator.Actor{Name: "panel:integration", Reason: reason, At: time.Now()}
}

func auditRows(t *testing.T, pool *postgres.Pool, action, target string) int {
	t.Helper()
	return countRows(t, pool, `SELECT count(*) FROM audit_logs WHERE action = $1 AND target_id = $2::uuid`, action, target)
}

func TestPanelModeration(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool, Language: "fa"}
	p := insertPlayer(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM player_moderation WHERE player_id = $1::uuid`, p.ID)
	})
	reader := postgres.NewModerationReader(pool)

	if _, err := ops.Moderate(ctx, p.PublicCode, "mute", time.Hour, operator.Actor{Name: "panel:x"}); !errors.Is(err, operator.ErrNoActor) {
		t.Fatalf("a moderation without a reason: %v", err)
	}
	m, err := ops.Moderate(ctx, p.PublicCode, "mute", time.Hour, panelActor("spam in the city group"))
	if err != nil || m.No == 0 || m.EndsAt == nil {
		t.Fatalf("mute: %+v %v", m, err)
	}
	if _, err := ops.Moderate(ctx, p.PublicCode, "mute", 0, panelActor("again")); !errors.Is(err, postgres.ErrPanelConflict) {
		t.Fatalf("a second mute: %v", err)
	}
	st, err := reader.Standing(ctx, p.TelegramUserID, time.Now())
	if err != nil || !st.Muted || st.Banned || st.Until.IsZero() {
		t.Fatalf("standing after the mute: %+v %v", st, err)
	}
	if _, err := ops.Moderate(ctx, p.PublicCode, "ban", 0, panelActor("repeated abuse")); err != nil {
		t.Fatalf("ban: %v", err)
	}
	if st, _ = reader.Standing(ctx, p.TelegramUserID, time.Now()); !st.Banned {
		t.Fatalf("not banned: %+v", st)
	}
	// An hour later the mute has ended by itself; the ban stands.
	if st, _ = reader.Standing(ctx, p.TelegramUserID, time.Now().Add(2*time.Hour)); st.Muted || !st.Banned {
		t.Fatalf("two hours on: %+v", st)
	}
	for _, kind := range []string{"mute", "ban"} {
		if _, err := ops.LiftModeration(ctx, p.PublicCode, kind, panelActor("appeal accepted")); err != nil {
			t.Fatalf("lift %s: %v", kind, err)
		}
	}
	if _, err := ops.LiftModeration(ctx, p.PublicCode, "ban", panelActor("again")); !errors.Is(err, postgres.ErrPanelConflict) {
		t.Fatalf("lifting nothing: %v", err)
	}
	if st, _ = reader.Standing(ctx, p.TelegramUserID, time.Now()); st.Muted || st.Banned {
		t.Fatalf("still moderated: %+v", st)
	}
	for _, a := range []string{"player.mute", "player.ban", "player.unmute", "player.unban"} {
		if n := auditRows(t, pool, a, p.ID); n != 1 {
			t.Fatalf("%d %s audit rows", n, a)
		}
	}
	if _, err := ops.Moderate(ctx, "ZZZZZZZ", "ban", 0, panelActor("nobody")); !errors.Is(err, postgres.ErrNoSuchPlayer) {
		t.Fatalf("an unknown player: %v", err)
	}
}

func TestPanelEndsASentenceAndAStayNow(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool, Language: "fa"}
	p := insertPlayer(t, pool)
	var city string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities ORDER BY code LIMIT 1`).Scan(&city); err != nil {
		t.Skip("no city; run `admin content load`")
	}
	now := time.Now().UTC()
	jailAction, stayAction := newUUID(t), newUUID(t)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Raw().Exec(bg, `DELETE FROM jail_sentences WHERE player_id = $1::uuid`, p.ID)
		_, _ = pool.Raw().Exec(bg, `DELETE FROM hospital_stays WHERE player_id = $1::uuid`, p.ID)
		_, _ = pool.Raw().Exec(bg, `DELETE FROM game_actions WHERE id IN ($1::uuid, $2::uuid)`, jailAction, stayAction)
	})
	start := now.Add(-time.Minute)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO game_actions (id, action_type, actor_type, actor_id, status, started_at, finish_at)
		 VALUES ($1::uuid, 'crime.release', 'player', $2::uuid, 'scheduled', $3, $3::timestamptz + interval '2 hours')`,
			[]any{jailAction, p.ID, start}},
		{`INSERT INTO game_actions (id, action_type, actor_type, actor_id, status, started_at, finish_at)
		 VALUES ($1::uuid, 'health.discharge', 'player', $2::uuid, 'failed', $3, $3::timestamptz + interval '2 hours')`,
			[]any{stayAction, p.ID, start}},
		{`INSERT INTO jail_sentences (id, player_id, city_id, reason, term_seconds, game_action_id, status, starts_at, ends_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'arrest', 7200, $3::uuid, 'serving', $4, $4::timestamptz + interval '2 hours')`,
			[]any{p.ID, city, jailAction, start}},
		{`INSERT INTO hospital_stays (id, player_id, city_id, cause, status, health_in, health_out, admitted_at, ends_at, game_action_id)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'crime', 'admitted', 10, 60, $3, $3::timestamptz + interval '2 hours', $4::uuid)`,
			[]any{p.ID, city, start, stayAction}},
	} {
		if _, err := pool.Raw().Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	released, err := ops.Release(ctx, p.PublicCode, panelActor("wrongly jailed by a bug"))
	if err != nil || released.Action != jailAction || released.EndsAt.After(time.Now()) {
		t.Fatalf("release: %+v %v", released, err)
	}
	discharged, err := ops.Discharge(ctx, p.PublicCode, panelActor("stuck in hospital"))
	if err != nil || discharged.Action != stayAction {
		t.Fatalf("discharge: %+v %v", discharged, err)
	}
	var due int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM game_actions WHERE id IN ($1::uuid, $2::uuid)
		AND status = 'scheduled' AND finish_at <= now()`, jailAction, stayAction).Scan(&due); err != nil || due != 2 {
		t.Fatalf("%d actions due now (%v), want 2", due, err)
	}
	if auditRows(t, pool, "player.release", p.ID) != 1 || auditRows(t, pool, "player.discharge", p.ID) != 1 {
		t.Fatal("the audit rows are missing")
	}
	// Not in jail: nothing to release.
	if _, err := pool.Raw().Exec(ctx, `UPDATE jail_sentences SET status = 'released', released_at = now() WHERE player_id = $1::uuid`,
		p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Release(ctx, p.PublicCode, panelActor("again")); !errors.Is(err, postgres.ErrPanelConflict) {
		t.Fatalf("releasing a free player: %v", err)
	}
}

func TestPanelRequeuesAStuckAction(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool, Language: "fa"}
	failed, done := newUUID(t), newUUID(t)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM game_actions WHERE id IN ($1::uuid, $2::uuid)`, failed, done)
	})
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO game_actions (id, action_type, actor_type, status, started_at, finish_at,
		completed_at, payload) VALUES ($1::uuid, 'integration.test', 'system', 'failed', now(), now() + interval '1 hour', now(),
		'{"last_error": "boom", "x": 1}'),
		($2::uuid, 'integration.test', 'system', 'completed', now(), now(), now(), '{}')`, failed, done); err != nil {
		t.Fatal(err)
	}
	out, err := ops.Requeue(ctx, failed, panelActor("the handler was fixed"))
	if err != nil || out.WasStatus != "failed" || out.LastError != "boom" {
		t.Fatalf("requeue: %+v %v", out, err)
	}
	var status string
	var hasErr bool
	if err := pool.Raw().QueryRow(ctx, `SELECT status, payload ? 'last_error' FROM game_actions WHERE id = $1::uuid`, failed).
		Scan(&status, &hasErr); err != nil || status != "scheduled" || hasErr {
		t.Fatalf("after the requeue: %s %v %v", status, hasErr, err)
	}
	if _, err := ops.Requeue(ctx, done, panelActor("no")); !errors.Is(err, postgres.ErrPanelConflict) {
		t.Fatalf("requeueing a completed action: %v", err)
	}
	if auditRows(t, pool, "action.requeue", failed) != 1 {
		t.Fatal("no audit row")
	}
}

func TestPanelDissolvesACompany(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ops := operator.Ops{Pool: pool, Language: "fa"}
	owner := insertPlayer(t, pool)
	var city, kind string
	var version int
	if err := pool.Raw().QueryRow(ctx, `SELECT c.id::text, v.version FROM cities c JOIN content_versions v ON v.status = 'active'
		WHERE c.jurisdiction_id IS NOT NULL ORDER BY c.code LIMIT 1`).Scan(&city, &version); err != nil {
		t.Skip("no content; run `admin content load`")
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT d.code FROM content_documents d JOIN content_versions v ON v.id = d.content_version_id
		WHERE v.status = 'active' AND d.kind = 'company_type' ORDER BY d.position LIMIT 1`).Scan(&kind); err != nil {
		kind = "bakery"
	}
	id := newUUID(t)
	code := "ZQ" + id[:4]
	code = upperASCII(code)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Raw().Exec(bg, `DELETE FROM outbox WHERE payload->>'company_id' = $1`, id)
		_, _ = pool.Raw().Exec(bg, `DELETE FROM company_openings WHERE company_id = $1::uuid`, id)
		_, _ = pool.Raw().Exec(bg, `DELETE FROM companies WHERE id = $1::uuid`, id)
		_, _ = pool.Raw().Exec(bg, `DELETE FROM accounts WHERE owner_id = $1::uuid AND balance = 0`, id)
	})
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id,
		status, price_bps, total_shares, registration_fee, content_version, founded_at, updated_at)
		VALUES ($1::uuid, $2, 'Integration Co', $2, $3, $4::uuid, $5::uuid, 'active', 10000, 100, 0, $6, now(), now())`,
		id, code, kind, city, owner.ID, version); err != nil {
		t.Fatal(err)
	}
	out, err := ops.Dissolve(ctx, code, panelActor("a company used for fraud"))
	if err != nil || out.Code != code {
		t.Fatalf("dissolve: %+v %v", out, err)
	}
	var status, reason string
	if err := pool.Raw().QueryRow(ctx, `SELECT status, close_reason FROM companies WHERE id = $1::uuid`, id).
		Scan(&status, &reason); err != nil || status != "dissolved" || reason != "operator" {
		t.Fatalf("after dissolving: %s %s %v", status, reason, err)
	}
	if _, err := ops.Dissolve(ctx, code, panelActor("again")); err == nil {
		t.Fatal("a dissolved company was dissolved again")
	}
}

func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
