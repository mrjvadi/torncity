//go:build integration

// Integration test for a course in hospital (docs/adr/0024-property-and-
// politics.md, leftovers of stage E): a student hurt mid-course and admitted
// has the course stand still until the discharge, like a prisoner's, and it
// then finishes exactly as late as the stay made it.
package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

func TestHospitalPausesTheCourse(t *testing.T) {
	w := newHealthWorld(t)
	ctx := testCtx(t)
	snap := w.registry.Current()
	pick, ok := snap.CrimeDef("pickpocketing")
	if !ok || pick.Failure.Injury == nil {
		t.Skip("the active content's pickpocketing does not hurt; run `admin content load`")
	}
	if _, ok := snap.Course("first_aid"); !ok {
		t.Skip("the active content has no first_aid course; run `admin content load`")
	}
	restoreNPCProceeds(t, w.pool)
	student := w.resident(20_000)
	t.Cleanup(func() { purgeStageE(t, w.pool, student.ID) })
	t.Cleanup(func() { purgeStudiesFor(t, w.pool, student.ID) })

	cities := postgres.NewCityRepository(w.pool)
	edu := handlers.NewEducationHandler(w.uow, workIDs{t}, nil, w.registry, cities, gameScale, 5, time.Hour, w.now)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'university', place_since = now() WHERE id = $1::uuid`,
		student.ID); err != nil {
		t.Fatal(err)
	}
	resp, err := edu.Enroll(ctx, w.meta(student, "education.enroll"), handlers.CourseRequest{Course: "first_aid", Method: "cash"})
	w.ok("enrol", resp, err)
	var started, completes time.Time
	if err := w.pool.Raw().QueryRow(ctx, `SELECT started_at, completes_at FROM enrollments WHERE player_id = $1::uuid`,
		student.ID).Scan(&started, &completes); err != nil {
		t.Fatalf("no enrolment: %v (%s)", err, resp.Text)
	}

	// Thirty seconds in, a failed pickpocketing hurts them into hospital.
	w.advance(30 * time.Second)
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE player_stats SET health = 20 WHERE player_id = $1::uuid`, student.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'bazaar' WHERE id = $1::uuid`, student.ID); err != nil {
		t.Fatal(err)
	}
	dice := &crimeDice{}
	crimes := handlers.NewCrimeHandler(w.uow, hurtingIDs{t, pick.Failure.Injury.Injury()}, nil, w.registry, cities,
		postgres.NewPolicyReader(w.pool, nil), gameScale, dice, crimeRules(), time.Hour, w.now)
	dice.script(9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999)
	resp, err = crimes.Commit(ctx, w.meta(student, "crime.commit"), handlers.CrimeCommitRequest{Crime: "pickpocketing"})
	w.ok("a failed pickpocketing", resp, err)
	var (
		stayID, action string
		admitted, ends time.Time
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, admitted_at, ends_at FROM hospital_stays
	  WHERE player_id = $1::uuid AND status = 'admitted'`, student.ID).Scan(&stayID, &action, &admitted, &ends); err != nil {
		t.Fatalf("the student was not admitted: %v (%s)", err, resp.Text)
	}
	var paused *time.Time
	if err := w.pool.Raw().QueryRow(ctx, `SELECT paused_at FROM enrollments WHERE player_id = $1::uuid`, student.ID).
		Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if paused == nil || !paused.Equal(admitted) {
		t.Fatalf("the course's paused_at is %v, want the admission at %s", paused, admitted)
	}

	// The discharge resumes it, pushed back by the stay, once.
	limits, _ := bank.NewLimits(1, 1_000_000_000)
	hosp := handlers.NewHealthHandler(w.uow, workIDs{t}, nil, w.registry, cities, gameScale, limits, time.Hour, w.now)
	w.advance(ends.Sub(w.now()) + time.Second)
	for range 2 {
		if _, err := hosp.Discharge(ctx, w.scheduler("health.discharge"), handlers.HealthScheduledRequest{ActionID: action,
			ActorID: student.ID, ReferenceID: stayID}); err != nil {
			t.Fatalf("discharge: %v", err)
		}
	}
	var (
		resumedAt *time.Time
		newEnd    time.Time
		status    string
		scheduled int
	)
	if err := w.pool.Raw().QueryRow(ctx, `SELECT paused_at, completes_at, status FROM enrollments WHERE player_id = $1::uuid`,
		student.ID).Scan(&resumedAt, &newEnd, &status); err != nil {
		t.Fatal(err)
	}
	if resumedAt != nil || status != application.EnrollmentInProgress {
		t.Fatalf("after the discharge the course is %s, paused %v", status, resumedAt)
	}
	stood := ends.Sub(admitted)
	if want := completes.Add(stood); !newEnd.Equal(want) {
		t.Fatalf("the course now ends %s, want %s (pushed back by the %s in hospital)", newEnd, want, stood)
	}
	scheduled = countRows(t, w.pool, `SELECT count(*) FROM game_actions WHERE reference_type = 'enrollments'
	   AND actor_id = $1::uuid AND finish_at = $2`, student.ID, newEnd)
	if scheduled != 1 {
		t.Fatalf("%d completions scheduled at the new end, want 1", scheduled)
	}
}
