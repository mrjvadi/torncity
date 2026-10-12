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

// MineRepository is the mine's port over migration 0153: deposits (what was taken, deltas only), surveys, the state of a minehead, the
// timed orders of builders and the hours of a building's work.
type MineRepository struct{ q querier }

var _ application.MineRepository = (*MineRepository)(nil)

// LockDeposit makes the deposit's row on first sight and locks it.
func (r *MineRepository) LockDeposit(ctx context.Context, id, resource string, reserve int64, at time.Time) (application.OreDeposit, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO ore_deposits (deposit_id, resource, reserve, taken, updated_at) VALUES ($1, $2, $3, 0, $4) ON CONFLICT DO NOTHING`, id, resource, reserve, at.UTC()); err != nil {
		return application.OreDeposit{}, fmt.Errorf("postgres: making the deposit: %w", err)
	}
	d := application.OreDeposit{ID: id}
	if err := r.q.QueryRow(ctx, `SELECT resource, taken FROM ore_deposits WHERE deposit_id = $1 FOR UPDATE`, id).Scan(&d.Resource, &d.Taken); err != nil {
		return d, fmt.Errorf("postgres: locking the deposit: %w", err)
	}
	return d, nil
}

// Deposit reads a deposit.
func (r *MineRepository) Deposit(ctx context.Context, id string) (application.OreDeposit, error) {
	d := application.OreDeposit{ID: id}
	err := r.q.QueryRow(ctx, `SELECT resource, taken FROM ore_deposits WHERE deposit_id = $1`, id).Scan(&d.Resource, &d.Taken)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("postgres: reading the deposit: %w", err)
	}
	return d, nil
}

// Take adds to what was taken.
func (r *MineRepository) Take(ctx context.Context, id string, qty int64, at time.Time) error {
	tag, err := r.q.Exec(ctx, `UPDATE ore_deposits SET taken = taken + $2, updated_at = $3 WHERE deposit_id = $1`, id, qty, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: taking ore from the deposit: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: the deposit %s has no row to take from", id)
	}
	return nil
}

// RecordExtraction writes a shift's extraction once.
func (r *MineRepository) RecordExtraction(ctx context.Context, e application.OreExtraction) (bool, error) {
	tag, err := r.q.Exec(ctx, `
		INSERT INTO ore_extractions (shift_id, settlement_id, building_id, deposit_id, item_code, quantity, depth, at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8) ON CONFLICT (shift_id) DO NOTHING`,
		e.ShiftID, e.SettlementID, e.BuildingID, e.DepositID, e.Item, e.Qty, e.Depth, e.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an extraction: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Survey reads what a settlement learned of a deposit.
func (r *MineRepository) Survey(ctx context.Context, depositID, settlementID string) (*application.DepositSurvey, error) {
	var s application.DepositSurvey
	var feeTx *string
	err := r.q.QueryRow(ctx, `SELECT deposit_id, settlement_id::text, surveyed_by::text, surveyed_at, grade_permille, reserve_estimate, fee, fee_tx::text
		FROM ore_deposit_surveys WHERE deposit_id = $1 AND settlement_id = $2::uuid`, depositID, settlementID).
		Scan(&s.DepositID, &s.SettlementID, &s.By, &s.At, &s.GradePermille, &s.ReserveEstimate, &s.Fee, &feeTx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a survey: %w", err)
	}
	if feeTx != nil {
		s.FeeTx = *feeTx
	}
	return &s, nil
}

// PutSurvey stores a survey once.
func (r *MineRepository) PutSurvey(ctx context.Context, s application.DepositSurvey) (bool, error) {
	var feeTx any
	if s.FeeTx != "" {
		feeTx = s.FeeTx
	}
	tag, err := r.q.Exec(ctx, `
		INSERT INTO ore_deposit_surveys (deposit_id, settlement_id, surveyed_by, surveyed_at, grade_permille, reserve_estimate, fee, fee_tx)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid) ON CONFLICT DO NOTHING`,
		s.DepositID, s.SettlementID, s.By, s.At.UTC(), s.GradePermille, s.ReserveEstimate, s.Fee, feeTx)
	if err != nil {
		return false, fmt.Errorf("postgres: storing a survey: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const mineStateColumns = `building_id::text, settlement_id::text, shafts, hoist, ore_bin, adit_m, pump, vent, fall_m, day_done, updated_at`

func scanMineState(row pgx.Row) (application.MineState, error) {
	var s application.MineState
	err := row.Scan(&s.BuildingID, &s.SettlementID, &s.Shafts, &s.Hoist, &s.OreBin, &s.AditM, &s.Pump, &s.Vent, &s.FallM, &s.DayDone, &s.UpdatedAt)
	return s, err
}

// State reads a minehead's modules.
func (r *MineRepository) State(ctx context.Context, buildingID string) (*application.MineState, error) {
	s, err := scanMineState(r.q.QueryRow(ctx, `SELECT `+mineStateColumns+` FROM mine_state WHERE building_id = $1::uuid`, buildingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a mine: %w", err)
	}
	return &s, nil
}

// EnsureState makes the row on first sight and locks it.
func (r *MineRepository) EnsureState(ctx context.Context, buildingID, settlementID string, at time.Time) (application.MineState, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO mine_state (building_id, settlement_id, updated_at) VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		buildingID, settlementID, at.UTC()); err != nil {
		return application.MineState{}, fmt.Errorf("postgres: making the mine: %w", err)
	}
	s, err := scanMineState(r.q.QueryRow(ctx, `SELECT `+mineStateColumns+` FROM mine_state WHERE building_id = $1::uuid FOR UPDATE`, buildingID))
	if err != nil {
		return s, fmt.Errorf("postgres: locking the mine: %w", err)
	}
	return s, nil
}

// SetState writes the modules.
func (r *MineRepository) SetState(ctx context.Context, s application.MineState) error {
	tag, err := r.q.Exec(ctx, `UPDATE mine_state SET shafts = $2, hoist = $3, ore_bin = $4, adit_m = $5, pump = $6, vent = $7, fall_m = $8, updated_at = $9 WHERE building_id = $1::uuid`,
		s.BuildingID, s.Shafts, s.Hoist, s.OreBin, s.AditM, s.Pump, s.Vent, s.FallM, s.UpdatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: writing the mine: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return application.ErrMineNotFound
	}
	return nil
}

// AdvanceDay moves the mine's day exactly once.
func (r *MineRepository) AdvanceDay(ctx context.Context, buildingID string, from, to int64) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE mine_state SET day_done = $3 WHERE building_id = $1::uuid AND day_done = $2`, buildingID, from, to)
	if err != nil {
		return false, fmt.Errorf("postgres: advancing the mine's day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Levels lists the levels of a mine.
func (r *MineRepository) Levels(ctx context.Context, buildingID string) ([]application.MineLevel, error) {
	rows, err := r.q.Query(ctx, `SELECT depth, condition_bps, water_bps FROM mine_levels WHERE building_id = $1::uuid ORDER BY depth`, buildingID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the levels: %w", err)
	}
	defer rows.Close()
	var out []application.MineLevel
	for rows.Next() {
		var l application.MineLevel
		if err := rows.Scan(&l.Depth, &l.ConditionBPS, &l.WaterBPS); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PutLevel writes a level.
func (r *MineRepository) PutLevel(ctx context.Context, buildingID string, l application.MineLevel) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO mine_levels (building_id, depth, condition_bps, water_bps) VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (building_id, depth) DO UPDATE SET condition_bps = EXCLUDED.condition_bps, water_bps = EXCLUDED.water_bps`,
		buildingID, l.Depth, l.ConditionBPS, l.WaterBPS); err != nil {
		return fmt.Errorf("postgres: writing a level: %w", err)
	}
	return nil
}

// Mines lists the states of a settlement's mineheads.
func (r *MineRepository) Mines(ctx context.Context, settlementID string) ([]application.MineState, error) {
	rows, err := r.q.Query(ctx, `SELECT `+mineStateColumns+` FROM mine_state WHERE settlement_id = $1::uuid ORDER BY building_id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the mines: %w", err)
	}
	defer rows.Close()
	var out []application.MineState
	for rows.Next() {
		s, err := scanMineState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// WorkingByRole counts a building's working shifts by staff role.
func (r *MineRepository) WorkingByRole(ctx context.Context, buildingID string) (map[string]int, error) {
	rows, err := r.q.Query(ctx, `SELECT staff_role, count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'working' GROUP BY staff_role`, buildingID)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting the crew: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			return nil, err
		}
		out[role] = n
	}
	return out, rows.Err()
}

// ShiftsSince counts a building's finished shifts of a role since an instant.
func (r *MineRepository) ShiftsSince(ctx context.Context, buildingID, role string, since time.Time) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND staff_role = $2 AND status = 'done' AND finished_at >= $3`,
		buildingID, role, since.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting the shifts of a role: %w", err)
	}
	return n, nil
}

const workColumns = `id::text, settlement_id::text, kind, ref_id::text, param, status, materials, wage, COALESCE(wage_tx::text, ''), COALESCE(ordered_by::text, ''), started_at, due_at, done_at, COALESCE(game_action_id::text, '')`

func scanBuildWork(row pgx.Row) (application.BuildWork, error) {
	var w application.BuildWork
	var param, mat []byte
	if err := row.Scan(&w.ID, &w.SettlementID, &w.Kind, &w.RefID, &param, &w.Status, &mat, &w.Wage, &w.WageTx, &w.OrderedBy, &w.StartedAt, &w.DueAt, &w.DoneAt, &w.GameActionID); err != nil {
		return w, err
	}
	if err := json.Unmarshal(param, &w.Param); err != nil {
		return w, fmt.Errorf("postgres: reading an order's detail: %w", err)
	}
	if err := json.Unmarshal(mat, &w.Materials); err != nil {
		return w, fmt.Errorf("postgres: reading an order's materials: %w", err)
	}
	return w, nil
}

// AddWork stores a timed order.
func (r *MineRepository) AddWork(ctx context.Context, w application.BuildWork) error {
	param, err := json.Marshal(nonNilAny(w.Param))
	if err != nil {
		return err
	}
	mat, err := json.Marshal(nonNilQty(w.Materials))
	if err != nil {
		return err
	}
	var wageTx, by, action any
	if w.WageTx != "" {
		wageTx = w.WageTx
	}
	if w.OrderedBy != "" {
		by = w.OrderedBy
	}
	if w.GameActionID != "" {
		action = w.GameActionID
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO build_works (id, settlement_id, kind, ref_id, param, materials, wage, wage_tx, ordered_by, started_at, due_at, game_action_id)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, $6::jsonb, $7, $8::uuid, $9::uuid, $10, $11, $12::uuid)`,
		w.ID, w.SettlementID, w.Kind, w.RefID, string(param), string(mat), w.Wage, wageTx, by, w.StartedAt.UTC(), w.DueAt.UTC(), action); err != nil {
		return fmt.Errorf("postgres: storing a works order: %w", err)
	}
	return nil
}

