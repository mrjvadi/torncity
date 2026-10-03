package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// Teachers (docs/research/2026-10-03-activities-audit.md section 7). A course in
// a founded settlement is taught only while somebody teaches it.

// The kinds and employers of a course teacher.
const (
	TeacherNPC    = "npc"
	TeacherPlayer = "player"
	// EmployerSettlement: hired by the school, paid from the treasury per student.
	EmployerSettlement = "settlement"
	// EmployerSelf: teaching at home, paid by the student directly.
	EmployerSelf = "self"
)

// CourseTeacher is one course_teachers row: who teaches a course in a settlement.
type CourseTeacher struct {
	ID           string
	SettlementID string
	CourseCode   string
	Kind         string
	// PlayerID is the teacher when Kind is player.
	PlayerID  string
	Employer  string
	StartedAt time.Time
}

// ClassSeat ties an enrolment to its teacher and the wage the class owes.
type ClassSeat struct {
	EnrollmentID string
	TeacherID    string
	SettlementID string
	// Wage is owed by the treasury at completion (0: paid by the student).
	Wage     int64
	WagePaid bool
}

// TeacherRepository is the teaching posts of settlements. It is part of the
// education repository (tx.Education()).
type TeacherRepository interface {
	// Teachers lists the active teachers of a course in a settlement, oldest first.
	Teachers(ctx context.Context, settlementID, courseCode string) ([]CourseTeacher, error)
	// SettlementTeachers lists every active teacher of a settlement.
	SettlementTeachers(ctx context.Context, settlementID string) ([]CourseTeacher, error)
	// TeachingPosts lists the posts a player holds, active.
	TeachingPosts(ctx context.Context, playerID string) ([]CourseTeacher, error)
	// AddTeacher records a post; a second active post for the same course (NPC)
	// or the same player is ErrAlreadyTeaching.
	AddTeacher(ctx context.Context, t CourseTeacher) error
	// EndTeacher ends a post at at; ended is false when it had ended already.
	EndTeacher(ctx context.Context, id string, at time.Time) (ended bool, err error)
	// Teacher returns an active post, or ErrNoTeacher.
	Teacher(ctx context.Context, id string) (CourseTeacher, error)
	// Students counts the enrolments in progress a teacher has.
	Students(ctx context.Context, teacherID string) (int, error)
	// Seat records the enrolment's teacher and the wage it owes.
	Seat(ctx context.Context, s ClassSeat) error
	// SeatOf returns an enrolment's seat; nil when it has none (a content city).
	SeatOf(ctx context.Context, enrollmentID string) (*ClassSeat, error)
	// PaySeat marks the wage paid; fresh is false when it already was, so a
	// redelivered completion pays once.
	PaySeat(ctx context.Context, enrollmentID string, at time.Time, paid int64) (fresh bool, err error)
	// TeacherAny returns a post active or ended (a class finishing after its
	// teacher left still pays them).
	TeacherAny(ctx context.Context, id string) (CourseTeacher, error)
}

var (
	ErrAlreadyTeaching = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyTeaching", "already teaching that course here")
	ErrNoTeacher = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoTeacher", "no such teaching post")
)
