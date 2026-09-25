package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ONE CONNECTION PER COMMAND.
//
// A unit of work holds one pooled connection for its whole transaction. A
// repository built over the pool (a city lookup, the policy resolver, the
// player search) used to take a SECOND connection for every read — and a
// handler reads through several of them while its transaction is open. Under
// load every connection of the pool was held by a transaction waiting for a
// second one: each waiter sat "idle in transaction" on its row locks, the
// rest queued behind those locks, and nothing moved. PostgreSQL cannot see
// that deadlock; it is in the pool.
//
// So a unit of work puts its transaction on the context it hands to its
// function, and every repository built over the pool runs its statements on
// that transaction when the context carries one (routed, below): inside a
// command there is exactly one connection, the transaction's, and a read
// through a pool-level repository sees the command's own writes. Outside a
// unit of work the same repository uses the pool as before. A unit of work
// opened inside another becomes a savepoint of the outer transaction, for
// the same reason.

// ambientKey keys the open transaction on a context.
type ambientKey struct{}

// withAmbient returns ctx carrying tx as the open transaction.
func withAmbient(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, ambientKey{}, tx)
}

// ambient is the transaction ctx carries, nil for none.
func ambient(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(ambientKey{}).(pgx.Tx)
	return tx
}

// InTransaction reports whether ctx is inside a unit of work. For guards and
// tests.
func InTransaction(ctx context.Context) bool { return ambient(ctx) != nil }

// routed is the pool as a repository sees it: the open transaction of the
// context when there is one, the pool otherwise.
//
// On the open transaction, every statement runs in a savepoint of its own.
// A pool-level repository may meet an error it treats as an answer — a
// malformed id read as "no such row" — and on a pooled connection that error
// cost nothing; inside the command's transaction it would abort the whole
// transaction and every statement after it. The savepoint is rolled back on
// an error and released otherwise, so the command's transaction stays as
// usable as it was.
type routed struct {
	pool *pgxpool.Pool
}

var _ transactor = routed{}

func (r routed) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx := ambient(ctx)
	if tx == nil {
		return r.pool.Exec(ctx, sql, args...)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := sp.Exec(ctx, sql, args...)
	return tag, settle(ctx, sp, err)
}

func (r routed) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx := ambient(ctx)
	if tx == nil {
		return r.pool.Query(ctx, sql, args...)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := sp.Query(ctx, sql, args...)
	if err != nil {
		return nil, settle(ctx, sp, err)
	}
	return &savepointRows{Rows: rows, sp: sp, ctx: ctx}, nil
}

func (r routed) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tx := ambient(ctx)
	if tx == nil {
		return r.pool.QueryRow(ctx, sql, args...)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return errRow{err: err}
	}
	return savepointRow{row: sp.QueryRow(ctx, sql, args...), sp: sp, ctx: ctx}
}

// Begin opens a transaction on the pool, or a savepoint of the open one.
func (r routed) Begin(ctx context.Context) (pgx.Tx, error) {
	if tx := ambient(ctx); tx != nil {
		return tx.Begin(ctx)
	}
	return r.pool.Begin(ctx)
}

// settle ends a statement's savepoint: released when the statement
// succeeded (or found no row), rolled back when it failed. err is returned
// as it was, unless ending the savepoint failed too.
func settle(ctx context.Context, sp pgx.Tx, err error) error {
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		if cerr := sp.Commit(ctx); cerr != nil {
			return errors.Join(err, cerr)
		}
		return err
	}
	if rerr := sp.Rollback(context.WithoutCancel(ctx)); rerr != nil && !errors.Is(rerr, pgx.ErrTxClosed) {
		return errors.Join(err, rerr)
	}
	return err
}

// savepointRow ends its savepoint when it is scanned.
type savepointRow struct {
	row pgx.Row
	sp  pgx.Tx
	ctx context.Context
}

func (r savepointRow) Scan(dest ...any) error {
	return settle(r.ctx, r.sp, r.row.Scan(dest...))
}

// errRow is a row that could not be read.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// savepointRows ends its savepoint when the rows are done: read to the end,
// or closed.
type savepointRows struct {
	pgx.Rows
	sp   pgx.Tx
	ctx  context.Context
	done bool
	err  error
}

func (r *savepointRows) finish() {
	if r.done {
		return
	}
	r.done = true
	r.Rows.Close()
	r.err = settle(r.ctx, r.sp, r.Rows.Err())
}

func (r *savepointRows) Next() bool {
	if r.done {
		return false
	}
	if r.Rows.Next() {
		return true
	}
	r.finish()
	return false
}

func (r *savepointRows) Close() { r.finish() }

func (r *savepointRows) Err() error {
	if r.done {
		return r.err
	}
	return r.Rows.Err()
}

// shared is the pool as the repositories built over it use it.
func (p *Pool) shared() routed { return routed{pool: p.pool} }
