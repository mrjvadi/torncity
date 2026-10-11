package postgres

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ApprenticeRepository is the apprenticeships (migration 0148), bound to one transaction.
type ApprenticeRepository struct{ q querier }

var _ application.ApprenticeRepository = (*ApprenticeRepository)(nil)

const apprenticeCols = `id::text, settlement_id::text, workplace_id::text, craft, master_kind, COALESCE(master_id::text, ''), apprentice_id::text,
	term_days, fee, wage_share_bps, status, reason, offered_at, started_at, ends_at, ended_at, shifts, xp_extra, COALESCE(master_xp_day, -1),
	fee_paid, refunded, certified_at`

func scanApprentice(r interface{ Scan(dest ...any) error }) (application.Apprenticeship, error) {
	var a application.Apprenticeship
	var started, ends, ended, certified *time.Time
	if err := r.Scan(&a.ID, &a.SettlementID, &a.WorkplaceID, &a.Craft, &a.MasterKind, &a.MasterID, &a.ApprenticeID, &a.TermDays, &a.Fee,
		&a.WageShareBPS, &a.Status, &a.Reason, &a.OfferedAt, &started, &ends, &ended, &a.Shifts, &a.XPExtra, &a.MasterXPDay,
		&a.FeePaid, &a.Refunded, &certified); err != nil {
		return a, err
	}
	for _, x := range []struct {
		from *time.Time
		to   *time.Time
	}{{started, &a.StartedAt}, {ends, &a.EndsAt}, {ended, &a.EndedAt}, {certified, &a.CertifiedAt}} {
		if x.from != nil {
			*x.to = *x.from
		}
	}
	return a, nil
}

func (r *ApprenticeRepository) list(ctx context.Context, where string, args ...any) ([]application.Apprenticeship, error) {
	rows, err := r.q.Query(ctx, `SELECT `+apprenticeCols+` FROM apprenticeships WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing apprenticeships: %w", err)
	}
	defer rows.Close()
	var out []application.Apprenticeship
	for rows.Next() {
		a, err := scanApprentice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *ApprenticeRepository) Create(ctx context.Context, a application.Apprenticeship) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO apprenticeships (id, settlement_id, workplace_id, craft, master_kind, master_id, apprentice_id, term_days, fee,
		wage_share_bps, status, offered_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NULLIF($6, '')::uuid, $7::uuid, $8, $9, $10, $11, $12)`,
		a.ID, a.SettlementID, a.WorkplaceID, a.Craft, a.MasterKind, a.MasterID, a.ApprenticeID, a.TermDays, a.Fee, a.WageShareBPS, a.Status,
		a.OfferedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: creating an apprenticeship: %w", err)
	}
	return nil
}

func (r *ApprenticeRepository) Get(ctx context.Context, id string) (*application.Apprenticeship, error) {
	a, err := scanApprentice(r.q.QueryRow(ctx, `SELECT `+apprenticeCols+` FROM apprenticeships WHERE id = $1::uuid`, id))
	if stderrors.Is(err, pgx.ErrNoRows) || isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an apprenticeship: %w", err)
	}
	return &a, nil
}

