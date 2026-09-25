package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the candidates of a campaign and the specialists a
// company hired, with their pay (migrations/0031_specialist_recruitment).

// Constraint names from migrations/0031 mapped to answers here.
const (
	recruitCandidatesSlotKey = "recruit_candidates_slot_key"
	npcStaffPaymentsPkey     = "npc_staff_payments_pkey"
)

const candidateColumns = `id::text, no, campaign_id::text, company_id::text, home_city_id::text, skill, level,
	preference, name_seed, expected, move_cost, value, chance_bps, check_no, seq, status, applied_at, expires_at,
	decided_at, COALESCE(decided_by::text, '')`

func scanCandidate(row pgx.Row) (*application.RecruitCandidate, error) {
	var c application.RecruitCandidate
	if err := row.Scan(&c.ID, &c.No, &c.CampaignID, &c.CompanyID, &c.HomeCityID, &c.Skill, &c.Level, &c.Preference,
		&c.NameSeed, &c.Expected, &c.MoveCost, &c.Value, &c.ChanceBPS, &c.CheckNo, &c.Seq, &c.Status, &c.AppliedAt,
		&c.ExpiresAt, &c.DecidedAt, &c.DecidedBy); err != nil {
		return nil, err
	}
	c.AppliedAt, c.ExpiresAt, c.DecidedAt = c.AppliedAt.UTC(), c.ExpiresAt.UTC(), utcPtr(c.DecidedAt)
	return &c, nil
}

// AddCandidate inserts a candidate; false when the check's slot is taken.
func (r *RecruitRepository) AddCandidate(ctx context.Context, c application.RecruitCandidate) (application.RecruitCandidate, bool, error) {
	err := r.q.QueryRow(ctx, `INSERT INTO recruit_candidates (id, campaign_id, company_id, home_city_id, skill, level,
		preference, name_seed, expected, move_cost, value, chance_bps, check_no, seq, status, applied_at, expires_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 'pending', $15, $16)
		ON CONFLICT ON CONSTRAINT `+recruitCandidatesSlotKey+` DO NOTHING
		RETURNING no`,
		c.ID, c.CampaignID, c.CompanyID, c.HomeCityID, c.Skill, c.Level, c.Preference, c.NameSeed, c.Expected,
		c.MoveCost, c.Value, c.ChanceBPS, c.CheckNo, c.Seq, c.AppliedAt.UTC(), c.ExpiresAt.UTC()).Scan(&c.No)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("postgres: adding a candidate: %w", err)
	}
	c.Status = application.CandidatePending
	return c, true, nil
}

