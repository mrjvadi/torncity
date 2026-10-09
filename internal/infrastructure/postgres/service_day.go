package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ServiceDayRepository is the daily services' days (migration 0137), bound to one transaction.
type ServiceDayRepository struct{ q querier }

var _ application.ServiceDayRepository = (*ServiceDayRepository)(nil)

const serviceDayColumns = `settlement_id::text, day, staff, wage, COALESCE(wage_tx::text, ''), at`

func (r *ServiceDayRepository) read(ctx context.Context, row pgx.Row) (*application.ServiceDay, error) {
	var d application.ServiceDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Staff, &d.Wage, &d.WageTx, &d.At); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a service day: %w", err)
	}
	rows, err := r.q.Query(ctx, `SELECT building_id::text, service, held, idle, staff, wage, used, grace FROM service_day_posts
		WHERE settlement_id = $1::uuid AND day = $2 ORDER BY building_id`, d.SettlementID, d.Day)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the service posts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p application.ServicePost
		var used []byte
		if err := rows.Scan(&p.BuildingID, &p.Service, &p.Held, &p.Idle, &p.Staff, &p.Wage, &used, &p.Grace); err != nil {
			return nil, fmt.Errorf("postgres: scanning a service post: %w", err)
		}
		p.Used = map[string]int64{}
		if err := json.Unmarshal(used, &p.Used); err != nil {
			return nil, fmt.Errorf("postgres: reading what a post used: %w", err)
		}
		d.Posts = append(d.Posts, p)
	}
	return &d, rows.Err()
}

// Day returns one day, nil for none.
func (r *ServiceDayRepository) Day(ctx context.Context, settlementID string, day int64) (*application.ServiceDay, error) {
	return r.read(ctx, r.q.QueryRow(ctx, `SELECT `+serviceDayColumns+` FROM service_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
}

// Last returns the latest judged day, nil for none.
func (r *ServiceDayRepository) Last(ctx context.Context, settlementID string) (*application.ServiceDay, error) {
	return r.read(ctx, r.q.QueryRow(ctx, `SELECT `+serviceDayColumns+` FROM service_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
}

// RecordDay is the fence of a service day: the row, then its posts.
func (r *ServiceDayRepository) RecordDay(ctx context.Context, d application.ServiceDay) (bool, error) {
	var wage any
	if d.WageTx != "" {
		wage = d.WageTx
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO service_days (settlement_id, day, posts, held, staff, wage, wage_tx, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::uuid, $8) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, len(d.Posts), d.Held(), d.Staff, d.Wage, wage, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a service day: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	for _, p := range d.Posts {
		used := p.Used
		if used == nil {
			used = map[string]int64{}
		}
		raw, err := json.Marshal(used)
		if err != nil {
			return false, err
		}
		if _, err := r.q.Exec(ctx,
			`INSERT INTO service_day_posts (settlement_id, day, building_id, service, held, idle, staff, wage, used, grace)
			 VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9::jsonb, $10)`,
			d.SettlementID, d.Day, p.BuildingID, p.Service, p.Held, p.Idle, p.Staff, p.Wage, string(raw), p.Grace); err != nil {
			return false, fmt.Errorf("postgres: recording a service post: %w", err)
		}
	}
	return true, nil
}
