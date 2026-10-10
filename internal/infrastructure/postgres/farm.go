package postgres

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/farm"
)

// FarmRepository is the crops and the mill statute (migration 0144), bound to one transaction.
type FarmRepository struct{ q querier }

var _ application.FarmRepository = (*FarmRepository)(nil)

// NewFarmReader reads the crops outside a transaction, for the client API.
func NewFarmReader(p *Pool) *FarmRepository { return &FarmRepository{q: p.shared()} }

const farmCols = `id, settlement_id, building_id, rainfed, soil_bps, COALESCE(ordered_by::text, ''), ordered_at, sow_started, grow_from,
	tended, water_sum, water_n, harvest_started, yield_total, seed_spent, closed_at, result`

type rowScanner interface{ Scan(dest ...any) error }

func scanCycle(r rowScanner) (farm.Cycle, error) {
	var c farm.Cycle
	err := r.Scan(&c.ID, &c.SettlementID, &c.BuildingID, &c.Rainfed, &c.SoilBPS, &c.OrderedBy, &c.OrderedAt, &c.SowStarted, &c.GrowFrom,
		&c.Tended, &c.WaterSum, &c.WaterN, &c.HarvestStarted, &c.YieldTotal, &c.SeedSpent, &c.ClosedAt, &c.Result)
	return c, err
}

func (r *FarmRepository) Lock(ctx context.Context, buildingID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('farm:' || $1::text, 0))`, buildingID); err != nil {
		return fmt.Errorf("postgres: locking the farm: %w", err)
	}
	return nil
}

func (r *FarmRepository) one(ctx context.Context, query string, arg any) (*farm.Cycle, error) {
	c, err := scanCycle(r.q.QueryRow(ctx, query, arg))
	if stderrors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the crop: %w", err)
	}
	return &c, nil
}

func (r *FarmRepository) Open(ctx context.Context, buildingID string) (*farm.Cycle, error) {
	return r.one(ctx, `SELECT `+farmCols+` FROM farm_cycles WHERE building_id = $1::uuid AND closed_at IS NULL`, buildingID)
}

func (r *FarmRepository) Latest(ctx context.Context, buildingID string) (*farm.Cycle, error) {
	return r.one(ctx, `SELECT `+farmCols+` FROM farm_cycles WHERE building_id = $1::uuid ORDER BY ordered_at DESC, id DESC LIMIT 1`, buildingID)
}

func (r *FarmRepository) Insert(ctx context.Context, c farm.Cycle) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO farm_cycles (id, settlement_id, building_id, rainfed, soil_bps, ordered_by, ordered_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, NULLIF($6, '')::uuid, $7)`,
		c.ID, c.SettlementID, c.BuildingID, c.Rainfed, c.SoilBPS, c.OrderedBy, c.OrderedAt); err != nil {
		return fmt.Errorf("postgres: sowing the crop: %w", err)
	}
	return nil
}

func (r *FarmRepository) Save(ctx context.Context, c farm.Cycle) error {
	if _, err := r.q.Exec(ctx, `UPDATE farm_cycles SET sow_started = $2, grow_from = $3, tended = $4, water_sum = $5, water_n = $6,
		harvest_started = $7, yield_total = $8, seed_spent = $9 WHERE id = $1::uuid`,
		c.ID, c.SowStarted, c.GrowFrom, c.Tended, c.WaterSum, c.WaterN, c.HarvestStarted, c.YieldTotal, c.SeedSpent); err != nil {
		return fmt.Errorf("postgres: saving the crop: %w", err)
	}
	return nil
}

func (r *FarmRepository) Close(ctx context.Context, id string, at time.Time, result string) error {
	if _, err := r.q.Exec(ctx, `UPDATE farm_cycles SET closed_at = $2, result = $3 WHERE id = $1::uuid AND closed_at IS NULL`, id, at, result); err != nil {
		return fmt.Errorf("postgres: closing the crop: %w", err)
	}
	return nil
}

func (r *FarmRepository) OpenIn(ctx context.Context, settlementID string) ([]farm.Cycle, error) {
	rows, err := r.q.Query(ctx, `SELECT `+farmCols+` FROM farm_cycles WHERE settlement_id = $1::uuid AND closed_at IS NULL ORDER BY building_id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the crops: %w", err)
	}
	defer rows.Close()
	var out []farm.Cycle
	for rows.Next() {
		c, err := scanCycle(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning the crops: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *FarmRepository) MillToll(ctx context.Context, settlementID string) (int64, bool, error) {
	var bps int64
	err := r.q.QueryRow(ctx, `SELECT toll_bps FROM settlement_mill_policy WHERE settlement_id = $1::uuid`, settlementID).Scan(&bps)
	if stderrors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres: reading the miller's toll: %w", err)
	}
	return bps, true, nil
}

func (r *FarmRepository) SetMillToll(ctx context.Context, settlementID string, bps int64, by string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO settlement_mill_policy (settlement_id, toll_bps, set_by, set_at) VALUES ($1::uuid, $2, NULLIF($3, '')::uuid, $4)
		ON CONFLICT (settlement_id) DO UPDATE SET toll_bps = $2, set_by = NULLIF($3, '')::uuid, set_at = $4`, settlementID, bps, by, at); err != nil {
		return fmt.Errorf("postgres: setting the miller's toll: %w", err)
	}
	return nil
}
