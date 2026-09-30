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
