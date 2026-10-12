package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// DebtRepository is the loans between polities (migration 0154; docs/adr/0076 11), bound to one transaction.
type DebtRepository struct{ q querier }

var _ application.DebtRepository = (*DebtRepository)(nil)

const sovereignLoanColumns = `id::text, lender_kind, lender_id::text, borrower_kind, borrower_id::text, status, COALESCE(stream, ''), offered_by::text, COALESCE(accepted_by::text, ''),
	COALESCE(replaced_by::text, ''), COALESCE(replaces::text, ''), principal, rate_bps, fees_due, periods, periods_per_year, paid, arrears, offered_at, expires_at, accepted_at, next_due_at, closed_at`

func scanSovereignLoan(row pgx.Row) (*application.SovereignLoan, error) {
	var l application.SovereignLoan
	if err := row.Scan(&l.ID, &l.LenderKind, &l.LenderID, &l.BorrowerKind, &l.BorrowerID, &l.Status, &l.Stream, &l.OfferedBy, &l.AcceptedBy, &l.ReplacedBy, &l.Replaces,
		&l.Principal, &l.RateBPS, &l.FeesDue, &l.Periods, &l.PeriodsPerYear, &l.Paid, &l.Arrears, &l.OfferedAt, &l.ExpiresAt, &l.AcceptedAt, &l.NextDueAt, &l.ClosedAt); err != nil {
		return nil, err
	}
	l.OfferedAt, l.ExpiresAt = l.OfferedAt.UTC(), l.ExpiresAt.UTC()
	l.AcceptedAt, l.NextDueAt, l.ClosedAt = utcPtr(l.AcceptedAt), utcPtr(l.NextDueAt), utcPtr(l.ClosedAt)
	return &l, nil
}

// Insert writes an offered loan.
func (r *DebtRepository) Insert(ctx context.Context, l application.SovereignLoan) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO sovereign_loans (id, lender_kind, lender_id, borrower_kind, borrower_id, principal, rate_bps, periods, periods_per_year, stream, offered_by, offered_at, expires_at, replaces, status)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, $11::uuid, $12, $13, $14::uuid, $15)`,
		l.ID, l.LenderKind, l.LenderID, l.BorrowerKind, l.BorrowerID, l.Principal, l.RateBPS, l.Periods, l.PeriodsPerYear, nullText(l.Stream), l.OfferedBy, l.OfferedAt.UTC(), l.ExpiresAt.UTC(),
		nullText(l.Replaces), orStatus(l.Status)); err != nil {
		return fmt.Errorf("postgres: offering a loan: %w", err)
	}
	return nil
}

func orStatus(s string) string {
	if s == "" {
		return "offered"
	}
	return s
}

// ByID reads a loan, nil for none.
func (r *DebtRepository) ByID(ctx context.Context, id string, lock bool) (*application.SovereignLoan, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + sovereignLoanColumns + ` FROM sovereign_loans WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanSovereignLoan(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a loan: %w", err)
	}
	return l, nil
}

