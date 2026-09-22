package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// StatsRepository persists a player's condition.
//
// player_stats is split out of players because its write rate is far higher:
// energy and health tick constantly while a players row is nearly static. The
// primary key is player_id itself, so "one row per player" is a schema fact
// and not something this code has to maintain.
type StatsRepository struct {
	q querier
}

var _ application.StatsRepository = (*StatsRepository)(nil)

// NewStatsRepository returns a repository over the pool.
func NewStatsRepository(p *Pool) *StatsRepository { return &StatsRepository{q: p.Raw()} }

const selectStats = `
SELECT player_id, level, xp, health, max_health, energy, max_energy, happiness, stamina, reputation, updated_at
FROM player_stats
WHERE player_id = $1::uuid`

// Get returns the player's stats, or ErrStatsNotFound.
//
// The sentinel is this package's rather than one from
// internal/application/errors_phase1.go because that file declares none for
// this condition; see the note on ErrStatsNotFound. It is NOT reported as
// ErrPlayerNotFound: a player can exist with no stats row until EnsureDefaults
// has run, and claiming the player does not exist would be a false statement
// shown to the very person it is about.
func (r *StatsRepository) Get(ctx context.Context, playerID string) (*application.Stats, error) {
	var s application.Stats

	err := r.q.QueryRow(ctx, selectStats, playerID).Scan(
		&s.PlayerID, &s.Level, &s.XP, &s.Health, &s.MaxHealth,
		&s.Energy, &s.MaxEnergy, &s.Happiness, &s.Stamina, &s.Reputation, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStatsNotFound
		}
		return nil, fmt.Errorf("postgres: loading stats for player %s: %w", playerID, err)
	}

	return &s, nil
}

// insertStatsDefaults follows the reasoning written out on insertPlayer: two
// concurrent first-contact requests must leave one row, and the loser must
// receive the winning row rather than an error or a miss.
//
//  1. SELECT then INSERT loses the race outright — under READ COMMITTED both
//     callers read "absent" and both insert, and one then fails on the primary
//     key with nothing to return.
//
//  2. ON CONFLICT DO NOTHING plus a read-back is worse than it looks: DO
//     NOTHING does not block on the conflicting row, so the loser's SELECT runs
//     while the winner is still uncommitted and finds nothing. The result is an
//     intermittent "no stats" on exactly the race this is meant to survive.
//
//  3. ON CONFLICT DO UPDATE ... RETURNING, used here. DO UPDATE takes a row
//     lock, so the loser waits for the winner to commit and then reads the
//     winning row back in the same statement.
//
// The one difference from insertPlayer is that the update is a genuine no-op:
// updated_at is assigned from the existing row, never from EXCLUDED. This
// method's argument is a set of DEFAULTS, so writing EXCLUDED into any column
// would reset a live player's health, energy and experience to starting values
// the moment anything called EnsureDefaults again. Self-assignment still takes
// the row lock and still feeds RETURNING, which is all that is needed.
const insertStatsDefaults = `
INSERT INTO player_stats (player_id, level, xp, health, max_health, energy, max_energy, happiness, stamina, reputation, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (player_id) DO UPDATE SET updated_at = player_stats.updated_at
RETURNING player_id, level, xp, health, max_health, energy, max_energy, happiness, stamina, reputation, updated_at`

// EnsureDefaults creates the row on first contact and returns it.
//
// The returned stats are always the surviving row's, so a caller that lost the
// race carries the real numbers onward instead of the defaults it proposed.
func (r *StatsRepository) EnsureDefaults(ctx context.Context, playerID string, s application.Stats) (*application.Stats, error) {
	if playerID == "" {
		// player_id is a NOT NULL uuid with a foreign key to players. An empty
		// value would fail on the cast with a complaint about uuid syntax,
		// which sends whoever reads it looking at the wrong thing.
		return nil, fmt.Errorf("postgres: ensure stats defaults: player id is required")
	}

	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		// updated_at is NOT NULL and the schema has no DEFAULT now() anywhere,
		// so the application supplies every timestamp. A zero time would be
		// written as year 1, which sorts before everything and would make any
		// "stale stats" sweep pick this row first.
		updatedAt = time.Now().UTC()
	}

	var out application.Stats
	err := r.q.QueryRow(ctx, insertStatsDefaults,
		playerID,
		s.Level,
		s.XP,
		s.Health,
		s.MaxHealth,
		s.Energy,
		s.MaxEnergy,
		s.Happiness,
		s.Stamina,
		s.Reputation,
		updatedAt,
	).Scan(
		&out.PlayerID, &out.Level, &out.XP, &out.Health, &out.MaxHealth,
		&out.Energy, &out.MaxEnergy, &out.Happiness, &out.Stamina, &out.Reputation, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: ensuring stats defaults for player %s: %w", playerID, err)
	}

	return &out, nil
}

// updateStats writes the whole row. It is an UPDATE and not an upsert on
// purpose: EnsureDefaults is the only thing allowed to create the row, so a
// Save against a player who never had stats is a bug in the caller's sequence
// and is reported rather than papered over with an invented row.
const updateStats = `
UPDATE player_stats SET
    level      = $2,
    xp         = $3,
    health     = $4,
    max_health = $5,
    energy     = $6,
    max_energy = $7,
    happiness  = $8,
    stamina    = $9,
    reputation = $10,
    updated_at = $11
WHERE player_id = $1::uuid`

// Save writes the player's condition, or returns ErrStatsNotFound.
func (r *StatsRepository) Save(ctx context.Context, s application.Stats) error {
	if s.PlayerID == "" {
		return fmt.Errorf("postgres: save stats: player id is required")
	}

	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	tag, err := r.q.Exec(ctx, updateStats,
		s.PlayerID,
		s.Level,
		s.XP,
		s.Health,
		s.MaxHealth,
		s.Energy,
		s.MaxEnergy,
		s.Happiness,
		s.Stamina,
		s.Reputation,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: saving stats for player %s: %w", s.PlayerID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStatsNotFound
	}

	return nil
}
