package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// FXRepository implements application.FXRepository: the floating order book of the settlements' own
// moneys (migration 0130, docs/adr/0033 6.8).
type FXRepository struct{ q querier }

var _ application.FXRepository = (*FXRepository)(nil)

// NewFXRepository returns the repository over the pool, for callers outside a unit of work.
func NewFXRepository(p *Pool) *FXRepository { return &FXRepository{q: p.shared()} }

// FX returns the repository bound to this transaction.
func (t *tx) FX() application.FXRepository { return &FXRepository{q: t.q} }

const fxOrderColumns = `id::text, no, settlement_id::text, owner_kind, owner_id::text, side, kind, quantity, filled, price,
	notional_micro, escrow_left, fee_bps, status, created_at, expires_at, closed_at`

func scanFXOrder(row pgx.Row) (application.FXOrder, error) {
	var o application.FXOrder
	err := row.Scan(&o.ID, &o.No, &o.SettlementID, &o.Owner.Kind, &o.Owner.ID, &o.Side, &o.Kind, &o.Quantity, &o.Filled, &o.Price,
		&o.NotionalMicro, &o.EscrowLeft, &o.FeeBPS, &o.Status, &o.CreatedAt, &o.ExpiresAt, &o.ClosedAt)
	o.CreatedAt, o.ExpiresAt = o.CreatedAt.UTC(), o.ExpiresAt.UTC()
	o.ClosedAt = utcPtr(o.ClosedAt)
	return o, err
}

// InsertOrder writes a new order.
func (r *FXRepository) InsertOrder(ctx context.Context, o application.FXOrder) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO fx_orders (id, settlement_id, owner_kind, owner_id, side, kind, quantity, filled, price,
		notional_micro, escrow_left, fee_bps, status, created_at, expires_at, closed_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		o.ID, o.SettlementID, o.Owner.Kind, o.Owner.ID, o.Side, o.Kind, o.Quantity, o.Filled, o.Price, o.NotionalMicro, o.EscrowLeft,
		o.FeeBPS, o.Status, o.CreatedAt.UTC(), o.ExpiresAt.UTC(), o.ClosedAt); err != nil {
		return fmt.Errorf("postgres: placing an order on the book: %w", err)
	}
	return nil
}

// SaveOrder writes an order's fills, escrow, status and close time.
func (r *FXRepository) SaveOrder(ctx context.Context, o application.FXOrder) error {
	if _, err := r.q.Exec(ctx, `UPDATE fx_orders SET filled = $2, notional_micro = $3, escrow_left = $4, status = $5, closed_at = $6
		WHERE id = $1::uuid`, o.ID, o.Filled, o.NotionalMicro, o.EscrowLeft, o.Status, o.ClosedAt); err != nil {
		return fmt.Errorf("postgres: saving an order of the book: %w", err)
	}
	return nil
}

// OrderByID reads an order.
func (r *FXRepository) OrderByID(ctx context.Context, id string, lock bool) (*application.FXOrder, error) {
	sql := `SELECT ` + fxOrderColumns + ` FROM fx_orders WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	o, err := scanFXOrder(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUIDText(err) {
		return nil, application.ErrFXNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an order of the book: %w", err)
	}
	return &o, nil
}

func (r *FXRepository) list(ctx context.Context, sql string, args ...any) ([]application.FXOrder, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing orders of the book: %w", err)
	}
	defer rows.Close()
	var out []application.FXOrder
	for rows.Next() {
		o, err := scanFXOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an order of the book: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OpenOrders lists a currency's open orders of one side, best price first then oldest.
func (r *FXRepository) OpenOrders(ctx context.Context, settlementID, side string, limit int) ([]application.FXOrder, error) {
	order := "price ASC"
	if side == application.FXBuy {
		order = "price DESC"
	}
	return r.list(ctx, `SELECT `+fxOrderColumns+` FROM fx_orders WHERE settlement_id = $1::uuid AND side = $2 AND status = 'open'
		ORDER BY `+order+`, created_at, id LIMIT $3`, settlementID, side, limit)
}

// OpenCount counts an owner's open orders in a currency.
func (r *FXRepository) OpenCount(ctx context.Context, settlementID string, owner application.FXOwner) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM fx_orders WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND status = 'open'`,
		settlementID, owner.ID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting an owner's open orders: %w", err)
	}
	return n, nil
}

