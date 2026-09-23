package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// PlayerRepository is the players and player_bot_links adapter.
type PlayerRepository struct {
	q querier

	// defaultLanguage is what an insert writes when the caller supplied no
	// language. It is injected (player.default_language) rather than written
	// here, because this used to be the third independent copy of the same
	// literal and nothing kept the three in step.
	defaultLanguage string

	// codes draws a public player code. Nil means playercode.New, which is
	// what production uses; a test injects a scripted sequence to force a
	// collision.
	codes func() (string, error)
}

var _ application.PlayerRepository = (*PlayerRepository)(nil)

// NewPlayerRepository returns a repository using the pool directly, for reads
// that do not belong to a unit of work.
//
// defaultLanguage is player.default_language from the configuration. A caller
// that passes nothing gets defaultPlayerLanguage, which is the column default
// in migrations/0001_init.up.sql: the row has to carry something, and a NULL
// or empty language would break every screen that renders for that player.
func NewPlayerRepository(p *Pool, defaultLanguage string) *PlayerRepository {
	return &PlayerRepository{q: p.Raw(), defaultLanguage: defaultLanguage}
}

// defaultPlayerLanguage is the LAST-RESORT fallback, used only when no
// language was configured and none was supplied. It is deliberately the same
// string as the players.language column default in
// migrations/0001_init.up.sql, so a row written without a language and a row
// written by the database's own default agree.
const defaultPlayerLanguage = "fa"

// playerColumns is the column list scanPlayer reads, in its order. Every
// statement that returns a whole player selects exactly this.
const playerColumns = `id, telegram_user_id, username, display_name, language, city_id, status, created_at, public_code`

