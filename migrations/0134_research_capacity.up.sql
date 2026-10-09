-- 0134_research_capacity: research capacity and speed (ADR 0048).
--
-- Until now a settlement ran ONE research project at a time (settlement_research_one_running_idx). Now the
-- number of projects it can run follows what it has: one free slot for everybody (so a live settlement keeps
-- exactly what it had, an in-progress project is untouched) plus the slots of its staffed research buildings
-- (library, laboratory, academy). How fast a project goes and what it costs are decided when it STARTS and
-- recorded on the row (slot, speed, ahead-of-era factor, breakthrough discount, sharing bonus), so a later
-- change of config or staff never moves a running project's finish time.
--
-- The rest of the model:
--   research_days            the fence of a settlement's research day (first reader of a local day pays the
--                            scholars and draws the upkeep, like village_storage_days), one row per building
--                            in research_day_buildings telling which were staffed and by whom (skill levels).
--   research_posts           a player's post as a scholar in a research building (hired through the labour
--                            market, paid a day's wage by the treasury); the NPC posts come from the pool.
--   settlement_experience    breakthrough progress: points of real work in a field (learning by doing),
--                            spent as a discount on the next project of that field.
--   research_pacts           a research-sharing pact between two settlements; its partners holding an item
--                            speed a project of that item up (locked into the project at its start).
--
-- Conventions as 0114. Additive; never a wipe: every existing row gets the free slot at the old speed.
BEGIN;

DROP INDEX settlement_research_one_running_idx;
CREATE INDEX settlement_research_running_idx ON settlement_research (settlement_id) WHERE status = 'running';

ALTER TABLE settlement_research
    ADD COLUMN slot_ref     text    NOT NULL DEFAULT 'free',
    ADD COLUMN speed_bps    integer NOT NULL DEFAULT 10000,
    ADD COLUMN ahead_bps    integer NOT NULL DEFAULT 10000,
    ADD COLUMN discount_bps integer NOT NULL DEFAULT 0,
    ADD COLUMN share_bps    integer NOT NULL DEFAULT 0,
    ADD COLUMN spent_points bigint  NOT NULL DEFAULT 0,
    ADD CONSTRAINT settlement_research_slot_check CHECK (length(btrim(slot_ref)) > 0),
    ADD CONSTRAINT settlement_research_speed_check CHECK (speed_bps >= 1),
    ADD CONSTRAINT settlement_research_ahead_check CHECK (ahead_bps >= 10000),
    ADD CONSTRAINT settlement_research_discount_check CHECK (discount_bps BETWEEN 0 AND 10000),
    ADD CONSTRAINT settlement_research_share_check CHECK (share_bps >= 0 AND spent_points >= 0);

COMMENT ON COLUMN settlement_research.slot_ref IS
    'The slot the project took: free (every settlement has one) or the id of the research building (ADR 0048). The speed, ahead-of-era factor, breakthrough discount and sharing bonus are the quote at the start.';

CREATE TABLE research_days (
    settlement_id    uuid        NOT NULL,
    day              bigint      NOT NULL,
    buildings        integer     NOT NULL,
    staffed          integer     NOT NULL,
    scholars_player  integer     NOT NULL DEFAULT 0,
    scholars_npc     integer     NOT NULL DEFAULT 0,
    wage_player      bigint      NOT NULL DEFAULT 0,
    wage_npc         bigint      NOT NULL DEFAULT 0,
    ledger_player_tx uuid        NULL,
    ledger_npc_tx    uuid        NULL,
    upkeep_units     bigint      NOT NULL DEFAULT 0,
    at               timestamptz NOT NULL,

    CONSTRAINT research_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT research_days_counts_check CHECK (buildings >= 0 AND staffed >= 0 AND staffed <= buildings AND scholars_player >= 0 AND scholars_npc >= 0),
    CONSTRAINT research_days_amounts_check CHECK (wage_player >= 0 AND wage_npc >= 0 AND upkeep_units >= 0),
    CONSTRAINT research_days_player_wage_check CHECK ((wage_player > 0) = (ledger_player_tx IS NOT NULL)),
    CONSTRAINT research_days_npc_wage_check CHECK ((wage_npc > 0) = (ledger_npc_tx IS NOT NULL))
);

