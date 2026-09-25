package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// IdempotencyRepository records which command keys have already been handled.
type IdempotencyRepository struct {
	q querier
}

var _ application.IdempotencyRepository = (*IdempotencyRepository)(nil)

// NewIdempotencyRepository returns a repository over the pool.
func NewIdempotencyRepository(p *Pool) *IdempotencyRepository {
	return &IdempotencyRepository{q: p.shared()}
}

// reserveKey claims a command key, or does nothing if it is already claimed.
//
// ON CONFLICT DO NOTHING is the whole mechanism: the unique index on
// (player_id, idempotency_key) is what decides the race, inside the database,
// under whatever concurrency the gateway throws at it. A SELECT-then-INSERT
// would let two copies of the same command both read "not reserved" and both
// proceed, which for a command that spends money is precisely the failure this
// table exists to prevent.
//
// The key is scoped per player, matching the unique constraint in
// docs/database.md: two players may legitimately send the same key, and a
// global namespace would let one player's command silently suppress
// another's.
const reserveKey = `
INSERT INTO idempotency_keys (id, player_id, idempotency_key, request_id, command, created_at, expires_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
ON CONFLICT (player_id, idempotency_key) DO NOTHING`

// Reserve stores the key and reports whether it was newly created.
//
// A false return means this command was already processed and the caller must
// not execute it again.
//
// Freshness is read from the command tag's RowsAffected, which is the only
// honest source: DO NOTHING succeeds either way, so the statement returning no
// error says nothing about whether a row appeared. Assuming success meant
// insertion would make every replay look fresh and turn this table into an
// expensive no-op.
func (r *IdempotencyRepository) Reserve(ctx context.Context, key, playerID, requestID, command string, ttl time.Duration) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("postgres: idempotency reserve: key is required")
	}
	if playerID == "" {
		// player_id is a NOT NULL uuid with a foreign key. An empty value
		// would fail on the cast with a message about uuid syntax, which
		// sends whoever reads it looking at the wrong thing.
		return false, fmt.Errorf("postgres: idempotency reserve: player id is required")
	}
	if ttl <= 0 {
		// expires_at is NOT NULL and drives the sweeper. A non-positive ttl
		// would insert an already-expired row, which the sweeper would delete
		// and thereby un-reserve a command that is still in flight.
		return false, fmt.Errorf("postgres: idempotency reserve: ttl must be positive, got %s", ttl)
	}

	id, err := newUUID()
	if err != nil {
		return false, err
	}

	now := time.Now().UTC()

	tag, err := r.q.Exec(ctx, reserveKey,
		id,
		playerID,
		key,
		requestID,
		command,
		now,
		now.Add(ttl),
	)
	if err != nil {
		return false, fmt.Errorf("postgres: reserving idempotency key for player %s: %w", playerID, err)
	}

	return tag.RowsAffected() == 1, nil
}
