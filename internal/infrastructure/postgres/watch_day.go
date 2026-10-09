package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// WatchDayRepository is the night watch's days (migration 0137), bound to one transaction.
type WatchDayRepository struct{ q querier }

var _ application.WatchDayRepository = (*WatchDayRepository)(nil)

const watchDayColumns = `settlement_id::text, day, guards, wage, fuel, COALESCE(wage_tx::text, ''), at`

func (r *WatchDayRepository) read(ctx context.Context, row pgx.Row) (*application.WatchDay, error) {
	var d application.WatchDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Guards, &d.Wage, &d.Fuel, &d.WageTx, &d.At); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a watch day: %w", err)
	}
	rows, err := r.q.Query(ctx, `SELECT building_id::text, held, idle, guards, wage, fuel FROM watch_day_posts
		WHERE settlement_id = $1::uuid AND day = $2 ORDER BY building_id`, d.SettlementID, d.Day)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the watch posts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p application.WatchPost
		if err := rows.Scan(&p.BuildingID, &p.Held, &p.Idle, &p.Guards, &p.Wage, &p.Fuel); err != nil {
			return nil, fmt.Errorf("postgres: scanning a watch post: %w", err)
		}
		d.Posts = append(d.Posts, p)
	}
	return &d, rows.Err()
}

// Day returns one day, nil for none.
func (r *WatchDayRepository) Day(ctx context.Context, settlementID string, day int64) (*application.WatchDay, error) {
	return r.read(ctx, r.q.QueryRow(ctx, `SELECT `+watchDayColumns+` FROM watch_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
}

// Last returns the latest judged day, nil for none.
func (r *WatchDayRepository) Last(ctx context.Context, settlementID string) (*application.WatchDay, error) {
	return r.read(ctx, r.q.QueryRow(ctx, `SELECT `+watchDayColumns+` FROM watch_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
}

// RecordDay is the fence of a watch day: the row, then its posts.
func (r *WatchDayRepository) RecordDay(ctx context.Context, d application.WatchDay) (bool, error) {
	var wage any
	if d.WageTx != "" {
		wage = d.WageTx
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO watch_days (settlement_id, day, posts, held, guards, wage, fuel, wage_tx, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::uuid, $9) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, len(d.Posts), d.Held(), d.Guards, d.Wage, d.Fuel, wage, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a watch day: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	for _, p := range d.Posts {
		if _, err := r.q.Exec(ctx,
			`INSERT INTO watch_day_posts (settlement_id, day, building_id, held, idle, guards, wage, fuel) VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8)`,
			d.SettlementID, d.Day, p.BuildingID, p.Held, p.Idle, p.Guards, p.Wage, p.Fuel); err != nil {
			return false, fmt.Errorf("postgres: recording a watch post: %w", err)
		}
	}
	return true, nil
}
