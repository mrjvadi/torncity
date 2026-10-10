package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// StallKeeperRepository is the stall keepers' hires (migration 0139), bound to one transaction.
type StallKeeperRepository struct{ q querier }

var _ application.StallKeeperRepository = (*StallKeeperRepository)(nil)

const stallKeeperColumns = `id::text, settlement_id::text, owner_id::text, pay, share_bps, daily_wage, hired_at, ended_at, ended_reason`

func scanStallKeeper(row pgx.Row) (*application.StallKeeper, error) {
	var k application.StallKeeper
	if err := row.Scan(&k.ID, &k.SettlementID, &k.OwnerID, &k.Pay, &k.ShareBPS, &k.DailyWage, &k.HiredAt, &k.EndedAt, &k.EndedReason); err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *StallKeeperRepository) Hire(ctx context.Context, k application.StallKeeper) (bool, error) {
	id, err := ensureID(k.ID)
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO stall_keepers (id, settlement_id, owner_id, pay, share_bps, daily_wage, hired_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7) ON CONFLICT DO NOTHING`, id, k.SettlementID, k.OwnerID, k.Pay, k.ShareBPS, k.DailyWage, k.HiredAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: hiring a stall keeper: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *StallKeeperRepository) End(ctx context.Context, settlementID, ownerID string, at time.Time, reason string) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE stall_keepers SET ended_at = GREATEST($3, hired_at), ended_reason = $4
		WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND ended_at IS NULL`, settlementID, ownerID, at.UTC(), reason)
	if err != nil {
		return false, fmt.Errorf("postgres: ending a stall keeper: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *StallKeeperRepository) OfOwner(ctx context.Context, settlementID, ownerID string) (*application.StallKeeper, error) {
	k, err := scanStallKeeper(r.q.QueryRow(ctx, `SELECT `+stallKeeperColumns+` FROM stall_keepers
		WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND ended_at IS NULL`, settlementID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a stall keeper: %w", err)
	}
	return k, nil
}

func (r *StallKeeperRepository) Open(ctx context.Context, settlementID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM stall_keepers WHERE settlement_id = $1::uuid AND ended_at IS NULL`, settlementID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting the stall keepers: %w", err)
	}
	return n, nil
}

func (r *StallKeeperRepository) OfOwners(ctx context.Context, settlementID string, ownerIDs []string) (map[string]application.StallKeeper, error) {
	out := map[string]application.StallKeeper{}
	if len(ownerIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.Query(ctx, `SELECT `+stallKeeperColumns+` FROM stall_keepers
		WHERE settlement_id = $1::uuid AND owner_id = ANY($2::uuid[]) AND ended_at IS NULL`, settlementID, ownerIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the stall keepers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		k, err := scanStallKeeper(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a stall keeper: %w", err)
		}
		out[k.OwnerID] = *k
	}
	return out, rows.Err()
}

func (r *StallKeeperRepository) LastEnded(ctx context.Context, settlementID, ownerID string) (*application.StallKeeper, error) {
	k, err := scanStallKeeper(r.q.QueryRow(ctx, `SELECT `+stallKeeperColumns+` FROM stall_keepers
		WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND ended_at IS NOT NULL ORDER BY ended_at DESC LIMIT 1`, settlementID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the last ended stall keeper: %w", err)
	}
	return k, nil
}

func (r *StallKeeperRepository) OpenWages(ctx context.Context, settlementID string) ([]application.StallKeeper, error) {
	rows, err := r.q.Query(ctx, `SELECT `+stallKeeperColumns+` FROM stall_keepers
		WHERE settlement_id = $1::uuid AND ended_at IS NULL AND pay = 'wage' ORDER BY hired_at, id`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the wage keepers: %w", err)
	}
	defer rows.Close()
	var out []application.StallKeeper
	for rows.Next() {
		k, err := scanStallKeeper(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning a wage keeper: %w", err)
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (r *StallKeeperRepository) ClaimWage(ctx context.Context, k application.StallKeeper, day, amount int64, ledgerTx string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO stall_keeper_wages (keeper_id, day, settlement_id, owner_id, amount, ledger_tx, at)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6::uuid, $7) ON CONFLICT DO NOTHING`, k.ID, day, k.SettlementID, k.OwnerID, amount, ledgerTx, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: claiming a keeper wage day: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *StallKeeperRepository) Takings(ctx context.Context, settlementID, ownerID string, since time.Time) (sold, cut int64, err error) {
	var shareCut int64
	if err = r.q.QueryRow(ctx, `SELECT COALESCE(SUM(notional), 0)::bigint, COALESCE(SUM(keeper_cut), 0)::bigint FROM market_trades
		WHERE city_id = $1::uuid AND seller_id = $2::uuid AND away AND created_at >= $3`, settlementID, ownerID, since.UTC()).Scan(&sold, &shareCut); err != nil {
		return 0, 0, fmt.Errorf("postgres: summing the sales made while away: %w", err)
	}
	var wages int64
	if err = r.q.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0)::bigint FROM stall_keeper_wages
		WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND at >= $3`, settlementID, ownerID, since.UTC()).Scan(&wages); err != nil {
		return 0, 0, fmt.Errorf("postgres: summing the keeper wages: %w", err)
	}
	return sold, shareCut + wages, nil
}
