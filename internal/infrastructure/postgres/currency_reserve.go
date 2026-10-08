package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// The reserve tools' storage (migration 0131; docs/adr/0033 6.3 to 6.8, 6.13, 7).

var _ application.ReserveRepository = (*CurrencyRepository)(nil)

// ReserveBankJurisdiction is the id of the Reserve Bank's jurisdiction.
func (r *CurrencyRepository) ReserveBankJurisdiction(ctx context.Context) (string, error) {
	var id string
	err := r.q.QueryRow(ctx, `SELECT id::text FROM jurisdictions WHERE code = 'support_reserve_bank'`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: looking for the Reserve Bank: %w", err)
	}
	return id, nil
}

const interventionColumns = `id::text, settlement_id::text, side, units, price, posted_by::text, posted_at, execute_after, status,
	executed_at, COALESCE(order_id::text, ''), sup_used, COALESCE(refusal, '')`

func scanIntervention(row pgx.Row) (application.Intervention, error) {
	var i application.Intervention
	err := row.Scan(&i.ID, &i.SettlementID, &i.Side, &i.Units, &i.Price, &i.PostedBy, &i.PostedAt, &i.ExecuteAfter, &i.Status,
		&i.ExecutedAt, &i.OrderID, &i.SUPUsed, &i.Refusal)
	i.PostedAt, i.ExecuteAfter, i.ExecutedAt = i.PostedAt.UTC(), i.ExecuteAfter.UTC(), utcPtr(i.ExecutedAt)
	return i, err
}

// InsertIntervention writes a request.
func (r *CurrencyRepository) InsertIntervention(ctx context.Context, i application.Intervention) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO currency_interventions (id, settlement_id, side, units, price, posted_by, posted_at, execute_after, status)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8, 'pending')`,
		i.ID, i.SettlementID, i.Side, i.Units, i.Price, i.PostedBy, i.PostedAt.UTC(), i.ExecuteAfter.UTC()); err != nil {
		return fmt.Errorf("postgres: posting an intervention: %w", err)
	}
	return nil
}

// SaveIntervention writes the outcome of a request.
func (r *CurrencyRepository) SaveIntervention(ctx context.Context, i application.Intervention) error {
	if _, err := r.q.Exec(ctx, `UPDATE currency_interventions SET status = $2, executed_at = $3, order_id = NULLIF($4, '')::uuid, sup_used = $5,
		refusal = NULLIF($6, '') WHERE id = $1::uuid`, i.ID, i.Status, i.ExecutedAt, i.OrderID, i.SUPUsed, i.Refusal); err != nil {
		return fmt.Errorf("postgres: saving an intervention: %w", err)
	}
	return nil
}

func (r *CurrencyRepository) interventions(ctx context.Context, sql string, args ...any) ([]application.Intervention, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing interventions: %w", err)
	}
	defer rows.Close()
	var out []application.Intervention
	for rows.Next() {
		i, err := scanIntervention(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an intervention: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// DueInterventions lists pending requests whose time has come, locked.
func (r *CurrencyRepository) DueInterventions(ctx context.Context, now time.Time, limit int) ([]application.Intervention, error) {
	return r.interventions(ctx, `SELECT `+interventionColumns+` FROM currency_interventions WHERE status = 'pending' AND execute_after <= $1
		ORDER BY execute_after, id LIMIT $2 FOR UPDATE`, now.UTC(), limit)
}

// InterventionsOf lists a money's requests, newest first.
func (r *CurrencyRepository) InterventionsOf(ctx context.Context, settlementID string, limit int) ([]application.Intervention, error) {
	return r.interventions(ctx, `SELECT `+interventionColumns+` FROM currency_interventions WHERE settlement_id = $1::uuid
		ORDER BY posted_at DESC, id LIMIT $2`, settlementID, limit)
}

// InterventionUse sums what done interventions used since.
func (r *CurrencyRepository) InterventionUse(ctx context.Context, settlementID string, since time.Time) (int64, int64, error) {
	var sup, units int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(sup_used) FILTER (WHERE side = 'buy'), 0)::bigint,
		COALESCE(SUM(units) FILTER (WHERE side = 'sell'), 0)::bigint FROM currency_interventions
		WHERE settlement_id = $1::uuid AND status = 'done' AND executed_at >= $2`, settlementID, since.UTC()).Scan(&sup, &units); err != nil {
		return 0, 0, fmt.Errorf("postgres: summing the interventions: %w", err)
	}
	return sup, units, nil
}

// HasPendingIntervention says whether the player has a request waiting.
func (r *CurrencyRepository) HasPendingIntervention(ctx context.Context, settlementID, playerID string) (bool, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM currency_interventions WHERE settlement_id = $1::uuid AND posted_by = $2::uuid AND status = 'pending'`,
		settlementID, playerID).Scan(&n); err != nil {
		return false, fmt.Errorf("postgres: looking for a pending intervention: %w", err)
	}
	return n > 0, nil
}

// StabilisationUnits are the units the head's purchases brought in net of its sales.
func (r *CurrencyRepository) StabilisationUnits(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN b.purpose = 'intervention' THEN t.quantity - t.units_fee ELSE 0 END), 0)::bigint
		- COALESCE(SUM(CASE WHEN s.purpose = 'intervention' THEN t.quantity ELSE 0 END), 0)::bigint
		FROM fx_trades t JOIN fx_orders b ON b.id = t.buy_order_id JOIN fx_orders s ON s.id = t.sell_order_id
		WHERE t.settlement_id = $1::uuid`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing the intervention stock: %w", err)
	}
	return max(n, 0), nil
}