const selectPlayerByTelegramUserID = `
SELECT ` + playerColumns + `
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
	p, err := scanPlayer(r.q.QueryRow(ctx, selectPlayerByTelegramUserID, telegramUserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("postgres: loading player by telegram user %d: %w", telegramUserID, err)
	}
	return p, nil
}

const selectPlayerByID = `
SELECT ` + playerColumns + `
FROM players
WHERE id = $1::uuid`

// GetByID returns the player, or application.ErrPlayerNotFound.
//
// A malformed identifier is a miss, not a fault, for the reason CityRepository
// gives on ByID: an id that is not uuid text names no player.
func (r *PlayerRepository) GetByID(ctx context.Context, id string) (*application.Player, error) {
	p, err := scanPlayer(r.q.QueryRow(ctx, selectPlayerByID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUIDText(err) {
			return nil, application.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("postgres: loading player by id: %w", err)
	}
	return p, nil
}

// scanPlayer reads one row of playerColumns.
func scanPlayer(row pgx.Row) (*application.Player, error) {
	var (
		p        application.Player
		username *string
		cityID   *string
	)
	if err := row.Scan(
		&p.ID,
		&p.TelegramUserID,
		&username,
		&p.DisplayName,
		&p.Language,
		&cityID,
		&p.Status,
		&p.CreatedAt,
		&p.PublicCode,
	); err != nil {
		return nil, err
	}

	// username is NULL-able in the schema (a Telegram handle is optional and
	// mutable), city_id is NULL until the player has a city.
	if username != nil {
		p.Username = *username
	}
	p.CityID = cityID
	return &p, nil
}

// updatePlayerLanguage stores a player's chosen language. It touches that one
// column and updated_at, nothing else.
const updatePlayerLanguage = `
UPDATE players
   SET language = $2, updated_at = $3
 WHERE id = $1::uuid`

// SetLanguage stores the language the player chose, or returns
// application.ErrPlayerNotFound when no row has that id.
//
// It refuses an empty language outright: the column is NOT NULL and every
// screen rendered for this player reads it, so an empty value is a caller bug,
// never a choice. Whether lang is one the game actually speaks is checked by
// the caller against the message catalogue; see
// application.PlayerRepository.SetLanguage.
func (r *PlayerRepository) SetLanguage(ctx context.Context, playerID, lang string) error {
	if lang == "" {
		return fmt.Errorf("postgres: set language for player %s: empty language", playerID)
	}
	tag, err := r.q.Exec(ctx, updatePlayerLanguage, playerID, lang, time.Now().UTC())
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrPlayerNotFound
		}
		return fmt.Errorf("postgres: set language for player %s: %w", playerID, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPlayerNotFound
	}
	return nil
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
//
// language is NOT refreshed either. It starts as the Telegram client's
// language, but from then on it is the player's own choice (SetLanguage), and
// a first-contact upsert carrying whatever the client says today must not
// quietly undo it.
//
// # Where a new player stands, and where they live
//
// A new player is placed in a SPAWN city and that city also becomes their
// residence: city_id is where the player IS and changes with every journey,
// residence_city_id is where they LIVE and changes only through a deliberate
// residency change (migrations/0005_spawn_and_residence.up.sql). Both are
// written by the insert itself, from the same value ($6).
//
// $6 is chosen by Create before this statement runs: the caller's explicit
// city if it named one, otherwise content.PickSpawnCity over the active
// version's spawn weights. That pick is a pure function of the Telegram user
// id and the weights, so the two racing requests described above compute the
// SAME city, and whichever wins, the row agrees with both. Nothing here may
// ever use random(): a retried first contact would then disagree with itself.
//
// If no city has a positive spawn weight yet — content has never been loaded,
// or not since migration 0005 — $6 is NULL and the player is created with
// neither a city nor a residence. That is deliberate: failing first contact
// because an operator has not run the loader yet would lock everybody out of
// the bot, while a player with no city is recoverable — the next
// `admin content load` places every such player by the same pick
// (ContentStore.Apply).
//
// On conflict, both columns are only ever FILLED, never replaced: a returning
// player is never moved, and never rehomed, by first contact. A row that has a
// city but no residence takes its current city as its residence, which is the
// same rule the content loader applies.
//
// # The public code
//
// $9 is a code Create drew with crypto/rand (internal/shared/playercode). It
// is written by the INSERT and by nothing else: the DO UPDATE branch does not
// mention public_code, so the row that already exists keeps the code it was
// born with and RETURNING hands that code back. A code is something a player
// has shown to friends; a returning player's first contact — or the loser of
// the race above — silently replacing it would break every "find me with
// K7Q2M9A" already given out.
//
// A new row can still collide with ANOTHER player's code (players_public_code_key).
// That is not the telegram_user_id conflict ON CONFLICT names, so it raises
// 23505 instead; Create draws again and retries. See Create.
const insertPlayer = `
INSERT INTO players (id, telegram_user_id, username, display_name, language, city_id, residence_city_id, status, created_at, updated_at, public_code)
VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $6::uuid, $7, $8, $8, $9)
ON CONFLICT (telegram_user_id) DO UPDATE SET
    username          = EXCLUDED.username,
    display_name      = EXCLUDED.display_name,
    city_id           = COALESCE(players.city_id, EXCLUDED.city_id),
    residence_city_id = COALESCE(players.residence_city_id, players.city_id, EXCLUDED.residence_city_id),
    updated_at        = EXCLUDED.updated_at
