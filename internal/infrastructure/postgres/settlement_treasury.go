package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// SettlementTreasuryRepository records the village treasury's faucets
// (migration 0052_village_treasury).
type SettlementTreasuryRepository struct{ q querier }

var _ application.SettlementTreasuryRepository = (*SettlementTreasuryRepository)(nil)

// RecordGrant inserts the settlement's grant row; the primary key makes a
// second one a no-op that reports false.
func (r *SettlementTreasuryRepository) RecordGrant(ctx context.Context, settlementID string, amount int64,
	ledgerTransactionID, source, grantedBy string, at time.Time,
) (bool, error) {
	tag, err := r.q.Exec(ctx, `
		INSERT INTO settlement_grants (settlement_id, amount, ledger_transaction_id, source, granted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (settlement_id) DO NOTHING`,
		settlementID, amount, ledgerTransactionID, source, grantedBy, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording the settlement grant: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordDonation inserts one donation row.
func (r *SettlementTreasuryRepository) RecordDonation(ctx context.Context, d application.SettlementDonation) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_donations (id, settlement_id, player_id, amount, ledger_transaction_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		d.ID, d.SettlementID, d.PlayerID, d.Amount, d.LedgerTransactionID, d.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording the settlement donation: %w", err)
	}
	return nil
}

// RecordTopup inserts one operator top-up row.
func (r *SettlementTreasuryRepository) RecordTopup(ctx context.Context, id, settlementID string, amount int64,
	ledgerTransactionID, grantedBy, reason string, at time.Time,
) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_topups (id, settlement_id, amount, ledger_transaction_id, granted_by, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, settlementID, amount, ledgerTransactionID, grantedBy, reason, at.UTC()); err != nil {
		return fmt.Errorf("postgres: recording the settlement top-up: %w", err)
	}
	return nil
}

// FoundedWithoutGrant lists the founded settlements with no grant row.
func (r *SettlementTreasuryRepository) FoundedWithoutGrant(ctx context.Context) ([]string, error) {
	rows, err := r.q.Query(ctx, `
		SELECT c.id::text FROM cities c
		 WHERE c.origin = 'founded'
		   AND NOT EXISTS (SELECT 1 FROM settlement_grants g WHERE g.settlement_id = c.id)
		 ORDER BY c.founded_at, c.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing settlements without a grant: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LevyTaken is, per founded settlement, what the national levy took from its treasury.
func (r *SettlementTreasuryRepository) LevyTaken(ctx context.Context) (map[string]int64, error) {
	rows, err := r.q.Query(ctx, `
		SELECT c.id::text, SUM(-e.amount)::bigint
		  FROM ledger_entries e
		  JOIN accounts a ON a.id = e.account_id AND a.kind = 'city_treasury'
		  JOIN cities c ON c.id = a.owner_id AND c.origin = 'founded'
		 WHERE e.reason = 'national_levy' AND e.amount < 0
		 GROUP BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: summing the national levy: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("postgres: scanning the national levy: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// LevyRefunded lists the settlements already refunded.
func (r *SettlementTreasuryRepository) LevyRefunded(ctx context.Context) (map[string]bool, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text FROM levy_refunds`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing levy refunds: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning levy refunds: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RecordLevyRefund inserts the settlement's refund row; the primary key makes a second one a no-op that reports false.
func (r *SettlementTreasuryRepository) RecordLevyRefund(ctx context.Context, x application.LevyRefund) (bool, error) {
	tag, err := r.q.Exec(ctx, `
		INSERT INTO levy_refunds (settlement_id, country_id, levy, from_state_treasury, from_defence_fund, from_source,
		                          ledger_transaction_id, reason, operator, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9, $10)
		ON CONFLICT (settlement_id) DO NOTHING`,
		x.SettlementID, x.CountryID, x.Levy, x.FromStateTreasury, x.FromDefenceFund, x.FromSource, x.LedgerTransactionID, x.Reason, x.Operator, x.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a levy refund: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
