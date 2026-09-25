package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the stock exchange (migrations/0029_finance): a
// company's listing, its holders (company_shareholders, with the shares
// locked in sell orders and what they cost), its order book, its trades and
// its dividends.

// StockRepository implements application.StockRepository.
type StockRepository struct {
	q querier
}

var _ application.StockRepository = (*StockRepository)(nil)

// Listed lists the listed companies with their prices and volume.
func (r *StockRepository) Listed(ctx context.Context, since time.Time) ([]application.ListedCompany, error) {
	rows, err := r.q.Query(ctx, `
		SELECT `+companyColumnsC+`,
		       COALESCE((SELECT unit_price FROM share_trades t WHERE t.company_id = c.id
		                  ORDER BY created_at DESC, id DESC LIMIT 1), 0),
		       COALESCE((SELECT unit_price FROM share_trades t WHERE t.company_id = c.id
		                  ORDER BY created_at DESC, id DESC OFFSET 1 LIMIT 1), 0),
		       COALESCE((SELECT SUM(quantity) FROM share_trades t WHERE t.company_id = c.id AND t.created_at >= $1), 0)::bigint,
		       COALESCE(b.book, 0)::bigint, l.price
		  FROM companies c
		  JOIN stock_listings l ON l.company_id = c.id
		  LEFT JOIN (`+companyBookSQL+`) b ON b.id = c.id
		 WHERE c.status = 'active'
		 ORDER BY c.listed_at, c.id`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the exchange: %w", err)
	}
	defer rows.Close()
	var out []application.ListedCompany
	for rows.Next() {
		var l application.ListedCompany
		c := &l.Company
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason,
			&l.LastPrice, &l.PrevPrice, &l.Volume, &l.Book, &l.IPOPrice); err != nil {
			return nil, fmt.Errorf("postgres: scanning a listed company: %w", err)
		}
		c.FoundedAt, c.UpdatedAt, c.ClosedAt = c.FoundedAt.UTC(), c.UpdatedAt.UTC(), utcPtr(c.ClosedAt)
		out = append(out, l)
	}
	return out, rows.Err()
}

// Listing reads a company's listing.
func (r *StockRepository) Listing(ctx context.Context, companyID string) (*application.StockListing, error) {
	if !validUUID(companyID) {
		return nil, nil
	}
	l := application.StockListing{CompanyID: companyID}
	err := r.q.QueryRow(ctx, `SELECT listed_by::text, float_shares, price, book_per_share, fee,
		COALESCE(ledger_transaction_id::text, ''), listed_at FROM stock_listings WHERE company_id = $1::uuid`, companyID).
		Scan(&l.ListedBy, &l.FloatShares, &l.Price, &l.BookPerShare, &l.Fee, &l.LedgerTx, &l.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	l.At = l.At.UTC()
	return &l, nil
}

// RecordListing records a listing and marks the company listed.
func (r *StockRepository) RecordListing(ctx context.Context, l application.StockListing) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO stock_listings (company_id, listed_by, float_shares, price, book_per_share,
		fee, ledger_transaction_id, listed_at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8)`,
		l.CompanyID, l.ListedBy, l.FloatShares, l.Price, l.BookPerShare, l.Fee, nullText(l.LedgerTx), l.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a listing: %w", err)
	}
	if _, err := r.q.Exec(ctx, `UPDATE companies SET listed_at = $2, updated_at = $2 WHERE id = $1::uuid`,
		l.CompanyID, l.At.UTC()); err != nil {
		return fmt.Errorf("postgres: marking a company listed: %w", err)
	}
	return nil
}

// Book is a company's book value.
func (r *StockRepository) Book(ctx context.Context, companyID string) (int64, error) {
	var v int64
	err := r.q.QueryRow(ctx, `SELECT book::bigint FROM (`+companyBookSQL+`) b WHERE b.id = $1::uuid`, companyID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: valuing a company: %w", err)
	}
	return v, nil
}

// Revenue is what a company's settled periods earned.
func (r *StockRepository) Revenue(ctx context.Context, companyID string) (int64, error) {
	var v int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE(SUM(revenue), 0)::bigint FROM company_periods WHERE company_id = $1::uuid`,
		companyID).Scan(&v); err != nil {
		return 0, fmt.Errorf("postgres: reading a company's revenue: %w", err)
	}
	return v, nil
}

func scanHolding(row pgx.Row) (application.ShareHolding, error) {
	var h application.ShareHolding
	err := row.Scan(&h.CompanyID, &h.PlayerID, &h.Shares, &h.Locked, &h.Cost, &h.AcquiredAt)
	h.AcquiredAt = h.AcquiredAt.UTC()
	return h, err
}

