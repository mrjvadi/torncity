package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Test doubles for the phase 1 repositories.
//
// They exist for one purpose: the repositories translate what the server says
// into what the application understands, and that translation is the part a
// live database cannot exercise on demand. Provoking a real 23505 with the
// wrong constraint name, or a driver failure between two statements of a
// transaction, means arranging a schema that misbehaves. Handing the
// repository the exact error instead pins the mapping directly.
//
// Nothing here simulates PostgreSQL. Every claim about locking, atomicity and
// uniqueness is proved against a real server in tests/ under the integration
// build tag; these doubles only stand in for the wire.

// assign copies val into the pointer dest, the way a driver's Scan would.
//
// reflect rather than a type switch because the destinations include **string
// and **time.Time — the repositories scan NULL-able columns into pointer
// variables — and spelling every combination out by hand would drift the first
// time a column changes shape.
func assign(dest, val any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("scan destination %T is not a non-nil pointer", dest)
	}
	elem := dv.Elem()

	// A nil value is a SQL NULL: the destination keeps its zero value, which
	// for a pointer destination is the nil the repository then checks for.
	if val == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}

	vv := reflect.ValueOf(val)
	if !vv.Type().AssignableTo(elem.Type()) {
		return fmt.Errorf("cannot scan %T into %s", val, elem.Type())
	}
	elem.Set(vv)
	return nil
}

// fakeRow is a single-row result: either an error or a set of column values.
type fakeRow struct {
	err  error
	vals []any
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.vals) {
		return fmt.Errorf("scan wants %d columns, the fake row has %d", len(dest), len(r.vals))
	}
	for i := range dest {
		if err := assign(dest[i], r.vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// fakeRows is a multi-row result. pgx.Rows is embedded so only the four
// methods the repositories actually call have to be written; anything else
// would panic, which is the honest outcome for a call this double was never
// meant to answer.
type fakeRows struct {
	pgx.Rows

	vals [][]any
	next int

	scanErr error
	err     error

	closed bool
}

func (r *fakeRows) Next() bool {
	if r.next >= len(r.vals) {
		return false
	}
	r.next++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	return fakeRow{vals: r.vals[r.next-1]}.Scan(dest...)
}

func (r *fakeRows) Err() error { return r.err }

func (r *fakeRows) Close() { r.closed = true }

// call records one statement a repository sent.
type call struct {
	sql  string
	args []any
}

// fakeQuerier answers from canned results and records every statement.
//
// rowFunc exists for the one case a canned result cannot cover: a statement
// whose answer depends on an argument the repository generated itself, such as
// the friendship insert that decides "this row is new" by comparing the
// returned id with the one it just minted.
type fakeQuerier struct {
	execTag  pgconn.CommandTag
	execErr  error
	rowErr   error
	rowVals  []any
	rowFunc  func(sql string, args []any) fakeRow
	rows     *fakeRows
	queryErr error

	calls []call
}

func (q *fakeQuerier) record(sql string, args []any) {
	q.calls = append(q.calls, call{sql: sql, args: args})
}

// last returns the most recent statement, failing the caller's expectations
// loudly rather than panicking on an empty slice.
func (q *fakeQuerier) last() call {
	if len(q.calls) == 0 {
		return call{}
	}
	return q.calls[len(q.calls)-1]
}

func (q *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.record(sql, args)
	return q.execTag, q.execErr
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.record(sql, args)
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	if q.rows == nil {
		return &fakeRows{}, nil
	}
	return q.rows, nil
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.record(sql, args)
	if q.rowFunc != nil {
		return q.rowFunc(sql, args)
	}
	return fakeRow{err: q.rowErr, vals: q.rowVals}
}

// fakeTx stands in for a transaction. Like fakeRows it embeds the real
// interface, so a repository that reached for CopyFrom or SendBatch inside one
// of these operations would be caught rather than quietly tolerated.
type fakeTx struct {
	pgx.Tx

	rowErr  error
	rowVals []any
	execTag pgconn.CommandTag
	execErr error

	calls []call

	committed  bool
	rolledBack bool
	commitErr  error
}

func (t *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	t.calls = append(t.calls, call{sql: sql, args: args})
	return fakeRow{err: t.rowErr, vals: t.rowVals}
}

func (t *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.calls = append(t.calls, call{sql: sql, args: args})
	return t.execTag, t.execErr
}

func (t *fakeTx) Commit(context.Context) error {
	t.committed = true
	return t.commitErr
}

func (t *fakeTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

// fakeTransactor is a querier that can also open a transaction, which is what
// TravelRepository and FriendshipRepository are built on.
type fakeTransactor struct {
	fakeQuerier

	tx       *fakeTx
	beginErr error
}

func (t *fakeTransactor) Begin(context.Context) (pgx.Tx, error) {
	if t.beginErr != nil {
		return nil, t.beginErr
	}
	if t.tx == nil {
		t.tx = &fakeTx{}
	}
	return t.tx, nil
}

// nowForTest is a fixed instant. A literal is used rather than time.Now so
// that an assertion on a written timestamp cannot depend on when the test ran.
func nowForTest() time.Time {
	return time.Date(2024, time.March, 1, 12, 0, 0, 0, time.UTC)
}

// assertTimestampArgument checks that the repository supplied a real instant
// for a NOT NULL timestamptz column the caller left empty. The schema has no
// DEFAULT now() anywhere, so a zero value would be stored as year 1.
func assertTimestampArgument(t *testing.T, args []any, index int, what string) {
	t.Helper()

	if index >= len(args) {
		t.Fatalf("%s sent %d arguments, so there is no argument %d", what, len(args), index)
	}
	ts, ok := args[index].(time.Time)
	if !ok {
		t.Fatalf("%s sent %#v as its timestamp argument, want a time.Time", what, args[index])
	}
	if ts.IsZero() {
		t.Errorf("%s wrote a zero timestamp, which the schema stores as year 1", what)
	}
}

// The doubles must satisfy the very interfaces the repositories declare;
// otherwise these tests would be proving something about a different shape.
var (
	_ querier    = (*fakeQuerier)(nil)
	_ transactor = (*fakeTransactor)(nil)
	_ pgx.Row    = fakeRow{}
	_ pgx.Rows   = (*fakeRows)(nil)
	_ pgx.Tx     = (*fakeTx)(nil)
)
