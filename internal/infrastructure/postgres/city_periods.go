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

// This file persists a city's period (migrations/0024_legislature_and_budget):
// the clock that settles each city once per period, and each period's budget.

// CityPeriodRepository implements application.CityPeriodRepository.
type CityPeriodRepository struct {
	q querier
}

var _ application.CityPeriodRepository = (*CityPeriodRepository)(nil)

// Clock returns a city's clock, locked, opening it on first use.
func (r *CityPeriodRepository) Clock(ctx context.Context, cityID string, now time.Time) (*application.CityClock, error) {
	if !validUUID(cityID) {
		return nil, application.ErrCityNotFound
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO city_clocks (city_id, period_no, period_started_at, updated_at)
		 SELECT $1::uuid, 1, $2, $2 WHERE EXISTS (SELECT 1 FROM cities WHERE id = $1::uuid)
		 ON CONFLICT (city_id) DO NOTHING`, cityID, now.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening a city's clock: %w", err)
	}
	var c application.CityClock
	err := r.q.QueryRow(ctx,
		`SELECT city_id::text, period_no, period_started_at, next_at, COALESCE(action_id::text, ''), updated_at
		   FROM city_clocks WHERE city_id = $1::uuid FOR UPDATE`, cityID).Scan(
		&c.CityID, &c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID, &c.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrCityNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a city's clock: %w", err)
	}
	c.PeriodStartedAt, c.UpdatedAt = c.PeriodStartedAt.UTC(), c.UpdatedAt.UTC()
	if c.NextAt != nil {
		t := c.NextAt.UTC()
		c.NextAt = &t
	}
	return &c, nil
}

// SaveClock writes a clock.
func (r *CityPeriodRepository) SaveClock(ctx context.Context, c application.CityClock) error {
	var action any
	if c.ActionID != "" {
		action = c.ActionID
	}
	if _, err := r.q.Exec(ctx,
		`UPDATE city_clocks SET period_no = $2, period_started_at = $3, next_at = $4, action_id = $5::uuid, updated_at = $6
		  WHERE city_id = $1::uuid`,
		c.CityID, c.PeriodNo, c.PeriodStartedAt.UTC(), c.NextAt, action, c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a city's clock: %w", err)
	}
	return nil
}

// RecordBudget appends one period's budget.
func (r *CityPeriodRepository) RecordBudget(ctx context.Context, p application.CityBudgetPeriod) error {
	lines := p.Lines
	if lines == nil {
		lines = []application.CityBudgetLine{}
	}
	doc, err := json.Marshal(lines)
	if err != nil {
		return fmt.Errorf("postgres: encoding a budget: %w", err)
	}
	_, err = r.q.Exec(ctx,
		`INSERT INTO city_budget_periods (city_id, period_no, started_at, ended_at, treasury, spendable, spent, defence, lines)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)`,
		p.CityID, p.PeriodNo, p.StartedAt.UTC(), p.EndedAt.UTC(), p.Treasury, p.Spendable, p.Spent, p.Defence, doc)
	if violates(err, sqlstateUniqueViolation, "city_budget_periods_pkey") {
		return application.ErrPeriodSettled
	}
	if err != nil {
		return fmt.Errorf("postgres: recording a budget: %w", err)
	}
	return nil
}

// LatestBudget returns a city's most recent budget, nil for none.
func (r *CityPeriodRepository) LatestBudget(ctx context.Context, cityID string) (*application.CityBudgetPeriod, error) {
	return latestBudget(ctx, r.q, cityID)
}

func latestBudget(ctx context.Context, q querier, cityID string) (*application.CityBudgetPeriod, error) {
	if !validUUID(cityID) {
		return nil, nil
	}
	var (
		p   application.CityBudgetPeriod
		doc []byte
	)
	err := q.QueryRow(ctx,
		`SELECT city_id::text, period_no, started_at, ended_at, treasury, spendable, spent, defence, lines
		   FROM city_budget_periods WHERE city_id = $1::uuid ORDER BY period_no DESC LIMIT 1`, cityID).Scan(
		&p.CityID, &p.PeriodNo, &p.StartedAt, &p.EndedAt, &p.Treasury, &p.Spendable, &p.Spent, &p.Defence, &doc)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a city's budget: %w", err)
	}
	if err := json.Unmarshal(doc, &p.Lines); err != nil {
		return nil, fmt.Errorf("postgres: decoding a city's budget: %w", err)
	}
	p.StartedAt, p.EndedAt = p.StartedAt.UTC(), p.EndedAt.UTC()
	return &p, nil
}
