-- 0026_tariffs_and_achievements — stage F, leftovers and vehicles: the border
-- tariff charged on trade across a border, achievements awarded once from
-- the game's events, and the vehicle a journey was driven in. Rules:
-- internal/domain/diplomacy (Tariff), internal/domain/achievement,
-- internal/domain/vehicle. Content: configs/content/achievements.yml,
-- items.yml (a vehicle's block). Decision:
-- docs/adr/0024-property-and-politics.md. Money:
-- docs/adr/0009-economic-control.md (border_tariff, achievement_reward,
-- fuel).
--
-- EXACTLY ONCE. A trade is tariffed once (the reference is unique). An
-- achievement is awarded once per player (primary key), and an event moves
-- a player's achievements once (achievement_events is the consumer's inbox,
-- written in the same transaction as the progress).
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- border_tariffs — the tariff withheld from one sale across a border, paid
-- into the importing country's national treasury: the trade, the two
-- countries, its value, the rate after any treaty's discount, and the
-- tariff. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE border_tariffs (
    id              uuid        PRIMARY KEY,
    reference_type  text        NOT NULL,
    reference_id    uuid        NOT NULL,
    importer_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    exporter_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    value           bigint      NOT NULL,
    rate_bps        int         NOT NULL,
    tariff          bigint      NOT NULL,
    at              timestamptz NOT NULL,

    CONSTRAINT border_tariffs_reference_key UNIQUE (reference_type, reference_id),
    CONSTRAINT border_tariffs_reference_check CHECK (reference_type IN ('company_sales', 'market_trades')),
    CONSTRAINT border_tariffs_countries_check CHECK (importer_id <> exporter_id),
    CONSTRAINT border_tariffs_amounts_check CHECK (value > 0 AND rate_bps BETWEEN 1 AND 10000 AND tariff > 0
        AND tariff <= value)
);

CREATE TRIGGER border_tariffs_append_only
    BEFORE UPDATE OR DELETE ON border_tariffs
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER border_tariffs_no_truncate
    BEFORE TRUNCATE ON border_tariffs
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- achievement_progress — how far a player has come toward each achievement
-- (achievements.yml): the count of the events it counts.
-- ---------------------------------------------------------------------------
CREATE TABLE achievement_progress (
    player_id  uuid        NOT NULL REFERENCES players (id),
    code       text        NOT NULL,
    count      bigint      NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT achievement_progress_pkey PRIMARY KEY (player_id, code),
    CONSTRAINT achievement_progress_count_check CHECK (count >= 0)
);

-- ---------------------------------------------------------------------------
-- player_achievements — an achievement a player earned, once: when, and the
-- cash it paid (what the day's caps let through) and what they kept back.
-- ---------------------------------------------------------------------------
CREATE TABLE player_achievements (
    player_id  uuid        NOT NULL REFERENCES players (id),
    code       text        NOT NULL,
    awarded_at timestamptz NOT NULL,
    cash       bigint      NOT NULL,
    withheld   bigint      NOT NULL,

    CONSTRAINT player_achievements_pkey PRIMARY KEY (player_id, code),
    CONSTRAINT player_achievements_cash_check CHECK (cash >= 0 AND withheld >= 0)
);
CREATE INDEX player_achievements_day_idx ON player_achievements (awarded_at);

-- ---------------------------------------------------------------------------
-- achievement_events — the achievements consumer's inbox: an event moves a
-- player's achievements once.
-- ---------------------------------------------------------------------------
CREATE TABLE achievement_events (
    event_id     text        NOT NULL,
    player_id    uuid        NOT NULL REFERENCES players (id),
    subject      text        NOT NULL,
    processed_at timestamptz NOT NULL,

    CONSTRAINT achievement_events_pkey PRIMARY KEY (event_id, player_id)
);

COMMENT ON COLUMN travels.vehicle_id IS
    'The item piece (a vehicle, items.yml vehicle:) the player drove on the journey; NULL for a hired or public mode.';

COMMIT;
