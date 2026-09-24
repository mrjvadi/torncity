package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Index and constraint names from migrations/0011_jobs_and_education.up.sql
// that map to sentinels.
const (
	employmentsOneCurrentIdx      = "employments_one_current_idx"
	enrollmentsOneActiveIdx       = "enrollments_one_active_idx"
	certificationsPlayerCourseKey = "certifications_player_course_key"

	// From migrations/0012_timed_shifts.up.sql.
	shiftSessionsOneWorkingIdx = "shift_sessions_one_working_idx"
)

// courseSeatsLockNamespace prefixes a course code in the advisory lock key
// that serialises enrolments on one course; see SeatsTaken.
const courseSeatsLockNamespace = "course_seats:"

// The columns every read of a job or an enrolment scans, in scan order.
const (
	employmentColumns = `id::text, player_id::text, career_code, city_id::text, tier, rate, performance,
	       tier_since, shifts_in_tier, total_shifts, recent_shifts, total_earned, hired_at, updated_at,
	       COALESCE(company_id::text, ''), COALESCE(opening_id::text, '')`
	enrollmentColumns = `id::text, player_id::text, course_code, game_action_id::text, status, fee,
	       started_at, completes_at, completed_at, paused_at`
)

// EmploymentRepository implements application.EmploymentRepository.
type EmploymentRepository struct {
	q querier
}

var _ application.EmploymentRepository = (*EmploymentRepository)(nil)

func scanEmployment(row pgx.Row) (*application.Employment, error) {
	var e application.Employment
	if err := row.Scan(&e.ID, &e.PlayerID, &e.CareerCode, &e.CityID, &e.Tier, &e.Rate, &e.Performance,
		&e.TierSince, &e.ShiftsInTier, &e.TotalShifts, &e.RecentShifts, &e.TotalEarned,
		&e.HiredAt, &e.UpdatedAt, &e.CompanyID, &e.OpeningID); err != nil {
		return nil, err
	}
	e.TierSince = e.TierSince.UTC()
	e.HiredAt = e.HiredAt.UTC()
	e.UpdatedAt = e.UpdatedAt.UTC()
	for i := range e.RecentShifts {
		e.RecentShifts[i] = e.RecentShifts[i].UTC()
	}
	return &e, nil
}

