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

// The village economy's rows (migration 0055_village_economy): materials the
// village bought from Support, and shifts its residents work. It is part of
// the same repository as the treasury's faucets.

const settlementShiftsOneWorkingIdx = "settlement_shifts_one_working_idx"

var _ application.SettlementEconomyRepository = (*SettlementTreasuryRepository)(nil)

// RecordMaterialPurchase inserts one purchase row.
func (r *SettlementTreasuryRepository) RecordMaterialPurchase(ctx context.Context, p application.SettlementMaterialPurchase) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_material_purchases
		       (id, settlement_id, item_code, quantity, unit_price, total, ledger_transaction_id, bought_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		p.ID, p.SettlementID, p.Item, p.Quantity, p.UnitPrice, p.Total, p.LedgerTransactionID, p.BoughtBy, p.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: recording the material purchase: %w", err)
	}
	return nil
}

// StartShift inserts a working shift under the building's row lock.
func (r *SettlementTreasuryRepository) StartShift(ctx context.Context, s application.SettlementShift, workers int) error {
	// The lock is the building's own row: shifts of one workplace queue here,
	// no other row is contended.
	var locked string
	if err := r.q.QueryRow(ctx, `SELECT id::text FROM settlement_buildings WHERE id = $1::uuid FOR UPDATE`, s.BuildingID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrBuildingNotFound
		}
		return fmt.Errorf("postgres: locking the workplace: %w", err)
	}
	var running int
	if err := r.q.QueryRow(ctx,
		`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'working'`, s.BuildingID).Scan(&running); err != nil {
		return fmt.Errorf("postgres: counting the workplace's shifts: %w", err)
	}
	if running >= workers {
		return application.ErrWorkplaceFull
	}
	produced, err := json.Marshal(s.Produced)
	if err != nil {
		return err
	}
	consumed, err := json.Marshal(s.Consumed)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `
		INSERT INTO settlement_shifts
		       (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed, game_action_id, started_at, finish_at)
		VALUES ($1, $2, $3, $4, 'working', $5, 0, $6::jsonb, $7::jsonb, $8, $9, $10)`,
		s.ID, s.SettlementID, s.BuildingID, s.PlayerID, s.Wage, string(produced), string(consumed), s.GameActionID,
		s.StartedAt.UTC(), s.FinishAt.UTC())
	if violates(err, sqlstateUniqueViolation, settlementShiftsOneWorkingIdx) {
		return application.ErrAlreadyWorking
	}
	if err != nil {
		return fmt.Errorf("postgres: starting a shift: %w", err)
	}
	return nil
}

const shiftColumns = `id::text, settlement_id::text, building_id::text, COALESCE(player_id::text, ''), status, wage, wage_paid,
	produced, consumed, game_action_id::text, started_at, finish_at, finished_at,
	kind, COALESCE(job_id::text, ''), worker_kind, work_points, payer_kind, COALESCE(payer_id::text, ''), fee`

func scanShift(row pgx.Row) (application.SettlementShift, error) {
	var (
		s                  application.SettlementShift
		produced, consumed []byte
	)
	if err := row.Scan(&s.ID, &s.SettlementID, &s.BuildingID, &s.PlayerID, &s.Status, &s.Wage, &s.WagePaid,
		&produced, &consumed, &s.GameActionID, &s.StartedAt, &s.FinishAt, &s.FinishedAt,
		&s.Kind, &s.JobID, &s.WorkerKind, &s.WorkPoints, &s.PayerKind, &s.PayerID, &s.Fee); err != nil {
		return s, err
	}
	if err := json.Unmarshal(produced, &s.Produced); err != nil {
		return s, fmt.Errorf("postgres: reading a shift's output: %w", err)
	}
	if err := json.Unmarshal(consumed, &s.Consumed); err != nil {
		return s, fmt.Errorf("postgres: reading a shift's input: %w", err)
	}
	return s, nil
}

// Shift returns one shift.
func (r *SettlementTreasuryRepository) Shift(ctx context.Context, id string) (*application.SettlementShift, error) {
	s, err := scanShift(r.q.QueryRow(ctx, `SELECT `+shiftColumns+` FROM settlement_shifts WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrShiftNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a shift: %w", err)
	}
	return &s, nil
}

// FinishShift ends a working shift, exactly once.
func (r *SettlementTreasuryRepository) FinishShift(ctx context.Context, id string, produced map[string]int64, wagePaid int64, ledgerTransactionID string, at time.Time) (bool, error) {
	var ledgerTx any
	if ledgerTransactionID != "" {
		ledgerTx = ledgerTransactionID
	}
	made, err := json.Marshal(produced)
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx, `
		UPDATE settlement_shifts
		   SET status = 'done', wage_paid = $2, ledger_transaction_id = $3::uuid, finished_at = $4, produced = $5::jsonb
		 WHERE id = $1::uuid AND status = 'working'`,
		id, wagePaid, ledgerTx, at.UTC(), string(made))
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a shift: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// WorkingShifts lists a settlement's shifts in progress.
func (r *SettlementTreasuryRepository) WorkingShifts(ctx context.Context, settlementID string) ([]application.SettlementShift, error) {
	rows, err := r.q.Query(ctx, `SELECT `+shiftColumns+`
		FROM settlement_shifts WHERE settlement_id = $1::uuid AND status = 'working' ORDER BY started_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing shifts: %w", err)
	}
	defer rows.Close()
	var out []application.SettlementShift
	for rows.Next() {
		s, err := scanShift(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PlayerShift is the player's shift in progress, or nil.
func (r *SettlementTreasuryRepository) PlayerShift(ctx context.Context, playerID string) (*application.SettlementShift, error) {
	s, err := scanShift(r.q.QueryRow(ctx, `SELECT `+shiftColumns+`
		FROM settlement_shifts WHERE player_id = $1::uuid AND status = 'working'`, playerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the player's shift: %w", err)
	}
	return &s, nil
}
