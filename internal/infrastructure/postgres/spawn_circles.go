package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// SpawnCircles reads the current spawn circle and the first one of a world (docs/adr/0028 section 3.2, amendment
// 2026-10-09). With lock it takes the world's cursor row FOR UPDATE (creating it on the first founding), which
// serialises the foundings of that world until the transaction ends.
func (r *SettlementRepository) SpawnCircles(ctx context.Context, worldID string, lock bool) (cur, first *application.SpawnCircle, err error) {
	if lock {
		if _, err := r.q.Exec(ctx, `INSERT INTO spawn_circle_cursor (world_id) VALUES ($1::uuid) ON CONFLICT (world_id) DO NOTHING`, worldID); err != nil {
			return nil, nil, fmt.Errorf("postgres: creating the spawn cursor: %w", err)
		}
	}
	q := `SELECT current_idx FROM spawn_circle_cursor WHERE world_id = $1::uuid`
	if lock {
		q += ` FOR UPDATE`
	}
	var idx *int
	rows, err := r.q.Query(ctx, q, worldID)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading the spawn cursor: %w", err)
	}
	if rows.Next() {
		if err := rows.Scan(&idx); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("postgres: reading the spawn cursor: %w", err)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("postgres: reading the spawn cursor: %w", err)
	}
	if idx == nil {
		return nil, nil, nil
	}
	read := func(i int) (*application.SpawnCircle, error) {
		var c application.SpawnCircle
		err := r.q.QueryRow(ctx, `SELECT idx, lat_deg, lon_deg, radius_km, capacity, taken FROM spawn_circles WHERE world_id = $1::uuid AND idx = $2`, worldID, i).
			Scan(&c.Index, &c.LatDeg, &c.LonDeg, &c.RadiusKm, &c.Capacity, &c.Taken)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading spawn circle %d: %w", i, err)
		}
		return &c, nil
	}
	if cur, err = read(*idx); err != nil {
		return nil, nil, err
	}
	if *idx == 0 {
		return cur, cur, nil
	}
	if first, err = read(0); err != nil {
		return nil, nil, err
	}
	return cur, first, nil
}

// ApplySpawnPlan writes a spawn plan's effect on a world's circles.
func (r *SettlementRepository) ApplySpawnPlan(ctx context.Context, worldID string, p application.SpawnPlanRecord, at time.Time) error {
	for _, c := range p.Opened {
		if _, err := r.q.Exec(ctx, `INSERT INTO spawn_circles (world_id, idx, lat_deg, lon_deg, radius_km, capacity, taken, opened_at)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`, worldID, c.Index, c.LatDeg, c.LonDeg, c.RadiusKm, c.Capacity, c.Taken, at); err != nil {
			return fmt.Errorf("postgres: opening spawn circle %d: %w", c.Index, err)
		}
	}
	for _, c := range p.Closed {
		if _, err := r.q.Exec(ctx, `UPDATE spawn_circles SET closed_at = $3, close_reason = $4 WHERE world_id = $1::uuid AND idx = $2 AND closed_at IS NULL`,
			worldID, c.Index, at, c.Reason); err != nil {
			return fmt.Errorf("postgres: closing spawn circle %d: %w", c.Index, err)
		}
	}
	if _, err := r.q.Exec(ctx, `UPDATE spawn_circles SET taken = taken + 1 WHERE world_id = $1::uuid AND idx = $2`, worldID, p.Used); err != nil {
		return fmt.Errorf("postgres: counting a founding in spawn circle %d: %w", p.Used, err)
	}
	if _, err := r.q.Exec(ctx, `UPDATE spawn_circle_cursor SET current_idx = $2 WHERE world_id = $1::uuid`, worldID, p.Used); err != nil {
		return fmt.Errorf("postgres: moving the spawn cursor: %w", err)
	}
	return nil
}
