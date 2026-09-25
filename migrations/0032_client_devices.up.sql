-- 0032_client_devices — the game clients a player has linked (cmd/clientapi,
-- api/client-api.md) and the refresh tokens that keep them signed in.
--
-- DEVICES. A device is one signed-in client: a native build linked with a
-- one-time /link code, or a Telegram Mini App signed in with the data
-- Telegram signed. bot_id is the bot the player linked it through, the one
-- its commands are played through. A device ends when it is revoked: from
-- the bot (/devices), by signing out, by linking one too many, or when a
-- refresh token is used twice (a copy is in somebody else's hands).
--
-- REFRESH TOKENS. The client holds a random token; the table holds only its
-- SHA-256, so a copy of this table signs nobody in. Every use replaces it:
-- the used row is kept, marked used_at, so a second use of it is recognised
-- as theft and ends the device.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE client_devices (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id),
    bot_id         uuid        NULL REFERENCES telegram_bots (id),
    name           text        NOT NULL,
    via            text        NOT NULL,
    created_at     timestamptz NOT NULL,
    last_seen_at   timestamptz NOT NULL,
    revoked_at     timestamptz NULL,
    revoked_reason text        NULL,

    CONSTRAINT client_devices_via_check CHECK (via IN ('link', 'telegram')),
    CONSTRAINT client_devices_name_check CHECK (char_length(name) BETWEEN 1 AND 64),
    CONSTRAINT client_devices_revoked_check CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL))
);

CREATE INDEX client_devices_player_active_idx
    ON client_devices (player_id, created_at)
    WHERE revoked_at IS NULL;

CREATE TABLE client_refresh_tokens (
    token_hash text        PRIMARY KEY,
    device_id  uuid        NOT NULL REFERENCES client_devices (id) ON DELETE CASCADE,
    issued_at  timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz NULL,

    CONSTRAINT client_refresh_tokens_expiry_check CHECK (expires_at > issued_at)
);

CREATE INDEX client_refresh_tokens_device_idx ON client_refresh_tokens (device_id);

COMMIT;
