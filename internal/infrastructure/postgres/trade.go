package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file implements the shops, the player market and the auction house
// over the tables of migrations/0017_items_and_trade.up.sql.

// ShopRepository implements application.ShopRepository.
type ShopRepository struct {
	q querier
}

var _ application.ShopRepository = (*ShopRepository)(nil)

// Shelf returns a shelf, locked, creating it on first use with a zero
// restock time: the rule then fills it.
func (r *ShopRepository) Shelf(ctx context.Context, cityID, shop, item string) (*application.ShopShelf, error) {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO shop_shelves (city_id, shop_code, item_code, stock, restocked_at)
		 VALUES ($1::uuid, $2, $3, 0, 'epoch') ON CONFLICT DO NOTHING`, cityID, shop, item); err != nil {
		if isInvalidUUIDText(err) {
			return nil, application.ErrCityNotFound
		}
		return nil, fmt.Errorf("postgres: opening a shelf: %w", err)
	}
	s := application.ShopShelf{CityID: cityID, Shop: shop, Item: item}
	var at time.Time
	if err := r.q.QueryRow(ctx,
		`SELECT stock, restocked_at FROM shop_shelves
		  WHERE city_id = $1::uuid AND shop_code = $2 AND item_code = $3 FOR UPDATE`, cityID, shop, item).
		Scan(&s.Stock, &at); err != nil {
		return nil, fmt.Errorf("postgres: reading a shelf: %w", err)
	}
	if at.After(time.Unix(0, 0)) {
		s.RestockedAt = at.UTC()
	}
	return &s, nil
}

// SaveShelf writes a shelf back.
func (r *ShopRepository) SaveShelf(ctx context.Context, s application.ShopShelf) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE shop_shelves SET stock = $4, restocked_at = $5
		  WHERE city_id = $1::uuid AND shop_code = $2 AND item_code = $3`,
		s.CityID, s.Shop, s.Item, s.Stock, s.RestockedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: saving a shelf: %w", err)
	}
	return nil
}

// RecentSales counts the units bought of a good at a shop since a moment.
func (r *ShopRepository) RecentSales(ctx context.Context, cityID, shop, item string, since time.Time) (int, error) {
	var n int64
	if err := r.q.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0) FROM shop_sales
		  WHERE city_id = $1::uuid AND shop_code = $2 AND item_code = $3 AND direction = 'buy' AND created_at >= $4`,
		cityID, shop, item, since.UTC()).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting sales: %w", err)
	}
	return int(n), nil
}

// RecordSale writes one sale.
func (r *ShopRepository) RecordSale(ctx context.Context, s application.ShopSale) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	var method *string
	if s.Method != "" {
		method = &s.Method
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO shop_sales (id, player_id, city_id, shop_code, item_code, direction, quantity, unit_price, total,
		                         tax, method, ledger_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12::uuid, $13)`,
		id, s.PlayerID, s.CityID, s.Shop, s.Item, s.Direction, s.Qty, s.UnitPrice, s.Total, s.Tax, method,
		nullableUUID(s.LedgerTransactionID), s.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a sale: %w", err)
	}
	return nil
}

// MarketRepository implements application.MarketRepository.
type MarketRepository struct {
	q querier
}

var _ application.MarketRepository = (*MarketRepository)(nil)

const orderColumns = `id::text, no, city_id::text, item_code, side, order_type, quantity, filled, unit_price,
       owner_id::text, COALESCE(funding, ''), status, COALESCE(game_action_id::text, ''), created_at, expires_at, closed_at`

func scanOrder(row pgx.Row) (*application.MarketOrder, error) {
	var o application.MarketOrder
	if err := row.Scan(&o.ID, &o.No, &o.CityID, &o.Item, &o.Side, &o.Kind, &o.Qty, &o.Filled, &o.Price,
		&o.OwnerID, &o.Funding, &o.Status, &o.GameActionID, &o.CreatedAt, &o.ExpiresAt, &o.ClosedAt); err != nil {
		return nil, err
	}
	o.CreatedAt, o.ExpiresAt, o.ClosedAt = o.CreatedAt.UTC(), o.ExpiresAt.UTC(), utcPtr(o.ClosedAt)
	return &o, nil
}