// OrdersOf lists an owner's orders in a currency, open ones first then newest.
func (r *FXRepository) OrdersOf(ctx context.Context, settlementID string, owner application.FXOwner, limit int) ([]application.FXOrder, error) {
	return r.list(ctx, `SELECT `+fxOrderColumns+` FROM fx_orders WHERE settlement_id = $1::uuid AND owner_id = $2::uuid
		ORDER BY (status = 'open') DESC, created_at DESC LIMIT $3`, settlementID, owner.ID, limit)
}

// ExpiredOpen lists open orders past their expiry.
func (r *FXRepository) ExpiredOpen(ctx context.Context, settlementID string, now time.Time, limit int) ([]application.FXOrder, error) {
	return r.list(ctx, `SELECT `+fxOrderColumns+` FROM fx_orders WHERE settlement_id = $1::uuid AND status = 'open' AND expires_at <= $2
		ORDER BY expires_at, id LIMIT $3 FOR UPDATE`, settlementID, now.UTC(), limit)
}

// Depth aggregates open orders of one side by price, best first.
func (r *FXRepository) Depth(ctx context.Context, settlementID, side string, levels int) ([]application.FXLevel, error) {
	order := "price ASC"
	if side == application.FXBuy {
		order = "price DESC"
	}
	rows, err := r.q.Query(ctx, `SELECT price, SUM(quantity - filled)::bigint, count(*) FROM fx_orders
		WHERE settlement_id = $1::uuid AND side = $2 AND status = 'open' GROUP BY price ORDER BY `+order+` LIMIT $3`,
		settlementID, side, levels)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the book's depth: %w", err)
	}
	defer rows.Close()
	var out []application.FXLevel
	for rows.Next() {
		var l application.FXLevel
		if err := rows.Scan(&l.Price, &l.Units, &l.Orders); err != nil {
			return nil, fmt.Errorf("postgres: reading a level of the book: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// InsertTrade writes a fill.
func (r *FXRepository) InsertTrade(ctx context.Context, t application.FXTrade) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO fx_trades (id, settlement_id, buy_order_id, sell_order_id, buyer_kind, buyer_id, seller_kind,
		seller_id, quantity, price, sup_amount, sup_fee, units_fee, sup_transaction_id, vc_transaction_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8::uuid, $9, $10, $11, $12, $13, $14::uuid, $15::uuid, $16)`,
		t.ID, t.SettlementID, t.BuyOrderID, t.SellOrderID, t.Buyer.Kind, t.Buyer.ID, t.Seller.Kind, t.Seller.ID, t.Quantity, t.Price,
		t.SUP, t.SUPFee, t.UnitsFee, nullText(t.SUPTransactionID), t.VCTransactionID, t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a fill: %w", err)
	}
	return nil
}

// Trades lists a currency's latest fills.
func (r *FXRepository) Trades(ctx context.Context, settlementID string, limit int) ([]application.FXTrade, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, settlement_id::text, buy_order_id::text, sell_order_id::text, buyer_kind, buyer_id::text,
		seller_kind, seller_id::text, quantity, price, sup_amount, sup_fee, units_fee, COALESCE(sup_transaction_id::text, ''),
		vc_transaction_id::text, created_at FROM fx_trades WHERE settlement_id = $1::uuid ORDER BY created_at DESC, id LIMIT $2`,
		settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the fills: %w", err)
	}
	defer rows.Close()
	var out []application.FXTrade
	for rows.Next() {
		var t application.FXTrade
		if err := rows.Scan(&t.ID, &t.SettlementID, &t.BuyOrderID, &t.SellOrderID, &t.Buyer.Kind, &t.Buyer.ID, &t.Seller.Kind, &t.Seller.ID,
			&t.Quantity, &t.Price, &t.SUP, &t.SUPFee, &t.UnitsFee, &t.SUPTransactionID, &t.VCTransactionID, &t.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a fill: %w", err)
		}
		t.At = t.At.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// Stats sums the fills of a currency in [from, to).
func (r *FXRepository) Stats(ctx context.Context, settlementID string, from, to time.Time) (application.FXStats, error) {
	var s application.FXStats
	if err := r.q.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(quantity), 0)::bigint, COALESCE(SUM(quantity * price), 0)::bigint
		FROM fx_trades WHERE settlement_id = $1::uuid AND created_at >= $2 AND created_at < $3`,
		settlementID, from.UTC(), to.UTC()).Scan(&s.Trades, &s.Units, &s.NotionalMicro); err != nil {
		return s, fmt.Errorf("postgres: summing the fills: %w", err)
	}
	return s, nil
}

// Clock returns the book's clock, locked, starting it at period 1.
func (r *FXRepository) Clock(ctx context.Context, now time.Time) (*application.FXClock, error) {
	if _, err := r.q.Exec(ctx, `INSERT INTO fx_clock (id, period_no, period_started_at, updated_at)
		VALUES (1, 1, $1, $1) ON CONFLICT (id) DO NOTHING`, now.UTC()); err != nil {
		return nil, fmt.Errorf("postgres: opening the book's clock: %w", err)
	}
	var c application.FXClock
	if err := r.q.QueryRow(ctx, `SELECT period_no, period_started_at, next_at, COALESCE(action_id::text, ''), updated_at
		FROM fx_clock WHERE id = 1 FOR UPDATE`).Scan(&c.PeriodNo, &c.PeriodStartedAt, &c.NextAt, &c.ActionID, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("postgres: reading the book's clock: %w", err)
	}
	c.PeriodStartedAt, c.UpdatedAt, c.NextAt = c.PeriodStartedAt.UTC(), c.UpdatedAt.UTC(), utcPtr(c.NextAt)
	return &c, nil
}

// SaveClock writes the clock.
func (r *FXRepository) SaveClock(ctx context.Context, c application.FXClock) error {
	if _, err := r.q.Exec(ctx, `UPDATE fx_clock SET period_no = $1, period_started_at = $2, next_at = $3, action_id = $4::uuid,
		updated_at = $5 WHERE id = 1`, c.PeriodNo, c.PeriodStartedAt.UTC(), c.NextAt, nullText(c.ActionID), c.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving the book's clock: %w", err)
	}
	return nil
}

// RecordRate writes a period's reading, once per currency and period.
func (r *FXRepository) RecordRate(ctx context.Context, h application.FXRate) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO fx_rate_history (settlement_id, period_no, trades, volume_units, notional_micro, value_ppm,
		window_trades, x_ref_before, x_ref_after, created_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT DO NOTHING`, h.SettlementID, h.PeriodNo, h.Trades, h.VolumeUnits, h.NotionalMicro, h.ValuePPM, h.WindowTrades,
		h.XRefBefore, h.XRefAfter, h.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a rate reading: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Rates lists a currency's readings, newest first.
func (r *FXRepository) Rates(ctx context.Context, settlementID string, limit int) ([]application.FXRate, error) {
	rows, err := r.q.Query(ctx, `SELECT settlement_id::text, period_no, trades, volume_units, notional_micro, value_ppm, window_trades,
		x_ref_before, x_ref_after, created_at FROM fx_rate_history WHERE settlement_id = $1::uuid ORDER BY period_no DESC LIMIT $2`,
		settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the rate history: %w", err)
	}
	defer rows.Close()
	var out []application.FXRate
	for rows.Next() {
		var h application.FXRate
		if err := rows.Scan(&h.SettlementID, &h.PeriodNo, &h.Trades, &h.VolumeUnits, &h.NotionalMicro, &h.ValuePPM, &h.WindowTrades,
			&h.XRefBefore, &h.XRefAfter, &h.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a rate reading: %w", err)
		}
		h.At = h.At.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetXRef sets the reference rate of a chartered currency.
func (r *FXRepository) SetXRef(ctx context.Context, settlementID string, xRefPPM int64) error {
	if _, err := r.q.Exec(ctx, `UPDATE village_currency_state SET x_ref_ppm = $2 WHERE settlement_id = $1::uuid`, settlementID, xRefPPM); err != nil {
		return fmt.Errorf("postgres: moving the reference rate: %w", err)
	}
	return nil
}