// Current returns the player's current job, locked FOR UPDATE: two shifts of
// one player then run one after the other, and the second sees the energy,
// performance and fatigue history the first one left.
func (r *EmploymentRepository) Current(ctx context.Context, playerID string) (*application.Employment, error) {
	e, err := scanEmployment(r.q.QueryRow(ctx,
		`SELECT `+employmentColumns+`
		   FROM employments
		  WHERE player_id = $1::uuid AND ended_at IS NULL
		    FOR UPDATE`, playerID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrNotEmployed
	case isInvalidUUIDText(err):
		return nil, application.ErrNotEmployed
	case err != nil:
		return nil, fmt.Errorf("postgres: current job: %w", err)
	}
	return e, nil
}

// Hire inserts a current job. The partial unique index is what refuses a
// second one, whatever raced to create it.
func (r *EmploymentRepository) Hire(ctx context.Context, e application.Employment) error {
	id, err := ensureID(e.ID)
	if err != nil {
		return err
	}
	recent := e.RecentShifts
	if recent == nil {
		recent = []time.Time{}
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO employments (id, player_id, career_code, city_id, tier, rate, performance, tier_since,
		                          shifts_in_tier, total_shifts, recent_shifts, total_earned, hired_at, updated_at,
		                          company_id, opening_id)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::uuid, $16::uuid)`,
		id, e.PlayerID, e.CareerCode, e.CityID, e.Tier, e.Rate, e.Performance, e.TierSince.UTC(),
		e.ShiftsInTier, e.TotalShifts, recent, e.TotalEarned, e.HiredAt.UTC(), e.UpdatedAt.UTC(),
		nullableUUID(e.CompanyID), nullableUUID(e.OpeningID))
	if violates(err, sqlstateUniqueViolation, employmentsOneCurrentIdx) {
		return application.ErrAlreadyEmployed
	}
	if err != nil {
		return fmt.Errorf("postgres: hiring: %w", err)
	}
	return nil
}

// Save writes the progress of the current job. A job that has ended is not
// changed, and saying so is ErrNotEmployed.
func (r *EmploymentRepository) Save(ctx context.Context, e application.Employment) error {
	recent := e.RecentShifts
	if recent == nil {
		recent = []time.Time{}
	}
	tag, err := r.q.Exec(ctx,
		`UPDATE employments
		    SET tier = $2, rate = $3, performance = $4, tier_since = $5, shifts_in_tier = $6,
		        total_shifts = $7, recent_shifts = $8, total_earned = $9, updated_at = $10
		  WHERE id = $1::uuid AND ended_at IS NULL`,
		e.ID, e.Tier, e.Rate, e.Performance, e.TierSince.UTC(), e.ShiftsInTier,
		e.TotalShifts, recent, e.TotalEarned, e.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: saving job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotEmployed
	}
	return nil
}

// End closes the current job.
func (r *EmploymentRepository) End(ctx context.Context, employmentID, reason string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE employments SET ended_at = $2, end_reason = $3, updated_at = $2
		  WHERE id = $1::uuid AND ended_at IS NULL`,
		employmentID, at.UTC(), reason)
	if err != nil {
		return fmt.Errorf("postgres: ending job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNotEmployed
	}
	return nil
}

// RecordShift appends one shift to the payroll record.
func (r *EmploymentRepository) RecordShift(ctx context.Context, s application.WorkShift) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO work_shifts (id, employment_id, player_id, tier, worked_at, gross, tax, net, xp,
		                          performance_delta, fatigue_bps, ledger_transaction_id, company_id)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12::uuid, $13::uuid)`,
		id, s.EmploymentID, s.PlayerID, s.Tier, s.WorkedAt.UTC(), s.Gross, s.Tax, s.Net, s.XP,
		s.PerformanceDelta, s.FatigueBPS, nullableUUID(s.LedgerTransactionID), nullableUUID(s.CompanyID)); err != nil {
		return fmt.Errorf("postgres: recording shift: %w", err)
	}
	return nil
}

// shiftSessionColumns are the columns every read of a shift scans, in scan
// order.
const shiftSessionColumns = `id::text, employment_id::text, player_id::text, tier, game_action_id::text, status,
	       fatigue_bps, energy_cost, started_at, ends_at, completed_at,
	       COALESCE(company_id::text, ''), wage_reserved`

// ActiveShift returns the player's shift in progress. It takes no lock of
// its own, on purpose: every command that starts or ends a shift locks the
// player's job row first (Current), which already runs them in turn, and a
// departure that only asks "is this player at work?" must not queue behind a
// shift's settlement while holding the stats row that settlement needs.
// EndShift moves only a working row, so even an unserialised second ending
// changes nothing.
func (r *EmploymentRepository) ActiveShift(ctx context.Context, playerID string) (*application.ShiftSession, error) {
	var s application.ShiftSession
	err := r.q.QueryRow(ctx,
		`SELECT `+shiftSessionColumns+`
		   FROM shift_sessions
		  WHERE player_id = $1::uuid AND status = $2`, playerID, application.ShiftWorking).Scan(
		&s.ID, &s.EmploymentID, &s.PlayerID, &s.Tier, &s.GameActionID, &s.Status,
		&s.FatigueBPS, &s.EnergyCost, &s.StartedAt, &s.EndsAt, &s.CompletedAt, &s.CompanyID, &s.WageReserved)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoShiftInProgress
	case err != nil:
		return nil, fmt.Errorf("postgres: active shift: %w", err)
	}
	s.StartedAt = s.StartedAt.UTC()
	s.EndsAt = s.EndsAt.UTC()
	if s.CompletedAt != nil {
		t := s.CompletedAt.UTC()
		s.CompletedAt = &t
	}
	return &s, nil
}

// StartShift inserts a shift in progress. The partial unique index is what
// refuses a second one, whatever raced to start it.
func (r *EmploymentRepository) StartShift(ctx context.Context, s application.ShiftSession) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO shift_sessions (id, employment_id, player_id, tier, game_action_id, status,
		                             fatigue_bps, energy_cost, started_at, ends_at, company_id, wage_reserved)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, $11::uuid, $12)`,
		id, s.EmploymentID, s.PlayerID, s.Tier, s.GameActionID, application.ShiftWorking,
		s.FatigueBPS, s.EnergyCost, s.StartedAt.UTC(), s.EndsAt.UTC(), nullableUUID(s.CompanyID), s.WageReserved)
	if violates(err, sqlstateUniqueViolation, shiftSessionsOneWorkingIdx) {
		return application.ErrShiftInProgress
	}
	if err != nil {
		return fmt.Errorf("postgres: starting shift: %w", err)
	}
	return nil
}

