package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// VillageStorageRepository is the stores' days (migration 0114).
type VillageStorageRepository struct{ q querier }

const storageDayColumns = `settlement_id::text, day, buildings, kept, wage, COALESCE(ledger_transaction_id::text, ''),
	spoiled_units, spoil_carry, at`

func scanStorageDay(row pgx.Row) (*application.VillageStorageDay, error) {
	var d application.VillageStorageDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Buildings, &d.Kept, &d.Wage, &d.LedgerTransactionID,
		&d.SpoiledUnits, &d.SpoilCarry, &d.At); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a storage day: %w", err)
	}
	return &d, nil
}

// Day returns one day's row, nil for none.
func (r *VillageStorageRepository) Day(ctx context.Context, settlementID string, day int64) (*application.VillageStorageDay, error) {
	return scanStorageDay(r.q.QueryRow(ctx,
		`SELECT `+storageDayColumns+` FROM village_storage_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
}

// Last returns the latest settled day, nil for none.
func (r *VillageStorageRepository) Last(ctx context.Context, settlementID string) (*application.VillageStorageDay, error) {
	return scanStorageDay(r.q.QueryRow(ctx,
		`SELECT `+storageDayColumns+` FROM village_storage_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
}

// RecordDay is the fence of a storage day.
func (r *VillageStorageRepository) RecordDay(ctx context.Context, d application.VillageStorageDay) (bool, error) {
	var tx any
	if d.LedgerTransactionID != "" {
		tx = d.LedgerTransactionID
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO village_storage_days (settlement_id, day, buildings, kept, wage, ledger_transaction_id, spoiled_units, spoil_carry, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8, $9) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, d.Buildings, d.Kept, d.Wage, tx, d.SpoiledUnits, d.SpoilCarry, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a storage day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