RETURNING id, created_at, city_id::text, language, public_code`

// playersPublicCodeKey is the unique constraint on players.public_code, quoted
// from migrations/0007_player_public_code.up.sql. Create retries on exactly
// this constraint and on nothing else: any other 23505 from the insert is a
// real fault and must surface as one.
const playersPublicCodeKey = "players_public_code_key"

// maxPublicCodeAttempts bounds Create's retry on a code collision. With
// 31^7 codes, a draw collides with probability (players / 2.75e10); five
// consecutive collisions do not happen with an honest source, so reaching the
// bound means something is wrong with the source or the table, and it is
// reported rather than looped on.
const maxPublicCodeAttempts = 5

// selectSpawnCandidates reads the cities a new player may start in: the
// active content version's cities with a positive spawn weight. Cities an
// earlier load retired keep their old content_version_id and are excluded,
// whatever weight they last carried.
const selectSpawnCandidates = `
SELECT c.id::text, c.code, c.spawn_weight
  FROM cities c
  JOIN content_versions v ON v.id = c.content_version_id AND v.status = 'active'
 WHERE c.spawn_weight > 0`

// spawnCity returns the id of the city a new player with this Telegram user
// id starts in, or nil when no city has a positive spawn weight yet.
func (r *PlayerRepository) spawnCity(ctx context.Context, telegramUserID int64) (*string, error) {
	rows, err := r.q.Query(ctx, selectSpawnCandidates)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading spawn cities: %w", err)
	}
	defer rows.Close()

	var candidates []content.SpawnCandidate
	for rows.Next() {
		var c content.SpawnCandidate
		if err := rows.Scan(&c.ID, &c.Code, &c.Weight); err != nil {
			return nil, fmt.Errorf("postgres: scanning spawn city: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading spawn cities: %w", err)
	}

	picked, ok := content.PickSpawnCity(telegramUserID, candidates)
	if !ok {
		return nil, nil
	}
	return &picked.ID, nil
}

// Create inserts the player, or adopts the existing row for that Telegram
// user. p.ID, p.CreatedAt, p.CityID, p.Language and p.PublicCode are
// overwritten with the surviving row's values, so a caller that lost the race
// carries the right identity onward, every caller learns which city the player
// is standing in and which code they have, and a returning player's chosen
// language is what comes back, not the client's.
//
// A nil p.CityID asks for the player's spawn city, which also becomes their
// residence; a non-nil one places (and houses) the player there instead.
// p.CityID is still nil afterwards only if no spawn city exists yet (see
// insertPlayer).
//
// # Retrying a code collision
//
// Each attempt runs inside its own savepoint (inTx on a unit of work's
// transaction; a short transaction of its own on the pool). A unique
// violation aborts whatever transaction it happens in, so without the
// savepoint the retry would be refused by the server and — worse — the
// caller's whole unit of work would be dead. Rolled back to the savepoint,
// the outer transaction is intact and the next draw can go.
//
// A username given here is also taken from any other player whose record
// still claims it; see SetUsername.
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
	if u := strings.TrimSpace(p.Username); u != "" {
		username = &u
	}

	status := p.Status
	if status == "" {
		// players_status_check accepts active / banned / deleted; a new
		// player is active by definition.
		status = "active"
	}

	language := p.Language
	if language == "" {
		language = r.defaultLanguage
	}
	if language == "" {
		language = defaultPlayerLanguage
	}

	// Picked on every call, including for a player who already exists: the
	// statement is one upsert and cannot know in advance which branch it will
	// take. For an existing row the pick is discarded unless the row has no
	// city yet, in which case it is exactly the city the loader would give.
	cityID := p.CityID
	if cityID == nil {
		if cityID, err = r.spawnCity(ctx, p.TelegramUserID); err != nil {
			return fmt.Errorf("postgres: creating player for telegram user %d: %w", p.TelegramUserID, err)
		}
	}

	draw := r.codes
	if draw == nil {
		draw = playercode.New
	}

	var saved savedPlayer
	for attempt := 1; ; attempt++ {
		code, err := draw()
		if err != nil {
			return fmt.Errorf("postgres: creating player for telegram user %d: drawing a public code: %w", p.TelegramUserID, err)
		}

		saved, err = r.upsertPlayer(ctx, id, p.TelegramUserID, username, p.DisplayName, language, cityID, status, now, code)
		if err == nil {
			break
		}
		if !violates(err, sqlstateUniqueViolation, playersPublicCodeKey) || attempt >= maxPublicCodeAttempts {
			return fmt.Errorf("postgres: creating player for telegram user %d: %w", p.TelegramUserID, err)
		}
		// Another player already holds this code. Nothing was written; draw
		// again.
	}

	p.ID, p.CreatedAt, p.CityID, p.Language, p.PublicCode = saved.id, saved.createdAt, saved.cityID, saved.language, saved.publicCode
	p.Status = status
	if username != nil {
		p.Username = *username
		if err := r.releaseUsername(ctx, p.ID, *username, now); err != nil {
			return err
		}
	} else {
		p.Username = ""
	}

	return nil
}

// savedPlayer is what insertPlayer returns.
type savedPlayer struct {
	id         string
	createdAt  time.Time
	cityID     *string
	language   string
	publicCode string
}

// upsertPlayer runs insertPlayer once, inside a savepoint when the querier can
// open one; see Create.
func (r *PlayerRepository) upsertPlayer(
	ctx context.Context,
	id string,
	telegramUserID int64,
	username *string,
	displayName, language string,
	cityID *string,
	status string,
	now time.Time,
	code string,
) (savedPlayer, error) {
	var out savedPlayer
	run := func(ctx context.Context, q querier) error {
		return q.QueryRow(ctx, insertPlayer,
			id,
			telegramUserID,
			username,
			displayName,
			language,
			cityID,
			status,
			now,
			code,
		).Scan(&out.id, &out.createdAt, &out.cityID, &out.language, &out.publicCode)
	}

	t, ok := r.q.(transactor)
	if !ok {
		// Only a test double lacks Begin: both production queriers, the pool
		// and a unit of work's transaction, are transactors.
		return out, run(ctx, r.q)
	}
	err := inTx(ctx, t, func(ctx context.Context, tx pgx.Tx) error { return run(ctx, tx) })
	return out, err
}

// updatePlayerUsername stores what Telegram reports as the player's username
// now. $2 is NULL when the account has none: a username Telegram no longer
// reports must not stay searchable, or a search for it finds this player after
// someone else has taken the name.
const updatePlayerUsername = `
UPDATE players
   SET username = $2, updated_at = $3
 WHERE id = $1::uuid`

// releaseUsername takes a username away from every OTHER player whose record
// still claims it.
//
// Telegram lets one account hold a username at a time. So when this player is
// seen with it, any other row that says the same is out of date — that player
// renamed, or gave the name up, and has not talked to the bot since. Clearing
// it here is what keeps a search for the name from finding the previous owner.
//
// The comparison is on lower() because usernames are case-insensitive, and
// "username IS NOT NULL" is spelled out so the planner can use the partial
// index players_username_lower_idx.
const releaseUsername = `
UPDATE players
   SET username = NULL, updated_at = $3
 WHERE username IS NOT NULL
   AND lower(username) = lower($2)
   AND id <> $1::uuid`

// SetUsername records the username Telegram reports for the player now, or
// clears it when username is empty. See application.PlayerRepository.
func (r *PlayerRepository) SetUsername(ctx context.Context, playerID, username string) error {
	now := time.Now().UTC()

	var value *string
	if u := strings.TrimSpace(username); u != "" {
		value = &u
	}

	tag, err := r.q.Exec(ctx, updatePlayerUsername, playerID, value, now)
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.ErrPlayerNotFound
		}
		return fmt.Errorf("postgres: set username for player %s: %w", playerID, err)
	}
	if tag.RowsAffected() == 0 {
		return application.ErrPlayerNotFound
	}

	if value == nil {
		return nil
	}
	return r.releaseUsername(ctx, playerID, *value, now)
}

// releaseUsername runs the statement of the same name.
func (r *PlayerRepository) releaseUsername(ctx context.Context, playerID, username string, now time.Time) error {
	if _, err := r.q.Exec(ctx, releaseUsername, playerID, username, now); err != nil {
		return fmt.Errorf("postgres: releasing a username for player %s: %w", playerID, err)
	}
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
