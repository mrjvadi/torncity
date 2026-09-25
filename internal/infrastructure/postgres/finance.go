package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists finance (migrations/0029_finance): the finance clock,
// the national banks' loans and the credit record, savings, insurance, the
// gold dealer and the portfolio marks. The stock exchange is stocks.go.

// FinanceRepository implements application.FinanceRepository.
type FinanceRepository struct {
	q querier
}

var _ application.FinanceRepository = (*FinanceRepository)(nil)

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Clock returns the finance clock, locked.
func (r *FinanceRepository) Clock(ctx context.Context, now time.Time) (*application.FinanceClock, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO finance_clock (id, period_no, period_started_at, updated_at)
		VALUES (1, 1, $1, $1) ON CONFLICT (id) DO NOTHING`, now.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening the finance clock: %w", err)
	}
	var c application.FinanceClock
	if err := r.q.QueryRow(ctx, `SELECT period_no, period_started_at, next_at, COALESCE(action_id::text, ''), updated_at
		  FROM finance_clock WHERE id = 1 FOR UPDATE`).Scan(&c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID,
		&c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: reading the finance clock: %w", err)
	}
	c.PeriodStartedAt, c.UpdatedAt, c.NextAt = c.PeriodStartedAt.UTC(), c.UpdatedAt.UTC(), utcPtr(c.NextAt)
	return &c, nil
}

// CurrentPeriod reads the clock's period without a lock.
func (r *FinanceRepository) CurrentPeriod(ctx context.Context) (int64, *time.Time, error) {
	var no int64
	var next *time.Time
	err := r.q.QueryRow(ctx, `SELECT period_no, next_at FROM finance_clock WHERE id = 1`).Scan(&no, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("postgres: reading the finance period: %w", err)
	}
	return no, utcPtr(next), nil
}

// SaveClock writes the clock.
func (r *FinanceRepository) SaveClock(ctx context.Context, c application.FinanceClock) error {
	if _, err := r.q.Exec(ctx, `UPDATE finance_clock SET period_no = $1, period_started_at = $2, next_at = $3,
		action_id = $4::uuid, updated_at = $5 WHERE id = 1`,
		c.PeriodNo, c.PeriodStartedAt.UTC(), c.NextAt, nullText(c.ActionID), c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving the finance clock: %w", err)
	}
	return nil
}

// once runs an insert that does nothing on conflict and reports whether it
// wrote.
func (r *FinanceRepository) once(ctx context.Context, what, sql string, args ...any) (bool, error) {
	tag, err := r.q.Exec(ctx, sql, args...)
	if err != nil {
		return false, fmt.Errorf("postgres: recording %s: %w", what, err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordPeriod records a settled period.
func (r *FinanceRepository) RecordPeriod(ctx context.Context, periodNo int64, at time.Time) (bool, error) {
	return r.once(ctx, "a finance period", `INSERT INTO finance_periods (period_no, settled_at) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, periodNo, at.UTC())
}

// RecordFunding records a bank's funding for a period.
func (r *FinanceRepository) RecordFunding(ctx context.Context, countryID string, periodNo, amount int64, ledgerTx string,
	at time.Time,
) (bool, error) {
	return r.once(ctx, "a bank's funding", `INSERT INTO bank_fundings (country_id, period_no, amount,
		ledger_transaction_id, funded_at) VALUES ($1::uuid, $2, $3, $4::uuid, $5) ON CONFLICT DO NOTHING`,
		countryID, periodNo, amount, nullText(ledgerTx), at.UTC())
}

const loanColumns = `id::text, no, product, borrower_kind, player_id::text, COALESCE(company_id::text, ''),
	country_id::text, COALESCE(property_id::text, ''), principal, rate_bps, interest, periods, first_period,
	paid_periods, arrears, missed_total, principal_paid, interest_paid, fees_due, fees_paid, status, recovered,
	written_off, disbursement_transaction_id::text, opened_at, closed_at, updated_at`

