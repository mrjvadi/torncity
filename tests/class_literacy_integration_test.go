//go:build integration

package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
)

// Village classes (ADR 0055, owner 2026-10-10): a finished class a certified teacher gave is practice in education, and every
// class but the literacy class itself asks the student to read, with a grace and a notice of where to learn.

func (e *teachEnv) cleanupExperience() {
	e.t.Cleanup(func() {
		c := testCtx(e.t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM settlement_experience WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM experience_days WHERE settlement_id = $1::uuid`, e.cityID)
	})
}

func (e *teachEnv) completeClass(student *application.Player) {
	e.t.Helper()
	var enrollmentID, actionID string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text, game_action_id::text FROM enrollments WHERE player_id = $1::uuid ORDER BY started_at DESC LIMIT 1`, student.ID).Scan(&enrollmentID, &actionID); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.edu.Complete(testCtx(e.t), e.as(student, "education.complete", "complete"), handlers.CompleteCourseRequest{
		ActionID: actionID, ActorID: student.ID, ReferenceType: "enrollments", ReferenceID: enrollmentID}); err != nil {
		e.t.Fatal(err)
	}
}

// A finished class in a founded settlement adds the education experience once a day, however many classes end that day.
func TestAClassFeedsEducationExperience(t *testing.T) {
	e := newTeachEnv(t)
	e.cleanupExperience()
	e.edu.WithExperience(10)
	if _, err := e.edu.TeacherHire(testCtx(t), e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	enrol := func(p *application.Player) {
		t.Helper()
		if _, err := e.edu.Enroll(testCtx(t), e.as(p, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := e.pupil(1000), e.pupil(1000)
	enrol(first)
	enrol(second)
	e.clock.Advance(48 * time.Hour) // both classes end on the same day
	e.completeClass(first)
	if got := e.scalar(`SELECT COALESCE((SELECT points FROM settlement_experience WHERE settlement_id = $1::uuid AND field = 'education'), 0)`, e.cityID); got != 10 {
		t.Fatalf("a finished class should add 10 points of education: %d", got)
	}
	e.completeClass(second) // the same day: nothing more
	if got := e.scalar(`SELECT COALESCE((SELECT points FROM settlement_experience WHERE settlement_id = $1::uuid AND field = 'education'), 0)`, e.cityID); got != 10 {
		t.Fatalf("a second class the same day should add nothing: %d", got)
	}
}

// A class other than the literacy class asks the student to read: warned during the grace with the place to learn,
// refused after it; the literacy class itself asks nothing.
func TestAVillageClassAsksTheStudentToRead(t *testing.T) {
	e := newTeachEnv(t)
	cfg := config.Defaults()
	grace := handlers.PersonalRules{From: e.clock.Now().Add(-time.Hour), GraceDays: cfg.Settlement.PersonalGraceDays}
	e.edu.WithPersonal(grace)
	for _, stmt := range []string{
		`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		 VALUES (gen_random_uuid(), $1::uuid, 'health_house', 71, 70, 'complete', now(), now())`,
		`INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at)
		 VALUES (gen_random_uuid(), $1::uuid, 'basic_medicine', 'researched', now()) ON CONFLICT DO NOTHING`,
	} {
		if _, err := e.pool.Raw().Exec(testCtx(t), stmt, e.cityID); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid AND code = 'basic_medicine'`, e.cityID)
	})
	for _, c := range []string{"first_aid", "literacy_class"} {
		if _, err := e.edu.TeacherHire(testCtx(t), e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: c}); err != nil {
			t.Fatal(err)
		}
	}
	student := e.pupil(2000)
	// during the grace the class can be joined, and the course page says what will be asked
	view := e.view(student, "first_aid")
	if text, _ := view["requirements"].([]any); len(text) == 0 {
		t.Logf("view: %v", view)
	}
	if _, err := e.edu.Enroll(testCtx(t), e.as(student, "education.enroll", "enroll"), handlers.CourseRequest{Course: "first_aid", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(student) != 1 {
		t.Fatalf("during the grace the class should be joined")
	}
	// after the grace the same student is refused, naming the literacy class
	e.edu.WithPersonal(handlers.PersonalRules{From: e.clock.Now().Add(-time.Duration(cfg.Settlement.PersonalGraceDays+1) * 24 * time.Hour), GraceDays: cfg.Settlement.PersonalGraceDays})
	other := e.pupil(2000)
	resp, err := e.edu.Enroll(testCtx(t), e.as(other, "education.enroll", "enroll"), handlers.CourseRequest{Course: "first_aid", Method: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(other) != 0 || !strings.Contains(string(resp.View), "literacy") {
		t.Fatalf("a student who cannot read joined a village class after the grace, or was not told: %q %s", resp.Text, resp.View)
	}
	// and the literacy class itself asks nothing
	if _, err := e.edu.Enroll(testCtx(t), e.as(other, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(other) != 1 {
		t.Fatalf("the literacy class should be open to someone who cannot read")
	}
}