CREATE TABLE research_day_buildings (
    settlement_id uuid      NOT NULL,
    day           bigint    NOT NULL,
    building_id   uuid      NOT NULL,
    type_code     text      NOT NULL,
    -- skills are the levels of the scholars on duty that day; empty: the building stood idle (no scholars,
    -- no wage money or no upkeep in stock) and gave no slots.
    skills        integer[] NOT NULL DEFAULT '{}',
    staffed       boolean   NOT NULL,
    -- idle says why a building did not work: no_scholars, no_wage or no_upkeep; empty when it worked.
    idle          text      NOT NULL DEFAULT '',

    CONSTRAINT research_day_buildings_idle_check CHECK (idle IN ('', 'no_scholars', 'no_wage', 'no_upkeep') AND (staffed = (idle = ''))),
    CONSTRAINT research_day_buildings_pk PRIMARY KEY (settlement_id, day, building_id),
    CONSTRAINT research_day_buildings_day_fk FOREIGN KEY (settlement_id, day) REFERENCES research_days (settlement_id, day) ON DELETE CASCADE
);

CREATE TABLE research_posts (
    building_id   uuid        NOT NULL REFERENCES settlement_buildings (id),
    player_id     uuid        NOT NULL REFERENCES players (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    since         timestamptz NOT NULL,

    CONSTRAINT research_posts_pk PRIMARY KEY (building_id, player_id)
);
-- one post per player: a scholar works in one place
CREATE UNIQUE INDEX research_posts_player_idx ON research_posts (player_id);
CREATE INDEX research_posts_settlement_idx ON research_posts (settlement_id);

CREATE TABLE settlement_experience (
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    field         text        NOT NULL,
    points        bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT settlement_experience_pk PRIMARY KEY (settlement_id, field),
    CONSTRAINT settlement_experience_check CHECK (points >= 0 AND length(btrim(field)) > 0)
);

CREATE TABLE research_pacts (
    id            uuid        PRIMARY KEY,
    settlement_a  uuid        NOT NULL REFERENCES cities (id),
    settlement_b  uuid        NOT NULL REFERENCES cities (id),
    status        text        NOT NULL,
    proposed_by   uuid        NULL REFERENCES players (id),
    proposed_at   timestamptz NOT NULL,
    answered_at   timestamptz NULL,
    ended_at      timestamptz NULL,

    CONSTRAINT research_pacts_status_check CHECK (status IN ('proposed', 'active', 'declined', 'ended')),
    CONSTRAINT research_pacts_distinct_check CHECK (settlement_a <> settlement_b)
);
-- at most one open pact (proposed or active) between two settlements, whichever way round
CREATE UNIQUE INDEX research_pacts_open_idx ON research_pacts (LEAST(settlement_a, settlement_b), GREATEST(settlement_a, settlement_b))
    WHERE status IN ('proposed', 'active');
CREATE INDEX research_pacts_a_idx ON research_pacts (settlement_a) WHERE status IN ('proposed', 'active');
CREATE INDEX research_pacts_b_idx ON research_pacts (settlement_b) WHERE status IN ('proposed', 'active');

-- The new charter permission research.share (offer, answer and end a research-sharing pact): the head office of a
-- stored charter that may start research gets it with the same hand (the founder's office holds every power; a
-- charter that already took research.start from it keeps that choice). Offices created later get it from the code.
UPDATE charter_offices
   SET grants = grants || '[{"p": "research.share"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "research.start"}]'::jsonb AND NOT grants @> '[{"p": "research.share"}]'::jsonb;

COMMIT;
