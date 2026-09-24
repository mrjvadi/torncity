//go:build integration

// Integration test for a course in jail (migration 0016,
// docs/adr/0019-crime-engine.md "Time in jail"): a student jailed mid-course
// has the course stand still until release, and it then finishes exactly as
// late as the jail made it — through the real handlers, the real schedule
// rows and the real database, with the completion delivered twice and the
// release delivered twice.
package tests

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func TestJailPausesTheCourse(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	registry := crimeRegistry(t, pool)
	ctx := testCtx(t)
	var hasPause bool
	if err := pool.Raw().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'enrollments' AND column_name = 'paused_at')`).
		Scan(&hasPause); err != nil || !hasPause {
		t.Skip("enrollments.paused_at does not exist; apply migration 0016 first")
	}
	if _, ok := registry.Current().Course("first_aid"); !ok {
		t.Skip("the active content has no first_aid course; run `admin content load`")
	}

	cities := postgres.NewCityRepository(pool)
	city, err := cities.ByCode(ctx, "ostmarch")
	if err != nil {
		t.Skipf("the shipped city ostmarch is not loaded: %v", err)
	}
	policy := postgres.NewPolicyReader(pool, nil)
	if _, err := policy.Get(ctx, city.JurisdictionID, application.LeverBailPerHour); err != nil {
		t.Skipf("the police chief's levers are not loaded: %v", err)
	}

	clock := time.Now().UTC().Truncate(time.Second)
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	set := func(at time.Time) { clockMu.Lock(); clock = at; clockMu.Unlock() }

	restoreNPCProceeds(t, pool)
	// A bot for the profile to record its link to; registered before the
	// player, so it is removed after the link.
	botID := insertBot(t, pool)
	student := crimePlayer(t, pool, city.ID, now())
	// Registered after crimePlayer's, so it runs first: the course rows go
	// before the crime cleanup deletes the scheduled actions they point at.
	t.Cleanup(func() { purgeStudiesFor(t, pool, student.ID) })
	grant(t, pool, application.AccountPlayerCash, student.ID, 5_000)
	// Courses are taught at the university (places.yml); a crime is done
	// wherever the player stands.
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'university', place_since = $2 WHERE id = $1::uuid`,
		student.ID, now()); err != nil {
		t.Skipf("players.place_code is not there (migration 0015): %v", err)
	}

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	dice := &crimeDice{}
	crimes := handlers.NewCrimeHandler(uow, workIDs{t}, nil, registry, cities, policy, gameScale, dice, crimeRules(), time.Hour, now)
	edu := handlers.NewEducationHandler(uow, workIDs{t}, nil, registry, cities, gameScale, 5, time.Hour, now)
	profile := handlers.NewProfileHandler(uow, workIDs{t}, nil, cities, "fa", time.Hour, now).WithWork(registry, policy)
	metaFor := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = student.TelegramUserID
		m.PlayerID = student.ID
		m.Command = command
		m.Language = "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	scheduled := func(command string) envelope.Metadata {
		m := metaFor(command)
		m.TelegramUserID = 0
		return m
	}
	type enrolment struct {
		id, actionID         string
		startedAt, completes time.Time
		pausedAt             *time.Time
		status               string
		actionFinish         time.Time
	}
	read := func() enrolment {
		t.Helper()
		var e enrolment
		if err := pool.Raw().QueryRow(testCtx(t),
			`SELECT e.id::text, e.game_action_id::text, e.started_at, e.completes_at, e.paused_at, e.status, a.finish_at
			   FROM enrollments e JOIN game_actions a ON a.id = e.game_action_id
			  WHERE e.player_id = $1::uuid ORDER BY e.started_at DESC LIMIT 1`, student.ID).
			Scan(&e.id, &e.actionID, &e.startedAt, &e.completes, &e.pausedAt, &e.status, &e.actionFinish); err != nil {
			t.Fatalf("reading the enrolment: %v", err)
		}
		return e
	}

	// 1. Enrol: first_aid is two real minutes on the game clock.
	enrolled, err := edu.Enroll(ctx, metaFor("education.enroll"), handlers.CourseRequest{Course: "first_aid", Method: "cash"})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM enrollments WHERE player_id = $1::uuid`, student.ID); n != 1 {
		t.Fatalf("no enrolment; the course answered:\n%s", transcriptText(enrolled))
	}
	before := read()
	if before.pausedAt != nil || before.status != application.EnrollmentInProgress {
		t.Fatalf("a fresh enrolment is %+v", before)
	}

	// 2. Thirty seconds in, a timed scam; it ends in an arrest and a jail
	//    sentence (the dice: a failure, an arrest, the shortest sentence).
	set(before.startedAt.Add(30 * time.Second))
	// The scam is worked in the city centre, not at the university.
	if _, err := pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'city_centre', place_since = $2 WHERE id = $1::uuid`,
		student.ID, now()); err != nil {
		t.Fatal(err)
	}
	committed, err := crimes.Commit(ctx, metaFor("crime.commit"), handlers.CrimeCommitRequest{Crime: "street_scam"})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM crimes WHERE player_id = $1::uuid`, student.ID); n != 1 {
		t.Fatalf("no scam started; the crime answered:\n%s", transcriptText(committed))
	}
	var attemptID string
	var resolveAt time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT c.id::text, a.finish_at FROM crimes c JOIN game_actions a ON a.id = c.game_action_id
		  WHERE c.player_id = $1::uuid AND c.status = 'in_progress'`, student.ID).Scan(&attemptID, &resolveAt); err != nil {
		t.Fatalf("the scam is not under way: %v", err)
	}
	jailedAt := resolveAt.Add(time.Second)
	set(jailedAt)
	dice.script(9999, 0, 0, 0)
	if _, err := crimes.Resolve(ctx, scheduled("crime.resolve"),
		handlers.CrimeScheduledRequest{ActorID: student.ID, ReferenceType: "crimes", ReferenceID: attemptID}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var sentenceID string
	var releaseAt time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT id::text, ends_at FROM jail_sentences WHERE player_id = $1::uuid AND status = 'serving'`, student.ID).
		Scan(&sentenceID, &releaseAt); err != nil {
		t.Fatalf("the student is not in jail: %v", err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'player_id' = $2`,
		subjects.Event("crime", "jailed"), student.ID); n != 1 {
		t.Errorf("crime.jailed events = %d, want 1 (the city's group reads it)", n)
	}

	// The course stands still from the jailing.
	paused := read()
	if paused.pausedAt == nil || !paused.pausedAt.Equal(jailedAt) {
		t.Fatalf("paused_at = %v, want the jailing at %s", paused.pausedAt, jailedAt)
	}
	// The home screen says jail, and the course is paused.
	pm := metaFor("player.profile.get")
	pm.BotID = botID
	resp, err := profile.Handle(ctx, pm)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"profile.jail_in", "profile.jail_blocks", "profile.course_paused", "crime.button.jail"} {
		if !strings.Contains(transcriptText(resp), key) {
			t.Errorf("the profile in jail lacks %s:\n%s", key, transcriptText(resp))
		}
	}
	// No new course from a cell.
	if _, err := edu.Enroll(ctx, metaFor("education.enroll"), handlers.CourseRequest{Course: "evening_accounting", Method: "cash"}); err == nil {
		// Refused either way — one course at a time, and jail — but it must
		// not start one.
		if n := countRows(t, pool, `SELECT count(*) FROM enrollments WHERE player_id = $1::uuid`, student.ID); n != 1 {
			t.Errorf("a course started from jail")
		}
	}

	// 3. The course's original completion falls due while in jail: twice
	//    delivered, it completes nothing.
	set(before.completes.Add(time.Second))
	orig := handlers.CompleteCourseRequest{ActionID: before.actionID, ActorID: student.ID, ReferenceType: "enrollments", ReferenceID: before.id}
	for i := 0; i < 2; i++ {
		if _, err := edu.Complete(ctx, scheduled("education.complete"), orig); err != nil {
			t.Fatalf("Complete (in jail) #%d: %v", i+1, err)
		}
	}
	if got := read(); got.status != application.EnrollmentInProgress {
		t.Fatalf("the course finished in jail: %+v", got)
	}

	// 4. The sentence is served; the release, delivered twice, resumes the
	//    course once, moved on by exactly the time in jail.
	set(releaseAt.Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		if _, err := crimes.Release(ctx, scheduled("crime.release"),
			handlers.CrimeScheduledRequest{ActorID: student.ID, ReferenceID: sentenceID}); err != nil {
			t.Fatalf("Release #%d: %v", i+1, err)
		}
	}
	resumed := read()
	stood := releaseAt.Sub(jailedAt)
	if resumed.pausedAt != nil {
		t.Errorf("the course is still paused after release")
	}
	if !resumed.completes.Equal(before.completes.Add(stood)) || !resumed.startedAt.Equal(before.startedAt.Add(stood)) {
		t.Errorf("course moved to %s-%s, want %s-%s (jail %s)", resumed.startedAt, resumed.completes,
			before.startedAt.Add(stood), before.completes.Add(stood), stood)
	}
	if resumed.actionID == before.actionID || !resumed.actionFinish.Equal(resumed.completes) {
		t.Errorf("no new completion at the new end: action %s at %s", resumed.actionID, resumed.actionFinish)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM game_actions WHERE actor_id = $1::uuid AND action_type = $2`,
		student.ID, application.EducationActionType); n != 2 {
		t.Errorf("education actions = %d, want the original and one new (the release resumed once)", n)
	}

	// 5. The old completion, delivered late, still does nothing; the new one
	//    finishes the course, once.
	set(resumed.completes.Add(time.Second))
	if _, err := edu.Complete(ctx, scheduled("education.complete"), orig); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.status != application.EnrollmentInProgress {
		t.Fatalf("the stale completion finished the course")
	}
	fresh := handlers.CompleteCourseRequest{ActionID: resumed.actionID, ActorID: student.ID, ReferenceType: "enrollments", ReferenceID: resumed.id}
	for i := 0; i < 2; i++ {
		if _, err := edu.Complete(ctx, scheduled("education.complete"), fresh); err != nil {
			t.Fatalf("Complete #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, pool, `SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'first_aid'`, student.ID); n != 1 {
		t.Errorf("certificates = %d, want exactly 1", n)
	}
	verifyLedger(t, pool)
}

// purgeStudiesFor removes a player's certificates, enrolments and study
// events, before anything else deletes the scheduled actions they point at.
func purgeStudiesFor(t *testing.T, pool *postgres.Pool, playerID string) {
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
		`ALTER TABLE certifications DISABLE TRIGGER certifications_append_only`,
		`DELETE FROM certifications WHERE player_id = $1::uuid`,
		`DELETE FROM enrollments WHERE player_id = $1::uuid`,
		`DELETE FROM player_bot_links WHERE player_id = $1::uuid`,
		`DELETE FROM outbox WHERE payload->>'player_id' = $1`,
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

// transcriptText is a response's text and button labels, for a key search.
func transcriptText(resp *presenter.Response) string {
	if resp == nil {
		return ""
	}
	out := resp.Text
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, b := range row {
				out += "\n" + b.Text
			}
		}
	}
	return out
}
