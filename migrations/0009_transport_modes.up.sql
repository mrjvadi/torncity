-- 0009_transport_modes — transport modes as content, and the mode and fare of
-- every journey.
-- Authority: docs/adr/0004-content-system.md (content is versioned rows),
-- docs/adr/0009-economic-control.md (a fare is a ledger transaction, reasons
-- travel_fare and transit_fare), docs/adr/0015-player-held-offices.md (a
-- public fare's city multiplier is the city.transit_fare lever, never a
-- column here). Schema: docs/database.md, section 8.
--
-- What is content and what is state:
--   * transport_facilities, transport_modes — content, one set of rows per
--     content version, like city_routes: re-activating an old version needs no
--     file.
--   * cities.facilities — content written in place with the rest of the city
--     row, like its name.
--   * city_routes.modes — content, per version with its route. NULL means the
--     author did not list modes and they are derived from the facilities.
--   * travels.mode, travels.ledger_transaction_id, travels.content_version —
--     state: which mode a journey used, the ledger transaction that paid its
--     fare, and the content version that priced it (ADR 0004 rule 3). All
--     NULL for a journey that began before this migration.
--
-- The rules — how a mode's numbers become a fare and a duration, how demand
-- moves the fare — stay in internal/domain/travel and are not represented
-- here. The CHECKs below are the last line of defence behind the loader's
-- validation, not the primary check.

BEGIN;

CREATE TABLE transport_facilities (
    content_version_id uuid NOT NULL REFERENCES content_versions (id),
    code               text NOT NULL,

    CONSTRAINT transport_facilities_pkey PRIMARY KEY (content_version_id, code),
    CONSTRAINT transport_facilities_code_check
        CHECK (code ~ '^[a-z][a-z0-9_]*$' AND length(code) <= 12)
);

CREATE TABLE transport_modes (
    id                     uuid    PRIMARY KEY,
    content_version_id     uuid    NOT NULL REFERENCES content_versions (id),
    -- File order: the order players are offered the modes in.
    position               int     NOT NULL,
    code                   text    NOT NULL,
    name                   text    NOT NULL,
    public                 boolean NOT NULL,
    requires               text[]  NOT NULL,
    speed                  int     NOT NULL,
    boarding_seconds       bigint  NOT NULL,
    base_fare              bigint  NOT NULL,
    fare_per_distance      bigint  NOT NULL,
    energy_cost            int     NOT NULL,
    demand_window_seconds  bigint  NOT NULL,
    demand_free_departures int     NOT NULL,
    demand_step_bps        int     NOT NULL,
    demand_max_bps         int     NOT NULL,

    CONSTRAINT transport_modes_version_code_key UNIQUE (content_version_id, code),
    CONSTRAINT transport_modes_version_position_key UNIQUE (content_version_id, position),
    CONSTRAINT transport_modes_code_check
        CHECK (code ~ '^[a-z][a-z0-9_]*$' AND length(code) <= 12),
    CONSTRAINT transport_modes_name_check CHECK (name <> ''),
    CONSTRAINT transport_modes_speed_check CHECK (speed > 0),
    CONSTRAINT transport_modes_boarding_check CHECK (boarding_seconds >= 0),
    CONSTRAINT transport_modes_fare_check CHECK (base_fare >= 0 AND fare_per_distance >= 0),
    CONSTRAINT transport_modes_energy_check CHECK (energy_cost >= 0),
    CONSTRAINT transport_modes_demand_check
        CHECK (demand_window_seconds > 0 AND demand_free_departures >= 0
               AND demand_step_bps >= 0 AND demand_max_bps >= 10000)
);

ALTER TABLE cities
    ADD COLUMN facilities text[] NOT NULL DEFAULT '{}';

COMMENT ON COLUMN cities.facilities IS
    'Transport facilities (transport.yml codes) the city has. A mode requiring a facility serves a route only when both ends have it.';

ALTER TABLE city_routes
    ADD COLUMN modes text[] NULL;

COMMENT ON COLUMN city_routes.modes IS
    'The transport modes the author listed for this route; NULL means derived from the endpoints'' facilities.';

ALTER TABLE travels
    ADD COLUMN mode                  text   NULL,
    ADD COLUMN ledger_transaction_id uuid   NULL,
    ADD COLUMN content_version       int    NULL;

COMMENT ON COLUMN travels.mode IS
    'The transport mode (content code) the journey used. NULL for journeys before transport modes existed.';
COMMENT ON COLUMN travels.ledger_transaction_id IS
    'The ledger transaction that paid the fare (travels.cost). NULL when the fare was zero.';
COMMENT ON COLUMN travels.content_version IS
    'The content version that priced the journey (ADR 0004 rule 3).';

-- Demand pricing counts the departures on one route by one mode within a
-- recent window, on every quote. This index answers that count from the
-- index alone.
CREATE INDEX travels_demand_idx ON travels (from_city_id, to_city_id, mode, departed_at);

COMMIT;