func collectOrders(rows pgx.Rows, err error) ([]application.MarketOrder, error) {
	if err != nil {
		return nil, fmt.Errorf("postgres: reading orders: %w", err)
	}
	defer rows.Close()
	var out []application.MarketOrder
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning an order: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// LockBook serialises one book.
func (r *MarketRepository) LockBook(ctx context.Context, cityID, item string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('book:' || $1 || ':' || $2))`, cityID, item); err != nil {
		return fmt.Errorf("postgres: locking a book: %w", err)
	}
	return nil
}

// OpenOrders returns a book's resting orders.
func (r *MarketRepository) OpenOrders(ctx context.Context, cityID, item string) ([]application.MarketOrder, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+orderColumns+` FROM market_orders
		  WHERE city_id = $1::uuid AND item_code = $2 AND status = 'open' ORDER BY created_at, id FOR UPDATE`, cityID, item)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	return collectOrders(rows, err)
}

// Order returns one order, locked.
func (r *MarketRepository) Order(ctx context.Context, id string) (*application.MarketOrder, error) {
	o, err := scanOrder(r.q.QueryRow(ctx, `SELECT `+orderColumns+` FROM market_orders WHERE id = $1::uuid FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrOrderNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an order: %w", err)
	}
	return o, nil
}

// OrderByNo returns one order by its public number, locked.
func (r *MarketRepository) OrderByNo(ctx context.Context, no int64) (*application.MarketOrder, error) {
	o, err := scanOrder(r.q.QueryRow(ctx, `SELECT `+orderColumns+` FROM market_orders WHERE no = $1 FOR UPDATE`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrOrderNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an order: %w", err)
	}
	return o, nil
}

// PlaceOrder inserts an order and returns it with its number.
func (r *MarketRepository) PlaceOrder(ctx context.Context, o application.MarketOrder) (application.MarketOrder, error) {
	var funding *string
	if o.Funding != "" {
		funding = &o.Funding
	}
	var closed *time.Time
	if o.Status != application.OrderOpen {
		t := o.CreatedAt.UTC()
		closed = &t
	}
	err := r.q.QueryRow(ctx,
		`INSERT INTO market_orders (id, city_id, item_code, side, order_type, quantity, filled, unit_price, owner_id,
		                            funding, status, game_action_id, created_at, expires_at, closed_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9::uuid, $10, $11, $12::uuid, $13, $14, $15)
		 RETURNING no`,
		o.ID, o.CityID, o.Item, o.Side, o.Kind, o.Qty, o.Filled, o.Price, o.OwnerID, funding, o.Status,
		nullableUUID(o.GameActionID), o.CreatedAt.UTC(), o.ExpiresAt.UTC(), closed).Scan(&o.No)
	if err != nil {
		return o, fmt.Errorf("postgres: placing an order: %w", err)
	}
	o.ClosedAt = closed
	return o, nil
}

// UpdateOrder records an order's fill and status.
func (r *MarketRepository) UpdateOrder(ctx context.Context, id string, filled int64, status string, closedAt *time.Time) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE market_orders SET filled = $2, status = $3, closed_at = $4 WHERE id = $1::uuid`,
		id, filled, status, utcPtr(closedAt)); err != nil {
		return fmt.Errorf("postgres: updating an order: %w", err)
	}
	return nil
}

// RecordTrade writes one fill.
func (r *MarketRepository) RecordTrade(ctx context.Context, t application.MarketTrade) error {
	id, err := ensureID(t.ID)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO market_trades (id, city_id, item_code, buy_order_id, sell_order_id, buyer_id, seller_id, quantity,
		                            unit_price, notional, fee, ledger_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11, $12::uuid, $13)`,
		id, t.CityID, t.Item, t.BuyOrder, t.SellOrder, t.Buyer, t.Seller, t.Qty, t.Price, t.Notional, t.Fee,
		t.LedgerTransactionID, t.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a trade: %w", err)
	}
	return nil
}

