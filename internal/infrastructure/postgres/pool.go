// Package postgres implements the persistence ports over PostgreSQL.
//
// PostgreSQL is the only source of truth in this system: Redis answers
// coordination questions and NATS moves messages, but any value a player can
// lose money over is settled here. Two consequences run through this package.
//
// First, every state change that announces itself writes its outbox row in the
// same transaction as the change. Publishing from a handler after a commit
// gives the two well-known failures — an event for a change that rolled back,
// or a change nobody was told about — and the outbox is what removes both.
//
// Second, every insert that a retry could reach twice is written to be
// idempotent at the SQL level rather than guarded by an application-side
// "check then insert", which is not a guard at all under concurrency.
//
// Column names and types come from docs/database.md, which is the schema
// authority, and match migrations/0001_init.up.sql.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5"
)

// Pool owns the connection pool.
//
// It wraps *pgxpool.Pool rather than exposing it so that the rest of the
// codebase depends on this package, not on the driver, and so a future change
// of pool settings or driver has one place to happen.
type Pool struct {
	pool *pgxpool.Pool
}

// New parses dsn, opens the pool and verifies it.
//
// The Ping is deliberate: pgxpool connects lazily, so without it a wrong host
// or password would first surface on a player's command rather than at boot.
func New(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// The DSN carries a password, so only the failure is reported.
		return nil, fmt.Errorf("postgres: invalid dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: opening pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		// A constructor that fails must not leak the pool it opened.
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed: %w", err)
	}

	return &Pool{pool: pool}, nil
}

// Raw exposes the underlying pool for adapters in this package.
func (p *Pool) Raw() *pgxpool.Pool { return p.pool }

// Close waits for in-flight queries and releases every connection.
func (p *Pool) Close() {
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
}

// querier is the subset of pgx both *pgxpool.Pool and pgx.Tx provide.
//
// Every repository in this package takes a querier, which is what lets the
// same repository code run on a pooled connection for a standalone read and
// inside a transaction for a unit of work. Without it each repository would
// need a transactional twin, and the two copies would drift.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Compile-time proof that both implementations still fit the subset.
var (
	_ querier = (*pgxpool.Pool)(nil)
	_ querier = (pgx.Tx)(nil)
)