const withdrawalColumns = `id::text, settlement_id::text, sup, requested_by::text, requested_at, execute_after, status, executed_at,
	COALESCE(ledger_transaction_id::text, ''), COALESCE(refusal, '')`

func scanWithdrawal(row pgx.Row) (application.Withdrawal, error) {
	var w application.Withdrawal
	err := row.Scan(&w.ID, &w.SettlementID, &w.SUP, &w.RequestedBy, &w.RequestedAt, &w.ExecuteAfter, &w.Status, &w.ExecutedAt, &w.TransactionID, &w.Refusal)
	w.RequestedAt, w.ExecuteAfter, w.ExecutedAt = w.RequestedAt.UTC(), w.ExecuteAfter.UTC(), utcPtr(w.ExecutedAt)
	return w, err
}

// InsertWithdrawal writes a request.
func (r *CurrencyRepository) InsertWithdrawal(ctx context.Context, w application.Withdrawal) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO currency_withdrawals (id, settlement_id, sup, requested_by, requested_at, execute_after, status)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, 'pending')`, w.ID, w.SettlementID, w.SUP, w.RequestedBy, w.RequestedAt.UTC(), w.ExecuteAfter.UTC()); err != nil {
		return fmt.Errorf("postgres: announcing a withdrawal: %w", err)
	}
	return nil
}

// SaveWithdrawal writes the outcome of a request.
func (r *CurrencyRepository) SaveWithdrawal(ctx context.Context, w application.Withdrawal) error {
	if _, err := r.q.Exec(ctx, `UPDATE currency_withdrawals SET status = $2, executed_at = $3, ledger_transaction_id = NULLIF($4, '')::uuid,
		refusal = NULLIF($5, '') WHERE id = $1::uuid`, w.ID, w.Status, w.ExecutedAt, w.TransactionID, w.Refusal); err != nil {
		return fmt.Errorf("postgres: saving a withdrawal: %w", err)
	}
	return nil
}

func (r *CurrencyRepository) withdrawals(ctx context.Context, sql string, args ...any) ([]application.Withdrawal, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing withdrawals: %w", err)
	}
	defer rows.Close()
	var out []application.Withdrawal
	for rows.Next() {
		w, err := scanWithdrawal(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a withdrawal: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DueWithdrawals lists pending withdrawals whose notice has passed, locked.
func (r *CurrencyRepository) DueWithdrawals(ctx context.Context, now time.Time, limit int) ([]application.Withdrawal, error) {
	return r.withdrawals(ctx, `SELECT `+withdrawalColumns+` FROM currency_withdrawals WHERE status = 'pending' AND execute_after <= $1
		ORDER BY execute_after, id LIMIT $2 FOR UPDATE`, now.UTC(), limit)
}

// WithdrawalsOf lists a money's requests, newest first.
func (r *CurrencyRepository) WithdrawalsOf(ctx context.Context, settlementID string, limit int) ([]application.Withdrawal, error) {
	return r.withdrawals(ctx, `SELECT `+withdrawalColumns+` FROM currency_withdrawals WHERE settlement_id = $1::uuid
		ORDER BY requested_at DESC, id LIMIT $2`, settlementID, limit)
}

// PendingWithdrawalSUP is what the pending requests ask for in all.
func (r *CurrencyRepository) PendingWithdrawalSUP(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(sup), 0)::bigint FROM currency_withdrawals WHERE settlement_id = $1::uuid AND status = 'pending'`,
		settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing the pending withdrawals: %w", err)
	}
	return n, nil
}

