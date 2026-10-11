//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
)

// The teacher's own skill in a class (ADR 0071): a craft course is taught by a player who is good at the craft, gives what he can give and no
// more than he is, and the certificate carries his name.
func TestTheTeachersSkillShapesACraftClass(t *testing.T) {
	e := newTeachEnv(t)
	ctx := testCtx(t)
	e.edu.WithTeachingRules(application.TeachingRules{RuleAt: e.clock.Now().Add(-48 * time.Hour)})
	teacher := e.pupil(1000)
	t.Cleanup(func() { // the seats of the classes he gave hold his post
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM class_seats WHERE teacher_id IN (SELECT id FROM course_teachers WHERE player_id = $1::uuid)`, teacher.ID)
	})
	certifyIn(t, e.pool, teacher.ID, "literacy_class")
	certifyIn(t, e.pool, teacher.ID, "carpentry_craft")
	if _, err := e.edu.TeacherStart(ctx, e.as(teacher, "education.teach", "teach"), handlers.TeachRequest{Course: "carpentry_craft", Mode: "home"}); err != nil {
		t.Fatal(err)
	}
	if e.scalar(`SELECT count(*) FROM course_teachers WHERE player_id = $1::uuid AND ended_at IS NULL`, teacher.ID) != 1 {
		t.Fatal("the certificate holder opened a home class")
	}
	pupil := func(xp int64) *application.Player {
		p := e.pupil(2000)
		certifyIn(t, e.pool, p.ID, "literacy_class")
		if xp > 0 {
			skillAt(t, e.pool, p.ID, "carpentry", xp)
		}
		return p
	}
	enrol := func(p *application.Player) bool {
		t.Helper()
		resp, err := e.edu.Enroll(ctx, e.as(p, "education.enroll", "enroll"), handlers.CourseRequest{Course: "carpentry_craft", Method: "cash"})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp
		return e.scalar(`SELECT count(*) FROM enrollments WHERE player_id = $1::uuid AND status = $2`, p.ID, application.EnrollmentInProgress) == 1
	}
	xp := func(p *application.Player) int64 {
		return e.scalar(`SELECT COALESCE((SELECT xp FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'carpentry'), 0)`, p.ID)
	}

	// a teacher with no carpentry in his hands teaches nobody
	first := pupil(0)
	if enrol(first) {
		t.Fatal("a teacher below the least craft level takes a student")
	}
	// at level 3 he does; a class gives the course's 200 xp, and the certificate has his name
	skillAt(t, e.pool, teacher.ID, "carpentry", 600)
	if !enrol(first) {
		t.Fatal("a level 3 teacher takes a student")
	}
	e.clock.Advance(48 * time.Hour)
	e.completeClass(first)
	if got := xp(first); got != 200 {
		t.Errorf("a class gives the course's xp: %d", got)
	}
	if e.scalar(`SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'carpentry_craft' AND route = 'class' AND issued_by = $2::uuid`, first.ID, teacher.ID) != 1 {
		t.Error("the certificate says who taught")
	}
	// a student close to his teacher gets only what is left under him: level 3 teaches up to level 2 (599 xp)
	near := pupil(500)
	if !enrol(near) {
		t.Fatal("the second student")
	}
	e.clock.Advance(48 * time.Hour)
	e.completeClass(near)
	if got := xp(near); got != 599 {
		t.Errorf("a student is not taught above his teacher: %d, want 599", got)
	}
	// the teacher's teaching skill (level 2: 300 xp) adds 2 percent
	skillAt(t, e.pool, teacher.ID, "teaching", 300)
	third := pupil(0)
	if !enrol(third) {
		t.Fatal("the third student")
	}
	e.clock.Advance(48 * time.Hour)
	e.completeClass(third)
	if got := xp(third); got != 204 {
		t.Errorf("a teacher of teaching level 2 gives 2 percent more: %d, want 204", got)
	}
	// before the rule date nothing of this holds: the old class gave any holder's full xp
	e.edu.WithTeachingRules(application.TeachingRules{RuleAt: e.clock.Now().Add(200 * time.Hour)})
	skillAt(t, e.pool, teacher.ID, "carpentry", 0)
	old := pupil(500)
	if !enrol(old) {
		t.Fatal("before the rule a certificate is enough")
	}
	e.clock.Advance(48 * time.Hour)
	e.completeClass(old)
	if got := xp(old); got != 700 {
		t.Errorf("before the rule the full xp: %d, want 700", got)
	}
}