// PlayerOrders lists a player's orders, open first then the latest.
func (r *MarketRepository) PlayerOrders(ctx context.Context, playerID string, limit int) ([]application.MarketOrder, error) {
	rows, err := r.q.Query(ctx,
		`SELECT `+orderColumns+` FROM market_orders WHERE owner_id = $1::uuid
		  ORDER BY (status = 'open') DESC, created_at DESC LIMIT $2`, playerID, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	return collectOrders(rows, err)
}

// CountOpen counts a player's resting orders.
func (r *MarketRepository) CountOpen(ctx context.Context, playerID string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx, `SELECT count(*) FROM market_orders WHERE owner_id = $1::uuid AND status = 'open'`, playerID).Scan(&n)
	if err != nil && !isInvalidUUIDText(err) {
		return 0, fmt.Errorf("postgres: counting orders: %w", err)
	}
	return n, nil
}

// Books sums a city's books.
func (r *MarketRepository) Books(ctx context.Context, cityID string) ([]application.BookLine, error) {
	rows, err := r.q.Query(ctx,
		`WITH open AS (
		     SELECT item_code, side, unit_price, quantity - filled AS left_qty
		       FROM market_orders WHERE city_id = $1::uuid AND status = 'open'
		 ), last AS (
		     SELECT DISTINCT ON (item_code) item_code, unit_price
		       FROM market_trades WHERE city_id = $1::uuid ORDER BY item_code, created_at DESC
		 ), items AS (
		     SELECT item_code FROM open UNION SELECT item_code FROM last
		 )
		 SELECT i.item_code,
		        COALESCE((SELECT max(unit_price) FROM open o WHERE o.item_code = i.item_code AND side = 'buy'), 0),
		        COALESCE((SELECT min(unit_price) FROM open o WHERE o.item_code = i.item_code AND side = 'sell'), 0),
		        COALESCE((SELECT sum(left_qty) FROM open o WHERE o.item_code = i.item_code AND side = 'buy'), 0),
		        COALESCE((SELECT sum(left_qty) FROM open o WHERE o.item_code = i.item_code AND side = 'sell'), 0),
		        COALESCE((SELECT unit_price FROM last l WHERE l.item_code = i.item_code), 0)
		   FROM items i ORDER BY i.item_code`, cityID)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading books: %w", err)
	}
	defer rows.Close()
	var out []application.BookLine
	for rows.Next() {
		var b application.BookLine
		if err := rows.Scan(&b.Item, &b.BestBid, &b.BestAsk, &b.BidQty, &b.AskQty, &b.LastPrice); err != nil {
			return nil, fmt.Errorf("postgres: scanning a book: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RecentTrades lists a book's latest fills.
func (r *MarketRepository) RecentTrades(ctx context.Context, cityID, item string, limit int) ([]application.MarketTrade, error) {
	rows, err := r.q.Query(ctx,
		`SELECT id::text, city_id::text, item_code, buy_order_id::text, sell_order_id::text, buyer_id::text,
		        seller_id::text, quantity, unit_price, notional, fee, ledger_transaction_id::text, created_at
		   FROM market_trades WHERE city_id = $1::uuid AND item_code = $2 ORDER BY created_at DESC LIMIT $3`,
		cityID, item, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading trades: %w", err)
	}
	defer rows.Close()
	var out []application.MarketTrade
	for rows.Next() {
		var t application.MarketTrade
		if err := rows.Scan(&t.ID, &t.CityID, &t.Item, &t.BuyOrder, &t.SellOrder, &t.Buyer, &t.Seller, &t.Qty,
			&t.Price, &t.Notional, &t.Fee, &t.LedgerTransactionID, &t.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning a trade: %w", err)
		}
		t.At = t.At.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// AuctionRepository implements application.AuctionRepository.
type AuctionRepository struct {
	q querier
}

var _ application.AuctionRepository = (*AuctionRepository)(nil)

const auctionColumns = `a.id::text, a.no, a.city_id::text, a.seller_id::text, a.piece_id::text, a.item_code, a.reserve,
       a.step_bps, a.min_step, a.status, COALESCE(a.high_bid_id::text, ''), COALESCE(b.bidder_id::text, ''),
       COALESCE(b.amount, 0), a.game_action_id::text, a.opens_at, a.ends_at, a.closed_at, a.fee`

const auctionFrom = ` FROM auctions a LEFT JOIN auction_bids b ON b.id = a.high_bid_id`

func scanAuction(row pgx.Row) (*application.Auction, error) {
	var a application.Auction
	if err := row.Scan(&a.ID, &a.No, &a.CityID, &a.SellerID, &a.PieceID, &a.Item, &a.Reserve, &a.StepBPS, &a.MinStep,
		&a.Status, &a.HighBidID, &a.HighBidder, &a.HighBid, &a.GameActionID, &a.OpensAt, &a.EndsAt, &a.ClosedAt,
		&a.Fee); err != nil {
		return nil, err
	}
	a.OpensAt, a.EndsAt, a.ClosedAt = a.OpensAt.UTC(), a.EndsAt.UTC(), utcPtr(a.ClosedAt)
	return &a, nil
}

func collectAuctions(rows pgx.Rows, err error) ([]application.Auction, error) {
	if err != nil {
		return nil, fmt.Errorf("postgres: reading auctions: %w", err)
	}
	defer rows.Close()
	var out []application.Auction
	for rows.Next() {
		a, err := scanAuction(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning an auction: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// Auction returns one auction, locked.
func (r *AuctionRepository) Auction(ctx context.Context, id string) (*application.Auction, error) {
	a, err := scanAuction(r.q.QueryRow(ctx, `SELECT `+auctionColumns+auctionFrom+` WHERE a.id = $1::uuid FOR UPDATE OF a`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrAuctionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an auction: %w", err)
	}
	return a, nil
}

// AuctionByNo returns one auction by its public number, locked.
func (r *AuctionRepository) AuctionByNo(ctx context.Context, no int64) (*application.Auction, error) {
	a, err := scanAuction(r.q.QueryRow(ctx, `SELECT `+auctionColumns+auctionFrom+` WHERE a.no = $1 FOR UPDATE OF a`, no))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, application.ErrAuctionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an auction: %w", err)
	}
	return a, nil
}

// Open records a new auction and returns it with its number.
func (r *AuctionRepository) Open(ctx context.Context, a application.Auction) (application.Auction, error) {
	err := r.q.QueryRow(ctx,
		`INSERT INTO auctions (id, city_id, seller_id, piece_id, item_code, reserve, step_bps, min_step, status,
		                       game_action_id, opens_at, ends_at, fee)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'open', $9::uuid, $10, $11, 0)
		 RETURNING no`,
		a.ID, a.CityID, a.SellerID, a.PieceID, a.Item, a.Reserve, a.StepBPS, a.MinStep, a.GameActionID,
		a.OpensAt.UTC(), a.EndsAt.UTC()).Scan(&a.No)
	if err != nil {
		return a, fmt.Errorf("postgres: opening an auction: %w", err)
	}
	a.Status = application.AuctionOpen
	return a, nil
}

// PlaceBid records a new standing bid and marks the previous one outbid.
func (r *AuctionRepository) PlaceBid(ctx context.Context, b application.AuctionBid, previousBidID string) error {
	if previousBidID != "" {
		if _, err := r.q.Exec(ctx,
			`UPDATE auction_bids SET status = 'outbid' WHERE id = $1::uuid AND status = 'standing'`, previousBidID); err != nil {
			return fmt.Errorf("postgres: marking a bid outbid: %w", err)
		}
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO auction_bids (id, auction_id, bidder_id, amount, method, status, ledger_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'standing', $6::uuid, $7)`,
		b.ID, b.AuctionID, b.BidderID, b.Amount, b.Method, b.LedgerTransactionID, b.CreatedAt.UTC()); err != nil {
		return fmt.Errorf("postgres: placing a bid: %w", err)
	}
	if _, err := r.q.Exec(ctx, `UPDATE auctions SET high_bid_id = $2::uuid WHERE id = $1::uuid`, b.AuctionID, b.ID); err != nil {
		return fmt.Errorf("postgres: recording the standing bid: %w", err)
	}
	return nil
}

// Bid returns one bid.
func (r *AuctionRepository) Bid(ctx context.Context, id string) (*application.AuctionBid, error) {
	var b application.AuctionBid
	err := r.q.QueryRow(ctx,
		`SELECT id::text, auction_id::text, bidder_id::text, amount, method, status, ledger_transaction_id::text, created_at
		   FROM auction_bids WHERE id = $1::uuid`, id).
		Scan(&b.ID, &b.AuctionID, &b.BidderID, &b.Amount, &b.Method, &b.Status, &b.LedgerTransactionID, &b.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrAuctionNotFound
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a bid: %w", err)
	}
	b.CreatedAt = b.CreatedAt.UTC()
	return &b, nil
}

// Close ends an open auction; an auction already closed is left alone and
// reported as ErrAuctionNotFound.
func (r *AuctionRepository) Close(ctx context.Context, id, status string, fee int64, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE auctions SET status = $2, fee = $3, closed_at = $4 WHERE id = $1::uuid AND status = 'open'`,
		id, status, fee, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: closing an auction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrAuctionNotFound
	}
	return nil
}

// SetBidStatus records a bid's end.
func (r *AuctionRepository) SetBidStatus(ctx context.Context, id, status string) error {
	if _, err := r.q.Exec(ctx, `UPDATE auction_bids SET status = $2 WHERE id = $1::uuid`, id, status); err != nil {
		return fmt.Errorf("postgres: updating a bid: %w", err)
	}
	return nil
}

// List lists a city's open auctions.
func (r *AuctionRepository) List(ctx context.Context, cityID string, limit int) ([]application.Auction, error) {
	rows, err := r.q.Query(ctx, `SELECT `+auctionColumns+auctionFrom+`
		  WHERE a.city_id = $1::uuid AND a.status = 'open' ORDER BY a.ends_at, a.no LIMIT $2`, cityID, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	return collectAuctions(rows, err)
}

// Mine lists the auctions a player sells or has bid on.
func (r *AuctionRepository) Mine(ctx context.Context, playerID string, limit int) ([]application.Auction, error) {
	rows, err := r.q.Query(ctx, `SELECT `+auctionColumns+auctionFrom+`
		  WHERE a.seller_id = $1::uuid
		     OR EXISTS (SELECT 1 FROM auction_bids x WHERE x.auction_id = a.id AND x.bidder_id = $1::uuid)
		  ORDER BY (a.status = 'open') DESC, a.ends_at DESC LIMIT $2`, playerID, limit)
	if isInvalidUUIDText(err) {
		return nil, nil
	}
	return collectAuctions(rows, err)
}

// CountOpen counts a seller's open auctions.
func (r *AuctionRepository) CountOpen(ctx context.Context, sellerID string) (int, error) {
	var n int
	err := r.q.QueryRow(ctx, `SELECT count(*) FROM auctions WHERE seller_id = $1::uuid AND status = 'open'`, sellerID).Scan(&n)
	if err != nil && !isInvalidUUIDText(err) {
		return 0, fmt.Errorf("postgres: counting auctions: %w", err)
	}
	return n, nil
}
