package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// RailRepository is the railway port over migration 0153: lines in sections, stations, trains and their schedules, and the sections a
// train holds. A section holds one train, a passing loop two: the primary key (segment, slot) is the rule across replicas.
type RailRepository struct{ q querier }

var _ application.RailRepository = (*RailRepository)(nil)

const railLineColumns = `id::text, name, owner_kind, owner_id::text, home_settlement::text, class, status, created_by::text, rate_per_tonne_km, sections_total, sections_built, length_m, created_at`

func scanRailLine(row pgx.Row) (application.RailLine, error) {
	var l application.RailLine
	err := row.Scan(&l.ID, &l.Name, &l.OwnerKind, &l.OwnerID, &l.Home, &l.Class, &l.Status, &l.CreatedBy, &l.RatePerTonneKm, &l.SectionsTotal, &l.SectionsBuilt, &l.LengthM, &l.CreatedAt)
	return l, err
}

// AddLine stores a line with its sections and stations.
func (r *RailRepository) AddLine(ctx context.Context, l application.RailLine, segs []application.RailSegment, stops []application.RailStop) error {
	status := l.Status
	if status == "" {
		status = application.LinePlanned
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO rail_lines (id, name, owner_kind, owner_id, home_settlement, class, rate_per_tonne_km, status, sections_total, sections_built, length_m, created_by, created_at)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6, $7, $8, $9, 0, $10, $11::uuid, $12)`,
		l.ID, l.Name, l.OwnerKind, l.OwnerID, l.Home, l.Class, l.RatePerTonneKm, status, l.SectionsTotal, l.LengthM, l.CreatedBy, l.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: storing a rail line: %w", err)
	}
	for _, s := range segs {
		if _, err := r.q.Exec(ctx, `
			INSERT INTO rail_segments (id, line_id, seq, tile, length_m, grade_bps, loop, condition_bps) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, 10000)`,
			s.ID, l.ID, s.Seq, s.Tile, s.LengthM, s.GradeBPS, s.Loop); err != nil {
			return fmt.Errorf("postgres: storing a section: %w", err)
		}
	}
	for _, s := range stops {
		if err := r.AddStop(ctx, application.RailStop{ID: s.ID, LineID: l.ID, AfterSeq: s.AfterSeq, BuildingID: s.BuildingID, SettlementID: s.SettlementID}); err != nil {
			return err
		}
	}
	return nil
}

// Line reads a line.
func (r *RailRepository) Line(ctx context.Context, id string, lock bool) (*application.RailLine, error) {
	if !validUUID(id) {
		return nil, application.ErrLineNotFound
	}
	sql := `SELECT ` + railLineColumns + ` FROM rail_lines WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanRailLine(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrLineNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a rail line: %w", err)
	}
	return &l, nil
}