// Of lists the loans a polity is a party to, newest first.
func (r *DebtRepository) Of(ctx context.Context, kind, id string, limit int) ([]application.SovereignLoan, error) {
	rows, err := r.q.Query(ctx, `SELECT `+sovereignLoanColumns+` FROM sovereign_loans WHERE (lender_kind = $1 AND lender_id = $2::uuid) OR (borrower_kind = $1 AND borrower_id = $2::uuid) ORDER BY offered_at DESC, id LIMIT $3`, kind, id, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing loans: %w", err)
	}
	defer rows.Close()
	var out []application.SovereignLoan
	for rows.Next() {
		l, err := scanSovereignLoan(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a loan: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// Outstanding is the principal still owed on a polity's active loans (as borrower, or owed to it when asLender).
func (r *DebtRepository) Outstanding(ctx context.Context, kind, id string, asLender bool) (int64, error) {
	col := "borrower"
	if asLender {
		col = "lender"
	}
	var n int64
	err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(principal - (principal / periods) * LEAST(paid, periods - 1) - CASE WHEN paid >= periods THEN principal % periods ELSE 0 END), 0)::bigint
		FROM sovereign_loans WHERE status IN ('active', 'defaulted') AND `+col+`_kind = $1 AND `+col+`_id = $2::uuid`, kind, id).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: summing a polity's debt: %w", err)
	}
	return n, nil
}

// Service is the instalment a polity pays each period across its active loans (principal over the periods, rounded up).
func (r *DebtRepository) Service(ctx context.Context, kind, id string) (int64, error) {
	var n int64
	err := r.q.QueryRow(ctx, `SELECT COALESCE(sum(((principal + (principal * rate_bps * periods) / (10000 * periods_per_year) + periods - 1) / periods)), 0)::bigint
		FROM sovereign_loans WHERE status = 'active' AND borrower_kind = $1 AND borrower_id = $2::uuid`, kind, id).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: summing a polity's debt service: %w", err)
	}
	return n, nil
}

// SetStatus moves a loan from one status to another; false when it was not in `from`.
func (r *DebtRepository) SetStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE sovereign_loans SET status = $3, closed_at = CASE WHEN $3 IN ('repaid', 'written_off', 'restructured', 'declined', 'withdrawn', 'expired') THEN $4::timestamptz ELSE closed_at END
		WHERE id = $1::uuid AND status = $2`, id, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving a loan: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Accept activates an offered loan; false when it was not offered any more.
func (r *DebtRepository) Accept(ctx context.Context, id, by string, at, next time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE sovereign_loans SET status = 'active', accepted_by = $2::uuid, accepted_at = $3, next_due_at = $4 WHERE id = $1::uuid AND status = 'offered' AND expires_at > $3`,
		id, by, at.UTC(), next.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: accepting a loan: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Collected writes the result of one period.
func (r *DebtRepository) Collected(ctx context.Context, id string, paid, arrears int, feesDue int64, next *time.Time) error {
	var n any
	if next != nil {
		n = next.UTC()
	}
	if _, err := r.q.Exec(ctx, `UPDATE sovereign_loans SET paid = $2, arrears = $3, fees_due = $4, next_due_at = $5 WHERE id = $1::uuid`, id, paid, arrears, feesDue, n); err != nil {
		return fmt.Errorf("postgres: writing a loan's period: %w", err)
	}
	return nil
}

// Link records that a restructured loan was replaced by a new one.
func (r *DebtRepository) Link(ctx context.Context, oldID, newID string) error {
	if _, err := r.q.Exec(ctx, `UPDATE sovereign_loans SET replaced_by = $2::uuid WHERE id = $1::uuid`, oldID, newID); err != nil {
		return fmt.Errorf("postgres: linking loans: %w", err)
	}
	return nil
}

// PayPeriod writes one instalment; false when it was written already.
func (r *DebtRepository) PayPeriod(ctx context.Context, p application.SovereignPeriod) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO sovereign_loan_periods (loan_id, k, principal, interest, fee, ledger_tx, at) VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7) ON CONFLICT DO NOTHING`,
		p.LoanID, p.K, p.Principal, p.Interest, p.Fee, nullText(p.LedgerTx), p.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: paying a loan's period: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AddEvent appends a line to a loan's log.
func (r *DebtRepository) AddEvent(ctx context.Context, id, loanID, kind string, detail map[string]any, at time.Time) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO sovereign_loan_events (id, loan_id, kind, detail, at) VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5)`, id, loanID, kind, raw, at.UTC()); err != nil {
		return fmt.Errorf("postgres: writing a loan's log: %w", err)
	}
	return nil
}

// Events reads the newest lines of a loan's log.
func (r *DebtRepository) Events(ctx context.Context, loanID string, limit int) ([]application.SovereignEvent, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, loan_id::text, kind, detail, at FROM sovereign_loan_events WHERE loan_id = $1::uuid ORDER BY at DESC, id DESC LIMIT $2`, loanID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a loan's log: %w", err)
	}
	defer rows.Close()
	var out []application.SovereignEvent
	for rows.Next() {
		var e application.SovereignEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.LoanID, &e.Kind, &raw, &e.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &e.Detail)
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Record is a polity's public record as a borrower.
func (r *DebtRepository) Record(ctx context.Context, kind, id string) (application.DebtRecord, error) {
	var rec application.DebtRecord
	err := r.q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status <> 'offered' AND status <> 'declined' AND status <> 'withdrawn' AND status <> 'expired'),
		count(*) FILTER (WHERE status = 'repaid'),
		count(*) FILTER (WHERE arrears > 0 OR status = 'defaulted'),
		count(*) FILTER (WHERE status IN ('defaulted', 'written_off'))
		FROM sovereign_loans WHERE borrower_kind = $1 AND borrower_id = $2::uuid`, kind, id).Scan(&rec.Taken, &rec.Repaid, &rec.Late, &rec.Defaulted)
	if err != nil {
		return rec, fmt.Errorf("postgres: reading a polity's debt record: %w", err)
	}
	return rec, nil
}
