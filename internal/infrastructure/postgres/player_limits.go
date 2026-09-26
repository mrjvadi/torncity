package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// PlayerLimitRepository persists per-player overrides of tuning caps
// (migrations/0032_player_limits) — today, only company.max_per_player.
type PlayerLimitRepository struct{ q querier }

var _ application.PlayerLimitRepository = (*PlayerLimitRepository)(nil)

// NewPlayerLimitRepository returns the player limits over the pool, for the
// admin tool. Inside a unit of work, use Tx.PlayerLimits.
func NewPlayerLimitRepository(p *Pool) *PlayerLimitRepository {
	return &PlayerLimitRepository{q: p.shared()}
}

const getPlayerLimit = `
	SELECT player_id::text, max_companies, unlimited, granted_by, reason, created_at, updated_at
	  FROM player_limits WHERE player_id = $1::uuid`

// Get reads a player's override, or application.ErrNoPlayerLimit when they
// have none.
func (r *PlayerLimitRepository) Get(ctx context.Context, playerID string) (*application.PlayerLimit, error) {
	var l application.PlayerLimit
	err := r.q.QueryRow(ctx, getPlayerLimit, playerID).Scan(
		&l.PlayerID, &l.MaxCompanies, &l.Unlimited, &l.GrantedBy, &l.Reason, &l.CreatedAt, &l.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUIDText(err):
		return nil, application.ErrNoPlayerLimit
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a player's limit override: %w", err)
	}
	l.CreatedAt, l.UpdatedAt = l.CreatedAt.UTC(), l.UpdatedAt.UTC()
	return &l, nil
}

// upsertPlayerLimit replaces whatever override the player already had:
// exactly one row per player, kept current rather than appended to.
const upsertPlayerLimit = `
	INSERT INTO player_limits (player_id, max_companies, unlimited, granted_by, reason, created_at, updated_at)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $6)
	ON CONFLICT (player_id) DO UPDATE
	   SET max_companies = EXCLUDED.max_companies, unlimited = EXCLUDED.unlimited,
	       granted_by = EXCLUDED.granted_by, reason = EXCLUDED.reason, updated_at = EXCLUDED.updated_at`

// Set grants an override. unlimited stores no number, matching the
// migration's consistency CHECK; a numeric cap stores exactly the number
// given.
func (r *PlayerLimitRepository) Set(ctx context.Context, playerID string, maxCompanies *int, unlimited bool, grantedBy, reason string, now time.Time) error {
	if unlimited {
		maxCompanies = nil
	}
	if _, err := r.q.Exec(ctx, upsertPlayerLimit, playerID, maxCompanies, unlimited, grantedBy, reason, now.UTC()); err != nil {
		return fmt.Errorf("postgres: setting a player's limit override: %w", err)
	}
	return nil
}

const clearPlayerLimit = `DELETE FROM player_limits WHERE player_id = $1::uuid`

// Clear removes a player's override, restoring the config default.
func (r *PlayerLimitRepository) Clear(ctx context.Context, playerID string) error {
	if _, err := r.q.Exec(ctx, clearPlayerLimit, playerID); err != nil {
		return fmt.Errorf("postgres: clearing a player's limit override: %w", err)
	}
	return nil
}
