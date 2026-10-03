package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file implements the village shop over migrations/0109_village_shop.

// VillageShopRepository implements application.VillageShopRepository.
type VillageShopRepository struct {
	q querier
}

var _ application.VillageShopRepository = (*VillageShopRepository)(nil)

const villageShopLineCols = `stock, delivered_day, sold_today, delivered_total, sold_total, trimmed_total`

// Line returns a line of a shelf, locked, creating it empty on first use.
func (r *VillageShopRepository) Line(ctx context.Context, settlementID, line string) (*application.VillageShopLine, error) {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_lines (settlement_id, line, stock, delivered_day, sold_today, delivered_total, sold_total, trimmed_total)
		 VALUES ($1::uuid, $2, 0, -1, 0, 0, 0, 0) ON CONFLICT DO NOTHING`, settlementID, line); err != nil {
		if isInvalidUUIDText(err) {
			return nil, application.ErrCityNotFound
		}
		return nil, fmt.Errorf("postgres: opening a shop line: %w", err)
	}
	l := application.VillageShopLine{SettlementID: settlementID, Line: line}
	if err := r.q.QueryRow(ctx,
		`SELECT `+villageShopLineCols+` FROM village_shop_lines WHERE settlement_id = $1::uuid AND line = $2 FOR UPDATE`,
		settlementID, line).Scan(&l.Stock, &l.DeliveredDay, &l.SoldToday, &l.DeliveredTotal, &l.SoldTotal, &l.TrimmedTotal); err != nil {
		return nil, fmt.Errorf("postgres: reading a shop line: %w", err)
	}
	return &l, nil
}

// Lines returns every line of a village's shelf, unlocked.
func (r *VillageShopRepository) Lines(ctx context.Context, settlementID string) ([]application.VillageShopLine, error) {
	rows, err := r.q.Query(ctx,
		`SELECT line, `+villageShopLineCols+` FROM village_shop_lines WHERE settlement_id = $1::uuid ORDER BY line`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a shelf: %w", err)
	}
	defer rows.Close()
	var out []application.VillageShopLine
	for rows.Next() {
		l := application.VillageShopLine{SettlementID: settlementID}
		if err := rows.Scan(&l.Line, &l.Stock, &l.DeliveredDay, &l.SoldToday, &l.DeliveredTotal, &l.SoldTotal, &l.TrimmedTotal); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveLine writes a line back.
func (r *VillageShopRepository) SaveLine(ctx context.Context, l application.VillageShopLine) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE village_shop_lines SET stock = $3, delivered_day = $4, sold_today = $5,
		        delivered_total = $6, sold_total = $7, trimmed_total = $8
		  WHERE settlement_id = $1::uuid AND line = $2`,
		l.SettlementID, l.Line, l.Stock, l.DeliveredDay, l.SoldToday, l.DeliveredTotal, l.SoldTotal, l.TrimmedTotal); err != nil {
		return fmt.Errorf("postgres: saving a shop line: %w", err)
	}
	return nil
}

const villageShopDayCols = `settlement_id::text, day, outcome, wage, delivered_value, budget, COALESCE(ledger_transaction_id::text, ''), at`

func scanVillageShopDay(row pgx.Row) (*application.VillageShopDay, error) {
	var d application.VillageShopDay
	if err := row.Scan(&d.SettlementID, &d.Day, &d.Outcome, &d.Wage, &d.DeliveredValue, &d.Budget, &d.LedgerTransactionID, &d.At); err != nil {
		return nil, err
	}
	d.At = d.At.UTC()
	return &d, nil
}

// Day returns a delivery day, or nil when none was recorded.
func (r *VillageShopRepository) Day(ctx context.Context, settlementID string, day int64) (*application.VillageShopDay, error) {
	d, err := scanVillageShopDay(r.q.QueryRow(ctx,
		`SELECT `+villageShopDayCols+` FROM village_shop_days WHERE settlement_id = $1::uuid AND day = $2`, settlementID, day))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a shop day: %w", err)
	}
	return d, nil
}

