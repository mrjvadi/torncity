package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file implements the worn bags over migrations/0108_bags and
// 0110_starting_bag_grant (docs/adr/0046 section 4).

// BagRepository implements application.BagRepository.
type BagRepository struct {
	q querier
}

var _ application.BagRepository = (*BagRepository)(nil)

// Worn returns the player's worn bags, belt first.
func (r *BagRepository) Worn(ctx context.Context, playerID string) ([]application.WornBag, error) {
	rows, err := r.q.Query(ctx,
		`SELECT slot, piece_id::text, worn_through_day, worn_at FROM player_bags
		  WHERE player_id = $1::uuid ORDER BY slot`, playerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading worn bags: %w", err)
	}
	defer rows.Close()
	var out []application.WornBag
	for rows.Next() {
		b := application.WornBag{PlayerID: playerID}
		if err := rows.Scan(&b.Slot, &b.PieceID, &b.WornThroughDay, &b.WornAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning a worn bag: %w", err)
		}
		b.WornAt = b.WornAt.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// Wear puts a piece on in a slot, replacing whatever was worn there.
func (r *BagRepository) Wear(ctx context.Context, b application.WornBag) error {
	if _, err := r.q.Exec(ctx,
		`INSERT INTO player_bags (player_id, slot, piece_id, worn_through_day, worn_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5)
		 ON CONFLICT (player_id, slot) DO UPDATE
		    SET piece_id = EXCLUDED.piece_id, worn_through_day = EXCLUDED.worn_through_day, worn_at = EXCLUDED.worn_at`,
		b.PlayerID, b.Slot, b.PieceID, b.WornThroughDay, b.WornAt.UTC()); err != nil {
		return fmt.Errorf("postgres: wearing a bag: %w", err)
	}
	return nil
}

// TakeOff removes the bag in a slot.
func (r *BagRepository) TakeOff(ctx context.Context, playerID, slot string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM player_bags WHERE player_id = $1::uuid AND slot = $2`, playerID, slot); err != nil {
		return fmt.Errorf("postgres: taking a bag off: %w", err)
	}
	return nil
}

// TakeOffPiece removes a piece wherever it is worn.
func (r *BagRepository) TakeOffPiece(ctx context.Context, pieceID string) error {
	if _, err := r.q.Exec(ctx, `DELETE FROM player_bags WHERE piece_id = $1::uuid`, pieceID); err != nil {
		return fmt.Errorf("postgres: taking a piece off: %w", err)
	}
	return nil
}

// SetWornThrough records that a slot's wear is charged through a day.
func (r *BagRepository) SetWornThrough(ctx context.Context, playerID, slot string, day int64) error {
	if _, err := r.q.Exec(ctx,
		`UPDATE player_bags SET worn_through_day = GREATEST(worn_through_day, $3)
		  WHERE player_id = $1::uuid AND slot = $2`, playerID, slot, day); err != nil {
		return fmt.Errorf("postgres: settling a bag's wear: %w", err)
	}
	return nil
}

// Granted reports whether the player has had the starting bag.
func (r *BagRepository) Granted(ctx context.Context, playerID string) (bool, error) {
	var n int
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM starting_bag_grants WHERE player_id = $1::uuid`, playerID).Scan(&n); err != nil {
		return false, fmt.Errorf("postgres: reading the starting bag grant: %w", err)
	}
	return n > 0, nil
}

// RecordGrant writes the player's row of the starting grant, or reports false
// when they already have one.
func (r *BagRepository) RecordGrant(ctx context.Context, playerID, pieceID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx,
		`INSERT INTO starting_bag_grants (player_id, piece_id, granted_at) VALUES ($1::uuid, $2::uuid, $3)
		 ON CONFLICT (player_id) DO NOTHING`, playerID, pieceID, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording the starting bag grant: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// WithoutGrant lists up to limit players who have not had the starting bag.
func (r *BagRepository) WithoutGrant(ctx context.Context, after string, limit int) ([]string, error) {
	rows, err := r.q.Query(ctx,
		`SELECT p.id::text FROM players p
		  WHERE ($1 = '' OR p.id > $1::uuid)
		    AND NOT EXISTS (SELECT 1 FROM starting_bag_grants g WHERE g.player_id = p.id)
		  ORDER BY p.id LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing players without the starting bag: %w", err)
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
