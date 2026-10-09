package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// TradeRepository is the market day's orders and days (migration 0136), bound to one transaction.
type TradeRepository struct{ q querier }

var _ application.TradeRepository = (*TradeRepository)(nil)

// Orders lists the settlement's standing orders by item.
func (r *TradeRepository) Orders(ctx context.Context, settlementID string) ([]application.TradeOrder, error) {
	rows, err := r.q.Query(ctx, `SELECT item, keep, on_sale FROM trade_orders WHERE settlement_id = $1::uuid ORDER BY item`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing trade orders: %w", err)
	}
	defer rows.Close()
	var out []application.TradeOrder
	for rows.Next() {
		var o application.TradeOrder
		if err := rows.Scan(&o.Item, &o.Keep, &o.OnSale); err != nil {
			return nil, fmt.Errorf("postgres: scanning a trade order: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// SetOrder writes an order (an upsert: the last word of the head stands).
func (r *TradeRepository) SetOrder(ctx context.Context, settlementID string, o application.TradeOrder, by string, at time.Time) error {
	var who any
	if by != "" {
		who = by
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO trade_orders (settlement_id, item, keep, on_sale, updated_by, updated_at) VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6)
		 ON CONFLICT (settlement_id, item) DO UPDATE SET keep = EXCLUDED.keep, on_sale = EXCLUDED.on_sale, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`,
		settlementID, o.Item, o.Keep, o.OnSale, who, at.UTC()); err != nil {
		return fmt.Errorf("postgres: setting a trade order: %w", err)
	}
	return nil
}

const tradeDayColumns = `settlement_id::text, day, outcome, residents, cap, gross, wage, COALESCE(sale_tx::text, ''), COALESCE(wage_tx::text, ''), at`

func scanTradeDay(row pgx.Row) (*application.TradeDay, error) {
	var d application.TradeDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Outcome, &d.Residents, &d.Cap, &d.Gross, &d.Wage, &d.SaleTx, &d.WageTx, &d.At); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: reading a trade day: %w", err)
	}
	return &d, nil
}

func (r *TradeRepository) withLines(ctx context.Context, d *application.TradeDay, err error) (*application.TradeDay, error) {
	if err != nil || d == nil {
		return d, err
	}
	rows, err := r.q.Query(ctx, `SELECT item, qty, unit_price, reference_price FROM trade_day_lines WHERE settlement_id = $1::uuid AND day = $2 ORDER BY item`, d.SettlementID, d.Day)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading trade lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l application.TradeLine
		if err := rows.Scan(&l.Item, &l.Qty, &l.UnitPrice, &l.ReferencePrice); err != nil {
			return nil, fmt.Errorf("postgres: scanning a trade line: %w", err)
		}
		d.Lines = append(d.Lines, l)
	}
	return d, rows.Err()
}

// Day returns one day with its lines, nil for none.
func (r *TradeRepository) Day(ctx context.Context, settlementID string, day int64) (*application.TradeDay, error) {
	d, err := scanTradeDay(r.q.QueryRow(ctx, `SELECT `+tradeDayColumns+` FROM trade_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
	return r.withLines(ctx, d, err)
}

// Last returns the latest judged day, nil for none.
func (r *TradeRepository) Last(ctx context.Context, settlementID string) (*application.TradeDay, error) {
	d, err := scanTradeDay(r.q.QueryRow(ctx, `SELECT `+tradeDayColumns+` FROM trade_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
	return r.withLines(ctx, d, err)
}

// RecordDay is the fence of a market day: the row, then its lines.
func (r *TradeRepository) RecordDay(ctx context.Context, d application.TradeDay) (bool, error) {
	var sale, wage any
	if d.SaleTx != "" {
		sale = d.SaleTx
	}
	if d.WageTx != "" {
		wage = d.WageTx
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO trade_days (settlement_id, day, outcome, residents, cap, gross, wage, sale_tx, wage_tx, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::uuid, $9::uuid, $10) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, d.Outcome, d.Residents, d.Cap, d.Gross, d.Wage, sale, wage, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a trade day: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	for _, l := range d.Lines {
		if _, err := r.q.Exec(ctx,
			`INSERT INTO trade_day_lines (settlement_id, day, item, qty, unit_price, reference_price) VALUES ($1::uuid, $2, $3, $4, $5, $6)`,
			d.SettlementID, d.Day, l.Item, l.Qty, l.UnitPrice, l.ReferencePrice); err != nil {
			return false, fmt.Errorf("postgres: recording a trade line: %w", err)
		}
	}
	return true, nil
}