// LastDay returns the latest delivery day, or nil.
func (r *VillageShopRepository) LastDay(ctx context.Context, settlementID string) (*application.VillageShopDay, error) {
	d, err := scanVillageShopDay(r.q.QueryRow(ctx,
		`SELECT `+villageShopDayCols+` FROM village_shop_days WHERE settlement_id = $1::uuid ORDER BY day DESC LIMIT 1`, settlementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the last shop day: %w", err)
	}
	return d, nil
}

// RecordDay is the fence of the morning delivery.
func (r *VillageShopRepository) RecordDay(ctx context.Context, d application.VillageShopDay) (bool, error) {
	var tx any
	if d.LedgerTransactionID != "" {
		tx = d.LedgerTransactionID
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_days (settlement_id, day, outcome, wage, delivered_value, budget, ledger_transaction_id, at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::uuid, $8) ON CONFLICT (settlement_id, day) DO NOTHING`,
		d.SettlementID, d.Day, d.Outcome, d.Wage, d.DeliveredValue, d.Budget, tx, d.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a shop day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PlayerDay is what a player has bought of a line in a restock day.
func (r *VillageShopRepository) PlayerDay(ctx context.Context, playerID, settlementID, line string, day int64) (int64, error) {
	var n int64
	err := r.q.QueryRow(ctx,
		`SELECT quantity FROM village_shop_player_day
		  WHERE player_id = $1::uuid AND settlement_id = $2::uuid AND line = $3 AND day = $4`,
		playerID, settlementID, line, day).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: reading a player's day at the shop: %w", err)
	}
	return n, nil
}

// AddPlayerDay adds to what a player has bought of a line in a restock day.
func (r *VillageShopRepository) AddPlayerDay(ctx context.Context, playerID, settlementID, line string, day, qty int64) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_player_day (player_id, settlement_id, line, day, quantity)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		 ON CONFLICT (player_id, settlement_id, line, day) DO UPDATE SET quantity = village_shop_player_day.quantity + EXCLUDED.quantity`,
		playerID, settlementID, line, day, qty); err != nil {
		return fmt.Errorf("postgres: adding to a player's day at the shop: %w", err)
	}
	return nil
}

// RecordSale writes one sale.
func (r *VillageShopRepository) RecordSale(ctx context.Context, s application.VillageShopSale) error {
	id, err := ensureID(s.ID)
	if err != nil {
		return err
	}
	if _, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_sales (id, settlement_id, player_id, line, day, quantity, unit_price, reference_price,
		                                 total, tax, method, ledger_transaction_id, created_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12::uuid, $13)`,
		id, s.SettlementID, s.PlayerID, s.Line, s.Day, s.Quantity, s.UnitPrice, s.ReferencePrice, s.Total, s.Tax, s.Method,
		s.LedgerTransactionID, s.At.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a shop sale: %w", err)
	}
	return nil
}

// Terms are what the head set.
func (r *VillageShopRepository) Terms(ctx context.Context, settlementID string) (application.VillageShopTerms, error) {
	var capBPS, taxBPS *int64
	err := r.q.QueryRow(ctx, `SELECT price_cap_bps, tax_bps FROM village_shop_terms WHERE settlement_id = $1::uuid`,
		settlementID).Scan(&capBPS, &taxBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.VillageShopTerms{}, nil
	}
	if err != nil {
		return application.VillageShopTerms{}, fmt.Errorf("postgres: reading the shop's terms: %w", err)
	}
	var t application.VillageShopTerms
	if capBPS != nil {
		t.PriceCapBPS, t.HasCap = *capBPS, true
	}
	if taxBPS != nil {
		t.TaxBPS, t.HasTax = *taxBPS, true
	}
	return t, nil
}

// SetPriceCap records the ceiling the head set.
func (r *VillageShopRepository) SetPriceCap(ctx context.Context, settlementID string, capBPS int64, by string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_terms (settlement_id, price_cap_bps, set_by, updated_at) VALUES ($1::uuid, $2, $3::uuid, $4)
		 ON CONFLICT (settlement_id) DO UPDATE SET price_cap_bps = EXCLUDED.price_cap_bps, set_by = EXCLUDED.set_by, updated_at = EXCLUDED.updated_at`,
		settlementID, capBPS, by, at.UTC()); err != nil {
		return fmt.Errorf("postgres: setting the shop's price cap: %w", err)
	}
	return nil
}

// SetTax records the sales tax the head set.
func (r *VillageShopRepository) SetTax(ctx context.Context, settlementID string, taxBPS int64, by string, at time.Time) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO village_shop_terms (settlement_id, tax_bps, set_by, updated_at) VALUES ($1::uuid, $2, $3::uuid, $4)
		 ON CONFLICT (settlement_id) DO UPDATE SET tax_bps = EXCLUDED.tax_bps, set_by = EXCLUDED.set_by, updated_at = EXCLUDED.updated_at`,
		settlementID, taxBPS, by, at.UTC()); err != nil {
		return fmt.Errorf("postgres: setting the shop's sales tax: %w", err)
	}
	return nil
}
