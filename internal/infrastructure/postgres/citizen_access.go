package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mrjvadi/torncity/internal/application"
)

// The lot access side of the citizen port (migration 0100_lot_access,
// docs/adr/0043): releasing a lot, the road reserve and the journal of paid
// connections.

// LockLots takes a transaction-scoped advisory lock on a settlement's land.
func (r *CitizenRepository) LockLots(ctx context.Context, settlementID string) error {
	if _, err := r.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('lots:' || $1))`, settlementID); err != nil {
		return fmt.Errorf("postgres: locking a settlement's land: %w", err)
	}
	return nil
}

// ReleaseLot gives a lot back to the village. The row stays for the books;
// false means it was already released.
func (r *CitizenRepository) ReleaseLot(ctx context.Context, rel application.LotRelease) (bool, error) {
	var amount, txID any
	if rel.Kind == application.ReleaseRefund {
		amount, txID = rel.RefundAmount, rel.RefundLedgerTransactionID
	}
	tag, err := r.q.Exec(ctx, `
		UPDATE settlement_lots
		   SET released_at = $2, release_kind = $3, refund_amount = $4::bigint, refund_ledger_transaction_id = $5::uuid
		 WHERE id = $1::uuid AND released_at IS NULL`, rel.LotID, rel.At.UTC(), rel.Kind, amount, txID)
	if err != nil {
		return false, fmt.Errorf("postgres: releasing the settlement lot: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RoadReserves lists a settlement's reserved right-of-way.
func (r *CitizenRepository) RoadReserves(ctx context.Context, settlementID string) ([]application.RoadReserve, error) {
	rows, err := r.q.Query(ctx, `
		SELECT settlement_id::text, lot_x, lot_y, kind, serves_x, serves_y, created_at
		  FROM settlement_road_reserve WHERE settlement_id = $1::uuid ORDER BY lot_y, lot_x`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the road reserve: %w", err)
	}
	defer rows.Close()
	var out []application.RoadReserve
	for rows.Next() {
		var rr application.RoadReserve
		var sx, sy sql.NullInt32
		if err := rows.Scan(&rr.SettlementID, &rr.X, &rr.Y, &rr.Kind, &sx, &sy, &rr.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a road reserve row: %w", err)
		}
		rr.ServesX, rr.ServesY = int(sx.Int32), int(sy.Int32)
		out = append(out, rr)
	}
	return out, rows.Err()
}

// ReserveRoad records right-of-way lots; one already reserved is kept as it was.
func (r *CitizenRepository) ReserveRoad(ctx context.Context, rows []application.RoadReserve) error {
	for _, rr := range rows {
		var sx, sy any
		if rr.Kind == application.ReserveCorridor {
			sx, sy = rr.ServesX, rr.ServesY
		}
		if _, err := r.q.Exec(ctx, `
			INSERT INTO settlement_road_reserve (settlement_id, lot_x, lot_y, kind, serves_x, serves_y, created_at)
			VALUES ($1::uuid, $2, $3, $4, $5::int, $6::int, $7)
			ON CONFLICT (settlement_id, lot_x, lot_y) DO NOTHING`,
			rr.SettlementID, rr.X, rr.Y, rr.Kind, sx, sy, rr.CreatedAt.UTC()); err != nil {
			return fmt.Errorf("postgres: reserving a road lot: %w", err)
		}
	}
	return nil
}

// RecordConnection writes the journal row of a paid road.
func (r *CitizenRepository) RecordConnection(ctx context.Context, c application.LotConnection) error {
	var txID any
	if c.LedgerTransactionID != "" {
		txID = c.LedgerTransactionID
	}
	_, err := r.q.Exec(ctx, `
		INSERT INTO settlement_lot_connections (id, settlement_id, player_id, lot_x, lot_y, origin, road_lots, crossing_lots,
		                                        carved_lots, fee, ledger_transaction_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::uuid, $12)`,
		c.ID, c.SettlementID, c.PlayerID, c.X, c.Y, c.Origin, c.RoadLots, c.CrossingLots, c.CarvedLots, c.Fee, txID, c.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: recording the lot connection: %w", err)
	}
	return nil
}
