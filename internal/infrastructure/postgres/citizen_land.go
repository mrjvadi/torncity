package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// The stored road plans (migration 0110_roads_open_land, docs/adr/0044): the
// plans a head draws out of the first grid, their cells, and the lots they
// open. Every writer holds the settlement's land lock (LockLots) first.

var _ application.LandRoadRepository = (*CitizenRepository)(nil)

// Plans lists a settlement's live plans, oldest first.
func (r *CitizenRepository) Plans(ctx context.Context, settlementID string) ([]application.RoadPlanRow, error) {
	rows, err := r.q.Query(ctx, `
		SELECT id::text, settlement_id::text, drawn_by::text, class, surface, lots, crossing_lots, climb_m, length_m,
		       from_x, from_y, to_x, to_y, created_at, cancelled_at
		  FROM settlement_road_plans
		 WHERE settlement_id = $1::uuid AND cancelled_at IS NULL
		 ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing road plans: %w", err)
	}
	defer rows.Close()
	var out []application.RoadPlanRow
	for rows.Next() {
		var p application.RoadPlanRow
		var cancelled sql.NullTime
		if err := rows.Scan(&p.ID, &p.SettlementID, &p.DrawnBy, &p.Class, &p.Surface, &p.Lots, &p.CrossingLots, &p.ClimbM,
			&p.LengthM, &p.FromX, &p.FromY, &p.ToX, &p.ToY, &p.CreatedAt, &cancelled); err != nil {
			return nil, fmt.Errorf("postgres: scanning a road plan: %w", err)
		}
		if cancelled.Valid {
			t := cancelled.Time
			p.CancelledAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Cells lists the cells of the settlement's live plans, plan by plan in order.
func (r *CitizenRepository) Cells(ctx context.Context, settlementID string) ([]application.RoadCellRow, error) {
	rows, err := r.q.Query(ctx, `
		SELECT c.settlement_id::text, c.lot_x, c.lot_y, c.plan_id::text, c.seq, c.parent_x, c.parent_y, c.water,
		       c.elevation_m, c.tile_face, c.tile_gx, c.tile_gy, c.built_at
		  FROM settlement_road_cells c
		  JOIN settlement_road_plans p ON p.id = c.plan_id AND p.cancelled_at IS NULL
		 WHERE c.settlement_id = $1::uuid
		 ORDER BY p.created_at, c.plan_id, c.seq`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing road cells: %w", err)
	}
	defer rows.Close()
	var out []application.RoadCellRow
	for rows.Next() {
		var c application.RoadCellRow
		var px, py sql.NullInt32
		var built sql.NullTime
		var face int16
		if err := rows.Scan(&c.SettlementID, &c.X, &c.Y, &c.PlanID, &c.Seq, &px, &py, &c.Water, &c.ElevationM,
			&face, &c.Tile.GX, &c.Tile.GY, &built); err != nil {
			return nil, fmt.Errorf("postgres: scanning a road cell: %w", err)
		}
		c.Tile.Face = int(face)
		if px.Valid {
			c.HasParent, c.ParentX, c.ParentY = true, int(px.Int32), int(py.Int32)
		}
		if built.Valid {
			t := built.Time
			c.BuiltAt = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OpenLots lists the lots the settlement's roads opened.
func (r *CitizenRepository) OpenLots(ctx context.Context, settlementID string) ([]application.OpenLotRow, error) {
	rows, err := r.q.Query(ctx, `
		SELECT settlement_id::text, lot_x, lot_y, plan_id::text, serves_x, serves_y, dist, buildable, reason,
		       height_m, slope_m, biome, water, tags, tile_face, tile_gx, tile_gy
		  FROM settlement_open_lots WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing open lots: %w", err)
	}
	defer rows.Close()
	var out []application.OpenLotRow
	for rows.Next() {
		var o application.OpenLotRow
		var dist, face int16
		if err := rows.Scan(&o.SettlementID, &o.X, &o.Y, &o.PlanID, &o.ServesX, &o.ServesY, &dist, &o.Buildable, &o.Reason,
			&o.HeightM, &o.SlopeM, &o.Biome, &o.Water, &o.Tags, &face, &o.Tile.GX, &o.Tile.GY); err != nil {
			return nil, fmt.Errorf("postgres: scanning an open lot: %w", err)
		}
		o.Dist, o.Tile.Face = int(dist), int(face)
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertPlan stores a drawn road with its cells and open lots.
func (r *CitizenRepository) InsertPlan(ctx context.Context, p application.RoadPlanRow, cells []application.RoadCellRow, open []application.OpenLotRow) error {
	if _, err := r.q.Exec(ctx, `
		INSERT INTO settlement_road_plans (id, settlement_id, drawn_by, class, surface, lots, crossing_lots, climb_m, length_m,
		                                   from_x, from_y, to_x, to_y, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		p.ID, p.SettlementID, p.DrawnBy, p.Class, p.Surface, p.Lots, p.CrossingLots, p.ClimbM, p.LengthM,
		p.FromX, p.FromY, p.ToX, p.ToY, p.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: storing the road plan: %w", err)
	}
	if len(cells) > 0 {
		n := len(cells)
		xs, ys, seqs, pxs, pys := make([]int32, n), make([]int32, n), make([]int32, n), make([]*int32, n), make([]*int32, n)
		water, faces, gxs, gys := make([]int16, n), make([]int16, n), make([]int32, n), make([]int32, n)
		elev := make([]float64, n)
		for i, c := range cells {
			xs[i], ys[i], seqs[i] = int32(c.X), int32(c.Y), int32(c.Seq)
			if c.HasParent {
				px, py := int32(c.ParentX), int32(c.ParentY)
				pxs[i], pys[i] = &px, &py
			}
			water[i], elev[i] = int16(c.Water), c.ElevationM
			faces[i], gxs[i], gys[i] = int16(c.Tile.Face), c.Tile.GX, c.Tile.GY
		}
		if _, err := r.q.Exec(ctx, `
			INSERT INTO settlement_road_cells (settlement_id, lot_x, lot_y, plan_id, seq, parent_x, parent_y, water, elevation_m,
			                                   tile_face, tile_gx, tile_gy)
			SELECT $1::uuid, a.x, a.y, $2::uuid, a.seq, a.px, a.py, a.water, a.elev, a.face, a.gx, a.gy
			  FROM unnest($3::int[], $4::int[], $5::int[], $6::int[], $7::int[], $8::smallint[], $9::float8[], $10::smallint[], $11::int[], $12::int[])
			       AS a(x, y, seq, px, py, water, elev, face, gx, gy)
			ORDER BY a.seq`,
			p.SettlementID, p.ID, xs, ys, seqs, pxs, pys, water, elev, faces, gxs, gys); err != nil {
			return fmt.Errorf("postgres: storing the road cells: %w", err)
		}
		// a lot that became road is no longer a lot on offer
		if _, err := r.q.Exec(ctx, `
			DELETE FROM settlement_open_lots o USING settlement_road_cells c
			 WHERE c.plan_id = $1::uuid AND o.settlement_id = c.settlement_id AND o.lot_x = c.lot_x AND o.lot_y = c.lot_y`, p.ID); err != nil {
			return fmt.Errorf("postgres: closing the lots under a new road: %w", err)
		}
	}
	return r.upsertOpen(ctx, open, true)
}

// upsertOpen writes open lots; keep says an existing row stays as it is (a new
// plan never takes a lot from an older one), else the row is replaced.
func (r *CitizenRepository) upsertOpen(ctx context.Context, open []application.OpenLotRow, keep bool) error {
	if len(open) == 0 {
		return nil
	}
	const chunk = 2000
	for from := 0; from < len(open); from += chunk {
		to := from + chunk
		if to > len(open) {
			to = len(open)
		}
		part := open[from:to]
		n := len(part)
		xs, ys, sxs, sys, dist := make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n)
		build := make([]bool, n)
		reason, biome, water, tags, plan := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		h, s := make([]float64, n), make([]float64, n)
		faces, gxs, gys := make([]int32, n), make([]int32, n), make([]int32, n)
		for i, o := range part {
			xs[i], ys[i], sxs[i], sys[i], dist[i] = int32(o.X), int32(o.Y), int32(o.ServesX), int32(o.ServesY), int32(o.Dist)
			build[i], reason[i], biome[i], water[i] = o.Buildable, o.Reason, o.Biome, o.Water
			tags[i], plan[i] = strings.Join(o.Tags, ","), o.PlanID
			h[i], s[i] = o.HeightM, o.SlopeM
			faces[i], gxs[i], gys[i] = int32(o.Tile.Face), o.Tile.GX, o.Tile.GY
		}
		conflict := `ON CONFLICT (settlement_id, lot_x, lot_y) DO NOTHING`
		if !keep {
			conflict = `ON CONFLICT (settlement_id, lot_x, lot_y) DO UPDATE SET plan_id = EXCLUDED.plan_id, serves_x = EXCLUDED.serves_x,
				serves_y = EXCLUDED.serves_y, dist = EXCLUDED.dist, buildable = EXCLUDED.buildable, reason = EXCLUDED.reason,
				height_m = EXCLUDED.height_m, slope_m = EXCLUDED.slope_m, biome = EXCLUDED.biome, water = EXCLUDED.water,
				tags = EXCLUDED.tags`
		}
		if _, err := r.q.Exec(ctx, `
			INSERT INTO settlement_open_lots (settlement_id, lot_x, lot_y, plan_id, serves_x, serves_y, dist, buildable, reason,
			                                  height_m, slope_m, biome, water, tags, tile_face, tile_gx, tile_gy)
			SELECT $1::uuid, a.x, a.y, a.plan::uuid, a.sx, a.sy, a.dist, a.build, a.reason, a.h, a.s, a.biome, a.water,
			       CASE WHEN a.tags = '' THEN ARRAY[]::text[] ELSE string_to_array(a.tags, ',') END, a.face, a.gx, a.gy
			  FROM unnest($2::int[], $3::int[], $4::text[], $5::int[], $6::int[], $7::int[], $8::bool[], $9::text[], $10::float8[],
			              $11::float8[], $12::text[], $13::text[], $14::text[], $15::int[], $16::int[], $17::int[])
			       AS a(x, y, plan, sx, sy, dist, build, reason, h, s, biome, water, tags, face, gx, gy)
			`+conflict,
			part[0].SettlementID, xs, ys, plan, sxs, sys, dist, build, reason, h, s, biome, water, tags, faces, gxs, gys); err != nil {
			return fmt.Errorf("postgres: storing the open lots: %w", err)
		}
	}
	return nil
}

// MarkBuilt lays the road on the given cells that are not laid yet.
func (r *CitizenRepository) MarkBuilt(ctx context.Context, settlementID string, cells [][2]int, at time.Time) (int, error) {
	if len(cells) == 0 {
		return 0, nil
	}
	xs, ys := make([]int32, len(cells)), make([]int32, len(cells))
	for i, c := range cells {
		xs[i], ys[i] = int32(c[0]), int32(c[1])
	}
	tag, err := r.q.Exec(ctx, `
		UPDATE settlement_road_cells c SET built_at = $2
		  FROM unnest($3::int[], $4::int[]) AS w(x, y)
		 WHERE c.settlement_id = $1::uuid AND c.lot_x = w.x AND c.lot_y = w.y AND c.built_at IS NULL`,
		settlementID, at.UTC(), xs, ys)
	if err != nil {
		return 0, fmt.Errorf("postgres: laying the road: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// CancelPlan closes a plan and removes its cells.
func (r *CitizenRepository) CancelPlan(ctx context.Context, planID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE settlement_road_plans SET cancelled_at = $2 WHERE id = $1::uuid AND cancelled_at IS NULL`, planID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: cancelling the road plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := r.q.Exec(ctx, `DELETE FROM settlement_road_cells WHERE plan_id = $1::uuid`, planID); err != nil {
		return false, fmt.Errorf("postgres: removing the cells of a cancelled plan: %w", err)
	}
	return true, nil
}

// SyncOpenLots makes the stored open lots exactly want, keeping owned lots.
func (r *CitizenRepository) SyncOpenLots(ctx context.Context, settlementID string, want []application.OpenLotRow) error {
	xs, ys := make([]int32, len(want)), make([]int32, len(want))
	for i, o := range want {
		xs[i], ys[i] = int32(o.X), int32(o.Y)
	}
	if _, err := r.q.Exec(ctx, `
		DELETE FROM settlement_open_lots o
		 WHERE o.settlement_id = $1::uuid
		   AND NOT EXISTS (SELECT 1 FROM unnest($2::int[], $3::int[]) AS w(x, y) WHERE w.x = o.lot_x AND w.y = o.lot_y)
		   AND NOT EXISTS (SELECT 1 FROM settlement_lots l
		                    WHERE l.settlement_id = o.settlement_id AND l.lot_x = o.lot_x AND l.lot_y = o.lot_y AND l.released_at IS NULL)`,
		settlementID, xs, ys); err != nil {
		return fmt.Errorf("postgres: closing lots no road serves: %w", err)
	}
	return r.upsertOpen(ctx, want, false)
}

// ForeignTiles lists the tiles inside the box that carry a road of another settlement.
func (r *CitizenRepository) ForeignTiles(ctx context.Context, settlementID string, face int, gxMin, gxMax, gyMin, gyMax int32) ([]application.TileKey, error) {
	rows, err := r.q.Query(ctx, `
		SELECT DISTINCT tile_face, tile_gx, tile_gy FROM settlement_road_cells
		 WHERE settlement_id <> $1::uuid AND tile_face = $2 AND tile_gx BETWEEN $3 AND $4 AND tile_gy BETWEEN $5 AND $6`,
		settlementID, face, gxMin, gxMax, gyMin, gyMax)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading other settlements' tiles: %w", err)
	}
	defer rows.Close()
	var out []application.TileKey
	for rows.Next() {
		var k application.TileKey
		var f int16
		if err := rows.Scan(&f, &k.GX, &k.GY); err != nil {
			return nil, fmt.Errorf("postgres: scanning a tile: %w", err)
		}
		k.Face = int(f)
		out = append(out, k)
	}
	return out, rows.Err()
}