// LinesOf lists the lines that start at or run through a settlement (its own, or with a station of its).
func (r *RailRepository) LinesOf(ctx context.Context, settlementID string) ([]application.RailLine, error) {
	rows, err := r.q.Query(ctx, `SELECT `+railLineColumns+` FROM rail_lines
		WHERE status <> 'closed' AND (home_settlement = $1::uuid OR id IN (SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid))
		ORDER BY created_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing rail lines: %w", err)
	}
	defer rows.Close()
	var out []application.RailLine
	for rows.Next() {
		l, err := scanRailLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Segments lists a line's sections in order.
func (r *RailRepository) Segments(ctx context.Context, lineID string) ([]application.RailSegment, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, line_id::text, seq, tile, length_m, grade_bps, loop, condition_bps, wear_day, built_at
		FROM rail_segments WHERE line_id = $1::uuid ORDER BY seq`, lineID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing sections: %w", err)
	}
	defer rows.Close()
	var out []application.RailSegment
	for rows.Next() {
		var s application.RailSegment
		if err := rows.Scan(&s.ID, &s.LineID, &s.Seq, &s.Tile, &s.LengthM, &s.GradeBPS, &s.Loop, &s.ConditionBPS, &s.WearDay, &s.BuiltAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Stops lists a line's stations in order.
func (r *RailRepository) Stops(ctx context.Context, lineID string) ([]application.RailStop, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, line_id::text, after_seq, building_id::text, settlement_id::text FROM rail_stops WHERE line_id = $1::uuid ORDER BY after_seq`, lineID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing stations: %w", err)
	}
	defer rows.Close()
	var out []application.RailStop
	for rows.Next() {
		var s application.RailStop
		if err := rows.Scan(&s.ID, &s.LineID, &s.AfterSeq, &s.BuildingID, &s.SettlementID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AddStop stores a station.
func (r *RailRepository) AddStop(ctx context.Context, s application.RailStop) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO rail_stops (id, line_id, after_seq, building_id, settlement_id) VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid)`,
		s.ID, s.LineID, s.AfterSeq, s.BuildingID, s.SettlementID); err != nil {
		return fmt.Errorf("postgres: storing a station: %w", err)
	}
	return nil
}

// LayNext marks the next n sections built.
func (r *RailRepository) LayNext(ctx context.Context, lineID string, n int, at time.Time) (int, error) {
	tag, err := r.q.Exec(ctx, `
		UPDATE rail_segments SET built_at = $3
		 WHERE id IN (SELECT id FROM rail_segments WHERE line_id = $1::uuid AND built_at IS NULL ORDER BY seq LIMIT $2 FOR UPDATE)`, lineID, n, at.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: laying sections: %w", err)
	}
	laid := int(tag.RowsAffected())
	if laid > 0 {
		if _, err := r.q.Exec(ctx, `
			UPDATE rail_lines SET sections_built = sections_built + $2,
			       status = CASE WHEN sections_built + $2 >= sections_total THEN 'open' ELSE 'building' END
			 WHERE id = $1::uuid`, lineID, laid); err != nil {
			return 0, fmt.Errorf("postgres: counting the laid sections: %w", err)
		}
	}
	return laid, nil
}

// SetSegmentCondition writes a section's condition and the day it was worn to.
func (r *RailRepository) SetSegmentCondition(ctx context.Context, id string, condition, wearDay int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE rail_segments SET condition_bps = $2, wear_day = $3 WHERE id = $1::uuid`, id, condition, wearDay); err != nil {
		return fmt.Errorf("postgres: writing a section's condition: %w", err)
	}
	return nil
}

// SetRate sets a line's freight rate.
func (r *RailRepository) SetRate(ctx context.Context, lineID string, rate int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE rail_lines SET rate_per_tonne_km = $2 WHERE id = $1::uuid`, lineID, rate); err != nil {
		return fmt.Errorf("postgres: setting the freight rate: %w", err)
	}
	return nil
}

// SetLoop makes a section a passing loop (or not).
func (r *RailRepository) SetLoop(ctx context.Context, segmentID string, loop bool) error {
	if _, err := r.q.Exec(ctx, `UPDATE rail_segments SET loop = $2 WHERE id = $1::uuid`, segmentID, loop); err != nil {
		return fmt.Errorf("postgres: setting a loop: %w", err)
	}
	return nil
}

const railTrainColumns = `id::text, line_id::text, owner_kind, owner_id::text, name, status, COALESCE(at_stop::text, ''), COALESCE(game_action_id::text, ''),
	wagons, sched_pos, heading, leg_no, next_event_at, waiting_since, stuck_notified, served, wait_reason, created_at`

func scanRailTrain(row pgx.Row) (application.RailTrain, error) {
	var t application.RailTrain
	err := row.Scan(&t.ID, &t.LineID, &t.OwnerKind, &t.OwnerID, &t.Name, &t.Status, &t.AtStop, &t.GameActionID,
		&t.Wagons, &t.SchedPos, &t.Heading, &t.LegNo, &t.NextEventAt, &t.WaitingSince, &t.StuckNotified, &t.Served, &t.WaitReason, &t.CreatedAt)
	return t, err
}

// AddTrain stores a train.
func (r *RailRepository) AddTrain(ctx context.Context, t application.RailTrain) error {
	status := t.Status
	if status == "" {
		status = application.TrainIdle
	}
	heading := t.Heading
	if heading == 0 {
		heading = 1
	}
	if _, err := r.q.Exec(ctx, `
		INSERT INTO rail_trains (id, line_id, owner_kind, owner_id, name, wagons, status, at_stop, heading, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8::uuid, $9, $10)`,
		t.ID, t.LineID, t.OwnerKind, t.OwnerID, t.Name, t.Wagons, status, nullable(t.AtStop), heading, t.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: storing a train: %w", err)
	}
	return nil
}

// Train reads a train.
func (r *RailRepository) Train(ctx context.Context, id string, lock bool) (*application.RailTrain, error) {
	if !validUUID(id) {
		return nil, application.ErrTrainNotFound
	}
	sql := `SELECT ` + railTrainColumns + ` FROM rail_trains WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	t, err := scanRailTrain(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrTrainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a train: %w", err)
	}
	return &t, nil
}

func (r *RailRepository) trains(ctx context.Context, sql string, args ...any) ([]application.RailTrain, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing trains: %w", err)
	}
	defer rows.Close()
	var out []application.RailTrain
	for rows.Next() {
		t, err := scanRailTrain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TrainsOn lists the trains of a line.
func (r *RailRepository) TrainsOn(ctx context.Context, lineID string) ([]application.RailTrain, error) {
	return r.trains(ctx, `SELECT `+railTrainColumns+` FROM rail_trains WHERE line_id = $1::uuid ORDER BY created_at, id`, lineID)
}

// TrainsOfSettlement lists the trains of the lines a settlement is on.
func (r *RailRepository) TrainsOfSettlement(ctx context.Context, settlementID string) ([]application.RailTrain, error) {
	return r.trains(ctx, `SELECT `+railTrainColumns+` FROM rail_trains
		WHERE line_id IN (SELECT id FROM rail_lines WHERE home_settlement = $1::uuid UNION SELECT line_id FROM rail_stops WHERE settlement_id = $1::uuid)
		ORDER BY created_at, id`, settlementID)
}

// SetTrain writes a train's running state.
func (r *RailRepository) SetTrain(ctx context.Context, t application.RailTrain) error {
	tag, err := r.q.Exec(ctx, `
		UPDATE rail_trains SET status = $2, at_stop = $3::uuid, heading = $4, leg_no = $5, sched_pos = $6, next_event_at = $7, game_action_id = $8::uuid,
		       waiting_since = $9, stuck_notified = $10, name = $11, wagons = $12, served = $13, wait_reason = $14
		 WHERE id = $1::uuid`,
		t.ID, t.Status, nullable(t.AtStop), t.Heading, t.LegNo, t.SchedPos, t.NextEventAt, nullable(t.GameActionID), t.WaitingSince, t.StuckNotified, t.Name, t.Wagons, t.Served, t.WaitReason)
	if err != nil {
		return fmt.Errorf("postgres: writing a train: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return application.ErrTrainNotFound
	}
	return nil
}

// SetSchedule replaces a train's schedule.
func (r *RailRepository) SetSchedule(ctx context.Context, trainID string, steps []application.RailStep) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM rail_schedules WHERE train_id = $1::uuid`, trainID); err != nil {
		return fmt.Errorf("postgres: clearing a schedule: %w", err)
	}
	for i, s := range steps {
		if _, err := r.q.Exec(ctx, `INSERT INTO rail_schedules (train_id, seq, stop_id, op, item_code, stock_floor, wait_minutes) VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7)`,
			trainID, i, s.StopID, s.Op, s.Item, s.StockFloor, s.WaitMinutes); err != nil {
			return fmt.Errorf("postgres: storing a schedule step: %w", err)
		}
	}
	return nil
}