const holdingColumns = `company_id::text, player_id::text, shares, locked, cost, acquired_at`

// Holding reads a holding, locked.
func (r *StockRepository) Holding(ctx context.Context, companyID, playerID string) (application.ShareHolding, error) {
	h, err := scanHolding(r.q.QueryRow(ctx, `SELECT `+holdingColumns+` FROM company_shareholders
		WHERE company_id = $1::uuid AND player_id = $2::uuid FOR UPDATE`, companyID, playerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ShareHolding{CompanyID: companyID, PlayerID: playerID}, nil
	}
	if err != nil {
		return h, fmt.Errorf("postgres: reading a holding: %w", err)
	}
	return h, nil
}

// Holdings lists a company's holders, locked.
func (r *StockRepository) Holdings(ctx context.Context, companyID string) ([]application.ShareHolding, error) {
	rows, err := r.q.Query(ctx, `SELECT `+holdingColumns+` FROM company_shareholders WHERE company_id = $1::uuid
		ORDER BY player_id FOR UPDATE`, companyID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing holders: %w", err)
	}
	defer rows.Close()
	var out []application.ShareHolding
	for rows.Next() {
		h, err := scanHolding(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a holder: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HoldingsOf lists a player's holdings with their companies.
func (r *StockRepository) HoldingsOf(ctx context.Context, playerID string) ([]application.HoldingLine, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	rows, err := r.q.Query(ctx, `
		SELECT s.company_id::text, s.player_id::text, s.shares, s.locked, s.cost, s.acquired_at, `+companyColumnsC+`,
		       c.listed_at IS NOT NULL,
		       COALESCE((SELECT unit_price FROM share_trades t WHERE t.company_id = c.id
		                  ORDER BY created_at DESC, id DESC LIMIT 1), 0),
		       COALESCE(b.book, 0)::bigint
		  FROM company_shareholders s
		  JOIN companies c ON c.id = s.company_id
		  LEFT JOIN (`+companyBookSQL+`) b ON b.id = c.id
		 WHERE s.player_id = $1::uuid AND c.status = 'active'
		 ORDER BY s.acquired_at, c.id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing holdings: %w", err)
	}
	defer rows.Close()
	var out []application.HoldingLine
	for rows.Next() {
		var l application.HoldingLine
		h, c := &l.Holding, &l.Company
		if err := rows.Scan(&h.CompanyID, &h.PlayerID, &h.Shares, &h.Locked, &h.Cost, &h.AcquiredAt,
			&c.ID, &c.Code, &c.Name, &c.NameKey, &c.TypeCode, &c.CityID, &c.OwnerID,
			&c.ManagerID, &c.Status, &c.PriceBPS, &c.AutoAccept, &c.TotalShares, &c.Debt, &c.Arrears,
			&c.RatingBPS, &c.RegistrationFee, &c.RegistrationTransactionID, &c.ContentVersion,
			&c.FoundedAt, &c.UpdatedAt, &c.ClosedAt, &c.CloseReason, &l.Listed, &l.LastPrice, &l.Book); err != nil {
			return nil, fmt.Errorf("postgres: scanning a holding: %w", err)
		}
		h.AcquiredAt, c.FoundedAt, c.UpdatedAt, c.ClosedAt = h.AcquiredAt.UTC(), c.FoundedAt.UTC(), c.UpdatedAt.UTC(),
			utcPtr(c.ClosedAt)
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveHolding writes a holding; one of no shares is removed.
func (r *StockRepository) SaveHolding(ctx context.Context, h application.ShareHolding) error {
	if h.Shares <= 0 {
		if _, err := r.q.Exec(ctx, `DELETE FROM company_shareholders WHERE company_id = $1::uuid AND player_id = $2::uuid`,
			h.CompanyID, h.PlayerID); err != nil {
			return fmt.Errorf("postgres: removing a holding: %w", err)
		}
		return nil
	}
	if _, err := r.q.Exec(ctx, `INSERT INTO company_shareholders (company_id, player_id, shares, locked, cost, acquired_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6) ON CONFLICT (company_id, player_id) DO UPDATE
		SET shares = EXCLUDED.shares, locked = EXCLUDED.locked, cost = EXCLUDED.cost`,
		h.CompanyID, h.PlayerID, h.Shares, h.Locked, h.Cost, h.AcquiredAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a holding: %w", err)
	}
	return nil
}

// TransferControl makes a player the owner of a company.
func (r *StockRepository) TransferControl(ctx context.Context, companyID, playerID string, at time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE companies SET owner_player_id = $2::uuid,
		manager_player_id = CASE WHEN manager_player_id = $2::uuid THEN NULL ELSE manager_player_id END,
		updated_at = $3 WHERE id = $1::uuid`, companyID, playerID, at.UTC()); err != nil {
		return fmt.Errorf("postgres: passing control of a company: %w", err)
	}
	return nil
}

const shareOrderColumns = `id::text, no, company_id::text, side, quantity, filled, unit_price, owner_id::text, status,
	created_at, expires_at, closed_at`

func scanShareOrder(row pgx.Row) (*application.ShareOrder, error) {
	var o application.ShareOrder
	if err := row.Scan(&o.ID, &o.No, &o.CompanyID, &o.Side, &o.Qty, &o.Filled, &o.Price, &o.OwnerID, &o.Status,
		&o.CreatedAt, &o.ExpiresAt, &o.ClosedAt); err != nil {
		return nil, err
	}
	o.CreatedAt, o.ExpiresAt, o.ClosedAt = o.CreatedAt.UTC(), o.ExpiresAt.UTC(), utcPtr(o.ClosedAt)
	return &o, nil
}

func (r *StockRepository) orders(ctx context.Context, sql string, args ...any) ([]application.ShareOrder, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing share orders: %w", err)
	}
	defer rows.Close()
	var out []application.ShareOrder
	for rows.Next() {
		o, err := scanShareOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a share order: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// PlaceOrder inserts an order.
func (r *StockRepository) PlaceOrder(ctx context.Context, o application.ShareOrder) (application.ShareOrder, error) {
	var closed any
	if o.ClosedAt != nil {
		closed = o.ClosedAt.UTC()
	}
	err := r.q.QueryRow(ctx, `INSERT INTO share_orders (id, company_id, side, quantity, filled, unit_price, owner_id,
		status, created_at, expires_at, closed_at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9, $10, $11)
		RETURNING no`, o.ID, o.CompanyID, o.Side, o.Qty, o.Filled, o.Price, o.OwnerID, o.Status, o.CreatedAt.UTC(),
		o.ExpiresAt.UTC(), closed).Scan(&o.No)
	if err != nil {
		return o, fmt.Errorf("postgres: placing a share order: %w", err)
	}
	return o, nil
}

// OpenOrders lists a company's open orders.
func (r *StockRepository) OpenOrders(ctx context.Context, companyID string) ([]application.ShareOrder, error) {
	return r.orders(ctx, `SELECT `+shareOrderColumns+` FROM share_orders WHERE company_id = $1::uuid AND status = 'open'
		ORDER BY created_at, id`, companyID)
}

// Order reads an order by number.
func (r *StockRepository) Order(ctx context.Context, no int64) (*application.ShareOrder, error) {
	o, err := scanShareOrder(r.q.QueryRow(ctx, `SELECT `+shareOrderColumns+` FROM share_orders WHERE no = $1`, no))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, application.ErrShareOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a share order: %w", err)
	}
	return o, nil
}

// UpdateOrder writes an order's fill and status.
func (r *StockRepository) UpdateOrder(ctx context.Context, id string, filled int64, status string, closedAt *time.Time) error {
	if _, err := r.q.Exec(ctx, `UPDATE share_orders SET filled = $2, status = $3, closed_at = $4 WHERE id = $1::uuid`,
		id, filled, status, closedAt); err != nil {
		return fmt.Errorf("postgres: updating a share order: %w", err)
	}
	return nil
}

// OrdersOf lists a player's open orders.
func (r *StockRepository) OrdersOf(ctx context.Context, playerID string) ([]application.ShareOrder, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	return r.orders(ctx, `SELECT `+shareOrderColumns+` FROM share_orders WHERE owner_id = $1::uuid AND status = 'open'
		ORDER BY created_at DESC, id`, playerID)
}

// CountOpen counts a player's open orders.
func (r *StockRepository) CountOpen(ctx context.Context, playerID string) (int, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM share_orders WHERE owner_id = $1::uuid AND status = 'open'`,
		playerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting share orders: %w", err)
	}
	return n, nil
}

// ExpiredCompanies lists the companies with an expired open order.
func (r *StockRepository) ExpiredCompanies(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := r.q.Query(ctx, `SELECT DISTINCT company_id::text FROM share_orders WHERE status = 'open'
		AND expires_at <= $1 ORDER BY 1`, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: finding expired share orders: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecordTrade writes a trade.
func (r *StockRepository) RecordTrade(ctx context.Context, t application.ShareTrade) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO share_trades (id, company_id, buy_order_id, sell_order_id, buyer_id,
		seller_id, quantity, unit_price, notional, fee, ledger_transaction_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9, $10, $11::uuid, $12)`,
		t.ID, t.CompanyID, t.BuyOrder, t.SellOrder, t.Buyer, t.Seller, t.Qty, t.Price, t.Notional, t.Fee, t.LedgerTx,
		t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a share trade: %w", err)
	}
	return nil
}

// Trades lists a company's latest trades.
func (r *StockRepository) Trades(ctx context.Context, companyID string, limit int) ([]application.ShareTrade, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, company_id::text, buy_order_id::text, sell_order_id::text,
		buyer_id::text, seller_id::text, quantity, unit_price, notional, fee, ledger_transaction_id::text, created_at
		FROM share_trades WHERE company_id = $1::uuid ORDER BY created_at DESC, id DESC LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing share trades: %w", err)
	}
	defer rows.Close()
	var out []application.ShareTrade
	for rows.Next() {
		var t application.ShareTrade
		if err := rows.Scan(&t.ID, &t.CompanyID, &t.BuyOrder, &t.SellOrder, &t.Buyer, &t.Seller, &t.Qty, &t.Price,
			&t.Notional, &t.Fee, &t.LedgerTx, &t.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a share trade: %w", err)
		}
		t.At = t.At.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// LastPrice is a company's last trade price.
func (r *StockRepository) LastPrice(ctx context.Context, companyID string) (int64, error) {
	var p int64
	if err := r.q.QueryRow(ctx, `SELECT COALESCE((SELECT unit_price FROM share_trades WHERE company_id = $1::uuid
		ORDER BY created_at DESC, id DESC LIMIT 1), 0)`, companyID).Scan(&p); err != nil {
		return 0, fmt.Errorf("postgres: reading a last price: %w", err)
	}
	return p, nil
}

// PairTrades counts the trades between two players, each way.
func (r *StockRepository) PairTrades(ctx context.Context, companyID, a, b string, since time.Time) (int, int, error) {
	var ab, ba int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE seller_id = $2::uuid AND buyer_id = $3::uuid),
		count(*) FILTER (WHERE seller_id = $3::uuid AND buyer_id = $2::uuid)
		FROM share_trades WHERE company_id = $1::uuid AND created_at >= $4`, companyID, a, b, since.UTC()).Scan(&ab, &ba); err != nil {
		return 0, 0, fmt.Errorf("postgres: counting a pair's trades: %w", err)
	}
	return ab, ba, nil
}

// RecordDividend inserts a dividend.
func (r *StockRepository) RecordDividend(ctx context.Context, d application.Dividend) (application.Dividend, error) {
	err := r.q.QueryRow(ctx, `INSERT INTO dividends (id, company_id, declared_by, amount, tax, per_share, total_shares,
		paid, tax_transaction_id, declared_at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9::uuid, $10)
		RETURNING no`, d.ID, d.CompanyID, d.DeclaredBy, d.Amount, d.Tax, d.PerShare, d.TotalShares, d.Paid,
		nullText(d.TaxTx), d.At.UTC()).Scan(&d.No)
	if err != nil {
		return d, fmt.Errorf("postgres: recording a dividend: %w", err)
	}
	return d, nil
}

// RecordDividendPayment records one holder's part.
func (r *StockRepository) RecordDividendPayment(ctx context.Context, p application.DividendPayment) error {
	if _, err := r.q.Exec(ctx, `INSERT INTO dividend_payments (dividend_id, player_id, shares, amount,
		ledger_transaction_id) VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid)`,
		p.DividendID, p.PlayerID, p.Shares, p.Amount, p.LedgerTx); err != nil {
		return fmt.Errorf("postgres: recording a dividend payment: %w", err)
	}
	return nil
}

// Dividends lists a company's latest dividends.
func (r *StockRepository) Dividends(ctx context.Context, companyID string, limit int) ([]application.Dividend, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, no, company_id::text, declared_by::text, amount, tax, per_share,
		total_shares, paid, COALESCE(tax_transaction_id::text, ''), declared_at FROM dividends
		WHERE company_id = $1::uuid ORDER BY declared_at DESC, no DESC LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing dividends: %w", err)
	}
	defer rows.Close()
	var out []application.Dividend
	for rows.Next() {
		var d application.Dividend
		if err := rows.Scan(&d.ID, &d.No, &d.CompanyID, &d.DeclaredBy, &d.Amount, &d.Tax, &d.PerShare, &d.TotalShares,
			&d.Paid, &d.TaxTx, &d.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a dividend: %w", err)
		}
		d.At = d.At.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}