func (r *ApprenticeRepository) ActiveOf(ctx context.Context, apprenticeID string) (*application.Apprenticeship, error) {
	list, err := r.list(ctx, `apprentice_id = $1::uuid AND status = 'active'`, apprenticeID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (r *ApprenticeRepository) OfMaster(ctx context.Context, masterID string) ([]application.Apprenticeship, error) {
	return r.list(ctx, `master_id = $1::uuid AND status IN ('offered', 'active', 'completed') ORDER BY offered_at DESC LIMIT 50`, masterID)
}

func (r *ApprenticeRepository) OfApprentice(ctx context.Context, apprenticeID string, limit int) ([]application.Apprenticeship, error) {
	return r.list(ctx, `apprentice_id = $1::uuid ORDER BY offered_at DESC LIMIT $2`, apprenticeID, limit)
}

func (r *ApprenticeRepository) OfSettlement(ctx context.Context, settlementID string) ([]application.Apprenticeship, error) {
	return r.list(ctx, `settlement_id = $1::uuid AND status IN ('offered', 'active')`, settlementID)
}

func (r *ApprenticeRepository) Start(ctx context.Context, id string, at, ends time.Time, feePaid int64) error {
	tag, err := r.q.Exec(ctx, `UPDATE apprenticeships SET status = 'active', started_at = $2, ends_at = $3, fee_paid = $4
		WHERE id = $1::uuid AND status = 'offered'`, id, at.UTC(), ends.UTC(), feePaid)
	if err != nil {
		return fmt.Errorf("postgres: starting an apprenticeship: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: the apprenticeship %s is no longer on offer", id)
	}
	return nil
}

func (r *ApprenticeRepository) Finish(ctx context.Context, id, status, reason string, at time.Time, refund int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE apprenticeships SET status = $2, reason = $3, ended_at = $4, refunded = refunded + $5
		WHERE id = $1::uuid AND status IN ('offered', 'active')`, id, status, reason, at.UTC(), refund); err != nil {
		return fmt.Errorf("postgres: ending an apprenticeship: %w", err)
	}
	return nil
}

func (r *ApprenticeRepository) RecordShift(ctx context.Context, id string, extraXP int64, masterXPDay int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE apprenticeships SET shifts = shifts + 1, xp_extra = xp_extra + $2,
		master_xp_day = CASE WHEN $3 >= 0 THEN $3 ELSE master_xp_day END WHERE id = $1::uuid`, id, extraXP, masterXPDay); err != nil {
		return fmt.Errorf("postgres: counting an apprentice's shift: %w", err)
	}
	return nil
}

func (r *ApprenticeRepository) AddExam(ctx context.Context, e application.ApprenticeExam) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO apprentice_exams (id, apprenticeship_id, candidate_id, craft, examiner_kind, examiner_id, shifts, passed, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NULLIF($6, '')::uuid, $7, $8, $9) ON CONFLICT (apprenticeship_id) WHERE passed DO NOTHING`,
		e.ID, e.ApprenticeshipID, e.CandidateID, e.Craft, e.ExaminerKind, e.ExaminerID, e.Shifts, e.Passed, e.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an exam: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if e.Passed {
		if _, err := r.q.Exec(ctx, `UPDATE apprenticeships SET certified_at = $2 WHERE id = $1::uuid`, e.ApprenticeshipID, e.At.UTC()); err != nil {
			return false, fmt.Errorf("postgres: marking an apprenticeship certified: %w", err)
		}
	}
	return true, nil
}

func (r *ApprenticeRepository) ExamOf(ctx context.Context, apprenticeshipID string) (*application.ApprenticeExam, error) {
	var e application.ApprenticeExam
	err := r.q.QueryRow(ctx, `SELECT id::text, apprenticeship_id::text, candidate_id::text, craft, examiner_kind, COALESCE(examiner_id::text, ''), shifts, passed, at
		FROM apprentice_exams WHERE apprenticeship_id = $1::uuid ORDER BY passed DESC, at DESC LIMIT 1`, apprenticeshipID).Scan(&e.ID, &e.ApprenticeshipID, &e.CandidateID, &e.Craft,
		&e.ExaminerKind, &e.ExaminerID, &e.Shifts, &e.Passed, &e.At)
	if stderrors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an exam: %w", err)
	}
	return &e, nil
}

func (r *ApprenticeRepository) Lock(ctx context.Context, id string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "apprenticeship:"+id); err != nil {
		return fmt.Errorf("postgres: locking an apprenticeship: %w", err)
	}
	return nil
}

// CertifyVia records a certificate with its route and issuer once; fresh is false when the player holds it already.
func (r *EducationRepository) CertifyVia(ctx context.Context, c application.CertificateIssue) (bool, error) {
	id, err := newUUID()
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO certifications (id, player_id, course_code, enrollment_id, issued_at, issued_by, route, apprenticeship_id)
		 VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid, $7, NULLIF($8, '')::uuid)
		 ON CONFLICT ON CONSTRAINT `+certificationsPlayerCourseKey+` DO NOTHING`,
		id, c.PlayerID, c.CourseCode, c.EnrollmentID, c.At.UTC(), c.IssuedBy, c.Route, c.ApprenticeshipID)
	if err != nil {
		return false, fmt.Errorf("postgres: issuing certificate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