// Candidate reads a candidate by number.
func (r *RecruitRepository) Candidate(ctx context.Context, no int64, lock bool) (*application.RecruitCandidate, error) {
	sql := `SELECT ` + candidateColumns + ` FROM recruit_candidates WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	c, err := scanCandidate(r.q.QueryRow(ctx, sql, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrCandidateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a candidate: %w", err)
	}
	return c, nil
}

// Candidates lists a campaign's candidates.
func (r *RecruitRepository) Candidates(ctx context.Context, campaignID string) ([]application.RecruitCandidate, error) {
	rows, err := r.q.Query(ctx, `SELECT `+candidateColumns+` FROM recruit_candidates WHERE campaign_id = $1::uuid
		ORDER BY (status = 'pending') DESC, no DESC`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing candidates: %w", err)
	}
	defer rows.Close()
	var out []application.RecruitCandidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a candidate: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Decide moves a pending candidate to status.
func (r *RecruitRepository) Decide(ctx context.Context, id, status, by string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE recruit_candidates SET status = $2, decided_at = $3, decided_by = $4::uuid
		WHERE id = $1::uuid AND status = 'pending'`, id, status, at.UTC(), nullableUUID(by))
	if err != nil {
		return false, fmt.Errorf("postgres: deciding a candidate: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Withdraw withdraws a campaign's pending candidates.
func (r *RecruitRepository) Withdraw(ctx context.Context, campaignID string, at time.Time) (int, error) {
	tag, err := r.q.Exec(ctx, `UPDATE recruit_candidates SET status = 'withdrawn', decided_at = $2
		WHERE campaign_id = $1::uuid AND status = 'pending'`, campaignID, at.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: withdrawing candidates: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Specialists.

const staffColumns = `id::text, no, company_id::text, candidate_id::text, campaign_id::text, home_city_id::text, skill,
	level, preference, name_seed, salary, housing, accepted_bps, term_periods, served, expiring, unpaid_run,
	underpaid_run, shares, signing_paid, relocation_paid, equity_paid, status, COALESCE(leave_reason, ''), hired_at,
	left_at, updated_at`

func scanStaff(row pgx.Row) (*application.NPCStaff, error) {
	var s application.NPCStaff
	if err := row.Scan(&s.ID, &s.No, &s.CompanyID, &s.CandidateID, &s.CampaignID, &s.HomeCityID, &s.Skill, &s.Level,
		&s.Preference, &s.NameSeed, &s.Salary, &s.Housing, &s.AcceptedBPS, &s.TermPeriods, &s.Served, &s.Expiring,
		&s.UnpaidRun, &s.UnderpaidRun, &s.Shares, &s.SigningPaid, &s.RelocationPaid, &s.EquityPaid, &s.Status,
		&s.LeaveReason, &s.HiredAt, &s.LeftAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	s.HiredAt, s.LeftAt, s.UpdatedAt = s.HiredAt.UTC(), utcPtr(s.LeftAt), s.UpdatedAt.UTC()
	return &s, nil
}

// Hire inserts a specialist.
func (r *RecruitRepository) Hire(ctx context.Context, s application.NPCStaff) (application.NPCStaff, error) {
	if err := r.q.QueryRow(ctx, `INSERT INTO npc_staff (id, company_id, candidate_id, campaign_id, home_city_id, skill,
		level, preference, name_seed, salary, housing, accepted_bps, term_periods, served, expiring, unpaid_run,
		underpaid_run, shares, signing_paid, relocation_paid, equity_paid, status, hired_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, $12, $13, 0, false, 0, 0,
		        $14, $15, $16, 0, 'active', $17, $17)
		RETURNING no`,
		s.ID, s.CompanyID, s.CandidateID, s.CampaignID, s.HomeCityID, s.Skill, s.Level, s.Preference, s.NameSeed,
		s.Salary, s.Housing, s.AcceptedBPS, s.TermPeriods, s.Shares, s.SigningPaid, s.RelocationPaid,
		s.HiredAt.UTC()).Scan(&s.No); err != nil {
		return s, fmt.Errorf("postgres: hiring a specialist: %w", err)
	}
	s.Status, s.UpdatedAt = application.StaffActive, s.HiredAt
	return s, nil
}

// Staff lists a company's active specialists.
func (r *RecruitRepository) Staff(ctx context.Context, companyID string) ([]application.NPCStaff, error) {
	rows, err := r.q.Query(ctx, `SELECT `+staffColumns+` FROM npc_staff WHERE company_id = $1::uuid AND status = 'active'
		ORDER BY no`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing specialists: %w", err)
	}
	defer rows.Close()
	var out []application.NPCStaff
	for rows.Next() {
		s, err := scanStaff(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a specialist: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// Specialist reads a specialist by number.
func (r *RecruitRepository) Specialist(ctx context.Context, no int64) (*application.NPCStaff, error) {
	s, err := scanStaff(r.q.QueryRow(ctx, `SELECT `+staffColumns+` FROM npc_staff WHERE no = $1`, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrSpecialistNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a specialist: %w", err)
	}
	return s, nil
}

// SaveStaff writes a specialist's contract and standing.
func (r *RecruitRepository) SaveStaff(ctx context.Context, s application.NPCStaff) error {
	if _, err := r.q.Exec(ctx, `UPDATE npc_staff SET salary = $2, housing = $3, accepted_bps = $4, term_periods = $5,
		served = $6, expiring = $7, unpaid_run = $8, underpaid_run = $9, equity_paid = $10, status = $11,
		leave_reason = $12, left_at = $13, updated_at = $14
		WHERE id = $1::uuid`,
		s.ID, s.Salary, s.Housing, s.AcceptedBPS, s.TermPeriods, s.Served, s.Expiring, s.UnpaidRun, s.UnderpaidRun,
		s.EquityPaid, s.Status, nullableText(s.LeaveReason), s.LeftAt, s.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a specialist: %w", err)
	}
	return nil
}

// RecordPayment appends a period's pay; false when it was paid already.
func (r *RecruitRepository) RecordPayment(ctx context.Context, p application.NPCStaffPayment) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO npc_staff_payments (staff_id, period_no, company_id, amount,
		ledger_transaction_id, paid_at)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5::uuid, $6)
		ON CONFLICT ON CONSTRAINT `+npcStaffPaymentsPkey+` DO NOTHING`,
		p.StaffID, p.PeriodNo, p.CompanyID, p.Amount, p.LedgerTransactionID, p.PaidAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a specialist's pay: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PaidTotal is what a specialist has been paid in all.
func (r *RecruitRepository) PaidTotal(ctx context.Context, staffID string) (int64, error) {
	var v int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0)::bigint FROM npc_staff_payments WHERE staff_id = $1::uuid`,
		staffID).Scan(&v); err != nil {
		return 0, fmt.Errorf("postgres: summing a specialist's pay: %w", err)
	}
	return v, nil
}
