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
	if s.Board == nil {
		s.Board = map[string]int64{}
	}
	board, err := json.Marshal(s.Board)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `
		INSERT INTO settlement_shifts
		       (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed, game_action_id, started_at, finish_at, job_id, worker_kind,
		        meal_points, fed, output_bps, payer_kind, payer_id, board)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, 'working', $5, 0, $6::jsonb, $7::jsonb, $8, $9, $10, NULLIF($11, '')::uuid, $12, $13, $14, $15,
		        COALESCE(NULLIF($16, ''), 'settlement'), NULLIF($17, '')::uuid, $18::jsonb)`,
		s.ID, s.SettlementID, s.BuildingID, s.PlayerID, s.Wage, string(produced), string(consumed), s.GameActionID,
		s.StartedAt.UTC(), s.FinishAt.UTC(), s.JobID, workerKindOf(s), s.MealPoints, s.Fed, outputBPSOf(s), s.PayerKind, s.PayerID, string(board))
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
	kind, COALESCE(job_id::text, ''), worker_kind, work_points, payer_kind, COALESCE(payer_id::text, ''), fee,
	meal_points, fed, output_bps, condition_gain`

func scanShift(row pgx.Row) (application.SettlementShift, error) {
	var (
		s                  application.SettlementShift
		produced, consumed []byte
	)
	if err := row.Scan(&s.ID, &s.SettlementID, &s.BuildingID, &s.PlayerID, &s.Status, &s.Wage, &s.WagePaid,
		&produced, &consumed, &s.GameActionID, &s.StartedAt, &s.FinishAt, &s.FinishedAt,
		&s.Kind, &s.JobID, &s.WorkerKind, &s.WorkPoints, &s.PayerKind, &s.PayerID, &s.Fee,
		&s.MealPoints, &s.Fed, &s.OutputBPS, &s.ConditionGain); err != nil {
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

// workerKindOf is the worker of a production shift: a player unless the shift says an NPC.
func workerKindOf(s application.SettlementShift) string {
	if s.WorkerKind == application.LaborWorkerNPC {
		return application.LaborWorkerNPC
	}
	return application.LaborWorkerPlayer
}

// outputBPSOf is the productivity a shift is stored with: the full base unless one was set.
func outputBPSOf(s application.SettlementShift) int64 {
	if s.OutputBPS <= 0 {
		return 10_000
	}
	return s.OutputBPS
}

// Pot is the kitchen's uneaten food points, the row made on first sight and locked.
func (r *SettlementTreasuryRepository) Pot(ctx context.Context, settlementID string) (int64, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO settlement_kitchen (settlement_id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, settlementID); err != nil {
		return 0, fmt.Errorf("postgres: making the kitchen: %w", err)
	}
	var pot int64
	if err := r.q.QueryRow(ctx, `SELECT pot FROM settlement_kitchen WHERE settlement_id = $1::uuid FOR UPDATE`, settlementID).Scan(&pot); err != nil {
		return 0, fmt.Errorf("postgres: reading the kitchen: %w", err)
	}
	return pot, nil
}

// Eat opens the units into the pot and takes the meal out of it.
func (r *SettlementTreasuryRepository) Eat(ctx context.Context, settlementID, shiftID string, points int64, openings []application.MealOpening, at time.Time) error {
	var opened int64
	for _, o := range openings {
		if _, err := r.q.Exec(ctx, `INSERT INTO settlement_meals (id, settlement_id, shift_id, item_code, units, points_each, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7)`, o.ID, settlementID, shiftID, o.Item, o.Units, o.PointsEach, at.UTC()); err != nil {
			return fmt.Errorf("postgres: recording a meal opening: %w", err)
		}
		opened += o.Units * o.PointsEach
	}
	if _, err := r.q.Exec(ctx, `UPDATE settlement_kitchen SET opened_points = opened_points + $2, eaten_points = eaten_points + $3,
		pot = pot + $2 - $3 WHERE settlement_id = $1::uuid`, settlementID, opened, points); err != nil {
		return fmt.Errorf("postgres: eating from the kitchen: %w", err)
	}
	return nil
}

// Carry reads a workplace's undelivered fractions under its row lock.
func (r *SettlementTreasuryRepository) Carry(ctx context.Context, buildingID string) (map[string]int64, error) {
	var raw []byte
	if err := r.q.QueryRow(ctx, `SELECT carry FROM settlement_buildings WHERE id = $1::uuid FOR UPDATE`, buildingID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrBuildingNotFound
		}
		return nil, fmt.Errorf("postgres: reading a carry: %w", err)
	}
	out := map[string]int64{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("postgres: reading a carry: %w", err)
	}
	return out, nil
}

// SetCarry writes a workplace's undelivered fractions.
func (r *SettlementTreasuryRepository) SetCarry(ctx context.Context, buildingID string, carry map[string]int64) error {
	raw, err := json.Marshal(carry)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx, `UPDATE settlement_buildings SET carry = $2::jsonb WHERE id = $1::uuid`, buildingID, string(raw)); err != nil {
		return fmt.Errorf("postgres: writing a carry: %w", err)
	}
	return nil
}

// FinishPrivateShift ends a working shift of a privately owned workplace, exactly once.
func (r *SettlementTreasuryRepository) FinishPrivateShift(ctx context.Context, id string, produced map[string]int64, wagePaid, fee int64, ledgerTransactionID string, at time.Time) (bool, error) {
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
		   SET status = 'done', wage_paid = $2, fee = $3, ledger_transaction_id = $4::uuid, finished_at = $5, produced = $6::jsonb
		 WHERE id = $1::uuid AND status = 'working'`,
		id, wagePaid, fee, ledgerTx, at.UTC(), string(made))
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a private shift: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PrivateTakings sums the finished shifts of a citizen's workplace since an instant.
func (r *SettlementTreasuryRepository) PrivateTakings(ctx context.Context, buildingID string, since time.Time) (application.PrivateTakings, error) {
	out := application.PrivateTakings{Produced: map[string]int64{}}
	if err := r.q.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(wage_paid), 0)::bigint, COALESCE(SUM(fee), 0)::bigint
		FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done' AND kind = 'production' AND payer_kind = 'player' AND finished_at >= $2`,
		buildingID, since.UTC()).Scan(&out.Shifts, &out.Wages, &out.Levy); err != nil {
		return out, fmt.Errorf("postgres: summing a private workplace: %w", err)
	}
	rows, err := r.q.Query(ctx, `SELECT v.key, SUM(v.value::bigint)::bigint FROM settlement_shifts s, jsonb_each_text(s.produced) v
		WHERE s.building_id = $1::uuid AND s.status = 'done' AND s.kind = 'production' AND s.payer_kind = 'player' AND s.finished_at >= $2 GROUP BY v.key`,
		buildingID, since.UTC())
	if err != nil {
		return out, fmt.Errorf("postgres: summing what a private workplace made: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return out, err
		}
		out.Produced[k] = n
	}
	return out, rows.Err()
}
