package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// CityRepository reads the world's places.
//
// It is read-only by design, matching the port: cities arrive through the
// content loader, never through gameplay, so there is no Create here and no
// way for a player command to grow the world by accident.
type CityRepository struct {
	q querier
}

var _ application.CityRepository = (*CityRepository)(nil)

// NewCityRepository returns a repository using the pool directly. City reads
// are lookups against near-static content, so they do not belong to a unit of
// work.
func NewCityRepository(p *Pool) *CityRepository { return &CityRepository{q: p.Raw()} }

// treasury_account_id is deliberately absent from every statement below:
// application.City has no field for it, and migrations/0002_phase1.up.sql
// creates it NULL-able without the foreign key the accounts table will bring.
// Selecting a column nothing can carry would only invite a scan that fails.

const selectCities = `
SELECT id, code, name, tax_rate_bps, cost_of_living, population
FROM cities
ORDER BY code`

// List returns every city, ordered by code.
//
// The order is on code rather than name because code is the stable machine
// identifier: names are display text and will be localised, so ordering on
// them would silently reshuffle every menu when a translation lands.
func (r *CityRepository) List(ctx context.Context) ([]application.City, error) {
	rows, err := r.q.Query(ctx, selectCities)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cities: %w", err)
	}
	defer rows.Close()

	var out []application.City
	for rows.Next() {
		var c application.City
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.TaxRateBPS, &c.CostOfLiving, &c.Population); err != nil {
			return nil, fmt.Errorf("postgres: scanning city row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading city rows: %w", err)
	}

	return out, nil
}

const selectCityByID = `
SELECT id, code, name, tax_rate_bps, cost_of_living, population
FROM cities
WHERE id = $1::uuid`

// ByID returns the city, or application.ErrCityNotFound.
//
// A malformed identifier is reported as a miss rather than a fault: the
// argument reaches this repository from a callback payload or a command
// argument, so a value that is not uuid text names no city, exactly like a
// well-formed uuid that no row carries. Letting the server's complaint through
// instead would classify a bad button payload as an internal failure and put
// the caller's own string into the log line that recorded it.
func (r *CityRepository) ByID(ctx context.Context, id string) (*application.City, error) {
	var c application.City

	err := r.q.QueryRow(ctx, selectCityByID, id).Scan(
		&c.ID, &c.Code, &c.Name, &c.TaxRateBPS, &c.CostOfLiving, &c.Population,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUIDText(err) {
			return nil, application.ErrCityNotFound
		}
		return nil, fmt.Errorf("postgres: loading city by id: %w", err)
	}

	return &c, nil
}

const selectCityByCode = `
SELECT id, code, name, tax_rate_bps, cost_of_living, population
FROM cities
WHERE code = $1`

// ByCode returns the city with this code, or application.ErrCityNotFound.
//
// The lookup is exact, not case-insensitive: cities.code is UNIQUE and is the
// value seeds and configuration hard-code, so folding case here would let two
// spellings resolve to one row today and collide the moment a second city
// differs from it only in case.
func (r *CityRepository) ByCode(ctx context.Context, code string) (*application.City, error) {
	var c application.City

	err := r.q.QueryRow(ctx, selectCityByCode, code).Scan(
		&c.ID, &c.Code, &c.Name, &c.TaxRateBPS, &c.CostOfLiving, &c.Population,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrCityNotFound
		}
		return nil, fmt.Errorf("postgres: loading city by code: %w", err)
	}

	return &c, nil
}