// Work reads an order.
func (r *MineRepository) Work(ctx context.Context, id string) (*application.BuildWork, error) {
	if !validUUID(id) {
		return nil, application.ErrWorkNotFound
	}
	w, err := scanBuildWork(r.q.QueryRow(ctx, `SELECT `+workColumns+` FROM build_works WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrWorkNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a works order: %w", err)
	}
	return &w, nil
}

// FinishWork closes an order exactly once.
func (r *MineRepository) FinishWork(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE build_works SET status = 'done', done_at = $2 WHERE id = $1::uuid AND status = 'working'`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: finishing a works order: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *MineRepository) listWorks(ctx context.Context, sql string, args ...any) ([]application.BuildWork, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing works orders: %w", err)
	}
	defer rows.Close()
	var out []application.BuildWork
	for rows.Next() {
		w, err := scanBuildWork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// OpenWorks lists the orders of a ref in progress.
func (r *MineRepository) OpenWorks(ctx context.Context, kind, refID string) ([]application.BuildWork, error) {
	return r.listWorks(ctx, `SELECT `+workColumns+` FROM build_works WHERE kind = $1 AND ref_id = $2::uuid AND status = 'working' ORDER BY started_at, id`, kind, refID)
}

// WorksOf lists a settlement's latest orders.
func (r *MineRepository) WorksOf(ctx context.Context, settlementID string, limit int) ([]application.BuildWork, error) {
	if limit <= 0 {
		limit = 20
	}
	return r.listWorks(ctx, `SELECT `+workColumns+` FROM build_works WHERE settlement_id = $1::uuid ORDER BY started_at DESC, id LIMIT $2`, settlementID, limit)
}

// AddStats adds a shift to the hour's row of a building.
func (r *MineRepository) AddStats(ctx context.Context, s application.NodeStat) error {
	prod, err := json.Marshal(nonNilQty(s.Produced))
	if err != nil {
		return err
	}
	cons, err := json.Marshal(nonNilQty(s.Consumed))
	if err != nil {
		return err
	}
	// The sums are made inside the statement, key by key, so two replicas adding to one hour never lose an addition.
	if _, err := r.q.Exec(ctx, `
		INSERT INTO node_stats (building_id, bucket, settlement_id, produced, consumed, shifts)
		VALUES ($1::uuid, $2, $3::uuid, $4::jsonb, $5::jsonb, $6)
		ON CONFLICT (building_id, bucket) DO UPDATE SET
		  produced = (SELECT COALESCE(jsonb_object_agg(k, v), '{}'::jsonb) FROM (
		      SELECT k, SUM(v::bigint) AS v FROM (
		          SELECT key AS k, value AS v FROM jsonb_each_text(node_stats.produced)
		          UNION ALL SELECT key, value FROM jsonb_each_text(EXCLUDED.produced)) u GROUP BY k) s),
		  consumed = (SELECT COALESCE(jsonb_object_agg(k, v), '{}'::jsonb) FROM (
		      SELECT k, SUM(v::bigint) AS v FROM (
		          SELECT key AS k, value AS v FROM jsonb_each_text(node_stats.consumed)
		          UNION ALL SELECT key, value FROM jsonb_each_text(EXCLUDED.consumed)) u GROUP BY k) s),
		  shifts = node_stats.shifts + EXCLUDED.shifts`,
		s.BuildingID, s.Bucket.UTC(), s.SettlementID, string(prod), string(cons), max(s.Shifts, 1)); err != nil {
		return fmt.Errorf("postgres: adding to the hour's statistics: %w", err)
	}
	return nil
}

// Stats lists a settlement's rows since an instant.
func (r *MineRepository) Stats(ctx context.Context, settlementID string, since time.Time) ([]application.NodeStat, error) {
	rows, err := r.q.Query(ctx, `SELECT building_id::text, settlement_id::text, bucket, produced, consumed, shifts FROM node_stats
		WHERE settlement_id = $1::uuid AND bucket >= $2 ORDER BY bucket, building_id`, settlementID, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the statistics: %w", err)
	}
	defer rows.Close()
	var out []application.NodeStat
	for rows.Next() {
		var s application.NodeStat
		var p, c []byte
		if err := rows.Scan(&s.BuildingID, &s.SettlementID, &s.Bucket, &p, &c, &s.Shifts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(p, &s.Produced); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(c, &s.Consumed); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func nonNilQty(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func nonNilAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