func scanLoan(row pgx.Row) (*application.Loan, error) {
	var l application.Loan
	if err := row.Scan(&l.ID, &l.No, &l.Product, &l.BorrowerKind, &l.PlayerID, &l.CompanyID, &l.CountryID, &l.PropertyID,
		&l.Principal, &l.RateBPS, &l.Interest, &l.Periods, &l.FirstPeriod, &l.PaidPeriods, &l.Arrears, &l.MissedTotal,
		&l.PrincipalPaid, &l.InterestPaid, &l.FeesDue, &l.FeesPaid, &l.Status, &l.Recovered, &l.WrittenOff,
		&l.DisbursementTx, &l.OpenedAt, &l.ClosedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	l.OpenedAt, l.UpdatedAt, l.ClosedAt = l.OpenedAt.UTC(), l.UpdatedAt.UTC(), utcPtr(l.ClosedAt)
	return &l, nil
}

func (r *FinanceRepository) loans(ctx context.Context, sql string, args ...any) ([]application.Loan, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing loans: %w", err)
	}
	defer rows.Close()
	var out []application.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a loan: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// CreateLoan inserts a loan.
func (r *FinanceRepository) CreateLoan(ctx context.Context, l application.Loan) (application.Loan, error) {
	err := r.q.QueryRow(ctx, `
		INSERT INTO loans (id, product, borrower_kind, player_id, company_id, country_id, property_id, principal,
		                   rate_bps, interest, periods, first_period, status, disbursement_transaction_id, opened_at,
		                   updated_at)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11, $12, 'active', $13::uuid, $14, $14)
		RETURNING no`,
		l.ID, l.Product, l.BorrowerKind, l.PlayerID, nullText(l.CompanyID), l.CountryID, nullText(l.PropertyID),
		l.Principal, l.RateBPS, l.Interest, l.Periods, l.FirstPeriod, l.DisbursementTx, l.OpenedAt.UTC()).Scan(&l.No)
	if err != nil {
		return l, fmt.Errorf("postgres: creating a loan: %w", err)
	}
	l.Status, l.UpdatedAt = application.LoanActive, l.OpenedAt
	return l, nil
}

// LoanByNo reads a loan.
func (r *FinanceRepository) LoanByNo(ctx context.Context, no int64, lock bool) (*application.Loan, error) {
	sql := `SELECT ` + loanColumns + ` FROM loans WHERE no = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanLoan(r.q.QueryRow(ctx, sql, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrLoanNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a loan: %w", err)
	}
	return l, nil
}

// LoansOf lists a player's loans.
func (r *FinanceRepository) LoansOf(ctx context.Context, playerID string, limit int) ([]application.Loan, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return r.loans(ctx, `SELECT `+loanColumns+` FROM loans WHERE player_id = $1::uuid
		ORDER BY (status = 'active') DESC, opened_at DESC, no DESC LIMIT $2`, playerID, limit)
}

// DueLoans lists the running loans due at or before a period, locked.
func (r *FinanceRepository) DueLoans(ctx context.Context, periodNo int64) ([]application.Loan, error) {
	return r.loans(ctx, `SELECT `+loanColumns+` FROM loans WHERE status = 'active' AND first_period <= $1
		ORDER BY id FOR UPDATE`, periodNo)
}

// SaveLoan writes a loan's progress.
func (r *FinanceRepository) SaveLoan(ctx context.Context, l application.Loan) error {
	if _, err := r.q.Exec(ctx, `
		UPDATE loans SET paid_periods = $2, arrears = $3, missed_total = $4, principal_paid = $5, interest_paid = $6,
		       fees_due = $7, fees_paid = $8, status = $9, recovered = $10, written_off = $11, closed_at = $12,
		       property_id = $13::uuid, updated_at = $14
		 WHERE id = $1::uuid`,
		l.ID, l.PaidPeriods, l.Arrears, l.MissedTotal, l.PrincipalPaid, l.InterestPaid, l.FeesDue, l.FeesPaid, l.Status,
		l.Recovered, l.WrittenOff, l.ClosedAt, nullText(l.PropertyID), l.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a loan: %w", err)
	}
	return nil
}

// RecordLoanPeriod records one period of a loan.
func (r *FinanceRepository) RecordLoanPeriod(ctx context.Context, p application.LoanPeriod) (bool, error) {
	return r.once(ctx, "a loan's period", `
		INSERT INTO loan_periods (loan_id, period_no, due, paid, principal, interest, fees, new_fee, missed, defaulted, at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING`,
		p.LoanID, p.PeriodNo, p.Due, p.Paid, p.Principal, p.Interest, p.Fees, p.NewFee, p.Missed, p.Defaulted, p.At.UTC())
}

func (r *FinanceRepository) count(ctx context.Context, what, sql string, args ...any) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		if isInvalidUUIDText(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("postgres: counting %s: %w", what, err)
	}
	return n, nil
}

// LockBorrower serialises one player's borrowing.
func (r *FinanceRepository) LockBorrower(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('finance-borrower'), hashtext($1))`, playerID); err != nil {
		return fmt.Errorf("postgres: locking a borrower: %w", err)
	}
	return nil
}

// ActiveLoans counts a player's running loans.
func (r *FinanceRepository) ActiveLoans(ctx context.Context, playerID string) (int, error) {
	n, err := r.count(ctx, "loans", `SELECT count(*) FROM loans WHERE player_id = $1::uuid AND status = 'active'`, playerID)
	return int(n), err
}

// PledgedProperty reports whether a property secures a running loan.
func (r *FinanceRepository) PledgedProperty(ctx context.Context, propertyID string) (bool, error) {
	n, err := r.count(ctx, "pledges", `SELECT count(*) FROM loans WHERE property_id = $1::uuid AND status = 'active'`,
		propertyID)
	return n > 0, err
}

// Outstanding is the principal a country's bank has out.
func (r *FinanceRepository) Outstanding(ctx context.Context, countryID string) (int64, error) {
	return r.count(ctx, "principal out", `SELECT COALESCE(SUM(principal - principal_paid), 0)::bigint FROM loans
		WHERE country_id = $1::uuid AND status = 'active'`, countryID)
}

// loanOwedSQL is what a running loan still asks: the instalments not yet
// paid, and fees.
const loanOwedSQL = `(principal - principal_paid) + (interest - interest_paid) + fees_due`

// PersonalOwed is what a player owes on their own running loans.
func (r *FinanceRepository) PersonalOwed(ctx context.Context, playerID string) (int64, error) {
	return r.count(ctx, "debt", `SELECT COALESCE(SUM(`+loanOwedSQL+`), 0)::bigint FROM loans
		WHERE player_id = $1::uuid AND status = 'active' AND borrower_kind = 'player'`, playerID)
}

// AddCreditEvent records a credit event once.
func (r *FinanceRepository) AddCreditEvent(ctx context.Context, e application.CreditEvent) (bool, error) {
	return r.once(ctx, "a credit event", `INSERT INTO credit_events (id, player_id, loan_id, kind, period_no, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6) ON CONFLICT DO NOTHING`,
		e.ID, e.PlayerID, e.LoanID, e.Kind, e.PeriodNo, e.At.UTC())
}

// CreditRecord counts a player's credit events.
func (r *FinanceRepository) CreditRecord(ctx context.Context, playerID string, memory, window time.Time) (
	application.CreditRecord, error,
) {
	var c application.CreditRecord
	if !validUUID(playerID) {
		return c, nil
	}
	if err := r.q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE kind = 'on_time' AND at >= $2),
		       count(*) FILTER (WHERE kind = 'missed' AND at >= $2),
		       count(*) FILTER (WHERE kind = 'default' AND at >= $2),
		       count(*) FILTER (WHERE kind = 'repaid' AND at >= $2),
		       count(*) FILTER (WHERE kind = 'opened' AND at >= $3)
		  FROM credit_events WHERE player_id = $1::uuid`, playerID, memory.UTC(), window.UTC()).Scan(
		&c.OnTime, &c.Missed, &c.Defaults, &c.Repaid, &c.Opened); err != nil {
		return c, fmt.Errorf("postgres: reading a credit record: %w", err)
	}
	return c, nil
}

// ShiftsSince counts the shifts a player worked since then.
func (r *FinanceRepository) ShiftsSince(ctx context.Context, playerID string, since time.Time) (int64, error) {
	return r.count(ctx, "shifts", `SELECT count(*) FROM work_shifts WHERE player_id = $1::uuid AND worked_at >= $2`,
		playerID, since.UTC())
}

// SavingsMark reads a savings account's mark.
func (r *FinanceRepository) SavingsMark(ctx context.Context, playerID string) (*application.SavingsMark, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	m := application.SavingsMark{PlayerID: playerID}
	err := r.q.QueryRow(ctx, `SELECT marked_balance, marked_period, updated_at FROM savings_accounts
		WHERE player_id = $1::uuid`, playerID).Scan(&m.Balance, &m.Period, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a savings mark: %w", err)
	}
	return &m, nil
}

// SaveSavingsMark writes a mark.
func (r *FinanceRepository) SaveSavingsMark(ctx context.Context, m application.SavingsMark) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO savings_accounts (player_id, marked_balance, marked_period, updated_at)
		VALUES ($1::uuid, $2, $3, $4) ON CONFLICT (player_id) DO UPDATE
		SET marked_balance = EXCLUDED.marked_balance, marked_period = EXCLUDED.marked_period,
		    updated_at = EXCLUDED.updated_at`, m.PlayerID, m.Balance, m.Period, m.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a savings mark: %w", err)
	}
	return nil
}