// EndShift closes a working session. Only a working one moves, so a second
// completion of the same shift changes nothing and says so.
func (r *EmploymentRepository) EndShift(ctx context.Context, id, status string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE shift_sessions SET status = $2, completed_at = $3
		  WHERE id = $1::uuid AND status = $4`,
		id, status, at.UTC(), application.ShiftWorking)
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrNoShiftInProgress
		}
		return fmt.Errorf("postgres: ending shift: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNoShiftInProgress
	}
	return nil
}

// ResidenceCityID returns where the player lives, "" for nowhere yet.
func (r *EmploymentRepository) ResidenceCityID(ctx context.Context, playerID string) (string, error) {
	var city *string
	err := r.q.QueryRow(ctx,
		`SELECT residence_city_id::text FROM players WHERE id = $1::uuid`, playerID).Scan(&city)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return "", application.ErrPlayerNotFound
	case err != nil:
		return "", fmt.Errorf("postgres: reading residence: %w", err)
	}
	if city == nil {
		return "", nil
	}
	return *city, nil
}

// EducationRepository implements application.EducationRepository.
type EducationRepository struct {
	q querier
}

var _ application.EducationRepository = (*EducationRepository)(nil)

func scanEnrollment(row pgx.Row) (*application.Enrollment, error) {
	var e application.Enrollment
	if err := row.Scan(&e.ID, &e.PlayerID, &e.CourseCode, &e.GameActionID, &e.Status, &e.Fee,
		&e.StartedAt, &e.CompletesAt, &e.CompletedAt, &e.PausedAt); err != nil {
		return nil, err
	}
	if e.PausedAt != nil {
		t := e.PausedAt.UTC()
		e.PausedAt = &t
	}
	e.StartedAt = e.StartedAt.UTC()
	e.CompletesAt = e.CompletesAt.UTC()
	if e.CompletedAt != nil {
		t := e.CompletedAt.UTC()
		e.CompletedAt = &t
	}
	return &e, nil
}

// Active returns the course in progress, locked for the rest of the
// transaction so a completion and anything else touching it run in turn.
func (r *EducationRepository) Active(ctx context.Context, playerID string) (*application.Enrollment, error) {
	e, err := scanEnrollment(r.q.QueryRow(ctx,
		`SELECT `+enrollmentColumns+`
		   FROM enrollments
		  WHERE player_id = $1::uuid AND status = $2
		    FOR UPDATE`, playerID, application.EnrollmentInProgress))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoActiveEnrollment
	case err != nil:
		return nil, fmt.Errorf("postgres: active course: %w", err)
	}
	return e, nil
}

// SeatsTaken takes a transaction-scoped advisory lock on the course, then
// counts its students. The lock is what makes "count, then enrol" safe: a
// second enrolment waits here and then counts the first one.
func (r *EducationRepository) SeatsTaken(ctx context.Context, courseCode string) (int, error) {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		courseSeatsLockNamespace+courseCode); err != nil {
		return 0, fmt.Errorf("postgres: locking course seats: %w", err)
	}
	var n int
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM enrollments WHERE course_code = $1 AND status = $2`,
		courseCode, application.EnrollmentInProgress).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting course seats: %w", err)
	}
	return n, nil
}

