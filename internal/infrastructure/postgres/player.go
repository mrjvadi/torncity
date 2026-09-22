package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// PlayerRepository is the players and player_bot_links adapter.
type PlayerRepository struct {
	q querier
}

var _ application.PlayerRepository = (*PlayerRepository)(nil)

// NewPlayerRepository returns a repository using the pool directly, for reads
// that do not belong to a unit of work.
func NewPlayerRepository(p *Pool) *PlayerRepository { return &PlayerRepository{q: p.Raw()} }

const selectPlayerByTelegramUserID = `
SELECT id, telegram_user_id, username, display_name, language, city_id, status, created_at
FROM players
WHERE telegram_user_id = $1`

// GetByTelegramUserID returns the player, or application.ErrPlayerNotFound.
//
// telegram_user_id is globally unique and deliberately not combined with a bot
// id (docs/database.md, "قانون بحرانی"): whichever bot a person talks to, they
// resolve to this one row.
//
// The sentinel is returned unwrapped. Callers branch on it with errors.Is, and
// "no such player" is a normal first-contact outcome rather than a database
// fault — attaching the driver error would classify it as internal and put a
// generic failure in front of a player who simply has not registered yet.
func (r *PlayerRepository) GetByTelegramUserID(ctx context.Context, telegramUserID int64) (*application.Player, error) {
	var (
		p        application.Player
		username *string
		cityID   *string
	)

	err := r.q.QueryRow(ctx, selectPlayerByTelegramUserID, telegramUserID).Scan(
		&p.ID,
		&p.TelegramUserID,
		&username,
		&p.DisplayName,
		&p.Language,
		&cityID,
		&p.Status,
		&p.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("postgres: loading player by telegram user %d: %w", telegramUserID, err)
	}

	// username is NULL-able in the schema (a Telegram handle is optional and
	// mutable), city_id is NULL until the player has a city.
	if username != nil {
		p.Username = *username
	}
	p.CityID = cityID

	return &p, nil
}

// insertPlayer is written as a single upsert on purpose.
//
// The port requires that two concurrent first-contact requests for the same
// Telegram user produce one player, not two. Three strategies were available:
//
//  1. SELECT then INSERT. Not a candidate: under READ COMMITTED the SELECT
//     cannot see a concurrent uncommitted insert, so both requests read
//     "absent" and both insert. One then fails on the unique index — the
//     error path becomes the normal path, and the loser has no row to return.
//
//  2. INSERT ... ON CONFLICT DO NOTHING followed by a read-back. Correct only
//     if the read-back is guaranteed to find the row, and it is not: DO
//     NOTHING does not block on the conflicting row, so the loser's SELECT
//     runs while the winner is still uncommitted and finds nothing. The result
//     is an intermittent "player not found" on exactly the race this is meant
//     to survive.
//
//  3. What is used here: ON CONFLICT ... DO UPDATE ... RETURNING. DO UPDATE
//     takes a row lock, so the loser waits for the winner to commit and then
//     returns the winning row in the same statement. Exactly one row exists
//     and both callers receive its identity, in one round trip, with no error
//     path to get right.
//
// The update is not a no-op: the mutable profile fields are refreshed, because
// a returning player's Telegram handle or display name may have changed since
// the row was written. created_at and id are never touched — the loser of the
// race must adopt the winner's identity, not overwrite it.
const insertPlayer = `
INSERT INTO players (id, telegram_user_id, username, display_name, language, city_id, status, created_at, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8, $8)
ON CONFLICT (telegram_user_id) DO UPDATE SET
    username     = EXCLUDED.username,
    display_name = EXCLUDED.display_name,
    language     = EXCLUDED.language,
    updated_at   = EXCLUDED.updated_at
RETURNING id, created_at`

// Create inserts the player, or adopts the existing row for that Telegram
// user. p.ID and p.CreatedAt are overwritten with the surviving row's values,
// so a caller that lost the race carries the right identity onward.
func (r *PlayerRepository) Create(ctx context.Context, p *application.Player) error {
	if p == nil {
		return fmt.Errorf("postgres: create player: nil player")
	}

	id, err := ensureID(p.ID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	// NULL-able columns are passed as pointers so an empty string becomes
	// NULL rather than an empty Telegram handle, which would be a lie.
	var username *string
	if p.Username != "" {
		username = &p.Username
	}

	status := p.Status
	if status == "" {
		// players_status_check accepts active / banned / deleted; a new
		// player is active by definition.
		status = "active"
	}

	language := p.Language
	if language == "" {
		// Matches the column default in migrations/0001_init.up.sql.
		language = "fa"
	}

	if err := r.q.QueryRow(ctx, insertPlayer,
		id,
		p.TelegramUserID,
		username,
		p.DisplayName,
		language,
		p.CityID,
		status,
		now,
	).Scan(&p.ID, &p.CreatedAt); err != nil {
		return fmt.Errorf("postgres: creating player for telegram user %d: %w", p.TelegramUserID, err)
	}

	p.Status = status
	p.Language = language

	return nil
}

// upsertBotLink records or refreshes a player's chat with one bot.
//
// first_seen_at is set only on insert and never in the update: it answers
// "when did this person first start this bot", which a later /start must not
// rewrite. last_seen_at and telegram_chat_id are refreshed, and so is
// is_reachable — a player who messages the bot again has plainly unblocked it,
// and leaving a stale false there would keep them out of the partial index
// that notification routing reads.
const upsertBotLink = `
INSERT INTO player_bot_links (id, player_id, bot_id, telegram_chat_id, is_reachable, first_seen_at, last_seen_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $6)
ON CONFLICT (player_id, bot_id) DO UPDATE SET
    telegram_chat_id = EXCLUDED.telegram_chat_id,
    is_reachable     = EXCLUDED.is_reachable,
    last_seen_at     = EXCLUDED.last_seen_at`

// LinkBot records or refreshes the player's chat with one bot.
func (r *PlayerRepository) LinkBot(ctx context.Context, link application.BotLink) error {
	id, err := newUUID()
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	if _, err := r.q.Exec(ctx, upsertBotLink,
		id,
		link.PlayerID,
		link.BotID,
		link.TelegramChatID,
		link.IsReachable,
		now,
	); err != nil {
		return fmt.Errorf("postgres: linking player %s to bot %s: %w", link.PlayerID, link.BotID, err)
	}

	return nil
}
