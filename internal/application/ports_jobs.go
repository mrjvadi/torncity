package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of work and study: a player's job and the shifts
// they work in it, and their place on a course and the certificates it
// issues (migrations/0011_jobs_and_education.up.sql).
//
// The rules are internal/domain/job and internal/domain/education; careers
// and courses are content (internal/content). What is here is only what a
// handler needs to store and read back between two commands.

// EducationActionType is game_actions.action_type for a course coming to its end. It
// must stay equal to the action type the scheduler routes to the command
// education.complete; tests on both sides pin the spelling.
const EducationActionType = "education"

// ShiftActionType is game_actions.action_type for a shift of work coming to
// its end (migrations/0012). It must stay equal to the action type the
// scheduler routes to the command job.finish_shift; tests on both sides pin
// the spelling.
const ShiftActionType = "work_shift"

// Shift session statuses, as shift_sessions.status stores them.
const (
	ShiftWorking   = "working"
	ShiftCompleted = "completed"
	ShiftAbandoned = "abandoned"
)

// Employment end reasons, as employments.end_reason stores them.
const (
	EndResigned = "resigned"
)

// Enrollment statuses, as enrollments.status stores them. They are the
// domain's values (education.Status), restated here so a repository needs no
// domain import to write one.
const (
	EnrollmentInProgress = "in_progress"
	EnrollmentCompleted  = "completed"
)

// Employment is a player's position: an employments row.
type Employment struct {
	ID       string
	PlayerID string
	// CareerCode names a career of the content (jobs.yml).
	CareerCode string
	// CityID is where the job is.
	CityID string
	// Tier indexes the career's tiers from 0.
	Tier int
	// Rate is pay per full-output shift, minor units.
	Rate         int64
	Performance  int
	TierSince    time.Time
	ShiftsInTier int
	TotalShifts  int
	// RecentShifts are the shift starts still inside the fatigue window.
	RecentShifts []time.Time
	// TotalEarned is gross pay earned in this stint, minor units.
	TotalEarned int64
	HiredAt     time.Time
	UpdatedAt   time.Time
}

// WorkShift is one shift worked: a work_shifts row, the base employer's
// payroll record.
type WorkShift struct {
	ID               string
	EmploymentID     string
	PlayerID         string
	Tier             int
	WorkedAt         time.Time
	Gross, Tax, Net  int64
	XP               int64
	PerformanceDelta int
	FatigueBPS       int
	// LedgerTransactionID is the wage payment; empty for a shift that paid
	// nothing.
	LedgerTransactionID string
}

// ShiftSession is one shift from its start to its end: a shift_sessions row.
type ShiftSession struct {
	ID           string
	EmploymentID string
	PlayerID     string
	// Tier is the tier the shift was started in.
	Tier         int
	GameActionID string
	Status       string
	// FatigueBPS is the output the shift runs at, fixed at its start.
	FatigueBPS int
	// EnergyCost is what it cost, charged at the start.
	EnergyCost  int
	StartedAt   time.Time
	EndsAt      time.Time
	CompletedAt *time.Time
}

// EmploymentRepository persists jobs. Reach it through Tx.Employment, so a
// shift, its wage and the energy it cost commit together.
type EmploymentRepository interface {
	// Current returns the player's current job, locked for the rest of the
	// transaction so two shifts of one player run one after the other, or
	// ErrNotEmployed.
	Current(ctx context.Context, playerID string) (*Employment, error)
	// Hire records a new current job. A player who already has one is
	// ErrAlreadyEmployed, whatever raced to create it.
	Hire(ctx context.Context, e Employment) error
	// Save writes the progress of the current job e names: tier, rate,
	// performance, the tier clock, the shift counts and earnings.
	Save(ctx context.Context, e Employment) error
	// End closes the current job e names with reason at at.
	End(ctx context.Context, employmentID, reason string, at time.Time) error
	// RecordShift appends one shift to the payroll record.
	RecordShift(ctx context.Context, s WorkShift) error
	// ActiveShift returns the player's shift in progress, or
	// ErrNoShiftInProgress. It does not lock: starting and ending a shift
	// are serialised by the job row Current locks.
	ActiveShift(ctx context.Context, playerID string) (*ShiftSession, error)
	// StartShift records a shift in progress. A player already working one
	// is ErrShiftInProgress, whatever raced to start it.
	StartShift(ctx context.Context, s ShiftSession) error
	// EndShift moves the working session id names to status (ShiftCompleted
	// or ShiftAbandoned) at at, or returns ErrNoShiftInProgress when it is
	// not working any more.
	EndShift(ctx context.Context, id, status string, at time.Time) error
	// ResidenceCityID returns the city the player lives in (ADR 0014), or ""
	// for a player with no residence yet, or ErrPlayerNotFound.
	ResidenceCityID(ctx context.Context, playerID string) (string, error)
}

// Enrollment is a player's place on a course: an enrollments row.
type Enrollment struct {
	ID           string
	PlayerID     string
	CourseCode   string
	GameActionID string
	Status       string
	// Fee is what was charged, minor units.
	Fee         int64
	StartedAt   time.Time
	CompletesAt time.Time
	CompletedAt *time.Time
}

// Certification is a qualification held: a certifications row.
type Certification struct {
	CourseCode string
	IssuedAt   time.Time
}

// EducationRepository persists study. Reach it through Tx.Education, so an
// enrolment, its fee and its scheduled completion commit together.
type EducationRepository interface {
	// Active returns the player's course in progress, or
	// ErrNoActiveEnrollment.
	Active(ctx context.Context, playerID string) (*Enrollment, error)
	// SeatsTaken locks the course's seats for the rest of the transaction,
	// so two enrolments cannot both take its last seat, and returns how many
	// are taken.
	SeatsTaken(ctx context.Context, courseCode string) (int, error)
	// Enroll records a new enrolment in progress. A player already on a
	// course is ErrAlreadyEnrolled, whatever raced to enrol them.
	Enroll(ctx context.Context, e Enrollment) error
	// Complete marks the enrolment completed at at.
	Complete(ctx context.Context, enrollmentID string, at time.Time) error
	// Certify records a certificate. fresh is false when the player already
	// holds it; nothing is written then.
	Certify(ctx context.Context, playerID, courseCode, enrollmentID string, at time.Time) (fresh bool, err error)
	// Certifications lists the player's certificates, oldest first.
	Certifications(ctx context.Context, playerID string) ([]Certification, error)
}

// Work and study sentinels. Each is a condition a player can reach by
// pressing a button.
var (
	ErrNotEmployed = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotEmployed", "no current job")

	ErrAlreadyEmployed = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyEmployed", "already has a job")

	// ErrJobNotOffered means no employer in the player's city hires into
	// that career, or the career does not exist.
	ErrJobNotOffered = errors.Sentinel(errors.CodeNotFound,
		"application.ErrJobNotOffered", "that job is not offered here")

	// ErrNotAtWorkplace means the player is not in the city their job is in.
	ErrNotAtWorkplace = errors.Sentinel(errors.CodeInvalidInput,
		"application.ErrNotAtWorkplace", "not in the city of the job")

	ErrCourseNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCourseNotFound", "no such course")

	ErrNoActiveEnrollment = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoActiveEnrollment", "no course in progress")

	ErrAlreadyEnrolled = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyEnrolled", "already on a course")

	// ErrShiftInProgress means the player is at work: a shift is running.
	ErrShiftInProgress = errors.Sentinel(errors.CodeConflict,
		"application.ErrShiftInProgress", "a shift is in progress")

	ErrNoShiftInProgress = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoShiftInProgress", "no shift in progress")
)
