-- 0001_init — phase 0 walking skeleton.
-- Scope: identity + infrastructure only. No game-economy tables.
-- Schema authority: docs/database.md. Every column, type and constraint below
-- is taken from that document; nothing here is invented.
--
-- Conventions (docs/database.md, "اصول"):
--   * all instants are timestamptz, stored in UTC;
--   * money and Telegram identifiers are bigint (never float);
--   * surrogate keys are uuid, except where the document specifies otherwise
--     (outbox.id is bigserial because the outbox is polled in insertion order).

BEGIN;

-- ---------------------------------------------------------------------------
-- telegram_bots — registry of the bot fleet.
-- Created first: player_bot_links references it.
-- ---------------------------------------------------------------------------
CREATE TABLE telegram_bots (
    id               uuid        PRIMARY KEY,
    bot_key          text        NOT NULL UNIQUE,
    telegram_bot_id  bigint      NOT NULL UNIQUE,
    username         text        NOT NULL,
    -- Holds the NAME of an environment variable that carries the bot token
    -- (for example 'TORN_BOT01_TOKEN'), never the token itself. A real token
    -- must never be written to this column, to a migration, or to a seed file.
    token_secret_ref text        NOT NULL,
    status           text        NOT NULL,
    gateway_group    text        NULL,
    enabled          boolean     NOT NULL DEFAULT true,
    rate_limit       int         NOT NULL,   -- messages per second for this bot
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,

    CONSTRAINT telegram_bots_status_check
        CHECK (status IN ('active', 'paused', 'revoked'))
);

COMMENT ON COLUMN telegram_bots.token_secret_ref IS
    'Name of the environment variable holding the bot token. Never the token value itself.';

-- ---------------------------------------------------------------------------
-- players — global player identity, one row per human across the whole fleet.
-- ---------------------------------------------------------------------------
CREATE TABLE players (
    id               uuid        PRIMARY KEY,
    -- Globally UNIQUE on its own, deliberately NOT combined with a bot id.
    -- The natural key is the person, not the person-on-a-given-bot: whichever
    -- bot a player talks to, they must resolve to this same row. Making the
    -- key (telegram_user_id, bot_id) would give one human a separate world per
    -- bot and break the shared-world assumption the whole design rests on.
    telegram_user_id bigint      NOT NULL UNIQUE,
    username         text        NULL,       -- Telegram handle: mutable, not reliable
    display_name     text        NOT NULL,
    language         text        NOT NULL DEFAULT 'fa',
    -- docs/database.md declares city_id as FK -> cities. The cities table is
    -- out of scope for phase 0, so the column is created NULL-able with no
    -- foreign key. The FK constraint is added by the later migration that
    -- creates cities.
    city_id          uuid        NULL,
    status           text        NOT NULL,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,

    CONSTRAINT players_status_check
        CHECK (status IN ('active', 'banned', 'deleted'))
);

COMMENT ON COLUMN players.city_id IS
    'Current city. Foreign key to cities is added in a later migration, once the cities table exists.';

CREATE INDEX players_city_id_idx ON players (city_id);
CREATE INDEX players_status_idx  ON players (status);

-- ---------------------------------------------------------------------------
-- player_bot_links — which player has started which bot.
-- A Telegram bot may only message users that started it, so without this table
-- notifications cannot be routed in a multi-bot fleet.
-- ---------------------------------------------------------------------------
CREATE TABLE player_bot_links (
    id               uuid        PRIMARY KEY,
    player_id        uuid        NOT NULL REFERENCES players (id),
    bot_id           uuid        NOT NULL REFERENCES telegram_bots (id),
    telegram_chat_id bigint      NOT NULL,   -- private chat between player and this bot
    is_reachable     boolean     NOT NULL DEFAULT true,  -- false once the user blocks the bot
    first_seen_at    timestamptz NOT NULL,
    last_seen_at     timestamptz NOT NULL,

    -- One link row per (player, bot) pair: re-issuing /start updates the row.
    CONSTRAINT player_bot_links_player_bot_key UNIQUE (player_id, bot_id)
);

-- Partial index: notification routing only ever looks for a player's still
-- reachable links, so blocked links stay out of the index entirely.
CREATE INDEX player_bot_links_reachable_idx
    ON player_bot_links (player_id)
    WHERE is_reachable;

-- ---------------------------------------------------------------------------
-- outbox — transactional outbox. A row is written inside the same transaction
-- as the state change it announces, and published to NATS afterwards.
-- ---------------------------------------------------------------------------
CREATE TABLE outbox (
    id           bigserial   PRIMARY KEY,
    event_id     uuid        NOT NULL UNIQUE,
    subject      text        NOT NULL,   -- destination subject in NATS
    metadata     jsonb       NOT NULL,   -- request_id, trace_id, player_id, schema_version
    payload      jsonb       NOT NULL,
    status       text        NOT NULL DEFAULT 'pending',
    attempts     int         NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL,
    published_at timestamptz NULL,

    CONSTRAINT outbox_status_check
        CHECK (status IN ('pending', 'published', 'failed'))
);

-- Partial index on the primary key: the publisher polls unpublished rows in
-- insertion order, and published rows (the overwhelming majority over time)
-- are never scanned.
CREATE INDEX outbox_pending_idx
    ON outbox (id)
    WHERE status = 'pending';

-- ---------------------------------------------------------------------------
-- inbox_messages — consumer-side deduplication.
-- ---------------------------------------------------------------------------
CREATE TABLE inbox_messages (
    message_id   text        NOT NULL,   -- NATS message id
    consumer     text        NOT NULL,   -- consumer name
    processed_at timestamptz NOT NULL,

    -- Composite key, not message_id alone: the same message is legitimately
    -- delivered to several consumers, and each must process it exactly once.
    -- This is what turns NATS at-least-once delivery into effective
    -- exactly-once processing per consumer.
    CONSTRAINT inbox_messages_pkey PRIMARY KEY (message_id, consumer)
);

-- ---------------------------------------------------------------------------
-- idempotency_keys — command-level replay protection.
-- ---------------------------------------------------------------------------
CREATE TABLE idempotency_keys (
    id              uuid        PRIMARY KEY,
    player_id       uuid        NOT NULL REFERENCES players (id),
    idempotency_key text        NOT NULL,
    request_id      text        NOT NULL,
    command         text        NOT NULL,
    response_hash   text        NULL,       -- lets a replay return the original response
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,

    -- Scoped per player: two players may legitimately send the same key.
    CONSTRAINT idempotency_keys_player_key_key UNIQUE (player_id, idempotency_key)
);

-- Supports the sweeper that deletes expired keys without a full scan.
CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

COMMIT;
