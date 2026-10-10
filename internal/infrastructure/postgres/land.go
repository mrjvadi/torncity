package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/land"
)

// LandRepository is the land deltas and saplings (migration 0143), bound to one transaction.
type LandRepository struct{ q querier }

var _ application.LandRepository = (*LandRepository)(nil)

func (r *LandRepository) Rows(ctx context.Context, settlementID string) ([]land.Delta, error) {
	rows, err := r.q.Query(ctx, `SELECT lot_x, lot_y, trees_cut, rocks_cut, rock_work, regrow_anchor, clear_trees, clear_rocks, woodlot
		FROM settlement_land WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the land: %w", err)
	}
	defer rows.Close()
	var out []land.Delta
	for rows.Next() {
		var d land.Delta
		if err := rows.Scan(&d.X, &d.Y, &d.TreesCut, &d.RocksCut, &d.RockWork, &d.RegrowAnchor, &d.ClearTrees, &d.ClearRocks, &d.Woodlot); err != nil {
			return nil, fmt.Errorf("postgres: scanning the land: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *LandRepository) Saplings(ctx context.Context, settlementID string) ([]land.Sapling, error) {
	rows, err := r.q.Query(ctx, `SELECT lot_x, lot_y, planted_at, ready_at FROM settlement_saplings WHERE settlement_id = $1::uuid ORDER BY planted_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the saplings: %w", err)
	}
	defer rows.Close()
	var out []land.Sapling
	for rows.Next() {
		var s land.Sapling
		if err := rows.Scan(&s.X, &s.Y, &s.PlantedAt, &s.ReadyAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning the saplings: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *LandRepository) Lock(ctx context.Context, settlementID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('settlement_land:' || $1::text, 0))`, settlementID); err != nil {
		return fmt.Errorf("postgres: locking the land: %w", err)
	}
	return nil
}

func (r *LandRepository) Apply(ctx context.Context, settlementID string, p land.Pos, c application.LandChange, at time.Time) error {
	var anchor any
	if c.Anchor != nil {
		anchor = c.Anchor.UTC()
	}
	_, err := r.q.Exec(ctx, `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, trees_cut, rocks_cut, rock_work, regrow_anchor, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, CASE WHEN $6 THEN $7 ELSE 0 END, $8, $9)
		ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET
		    trees_cut = settlement_land.trees_cut + EXCLUDED.trees_cut,
		    rocks_cut = settlement_land.rocks_cut + EXCLUDED.rocks_cut,
		    rock_work = CASE WHEN $6 THEN $7 ELSE settlement_land.rock_work END,
		    regrow_anchor = CASE WHEN $4 > 0 THEN COALESCE($8, settlement_land.regrow_anchor) ELSE settlement_land.regrow_anchor END,
		    updated_at = $9`,
		settlementID, p.X, p.Y, c.Trees, c.Rocks, c.SetRockWork, c.RockWork, anchor, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: changing the land: %w", err)
	}
	return nil
}

func (r *LandRepository) Plant(ctx context.Context, settlementID string, p land.Pos, id, shiftID string, plantedAt, readyAt time.Time) error {
	var shift any
	if shiftID != "" {
		shift = shiftID
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO settlement_saplings (id, settlement_id, lot_x, lot_y, planted_at, ready_at, shift_id)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid)`, id, settlementID, p.X, p.Y, plantedAt.UTC(), readyAt.UTC(), shift); err != nil {
		return fmt.Errorf("postgres: planting a sapling: %w", err)
	}
	// planting changes the land: the layout version moves
	_, err := r.q.Exec(ctx, `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, updated_at) VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET updated_at = $4`, settlementID, p.X, p.Y, plantedAt.UTC())
	return err
}

func (r *LandRepository) Order(ctx context.Context, settlementID string, p land.Pos, trees, rocks bool, by string, at time.Time) error {
	var who any
	if by != "" {
		who = by
	}
	_, err := r.q.Exec(ctx, `INSERT INTO settlement_land (settlement_id, lot_x, lot_y, clear_trees, clear_rocks, ordered_by, ordered_at, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $7)
		ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET clear_trees = $4, clear_rocks = $5, ordered_by = $6::uuid, ordered_at = $7, updated_at = $7`,
		settlementID, p.X, p.Y, trees, rocks, who, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: ordering a clearing: %w", err)
	}
	return nil
}

// NewLandReader reads the land outside a transaction, for the client API.
func NewLandReader(p *Pool) *LandRepository { return &LandRepository{q: p.shared()} }
