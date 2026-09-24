package education

import (
	"errors"
	"testing"
	"time"
)

// A course in jail stands still: its progress and time left freeze at the
// jailing, it cannot finish, and on release it moves on by exactly the time
// it stood still.
func TestPausedCourseStandsStill(t *testing.T) {
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	e := Enrollment{CourseCode: testCourse().Code, Status: StatusInProgress, StartedAt: start, CompletesAt: start.Add(100 * time.Minute)}
	jailed := start.Add(40 * time.Minute)
	e.Paused = jailed

	later := start.Add(90 * time.Minute)
	if got := e.Remaining(later); got != 60*time.Minute {
		t.Errorf("remaining in jail = %s, want the 60m left at the jailing", got)
	}
	if got := e.Progress(later); got != 4000 {
		t.Errorf("progress in jail = %d, want 4000 frozen", got)
	}
	course := testCourse()
	if _, _, err := Complete(course, e, start.Add(200*time.Minute)); !errors.Is(err, ErrPaused) {
		t.Errorf("a paused course completed: %v", err)
	}

	released := start.Add(70 * time.Minute) // 30 minutes in jail
	r := e.Resumed(released)
	if r.IsPaused() || !r.CompletesAt.Equal(start.Add(130*time.Minute)) || !r.StartedAt.Equal(start.Add(30*time.Minute)) {
		t.Errorf("resumed = %+v", r)
	}
	if got := r.Remaining(released); got != 60*time.Minute {
		t.Errorf("remaining after release = %s, want the same 60m", got)
	}
	if got := r.Resumed(released.Add(time.Hour)); got != r {
		t.Error("resuming a running course moved it")
	}
	// A release stamped before the pause moves nothing.
	if got := e.Resumed(jailed.Add(-time.Minute)); !got.CompletesAt.Equal(e.CompletesAt) {
		t.Errorf("a release before the jailing moved the course to %s", got.CompletesAt)
	}
}