// Schedule reads a train's schedule.
func (r *RailRepository) Schedule(ctx context.Context, trainID string) ([]application.RailStep, error) {
	rows, err := r.q.Query(ctx, `SELECT train_id::text, seq, stop_id::text, op, item_code, stock_floor, wait_minutes FROM rail_schedules WHERE train_id = $1::uuid ORDER BY seq`, trainID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a schedule: %w", err)
	}
	defer rows.Close()
	var out []application.RailStep
	for rows.Next() {
		var s application.RailStep
		if err := rows.Scan(&s.TrainID, &s.Seq, &s.StopID, &s.Op, &s.Item, &s.StockFloor, &s.WaitMinutes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Reserve takes the sections for a train, all or none. The sections are locked in id order, so two trains that want overlapping
// sections queue on the first they share and cannot deadlock; the primary key (segment, slot) is the rule if a replica ever skips
// the lock.
func (r *RailRepository) Reserve(ctx context.Context, trainID string, leg int64, segmentIDs []string, at time.Time) (bool, []string, error) {
	ids := append([]string(nil), segmentIDs...)
	sort.Strings(ids)
	rows, err := r.q.Query(ctx, `SELECT id::text, loop FROM rail_segments WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return false, nil, fmt.Errorf("postgres: locking the sections: %w", err)
	}
	loop := map[string]bool{}
	for rows.Next() {
		var id string
		var l bool
		if err := rows.Scan(&id, &l); err != nil {
			rows.Close()
			return false, nil, err
		}
		loop[id] = l
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	if len(loop) != len(ids) {
		return false, nil, application.ErrLineNotFound
	}
	held := map[string][]string{}
	hrows, err := r.q.Query(ctx, `SELECT segment_id::text, train_id::text FROM rail_reservations WHERE segment_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return false, nil, fmt.Errorf("postgres: reading the sections' holders: %w", err)
	}
	for hrows.Next() {
		var seg, train string
		if err := hrows.Scan(&seg, &train); err != nil {
			hrows.Close()
			return false, nil, err
		}
		held[seg] = append(held[seg], train)
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return false, nil, err
	}
	var blockers []string
	for _, id := range ids {
		mine := false
		for _, t := range held[id] {
			mine = mine || t == trainID
		}
		if mine {
			continue
		}
		capacity := 1
		if loop[id] {
			capacity = 2
		}
		if len(held[id]) >= capacity {
			blockers = append(blockers, held[id]...)
		}
	}
	if len(blockers) > 0 {
		return false, blockers, nil
	}
	for _, id := range ids {
		mine := false
		for _, t := range held[id] {
			mine = mine || t == trainID
		}
		if mine {
			continue
		}
		slot := 0
		if len(held[id]) > 0 {
			slot = 1
		}
		if _, err := r.q.Exec(ctx, `INSERT INTO rail_reservations (segment_id, slot, train_id, leg_no, since) VALUES ($1::uuid, $2, $3::uuid, $4, $5)`,
			id, slot, trainID, leg, at.UTC()); err != nil {
			return false, nil, fmt.Errorf("postgres: reserving a section: %w", err)
		}
	}
	return true, nil, nil
}

// Release gives all of a train's sections back.
func (r *RailRepository) Release(ctx context.Context, trainID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM rail_reservations WHERE train_id = $1::uuid`, trainID); err != nil {
		return fmt.Errorf("postgres: releasing the sections: %w", err)
	}
	return nil
}

// Reservations lists the trains that hold each section of a line.
func (r *RailRepository) Reservations(ctx context.Context, lineID string) (map[string][]string, error) {
	rows, err := r.q.Query(ctx, `SELECT v.segment_id::text, v.train_id::text FROM rail_reservations v JOIN rail_segments s ON s.id = v.segment_id WHERE s.line_id = $1::uuid ORDER BY v.segment_id, v.slot`, lineID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the reservations: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var seg, train string
		if err := rows.Scan(&seg, &train); err != nil {
			return nil, err
		}
		out[seg] = append(out[seg], train)
	}
	return out, rows.Err()
}