// AddReleased raises the counter of SUP that left the pot for good.
func (r *CurrencyRepository) AddReleased(ctx context.Context, settlementID string, sup int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE village_currency_state SET released_sup = released_sup + $2 WHERE settlement_id = $1::uuid`, settlementID, sup); err != nil {
		return fmt.Errorf("postgres: counting the SUP released from the pot: %w", err)
	}
	return nil
}

// BeginWindDown moves a chartered money to wind_down.
func (r *CurrencyRepository) BeginWindDown(ctx context.Context, settlementID, reason string, at, ends time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE village_currency_state SET status = 'wind_down', wind_down_at = $2, wind_down_ends_at = $3, wind_down_reason = $4
		WHERE settlement_id = $1::uuid AND status = 'chartered'`, settlementID, at.UTC(), ends.UTC(), reason)
	if err != nil {
		return false, fmt.Errorf("postgres: beginning a wind-down: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Retire moves a money in wind-down to retired.
func (r *CurrencyRepository) Retire(ctx context.Context, settlementID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE village_currency_state SET status = 'retired', retired_at = $2 WHERE settlement_id = $1::uuid AND status = 'wind_down'`,
		settlementID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: retiring a money: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// InWindDown lists the settlements whose money is winding down.
func (r *CurrencyRepository) InWindDown(ctx context.Context) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text FROM village_currency_state WHERE status = 'wind_down' ORDER BY settlement_id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the winding-down moneys: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: reading a winding-down money: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ClaimableUnits are the units holders other than the treasury hold.
func (r *CurrencyRepository) ClaimableUnits(ctx context.Context, settlementID, code string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'foreign_holding' AND currency = $1 AND owner_id <> $2::uuid`,
		code, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting the claimable units: %w", err)
	}
	return n, nil
}

// InsertClaim writes a claim.
func (r *CurrencyRepository) InsertClaim(ctx context.Context, c application.Claim) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO currency_claims (id, settlement_id, player_id, units, sup, pot_before, claimable_before,
		sup_transaction_id, burn_transaction_id, created_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9::uuid, $10)`,
		c.ID, c.SettlementID, c.PlayerID, c.Units, c.SUP, c.PotBefore, c.ClaimableBefore, c.SUPTransactionID, c.BurnTransactionID, c.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a claim: %w", err)
	}
	return nil
}

// ClaimsOf lists a money's claims, newest first.
func (r *CurrencyRepository) ClaimsOf(ctx context.Context, settlementID string, limit int) ([]application.Claim, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, settlement_id::text, player_id::text, units, sup, pot_before, claimable_before,
		COALESCE(sup_transaction_id::text, ''), burn_transaction_id::text, created_at FROM currency_claims
		WHERE settlement_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing claims: %w", err)
	}
	defer rows.Close()
	var out []application.Claim
	for rows.Next() {
		var c application.Claim
		if err := rows.Scan(&c.ID, &c.SettlementID, &c.PlayerID, &c.Units, &c.SUP, &c.PotBefore, &c.ClaimableBefore, &c.SUPTransactionID, &c.BurnTransactionID, &c.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a claim: %w", err)
		}
		c.At = c.At.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// MacroRows lists a money's readings, newest first.
func (r *CurrencyRepository) MacroRows(ctx context.Context, settlementID string, limit int) ([]application.MacroRow, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text, period_no, supply_units, stabilisation_units, m_sup, y_sup, x_ref_ppm, tradable_ppm,
		nontradable_ppm, price_ppm, pi_local_bps, coverage_bps, supply_growth_bps, pot_sup, basis_sup, created_at
		FROM village_macro_periods WHERE settlement_id = $1::uuid ORDER BY period_no DESC LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the macro readings: %w", err)
	}
	defer rows.Close()
	var out []application.MacroRow
	for rows.Next() {
		var m application.MacroRow
		if err := rows.Scan(&m.SettlementID, &m.PeriodNo, &m.SupplyUnits, &m.Stabilisation, &m.M, &m.Y, &m.XRefPPM, &m.Tradable, &m.NonTradable,
			&m.Price, &m.PiLocalBPS, &m.CoverageBPS, &m.SupplyGrowthBPS, &m.PotSUP, &m.BasisSUP, &m.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a macro reading: %w", err)
		}
		m.At = m.At.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecordMacro writes a period's reading, once.
func (r *CurrencyRepository) RecordMacro(ctx context.Context, m application.MacroRow) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO village_macro_periods (settlement_id, period_no, supply_units, stabilisation_units, m_sup, y_sup, x_ref_ppm,
		tradable_ppm, nontradable_ppm, price_ppm, pi_local_bps, coverage_bps, supply_growth_bps, pot_sup, basis_sup, created_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) ON CONFLICT DO NOTHING`,
		m.SettlementID, m.PeriodNo, m.SupplyUnits, m.Stabilisation, m.M, m.Y, m.XRefPPM, m.Tradable, m.NonTradable, m.Price, m.PiLocalBPS,
		m.CoverageBPS, m.SupplyGrowthBPS, m.PotSUP, m.BasisSUP, m.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a macro reading: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Output is the period's output in SUP: the treasury's wage bill, in SUP and in the money at its rate.
func (r *CurrencyRepository) Output(ctx context.Context, settlementID string, from, to time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT
		COALESCE((SELECT SUM(e.amount) FROM ledger_entries e
		           WHERE e.amount > 0 AND e.created_at >= $2 AND e.created_at < $3
		             AND e.reason IN ('settlement_wage', 'labor_wage', 'labor_wage_npc', 'teacher_wage', 'teacher_wage_npc', 'shopkeeper_wage', 'storekeeper_wage')
		             AND EXISTS (SELECT 1 FROM ledger_entries d JOIN accounts a ON a.id = d.account_id
		                          WHERE d.transaction_id = e.transaction_id AND d.amount < 0 AND a.kind = 'city_treasury' AND a.owner_id = $1::uuid)), 0)::bigint
		+ COALESCE((SELECT SUM(sup_amount) FROM local_payments WHERE settlement_id = $1::uuid AND direction = 'pay'
		             AND created_at >= $2 AND created_at < $3), 0)::bigint`,
		settlementID, from.UTC(), to.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: summing the output: %w", err)
	}
	return n, nil
}
