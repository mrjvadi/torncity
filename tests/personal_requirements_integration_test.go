//go:build integration

package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The personal prerequisites of a post (ADR 0055, plan A7): the smith of a bloomery asks level 3, a scholar's post literacy; the
// carpenter and the mason ask no level (owner, 2026-10-10). The
// rules are built the way the service builds them (cmd/game personalRules): refused after the grace with what is missing,
// allowed during it with a warning, never silent.

type personalEnv struct {
	*workplaceEnv
}

func newPersonalEnv(t *testing.T, inGrace bool) *personalEnv {
	t.Helper()
	w := newWorkplaceEnv(t, "bloomery")
	cfg := config.Defaults()
	from := w.clock.Now().Add(-time.Hour) // the rule began an hour ago: the grace is running
	if !inGrace {
		from = w.clock.Now().Add(-time.Duration(cfg.Settlement.PersonalGraceDays+1) * 24 * time.Hour)
	}
	w.village.WithPersonal(handlers.PersonalRules{From: from, GraceDays: cfg.Settlement.PersonalGraceDays})
	w.stock("iron_ore", 6)
	w.stock("charcoal", 8)
	return &personalEnv{w}
}

func (e *personalEnv) work() *presentation.Response {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(e.worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: e.camp}))
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *personalEnv) setLevel(n int) {
	e.t.Helper()
	setLevelOf(e.t, e.researchEnv, e.worker.ID, n)
}

// During the grace a player below level 3 may still work at the bloomery, and the work screen warns him what will be
// needed and from when.
func TestPersonalPrerequisitesWarnDuringTheGrace(t *testing.T) {
	e := newPersonalEnv(t, true)
	e.setLevel(1)
	list := e.work()
	_ = list
	if n := len(e.workingShifts(e.camp)); n != 1 {
		t.Fatalf("during the grace the shift should start: %d running", n)
	}
	resp, err := rrc(e.village.Work(testCtx(t), e.as(e.head, "settlement.work", "work"), handlers.VillageWorkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var v village.WorkView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, p := range v.Places {
		for _, n := range p.Personal {
			warned = warned || (n.Kind == village.PersonalLevel && n.Need == 3)
		}
	}
	if !warned || v.PersonalUntil.IsZero() {
		t.Errorf("the work screen should warn of level 3 and say until when: %+v until %v", v.Places, v.PersonalUntil)
	}
}

// After the grace the same shift is refused, naming what is missing; at the level it works; a post that asks nothing of
// anyone (a labourer's at the smithy) is open to everyone.
func TestPersonalPrerequisitesRefuseAfterTheGrace(t *testing.T) {
	e := newPersonalEnv(t, false)
	e.setLevel(1)
	resp := e.work()
	if !strings.Contains(resp.Text, "شرط شخصی") || !strings.Contains(resp.Text, "سطح") {
		t.Fatalf("a level 1 player at the bloomery after the grace: %q", resp.Text)
	}
	if n := len(e.workingShifts(e.camp)); n != 0 {
		t.Fatalf("a refused shift started: %d running", n)
	}
	e.setLevel(3)
	if resp := e.work(); strings.Contains(resp.Text, "شرط شخصی") {
		t.Fatalf("a level 3 player was refused: %q", resp.Text)
	}
	if n := len(e.workingShifts(e.camp)); n != 1 {
		t.Fatalf("a level 3 player's shift should run: %d", n)
	}
	// the smithy has a smith (level 3) and a labourer who needs nothing: anyone may work there
	e.stock("bloom", 2)
	e.stock("charcoal", 2)
	smithy := e.building("smithy")
	other := e.scholar()
	setLevelOf(t, e.researchEnv, other.ID, 1)
	resp2, err := rrc(e.village.Work(testCtx(t), e.as(other, "settlement.work", "work"), handlers.VillageWorkRequest{ID: smithy}))
	if err != nil || strings.Contains(resp2.Text, "شرط شخصی") {
		t.Fatalf("the labourer's post at the smithy asks nothing: %v %q", err, resp2.Text)
	}
}

// A scholar's post asks literacy: refused after the grace until the player holds the literacy certificate.
func TestPersonalLiteracyForAScholarsPost(t *testing.T) {
	e := newPersonalEnv(t, false)
	lib := e.building("library")
	reader := e.scholar()
	take := func() *presentation.Response {
		t.Helper()
		resp, err := rrc(e.village.ResearchDesk(testCtx(t), e.as(reader, "settlement.research", "research"), handlers.VillageResearchRequest{Action: "post", Code: lib}))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := take(); !strings.Contains(r.Text, "شرط شخصی") {
		t.Fatalf("an illiterate player took a scholar's post: %q", r.Text)
	}
	certify(t, e.researchEnv, reader.ID, "literacy_class")
	if r := take(); strings.Contains(r.Text, "شرط شخصی") {
		t.Fatalf("a literate player was refused: %q", r.Text)
	}
}

// setLevelOf gives a player the stats row at a level (the row is made on first contact in the game).
func setLevelOf(t *testing.T, e *researchEnv, playerID string, level int) {
	t.Helper()
	ctx := testCtx(t)
	if _, err := postgres.NewStatsRepository(e.pool).EnsureDefaults(ctx, playerID, application.Stats{
		PlayerID: playerID, Level: level, Health: 100, MaxHealth: 100, Energy: 100, MaxEnergy: 100, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE player_stats SET level = $2 WHERE player_id = $1::uuid`, playerID, level); err != nil {
		t.Fatal(err)
	}
}

// certify gives a player a course certificate without the course (the enrolment it names is a bare row; the foreign keys are
// switched off for this one transaction).
func certify(t *testing.T, e *researchEnv, playerID, course string) {
	t.Helper()
	certifyIn(t, e.pool, playerID, course)
}

// certifyIn is certify against a pool, for the tests that have no research env.
func certifyIn(t *testing.T, pool *postgres.Pool, playerID, course string) {
	t.Helper()
	e := struct{ pool *postgres.Pool }{pool}
	ctx := testCtx(t)
	tx, err := e.pool.Raw().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	enrol := newUUID(t)
	if _, err := tx.Exec(ctx, `INSERT INTO enrollments (id, player_id, course_code, game_action_id, status, fee, started_at, completes_at, completed_at)
		VALUES ($1::uuid, $2::uuid, $3, gen_random_uuid(), 'completed', 0, now() - interval '2 hours', now() - interval '1 hour', now() - interval '1 hour')`, enrol, playerID, course); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO certifications (id, player_id, course_code, enrollment_id, issued_at) VALUES (gen_random_uuid(), $1::uuid, $2, $3::uuid, now())`, playerID, course, enrol); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := testCtx(t)
		tx, err := e.pool.Raw().Begin(c)
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(c) }()
		for _, stmt := range []string{`SET LOCAL session_replication_role = replica`,
			`DELETE FROM certifications WHERE player_id = $1::uuid`, `DELETE FROM enrollments WHERE player_id = $1::uuid`} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, playerID)
			}
			if _, err := tx.Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		_ = tx.Commit(c)
	})
}