// Savers lists the savings accounts with money in them, or a mark.
func (r *FinanceRepository) Savers(ctx context.Context) ([]application.Saver, error) {
	rows, err := r.q.Query(ctx, `
		SELECT p.id::text, COALESCE(a.balance, 0), m.marked_balance, m.marked_period, m.updated_at
		  FROM players p
		  LEFT JOIN accounts a ON a.kind = 'player_savings' AND a.owner_id = p.id
		  LEFT JOIN savings_accounts m ON m.player_id = p.id
		 WHERE COALESCE(a.balance, 0) > 0 OR COALESCE(m.marked_balance, 0) > 0
		 ORDER BY p.id FOR UPDATE OF p`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing savers: %w", err)
	}
	defer rows.Close()
	var out []application.Saver
	for rows.Next() {
		var s application.Saver
		var bal, per *int64
		var at *time.Time
		if err := rows.Scan(&s.PlayerID, &s.Balance, &bal, &per, &at); err != nil {
			return nil, fmt.Errorf("postgres: scanning a saver: %w", err)
		}
		if bal != nil {
			s.HasMark = true
			s.Mark = application.SavingsMark{PlayerID: s.PlayerID, Balance: *bal, Period: *per, UpdatedAt: at.UTC()}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RecordSavingsInterest records a period's interest once.
func (r *FinanceRepository) RecordSavingsInterest(ctx context.Context, s application.SavingsInterest) (bool, error) {
	return r.once(ctx, "savings interest", `
		INSERT INTO savings_interest (player_id, period_no, country_id, balance, rate_bps, amount, ledger_transaction_id,
		                              paid_at)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7::uuid, $8) ON CONFLICT DO NOTHING`,
		s.PlayerID, s.PeriodNo, s.CountryID, s.Balance, s.RateBPS, s.Amount, nullText(s.LedgerTx), s.At.UTC())
}

// SavingsEarned is the interest a player's savings earned.
func (r *FinanceRepository) SavingsEarned(ctx context.Context, playerID string) (int64, error) {
	return r.count(ctx, "interest", `SELECT COALESCE(SUM(amount), 0)::bigint FROM savings_interest
		WHERE player_id = $1::uuid`, playerID)
}

const policyColumns = `id::text, no, player_id::text, product, covers, country_id::text, COALESCE(property_id::text, ''),
	status, started_at, claims_from, ended_at, COALESCE(end_reason, ''), updated_at`

func scanPolicy(row pgx.Row) (*application.InsurancePolicy, error) {
	var p application.InsurancePolicy
	if err := row.Scan(&p.ID, &p.No, &p.PlayerID, &p.Product, &p.Covers, &p.CountryID, &p.PropertyID, &p.Status,
		&p.StartedAt, &p.ClaimsFrom, &p.EndedAt, &p.EndReason, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.StartedAt, p.ClaimsFrom, p.UpdatedAt, p.EndedAt = p.StartedAt.UTC(), p.ClaimsFrom.UTC(), p.UpdatedAt.UTC(),
		utcPtr(p.EndedAt)
	return &p, nil
}

func (r *FinanceRepository) policies(ctx context.Context, sql string, args ...any) ([]application.InsurancePolicy, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing policies: %w", err)
	}
	defer rows.Close()
	var out []application.InsurancePolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a policy: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// CreatePolicy inserts a policy.
func (r *FinanceRepository) CreatePolicy(ctx context.Context, p application.InsurancePolicy) (application.InsurancePolicy, error) {
	err := r.q.QueryRow(ctx, `
		INSERT INTO insurance_policies (id, player_id, product, covers, country_id, property_id, status, started_at,
		                                claims_from, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, 'active', $7, $8, $7) RETURNING no`,
		p.ID, p.PlayerID, p.Product, p.Covers, p.CountryID, nullText(p.PropertyID), p.StartedAt.UTC(),
		p.ClaimsFrom.UTC()).Scan(&p.No)
	if violates(err, sqlstateUniqueViolation, "insurance_policies_one_idx") {
		return p, application.ErrPolicyExists
	}
	if err != nil {
		return p, fmt.Errorf("postgres: creating a policy: %w", err)
	}
	p.Status, p.UpdatedAt = application.PolicyActive, p.StartedAt
	return p, nil
}

// PolicyByNo reads a policy, locked.
func (r *FinanceRepository) PolicyByNo(ctx context.Context, no int64) (*application.InsurancePolicy, error) {
	p, err := scanPolicy(r.q.QueryRow(ctx, `SELECT `+policyColumns+` FROM insurance_policies WHERE no = $1 FOR UPDATE`, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrPolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a policy: %w", err)
	}
	return p, nil
}

// PoliciesOf lists a player's policies.
func (r *FinanceRepository) PoliciesOf(ctx context.Context, playerID string, limit int) ([]application.InsurancePolicy, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return r.policies(ctx, `SELECT `+policyColumns+` FROM insurance_policies WHERE player_id = $1::uuid
		ORDER BY (status = 'active') DESC, started_at DESC, no DESC LIMIT $2`, playerID, limit)
}

// ActivePolicies lists every running policy, locked.
func (r *FinanceRepository) ActivePolicies(ctx context.Context) ([]application.InsurancePolicy, error) {
	return r.policies(ctx, `SELECT `+policyColumns+` FROM insurance_policies WHERE status = 'active'
		ORDER BY id FOR UPDATE`)
}

// CoveringPolicy is a player's running policy of a cover.
func (r *FinanceRepository) CoveringPolicy(ctx context.Context, playerID, covers string) (*application.InsurancePolicy, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	p, err := scanPolicy(r.q.QueryRow(ctx, `SELECT `+policyColumns+` FROM insurance_policies
		WHERE player_id = $1::uuid AND covers = $2 AND status = 'active' ORDER BY started_at, id LIMIT 1 FOR UPDATE`,
		playerID, covers))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a covering policy: %w", err)
	}
	return p, nil
}

// PropertyPolicies lists the running policies on a city's properties.
func (r *FinanceRepository) PropertyPolicies(ctx context.Context, cityID string) ([]application.InsurancePolicy, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	return r.policies(ctx, `SELECT `+policyColumnsI+` FROM insurance_policies i JOIN properties pr ON pr.id = i.property_id
		WHERE i.status = 'active' AND pr.city_id = $1::uuid AND pr.status = 'owned' AND pr.owner_player_id = i.player_id
		ORDER BY i.id FOR UPDATE OF i`, cityID)
}

const policyColumnsI = `i.id::text, i.no, i.player_id::text, i.product, i.covers, i.country_id::text,
	COALESCE(i.property_id::text, ''), i.status, i.started_at, i.claims_from, i.ended_at, COALESCE(i.end_reason, ''),
	i.updated_at`

// SavePolicy writes a policy's status.
func (r *FinanceRepository) SavePolicy(ctx context.Context, p application.InsurancePolicy) error {
	if _, err := r.q.Exec(ctx, `UPDATE insurance_policies SET status = $2, ended_at = $3, end_reason = $4, updated_at = $5
		WHERE id = $1::uuid`, p.ID, p.Status, p.EndedAt, nullText(p.EndReason), p.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a policy: %w", err)
	}
	return nil
}

// PremiumPaid reports whether a policy's premium for a period is paid.
func (r *FinanceRepository) PremiumPaid(ctx context.Context, policyID string, periodNo int64) (bool, error) {
	n, err := r.count(ctx, "premiums", `SELECT count(*) FROM insurance_premiums WHERE policy_id = $1::uuid
		AND period_no = $2`, policyID, periodNo)
	return n > 0, err
}

// RecordPremium records a premium once.
func (r *FinanceRepository) RecordPremium(ctx context.Context, policyID string, periodNo, amount int64, ledgerTx string,
	at time.Time,
) (bool, error) {
	return r.once(ctx, "a premium", `INSERT INTO insurance_premiums (policy_id, period_no, amount, ledger_transaction_id,
		paid_at) VALUES ($1::uuid, $2, $3, $4::uuid, $5) ON CONFLICT DO NOTHING`,
		policyID, periodNo, amount, ledgerTx, at.UTC())
}

// RecordClaim records a claim once.
func (r *FinanceRepository) RecordClaim(ctx context.Context, c application.InsuranceClaim) (bool, error) {
	return r.once(ctx, "a claim", `
		INSERT INTO insurance_claims (id, policy_id, player_id, source, loss, due, paid, ledger_transaction_id, claimed_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid, $9) ON CONFLICT DO NOTHING`,
		c.ID, c.PolicyID, c.PlayerID, c.Source, c.Loss, c.Due, c.Paid, nullText(c.LedgerTx), c.At.UTC())
}

// ClaimsPaid sums what a policy paid.
func (r *FinanceRepository) ClaimsPaid(ctx context.Context, policyID string) (int64, error) {
	return r.count(ctx, "claims", `SELECT COALESCE(SUM(paid), 0)::bigint FROM insurance_claims WHERE policy_id = $1::uuid`,
		policyID)
}

// Dealer reads the gold dealer, opening it from start.
func (r *FinanceRepository) Dealer(ctx context.Context, start application.GoldDealer, lock bool) (*application.GoldDealer, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO gold_dealer (id, price, stock, reserve, updated_at)
		VALUES (1, $1, $2, $2, $3) ON CONFLICT (id) DO NOTHING`, start.Price, start.Reserve, start.UpdatedAt.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening the gold dealer: %w", err)
	}
	sql := `SELECT price, stock, reserve, updated_at FROM gold_dealer WHERE id = 1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var d application.GoldDealer
	if err := r.q.QueryRow(ctx, sql).Scan(&d.Price, &d.Stock, &d.Reserve, &d.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: reading the gold dealer: %w", err)
	}
	d.UpdatedAt = d.UpdatedAt.UTC()
	return &d, nil
}

// SaveDealer writes the dealer.
func (r *FinanceRepository) SaveDealer(ctx context.Context, d application.GoldDealer) error {
	if _, err := r.q.Exec(ctx, `UPDATE gold_dealer SET price = $1, stock = $2, updated_at = $3 WHERE id = 1`,
		d.Price, d.Stock, d.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving the gold dealer: %w", err)
	}
	return nil
}

// GoldHolding reads a player's gold, locked.
func (r *FinanceRepository) GoldHolding(ctx context.Context, playerID string) (application.GoldHolding, error) {
	h := application.GoldHolding{PlayerID: playerID}
	if !validUUID(playerID) {
		return h, nil
	}
	err := r.q.QueryRow(ctx, `SELECT grams, cost, updated_at FROM gold_holdings WHERE player_id = $1::uuid FOR UPDATE`,
		playerID).Scan(&h.Grams, &h.Cost, &h.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("postgres: reading gold: %w", err)
	}
	return h, nil
}

// SaveGoldHolding writes a player's gold.
func (r *FinanceRepository) SaveGoldHolding(ctx context.Context, h application.GoldHolding) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO gold_holdings (player_id, grams, cost, updated_at)
		VALUES ($1::uuid, $2, $3, $4) ON CONFLICT (player_id) DO UPDATE
		SET grams = EXCLUDED.grams, cost = EXCLUDED.cost, updated_at = EXCLUDED.updated_at`,
		h.PlayerID, h.Grams, h.Cost, h.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving gold: %w", err)
	}
	return nil
}

// RecordGoldTrade writes a gold trade.
func (r *FinanceRepository) RecordGoldTrade(ctx context.Context, t application.GoldTrade) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO gold_trades (id, player_id, side, grams, unit_price, total, method,
		ledger_transaction_id, traded_at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid, $9)`,
		t.ID, t.PlayerID, t.Side, t.Grams, t.UnitPrice, t.Total, nullText(t.Method), t.LedgerTx, t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a gold trade: %w", err)
	}
	return nil
}

// GoldNetSince is grams bought less sold since then.
func (r *FinanceRepository) GoldNetSince(ctx context.Context, since time.Time) (int64, error) {
	return r.count(ctx, "gold demand", `SELECT COALESCE(SUM(CASE side WHEN 'buy' THEN grams ELSE -grams END), 0)::bigint
		FROM gold_trades WHERE traded_at >= $1`, since.UTC())
}

// RecordGoldPrice records a period's price once.
func (r *FinanceRepository) RecordGoldPrice(ctx context.Context, p application.GoldPrice) (bool, error) {
	return r.once(ctx, "a gold price", `INSERT INTO gold_prices (period_no, price, net_grams, set_at)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, p.PeriodNo, p.Price, p.NetGrams, p.At.UTC())
}

// GoldPrices lists the latest prices.
func (r *FinanceRepository) GoldPrices(ctx context.Context, limit int) ([]application.GoldPrice, error) {
	rows, err := r.q.Query(ctx, `SELECT period_no, price, net_grams, set_at FROM gold_prices
		ORDER BY period_no DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing gold prices: %w", err)
	}
	defer rows.Close()
	var out []application.GoldPrice
	for rows.Next() {
		var p application.GoldPrice
		if err := rows.Scan(&p.PeriodNo, &p.Price, &p.NetGrams, &p.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a gold price: %w", err)
		}
		p.At = p.At.UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// companyBookSQL values every active company: its treasury less its upkeep
// debt and what its running business loans still ask, never below zero.
const companyBookSQL = `
    SELECT c.id, c.total_shares,
           GREATEST(COALESCE(a.balance, 0) - c.debt - COALESCE(bl.owed, 0), 0) AS book
      FROM companies c
      LEFT JOIN accounts a ON a.kind = 'company_treasury' AND a.owner_id = c.id
      LEFT JOIN (SELECT company_id, SUM(` + loanOwedSQL + `) AS owed FROM loans
                  WHERE status = 'active' AND company_id IS NOT NULL GROUP BY company_id) bl ON bl.company_id = c.id
     WHERE c.status = 'active'`

// shareValueSQL values every holding: at the company's last trade price
// once it has traded, else its share of the book. $P names the players.
const shareValueSQL = `
    SELECT s.player_id,
           SUM(CASE WHEN lt.unit_price IS NOT NULL THEN s.shares::numeric * lt.unit_price
                    WHEN sl.price IS NOT NULL THEN s.shares::numeric * sl.price
                    ELSE b.book::numeric * s.shares / b.total_shares END) AS value
      FROM company_shareholders s
      JOIN (` + companyBookSQL + `) b ON b.id = s.company_id
      LEFT JOIN (SELECT DISTINCT ON (company_id) company_id, unit_price FROM share_trades
                  ORDER BY company_id, created_at DESC, id DESC) lt ON lt.company_id = s.company_id
      LEFT JOIN stock_listings sl ON sl.company_id = s.company_id
     WHERE s.player_id IN (SELECT id FROM p)
     GROUP BY s.player_id`

// Portfolios values every player's portfolio. $1 one player or ''; $2 the
// dealer's buying price of a gram.
func (r *FinanceRepository) Portfolios(ctx context.Context, playerID string, bid int64) ([]application.Portfolio, error) {
	if playerID != "" && !validUUID(playerID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `
WITH p AS (
    SELECT id, public_code, display_name, telegram_user_id FROM players
     WHERE status = 'active' AND ($1 = '' OR id = NULLIF($1, '')::uuid)
), stocks AS (`+shareValueSQL+`
), gold AS (
    SELECT player_id, grams FROM gold_holdings WHERE player_id IN (SELECT id FROM p)
), savings AS (
    SELECT owner_id, balance FROM accounts WHERE kind = 'player_savings' AND owner_id IN (SELECT id FROM p)
), flows AS (
    SELECT buyer_id AS pid, SUM(notional)::numeric AS amount FROM share_trades
     WHERE buyer_id IN (SELECT id FROM p) GROUP BY buyer_id
    UNION ALL
    SELECT seller_id, -SUM(notional - fee)::numeric FROM share_trades
     WHERE seller_id IN (SELECT id FROM p) GROUP BY seller_id
    UNION ALL
    SELECT player_id, -SUM(amount)::numeric FROM dividend_payments
     WHERE player_id IN (SELECT id FROM p) GROUP BY player_id
    UNION ALL
    SELECT player_id, SUM(CASE side WHEN 'buy' THEN total ELSE -total END)::numeric FROM gold_trades
     WHERE player_id IN (SELECT id FROM p) GROUP BY player_id
    UNION ALL
    SELECT a.owner_id, SUM(e.amount)::numeric FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
     WHERE a.kind = 'player_savings' AND e.reason IN ('savings_deposit', 'savings_withdrawal')
       AND a.owner_id IN (SELECT id FROM p)
     GROUP BY a.owner_id
), invested AS (
    SELECT pid, SUM(amount) AS amount FROM flows GROUP BY pid
)
SELECT p.id::text, p.public_code, p.display_name, p.telegram_user_id,
       COALESCE(stocks.value, 0)::bigint, (COALESCE(gold.grams, 0) * $2::bigint)::bigint,
       COALESCE(savings.balance, 0)::bigint, COALESCE(invested.amount, 0)::bigint
  FROM p
  LEFT JOIN stocks ON stocks.player_id = p.id
  LEFT JOIN gold ON gold.player_id = p.id
  LEFT JOIN savings ON savings.owner_id = p.id
  LEFT JOIN invested ON invested.pid = p.id
 WHERE stocks.player_id IS NOT NULL OR gold.player_id IS NOT NULL OR savings.owner_id IS NOT NULL
    OR invested.pid IS NOT NULL`, playerID, bid)
	if err != nil {
		return nil, fmt.Errorf("postgres: valuing portfolios: %w", err)
	}
	defer rows.Close()
	var out []application.Portfolio
	for rows.Next() {
		var p application.Portfolio
		if err := rows.Scan(&p.PlayerID, &p.Code, &p.Name, &p.TelegramUserID, &p.Stocks, &p.Gold, &p.Savings,
			&p.Invested); err != nil {
			return nil, fmt.Errorf("postgres: scanning a portfolio: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Marks reads the last portfolio marks.
func (r *FinanceRepository) Marks(ctx context.Context) (map[string]application.PortfolioMark, error) {
	rows, err := r.q.Query(ctx, `SELECT player_id::text, gain, value, marked_at FROM portfolio_marks`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading portfolio marks: %w", err)
	}
	defer rows.Close()
	out := map[string]application.PortfolioMark{}
	for rows.Next() {
		var m application.PortfolioMark
		if err := rows.Scan(&m.PlayerID, &m.Gain, &m.Value, &m.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a portfolio mark: %w", err)
		}
		out[m.PlayerID] = m
	}
	return out, rows.Err()
}

// SaveMarks writes portfolio marks.
func (r *FinanceRepository) SaveMarks(ctx context.Context, marks []application.PortfolioMark) error {
	for _, m := range marks {
		if _, err := r.q.Exec(ctx, `INSERT INTO portfolio_marks (player_id, gain, value, marked_at)
			VALUES ($1::uuid, $2, $3, $4) ON CONFLICT (player_id) DO UPDATE
			SET gain = EXCLUDED.gain, value = EXCLUDED.value, marked_at = EXCLUDED.marked_at`,
			m.PlayerID, m.Gain, m.Value, m.At.UTC()); err != nil {
			return fmt.Errorf("postgres: saving a portfolio mark: %w", err)
		}
	}
	return nil
}
