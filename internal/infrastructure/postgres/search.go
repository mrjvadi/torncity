package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
)

// Search paging bounds.
//
// MaxSearchLimit is the cap the repository enforces regardless of what the
// caller asked for. Without it, `limit` is a parameter a player controls
// through a command argument, and one request for a limit of ten million is a
// full table read, a multi-megabyte result set and a stalled connection — an
// outage anyone can cause by typing. The cap lives here rather than in a
// handler because it is the last layer before the database and is the only one
// that cannot be bypassed by a second caller written later.
//
// DefaultSearchLimit is what a caller that asked for nothing gets. A limit of
// zero would otherwise mean "no rows", which is never what a search screen
// wants.
const (
	MaxSearchLimit     = 50
	DefaultSearchLimit = 20
)

// PlayerSearchRepository finds other players by display name, for the social
// screens.
type PlayerSearchRepository struct {
	q querier
}

var _ application.PlayerSearch = (*PlayerSearchRepository)(nil)

// NewPlayerSearchRepository returns a repository over the pool.
func NewPlayerSearchRepository(p *Pool) *PlayerSearchRepository {
	return &PlayerSearchRepository{q: p.Raw()}
}

// searchPlayers matches display names case-insensitively.
//
// status = 'active' is the load-bearing predicate and is written as a fixed
// part of the statement, not as a parameter: banned and deleted players must
// never appear in a search result, and a caller must have no way to ask for
// them. A banned account surfacing in a friend search would let anyone confirm
// a ban and would offer a friend request to an account that can never answer
// it; a deleted one is a person who asked to stop existing in this game.
//
// $1 is the prefix pattern and $2 the substring pattern. Both are needed: the
// WHERE clause matches anywhere in the name, so a search for "an" finds
// "Hassan", while the first ORDER BY term lifts the names that actually START
// with the query above those that merely contain it — which is what someone
// typing the beginning of a name is looking for.
//
// ESCAPE is stated explicitly so that the escaping done in Go and the escaping
// the server expects cannot drift apart if a future server or connection
// setting changes the default; see escapeLikePattern.
//
// The order is completed by display_name and then id, making it total. Without
// the id tie-break, two players sharing a display name — which nothing forbids
// — could swap places between two pages of the same search and be shown twice
// or not at all.
const searchPlayers = `
SELECT id, telegram_user_id, username, display_name, language, city_id, status, created_at
FROM players
WHERE status = 'active'
  AND display_name ILIKE $2 ESCAPE '\'
ORDER BY (display_name ILIKE $1 ESCAPE '\') DESC, display_name, id
LIMIT $3 OFFSET $4`

// Search returns active players whose display name contains query, ignoring
// case, with prefix matches first.
//
// An empty query is allowed and lists active players alphabetically. That is
// safe precisely because of MaxSearchLimit: the result is bounded whatever was
// asked for, so "browse" is just a search that matches everyone.
func (r *PlayerSearchRepository) Search(ctx context.Context, query string, limit, offset int) ([]application.Player, error) {
	if offset < 0 {
		// A negative OFFSET is rejected by the server at execution time. It
		// is an off-by-one in the caller's paging arithmetic, and clamping it
		// to the first page is both what the caller meant and cheaper than a
		// failed round trip.
		offset = 0
	}

	pattern := escapeLikePattern(query)

	rows, err := r.q.Query(ctx, searchPlayers,
		pattern+"%",
		"%"+pattern+"%",
		clampSearchLimit(limit),
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: searching players: %w", err)
	}
	defer rows.Close()

	var out []application.Player
	for rows.Next() {
		var (
			p        application.Player
			username *string
			cityID   *string
		)
		if err := rows.Scan(
			&p.ID, &p.TelegramUserID, &username, &p.DisplayName,
			&p.Language, &cityID, &p.Status, &p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scanning player search row: %w", err)
		}

		// username is NULL-able (a Telegram handle is optional and mutable)
		// and city_id is NULL until the player has a city.
		if username != nil {
			p.Username = *username
		}
		p.CityID = cityID

		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading player search rows: %w", err)
	}

	return out, nil
}

// clampSearchLimit turns a caller's request into a limit this repository will
// actually run. See MaxSearchLimit.
func clampSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultSearchLimit
	case limit > MaxSearchLimit:
		return MaxSearchLimit
	default:
		return limit
	}
}

// escapeLikePattern neutralises the wildcards in a user-supplied search term.
//
// The term is typed by a player and is dropped between two % signs. Left
// alone, a term of "%" matches every row, and "_" matches any single
// character, so the cap on the result size would be all that stood between one
// message and a full listing of the player base. Escaping them makes a
// percent sign mean a percent sign.
//
// The backslash is escaped FIRST and the loop runs once over the string, so a
// term containing a literal backslash cannot come out as an escape character
// for whatever followed it — the classic ordering bug in this function, where
// escaping backslashes after wildcards turns the escape that was just added
// into an escaped backslash followed by a bare wildcard.
func escapeLikePattern(s string) string {
	if !strings.ContainsAny(s, `\%_`) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)

	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}

	return b.String()
}
