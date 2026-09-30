-- 0051_founding_form — the founding form
-- (docs/adr/0028-world-and-settlements.md section 3, extended).
--
-- A group's «ساخت روستا» no longer founds at once: it opens a short-lived
-- DRAFT, and the player who asked completes the village's details in the
-- game client - name, emblem, motto and the national currency the village
-- reserves - before anything exists. Submitting the draft founds the village
-- in one transaction.
--
-- 1. settlement_founding_drafts: one row per attempt. At most one OPEN draft
--    per group (partial unique index); an expired one is marked so by the
--    next «ساخت روستا» and never founds anything. A submitted draft keeps
--    the settlement it became, which is what makes a repeated submit answer
--    from the existing village instead of founding twice.
-- 2. cities: the emblem (four codes drawn by the client, written as emoji
--    by the server), the motto and name_key, the normalised name a second
--    village may not take (unique among founded settlements).
-- 3. village_currency_reservations: the currency a village reserves for the
--    day it declares a country (docs/adr/0029, phase C4). The village keeps
--    using SUP until then; the ledger and the currencies table are not
--    touched. The code is unique across reservations; that it is not an
--    existing currency is checked in the same transaction as the insert.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- CHECK constraints are named.

BEGIN;

CREATE TABLE settlement_founding_drafts (
    id                   uuid        PRIMARY KEY,
    chat_id              bigint      NOT NULL,
    bot_id               uuid        NOT NULL REFERENCES telegram_bots (id),
    founder_player_id    uuid        NOT NULL REFERENCES players (id),
    language             text        NOT NULL,
    suggested_name       text        NOT NULL,
    suggested_name_latin text        NOT NULL DEFAULT '',
    status               text        NOT NULL DEFAULT 'open',
    created_at           timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL,
    submitted_at         timestamptz NULL,
    settlement_id        uuid        NULL REFERENCES cities (id),

    CONSTRAINT founding_drafts_chat_check CHECK (chat_id < 0),
    CONSTRAINT founding_drafts_language_check CHECK (language ~ '^[a-z]{2,3}$'),
    CONSTRAINT founding_drafts_status_check CHECK (status IN ('open', 'submitted', 'expired')),
    CONSTRAINT founding_drafts_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT founding_drafts_submitted_check CHECK (
        (status = 'submitted') = (settlement_id IS NOT NULL AND submitted_at IS NOT NULL)
    )
);

-- One open draft per group: the race between two replicas handling the same
-- «ساخت روستا» ends at this index, the loser reading the winner's draft.
CREATE UNIQUE INDEX founding_drafts_open_chat_idx
    ON settlement_founding_drafts (chat_id) WHERE status = 'open';
CREATE INDEX founding_drafts_open_founder_idx
    ON settlement_founding_drafts (founder_player_id, created_at DESC) WHERE status = 'open';

COMMENT ON TABLE settlement_founding_drafts IS
    'A group founding waiting for its founder to complete the village details in the game client. Nothing exists until it is submitted; expired drafts found nothing.';
COMMENT ON COLUMN settlement_founding_drafts.suggested_name IS
    'The generated place name the form starts from, in the group''s language; the founder may change it.';

ALTER TABLE cities
    ADD COLUMN emblem_shape   text NULL,
    ADD COLUMN emblem_color_a text NULL,
    ADD COLUMN emblem_color_b text NULL,
    ADD COLUMN emblem_icon    text NULL,
    ADD COLUMN motto          text NULL,
    ADD COLUMN name_key       text NULL;

ALTER TABLE cities
    ADD CONSTRAINT cities_emblem_check CHECK (
        (emblem_shape IS NULL AND emblem_color_a IS NULL AND emblem_color_b IS NULL AND emblem_icon IS NULL)
        OR (emblem_shape IS NOT NULL AND emblem_color_a IS NOT NULL AND emblem_color_b IS NOT NULL AND emblem_icon IS NOT NULL)
    ),
    ADD CONSTRAINT cities_motto_check CHECK (motto IS NULL OR char_length(motto) <= 200);

-- A founded settlement's name is unique among founded settlements (compared
-- by name_key: case, spaces, hyphens and the zero-width non-joiner ignored).
CREATE UNIQUE INDEX cities_founded_name_key_idx
    ON cities (name_key) WHERE origin = 'founded' AND name_key IS NOT NULL;

COMMENT ON COLUMN cities.emblem_shape IS
    'The village emblem''s outline; with emblem_color_a/_b and emblem_icon it is four codes of configs/content/founding.yml. NULL for a content city.';
COMMENT ON COLUMN cities.name_key IS
    'internal/domain/settlement.NameKey of the name: what two names are compared by.';

CREATE TABLE village_currency_reservations (
    code          text        PRIMARY KEY,
    name          text        NOT NULL,
    name_key      text        NOT NULL,
    symbol        text        NOT NULL,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    reserved_at   timestamptz NOT NULL,

    CONSTRAINT village_currency_reservations_code_check CHECK (code ~ '^[A-Z]{2,6}$'),
    CONSTRAINT village_currency_reservations_name_check CHECK (char_length(name) BETWEEN 1 AND 60),
    CONSTRAINT village_currency_reservations_symbol_check CHECK (char_length(symbol) BETWEEN 1 AND 8),
    CONSTRAINT village_currency_reservations_settlement_key UNIQUE (settlement_id),
    CONSTRAINT village_currency_reservations_name_key UNIQUE (name_key)
);

COMMENT ON TABLE village_currency_reservations IS
    'The national currency a village named at its founding. The village uses SUP until it declares a country (ADR 0029 C4), which turns the reservation into a row of currencies. The ledger does not read this table.';

COMMIT;
