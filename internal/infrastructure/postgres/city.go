package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
func NewCityRepository(p *Pool) *CityRepository { return &CityRepository{q: p.shared()} }

// tax_rate_bps is deliberately absent from every statement below too: it is
// the default of the city.tax_rate lever, and the rate in force is read with
// application.PolicyReader, never off this row (ADR 0015).
//
// treasury_account_id is deliberately absent from every statement below:
// application.City has no field for it, and migrations/0002_phase1.up.sql
// creates it NULL-able without the foreign key the accounts table will bring.
// Selecting a column nothing can carry would only invite a scan that fails.

const selectCities = `
SELECT id, code, name, COALESCE(jurisdiction_id::text, ''), cost_of_living, population
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
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.JurisdictionID, &c.CostOfLiving, &c.Population); err != nil {
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
SELECT id, code, name, COALESCE(jurisdiction_id::text, ''), cost_of_living, population
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
		&c.ID, &c.Code, &c.Name, &c.JurisdictionID, &c.CostOfLiving, &c.Population,
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
SELECT id, code, name, COALESCE(jurisdiction_id::text, ''), cost_of_living, population
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
		&c.ID, &c.Code, &c.Name, &c.JurisdictionID, &c.CostOfLiving, &c.Population,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrCityNotFound
		}
		return nil, fmt.Errorf("postgres: loading city by code: %w", err)
	}

	return &c, nil
}

// CityCache is a CityRepository read through memory.
//
// Cities change only when content is loaded (`admin content load`): a
// command reads them dozens of times — every price, every screen names one —
// and none of those reads should cost a round trip, let alone a pooled
// connection. The cache holds every city, reloaded when it is older than ttl
// (the content reload interval) and whenever a lookup misses, so a city a
// load added is found at once. A reload runs where the caller is: inside a
// unit of work, on its transaction (ambient.go).
type CityCache struct {
	repo *CityRepository
	ttl  time.Duration
	now  func() time.Time

	mu       sync.RWMutex
	loadedAt time.Time
	list     []application.City
	byID     map[string]application.City
	byCode   map[string]application.City
}

var _ application.CityRepository = (*CityCache)(nil)

// cityCacheMissReload is the least time between two reloads a miss causes.
const cityCacheMissReload = 2 * time.Second

// NewCityCache returns a cache over the pool that reloads after ttl.
func NewCityCache(p *Pool, ttl time.Duration) *CityCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CityCache{repo: NewCityRepository(p), ttl: ttl, now: time.Now}
}

// fresh returns the cached cities, reloading them when stale or forced.
func (c *CityCache) fresh(ctx context.Context, force bool) ([]application.City, map[string]application.City,
	map[string]application.City, error,
) {
	c.mu.RLock()
	list, byID, byCode, at := c.list, c.byID, c.byCode, c.loadedAt
	c.mu.RUnlock()
	age := c.now().Sub(at)
	// A miss reloads at most once in a while: a forged id must not turn
	// every press into a reload.
	force = force && age >= cityCacheMissReload
	if !force && byID != nil && age < c.ttl {
		return list, byID, byCode, nil
	}
	loaded, err := c.repo.List(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	byID = make(map[string]application.City, len(loaded))
	byCode = make(map[string]application.City, len(loaded))
	for _, city := range loaded {
		byID[city.ID], byCode[city.Code] = city, city
	}
	c.mu.Lock()
	c.list, c.byID, c.byCode, c.loadedAt = loaded, byID, byCode, c.now()
	c.mu.Unlock()
	return loaded, byID, byCode, nil
}

// List returns every city, ordered by code.
func (c *CityCache) List(ctx context.Context) ([]application.City, error) {
	list, _, _, err := c.fresh(ctx, false)
	if err != nil {
		return nil, err
	}
	return append([]application.City(nil), list...), nil
}

// ByID returns the city, or application.ErrCityNotFound.
func (c *CityCache) ByID(ctx context.Context, id string) (*application.City, error) {
	return c.lookup(ctx, id, func(_, byID, _ map[string]application.City) (application.City, bool) {
		city, ok := byID[id]
		return city, ok
	})
}

// ByCode returns the city with this code, or application.ErrCityNotFound.
func (c *CityCache) ByCode(ctx context.Context, code string) (*application.City, error) {
	return c.lookup(ctx, code, func(_, _, byCode map[string]application.City) (application.City, bool) {
		city, ok := byCode[code]
		return city, ok
	})
}

func (c *CityCache) lookup(ctx context.Context, key string,
	find func(_, byID, byCode map[string]application.City) (application.City, bool),
) (*application.City, error) {
	if key == "" {
		return nil, application.ErrCityNotFound
	}
	for _, force := range []bool{false, true} {
		_, byID, byCode, err := c.fresh(ctx, force)
		if err != nil {
			return nil, err
		}
		if city, ok := find(nil, byID, byCode); ok {
			return &city, nil
		}
	}
	return nil, application.ErrCityNotFound
}
