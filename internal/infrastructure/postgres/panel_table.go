package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The web panel's catalogue of lists (internal/panel) reads through one
// function: a statement the panel wrote, run in a READ ONLY transaction
// with a statement timeout, its rows handed back as plain values. The
// statements are constants of the panel's catalogue; nothing an operator
// types is ever spliced into them, only bound as parameters.

// PanelTable is a read's rows: the column names and, for each row, one
// value per column (string, int64, bool, float64, time.Time, a decoded JSON
// value, a list, or nil).
type PanelTable struct {
	Columns []string
	Rows    [][]any
}

// ErrPanelReadEmpty is a panel read given nothing to run.
var ErrPanelReadEmpty = errors.New("postgres: a panel read needs a statement")

// ErrPanelReadInCommand is a panel read attempted inside a command's unit
// of work, which it must never be: it opens a transaction of its own.
var ErrPanelReadInCommand = errors.New("postgres: a panel read must not run inside a command")

// PanelRead runs one read-only statement with a timeout. It opens its own
// read-only transaction, so the statement cannot change anything even by
// mistake, and a slow one is cancelled by the server before it can weigh on
// the game.
func (a *EconomyAdmin) PanelRead(ctx context.Context, timeout time.Duration, sql string, args ...any) (PanelTable, error) {
	var t PanelTable
	if sql == "" {
		return t, ErrPanelReadEmpty
	}
	if InTransaction(ctx) {
		return t, ErrPanelReadInCommand
	}
	tx, err := a.begin(ctx)
	if err != nil {
		return t, fmt.Errorf("postgres: panel read: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
		return t, fmt.Errorf("postgres: panel read: read only: %w", err)
	}
	if timeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`, timeout.Milliseconds())); err != nil {
			return t, fmt.Errorf("postgres: panel read: timeout: %w", err)
		}
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return t, fmt.Errorf("postgres: panel read: %w", err)
	}
	defer rows.Close()
	for _, f := range rows.FieldDescriptions() {
		t.Columns = append(t.Columns, f.Name)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return t, fmt.Errorf("postgres: panel read: %w", err)
		}
		for i, v := range vals {
			vals[i] = plainValue(v)
		}
		t.Rows = append(t.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return t, fmt.Errorf("postgres: panel read: %w", err)
	}
	return t, nil
}

// begin opens the read's transaction on the routed pool.
func (a *EconomyAdmin) begin(ctx context.Context) (pgx.Tx, error) {
	r, ok := a.q.(routed)
	if !ok {
		return nil, errors.New("postgres: panel reads need the pool")
	}
	return r.Begin(ctx)
}

// plainValue turns what the driver decodes into something JSON and CSV can
// carry: a uuid as its text, a numeric as a whole number (or a float when it
// is not one), a list element by element.
func plainValue(v any) any {
	switch x := v.(type) {
	case [16]byte:
		return formatUUID(x)
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		if i, err := x.Int64Value(); err == nil && i.Valid {
			return i.Int64
		}
		if f, err := x.Float64Value(); err == nil && f.Valid {
			return f.Float64
		}
		return nil
	case *big.Int:
		return x.String()
	case pgtype.Interval:
		if !x.Valid {
			return nil
		}
		return x.Microseconds/1_000_000 + int64(x.Days)*86400 + int64(x.Months)*30*86400
	case time.Time:
		return x.UTC()
	case []any:
		for i := range x {
			x[i] = plainValue(x[i])
		}
		return x
	case int32:
		return int64(x)
	case int16:
		return int64(x)
	case float32:
		return float64(x)
	}
	return v
}