// Enroll inserts an enrolment in progress.
func (r *EducationRepository) Enroll(ctx context.Context, e application.Enrollment) error {
	id, err := ensureID(e.ID)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO enrollments (id, player_id, course_code, game_action_id, status, fee, started_at, completes_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8)`,
		id, e.PlayerID, e.CourseCode, e.GameActionID, application.EnrollmentInProgress, e.Fee,
		e.StartedAt.UTC(), e.CompletesAt.UTC())
	if violates(err, sqlstateUniqueViolation, enrollmentsOneActiveIdx) {
		return application.ErrAlreadyEnrolled
	}
	if err != nil {
		return fmt.Errorf("postgres: enrolling: %w", err)
	}
	return nil
}

// Complete marks an enrolment in progress completed.
func (r *EducationRepository) Complete(ctx context.Context, enrollmentID string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE enrollments SET status = $2, completed_at = $3
		  WHERE id = $1::uuid AND status = $4`,
		enrollmentID, application.EnrollmentCompleted, at.UTC(), application.EnrollmentInProgress)
	if err != nil {
		return fmt.Errorf("postgres: completing course: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrNoActiveEnrollment
	}
	return nil
}

// Pause stops the player's course in progress at at, unless it is stopped
// already: a second jailing while one sentence runs keeps the first instant.
func (r *EducationRepository) Pause(ctx context.Context, playerID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE enrollments SET paused_at = $2
		  WHERE player_id = $1::uuid AND status = $3 AND paused_at IS NULL`,
		playerID, at.UTC(), application.EnrollmentInProgress)
	if err != nil {
		return false, fmt.Errorf("postgres: pausing course: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Resume restarts a paused course with its period moved on and its new
// completion. The paused_at predicate makes it happen once: a second release
// racing the first finds nothing paused and changes nothing.
func (r *EducationRepository) Resume(ctx context.Context, enrollmentID string, startedAt, completesAt time.Time, actionID string) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE enrollments
		    SET started_at = $2, completes_at = $3, game_action_id = $4::uuid, paused_at = NULL
		  WHERE id = $1::uuid AND status = $5 AND paused_at IS NOT NULL`,
		enrollmentID, startedAt.UTC(), completesAt.UTC(), actionID, application.EnrollmentInProgress)
	if err != nil {
		return false, fmt.Errorf("postgres: resuming course: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Certify records a certificate once. ON CONFLICT makes a repeat a quiet
// no-op instead of an error that would abort the transaction around it.
func (r *EducationRepository) Certify(ctx context.Context, playerID, courseCode, enrollmentID string, at time.Time) (bool, error) {
	id, err := newUUID()
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO certifications (id, player_id, course_code, enrollment_id, issued_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5)
		 ON CONFLICT ON CONSTRAINT `+certificationsPlayerCourseKey+` DO NOTHING`,
		id, playerID, courseCode, enrollmentID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: issuing certificate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Certifications lists a player's certificates, oldest first.
func (r *EducationRepository) Certifications(ctx context.Context, playerID string) ([]application.Certification, error) {
	rows, err := r.q.Query(ctx,
		`SELECT course_code, issued_at FROM certifications WHERE player_id = $1::uuid ORDER BY issued_at, course_code`,
		playerID)
	if err != nil {
		if isInvalidUUIDText(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: listing certificates: %w", err)
	}
	defer rows.Close()
	var out []application.Certification
	for rows.Next() {
		var c application.Certification
		if err := rows.Scan(&c.CourseCode, &c.IssuedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning certificate: %w", err)
		}
		c.IssuedAt = c.IssuedAt.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}
