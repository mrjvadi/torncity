package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// Constraint names from migrations/0120_course_teachers.up.sql.
const (
	courseTeachersNPCIdx    = "course_teachers_npc_idx"
	courseTeachersPlayerIdx = "course_teachers_player_idx"
)

const teacherColumns = `id::text, settlement_id::text, course_code, kind, COALESCE(player_id::text, ''), employer, started_at`

func scanTeacher(row pgx.Row) (application.CourseTeacher, error) {
	var t application.CourseTeacher
	if err := row.Scan(&t.ID, &t.SettlementID, &t.CourseCode, &t.Kind, &t.PlayerID, &t.Employer, &t.StartedAt); err != nil {
		return t, err
	}
	t.StartedAt = t.StartedAt.UTC()
	return t, nil
}

func (r *EducationRepository) queryTeachers(ctx context.Context, where string, args ...any) ([]application.CourseTeacher, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+teacherColumns+` FROM course_teachers WHERE `+where+` ORDER BY started_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing teachers: %w", err)
	}
	defer rows.Close()
	var out []application.CourseTeacher
	for rows.Next() {
		t, err := scanTeacher(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning teacher: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Teachers lists the active teachers of a course in a settlement.
func (r *EducationRepository) Teachers(ctx context.Context, settlementID, courseCode string) ([]application.CourseTeacher, error) {
	return r.queryTeachers(ctx, `settlement_id = $1::uuid AND course_code = $2 AND ended_at IS NULL`, settlementID, courseCode)
}

// SettlementTeachers lists every active teacher of a settlement.
func (r *EducationRepository) SettlementTeachers(ctx context.Context, settlementID string) ([]application.CourseTeacher, error) {
	return r.queryTeachers(ctx, `settlement_id = $1::uuid AND ended_at IS NULL`, settlementID)
}

// TeachingPosts lists the active posts a player holds.
func (r *EducationRepository) TeachingPosts(ctx context.Context, playerID string) ([]application.CourseTeacher, error) {
	return r.queryTeachers(ctx, `player_id = $1::uuid AND ended_at IS NULL`, playerID)
}

// AddTeacher records a post.
func (r *EducationRepository) AddTeacher(ctx context.Context, t application.CourseTeacher) error {
	id, err := ensureID(t.ID)
	if err != nil {
		return err
	}
	var player any
	if t.PlayerID != "" {
		player = t.PlayerID
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO course_teachers (id, settlement_id, course_code, kind, player_id, employer, started_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7)`,
		id, t.SettlementID, t.CourseCode, t.Kind, player, t.Employer, t.StartedAt.UTC())
	if violates(err, sqlstateUniqueViolation, courseTeachersNPCIdx) || violates(err, sqlstateUniqueViolation, courseTeachersPlayerIdx) {
		return application.ErrAlreadyTeaching
	}
	if err != nil {
		return fmt.Errorf("postgres: adding teacher: %w", err)
	}
	return nil
}

// EndTeacher ends a post.
func (r *EducationRepository) EndTeacher(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE course_teachers SET ended_at = $2 WHERE id = $1::uuid AND ended_at IS NULL`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: ending teacher: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Teacher returns an active post.
func (r *EducationRepository) Teacher(ctx context.Context, id string) (application.CourseTeacher, error) {
	t, err := scanTeacher(r.q.QueryRow(ctx,
		`SELECT `+teacherColumns+` FROM course_teachers WHERE id = $1::uuid AND ended_at IS NULL`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return t, application.ErrNoTeacher
	case err != nil:
		return t, fmt.Errorf("postgres: reading teacher: %w", err)
	}
	return t, nil
}

// TeacherAny returns a post, ended or not.
func (r *EducationRepository) TeacherAny(ctx context.Context, id string) (application.CourseTeacher, error) {
	t, err := scanTeacher(r.q.QueryRow(ctx,
		`SELECT `+teacherColumns+` FROM course_teachers WHERE id = $1::uuid`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return t, application.ErrNoTeacher
	case err != nil:
		return t, fmt.Errorf("postgres: reading teacher: %w", err)
	}
	return t, nil
}

// Students counts the enrolments in progress a teacher has.
func (r *EducationRepository) Students(ctx context.Context, teacherID string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM class_seats s JOIN enrollments e ON e.id = s.enrollment_id
		  WHERE s.teacher_id = $1::uuid AND e.status = $2`, teacherID, application.EnrollmentInProgress).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: counting students: %w", err)
	}
	return n, nil
}

// Seat records an enrolment's teacher.
func (r *EducationRepository) Seat(ctx context.Context, s application.ClassSeat) error {
	_, err := r.q.Exec(ctx,
		`INSERT INTO class_seats (enrollment_id, teacher_id, settlement_id, wage)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
		 ON CONFLICT (enrollment_id) DO NOTHING`,
		s.EnrollmentID, s.TeacherID, s.SettlementID, s.Wage)
	if err != nil {
		return fmt.Errorf("postgres: seating: %w", err)
	}
	return nil
}

// SeatOf returns an enrolment's seat, locked for the transaction.
func (r *EducationRepository) SeatOf(ctx context.Context, enrollmentID string) (*application.ClassSeat, error) {
	var s application.ClassSeat
	var paid *time.Time
	err := r.q.QueryRow(ctx,
		`SELECT enrollment_id::text, teacher_id::text, settlement_id::text, wage, wage_paid_at
		   FROM class_seats WHERE enrollment_id = $1::uuid FOR UPDATE`, enrollmentID).
		Scan(&s.EnrollmentID, &s.TeacherID, &s.SettlementID, &s.Wage, &paid)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading seat: %w", err)
	}
	s.WagePaid = paid != nil
	return &s, nil
}

// PaySeat marks the wage paid, once.
func (r *EducationRepository) PaySeat(ctx context.Context, enrollmentID string, at time.Time, paid int64) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`UPDATE class_seats SET wage_paid_at = $2, wage_paid = $3 WHERE enrollment_id = $1::uuid AND wage_paid_at IS NULL`,
		enrollmentID, at.UTC(), paid)
	if err != nil {
		return false, fmt.Errorf("postgres: paying seat: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
