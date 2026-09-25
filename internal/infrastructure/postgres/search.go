package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// PlayerSearchRepository finds one other player by an exact identifier, for
// the social screens.
//
// It used to match display names by substring, a page at a time, with LIKE
// escaping and a cap on the page size to keep a typed "%" from listing the
// player base. All of that is gone with the fuzzy search itself: every lookup
// here is an equality on an indexed column and returns at most one row, so
// there is no pattern to escape and no page to cap.
type PlayerSearchRepository struct {
	q querier
}

var _ application.PlayerSearch = (*PlayerSearchRepository)(nil)

// NewPlayerSearchRepository returns a repository over the pool.
func NewPlayerSearchRepository(p *Pool) *PlayerSearchRepository {
	return &PlayerSearchRepository{q: p.shared()}
}

// The three lookups.
//
// status = 'active' is the load-bearing predicate in each and is written as a
// fixed part of the statement, never as a parameter: banned and deleted
// players must never be found, and a caller must have no way to ask for them.
// A banned account surfacing in a friend search would let anyone confirm a
// ban, and would offer a friend request to an account that can never answer
// it; a deleted one is a person who asked to stop existing in this game.
//
// findPlayerByUsername picks the holder FIRST and checks status SECOND. A
// username can briefly sit on two rows (see releaseUsername and migration
// 0007: the index is deliberately not unique), and the most recently updated
// row is the one Telegram most recently vouched for. Filtering on status
// before choosing would let an older, stale row win whenever the current
// holder is banned — finding a stranger in place of "not found".
const (
	findPlayerByUsername = `
SELECT ` + playerColumns + `
FROM (
    SELECT ` + playerColumns + `
    FROM players
    WHERE username IS NOT NULL
      AND lower(username) = lower($1)
    ORDER BY updated_at DESC, id
    LIMIT 1
) AS holder
WHERE status = 'active'`

	findPlayerByTelegramUserID = `
SELECT ` + playerColumns + `
FROM players
WHERE status = 'active'
  AND telegram_user_id = $1`

	findPlayerByPublicCode = `
SELECT ` + playerColumns + `
FROM players
WHERE status = 'active'
  AND public_code = $1`
)

// Find returns the active player q names, or application.ErrPlayerNotFound.
func (r *PlayerSearchRepository) Find(ctx context.Context, q application.PlayerQuery) (*application.Player, error) {
	var (
		sql string
		arg any
	)
	switch q.Kind {
	case application.PlayerQueryUsername:
		name := strings.TrimPrefix(strings.TrimSpace(q.Username), "@")
		if name == "" {
			return nil, apperrors.InvalidInput("a username search names no username")
		}
		sql, arg = findPlayerByUsername, name
	case application.PlayerQueryTelegramUserID:
		if q.TelegramUserID <= 0 {
			return nil, apperrors.InvalidInput("a telegram id search names no id")
		}
		sql, arg = findPlayerByTelegramUserID, q.TelegramUserID
	case application.PlayerQueryPublicCode:
		code := playercode.Normalize(q.PublicCode)
		if !playercode.Valid(code) {
			// Not a code any player can hold, so no player has it. Answered
			// here rather than by the CHECK constraint so a malformed value
			// costs no round trip.
			return nil, application.ErrPlayerNotFound
		}
		sql, arg = findPlayerByPublicCode, code
	default:
		return nil, apperrors.InvalidInput("a player search names no identifier")
	}

	p, err := scanPlayer(r.q.QueryRow(ctx, sql, arg))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("postgres: finding a player: %w", err)
	}
	return p, nil
}
