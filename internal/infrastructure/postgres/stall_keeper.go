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

const stallKeeperColumns = `id::text, settlement_id::text, owner_id::text, share_bps, hired_at, ended_at`

func scanStallKeeper(row pgx.Row) (*application.StallKeeper, error) {
	var k application.StallKeeper
	if err := row.Scan(&k.ID, &k.SettlementID, &k.OwnerID, &k.ShareBPS, &k.HiredAt, &k.EndedAt); err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *StallKeeperRepository) Hire(ctx context.Context, k application.StallKeeper) (bool, error) {
	id, err := ensureID(k.ID)
	if err != nil {
		return false, err
	}
	tag, err := r.q.Exec(ctx, `INSERT INTO stall_keepers (id, settlement_id, owner_id, share_bps, hired_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5) ON CONFLICT DO NOTHING`, id, k.SettlementID, k.OwnerID, k.ShareBPS, k.HiredAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: hiring a stall keeper: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *StallKeeperRepository) End(ctx context.Context, settlementID, ownerID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE stall_keepers SET ended_at = GREATEST($3, hired_at)
		WHERE settlement_id = $1::uuid AND owner_id = $2::uuid AND ended_at IS NULL`, settlementID, ownerID, at.UTC())
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
