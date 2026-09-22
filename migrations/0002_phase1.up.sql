-- 0002_phase1 — phase 1: player, world and social.
-- Scope: the player's own numbers (stats, skills), the world's geography
-- (cities, travel) and the first social edge (friendships), plus the timed
-- operation table every scheduled thing in the game hangs from (game_actions).
-- Schema authority: docs/database.md. Every column, type and constraint below
-- is taken from that document; nothing here is invented.
--
-- Conventions inherited from 0001_init:
--   * all instants are timestamptz, stored in UTC;
--   * money is bigint minor units (never float);
--   * no DEFAULT now() anywhere — the application supplies every timestamp, so
--     that a replayed command produces the same row it produced the first time.
--
-- Creation order below follows the foreign keys:
--   cities -> (players.city_id FK) -> player_stats -> player_skills
--   -> game_actions -> travels (needs players, cities AND game_actions)
--   -> friendships.

BEGIN;

-- ---------------------------------------------------------------------------
-- cities — the world's geography. Created first: players.city_id has been
-- waiting for this table since 0001, and travels references it twice.
-- ---------------------------------------------------------------------------
CREATE TABLE cities (
    id                  uuid   PRIMARY KEY,
    code                text   NOT NULL UNIQUE,  -- stable machine identifier, safe to hard-code in seeds
    name                text   NOT NULL,
    -- Tax in basis points (1 bps = 0.01%), an integer, so city tax never
    -- introduces floating point into a money calculation.
    tax_rate_bps        int    NOT NULL,
    cost_of_living      bigint NOT NULL,         -- minor currency units, like every money column
    population          int    NOT NULL DEFAULT 0,
    -- docs/database.md declares treasury_account_id as FK -> accounts NOT NULL.
    -- The accounts table belongs to the ledger, which is a later phase, so the
    -- column is created NULL-able with no foreign key — exactly how 0001 left
    -- players.city_id. The FK (and the NOT NULL) arrive with the migration that
    -- creates accounts. This mirrors the 0001 precedent rather than inventing a
    -- placeholder accounts table that a later migration would have to undo.
    treasury_account_id uuid   NULL
);

COMMENT ON COLUMN cities.tax_rate_bps IS
    'City tax in basis points: 250 means 2.50%. Integer, so tax never becomes floating point money.';
COMMENT ON COLUMN cities.cost_of_living IS
    'Baseline periodic expense in minor currency units for a player residing in this city.';
COMMENT ON COLUMN cities.treasury_account_id IS
    'City treasury ledger account. Foreign key to accounts, and NOT NULL, are added in a later migration, once the accounts table exists.';

-- ---------------------------------------------------------------------------
-- The foreign key 0001 could not create. players.city_id was left as a bare
-- uuid because cities did not exist yet; now it does, so the reference becomes
-- real and a player can no longer point at a city that was never created.
-- ON DELETE is deliberately omitted (RESTRICT): a city with residents must not
-- silently vanish, and nulling a player's location on city deletion would hide
-- a data problem instead of surfacing it.
-- ---------------------------------------------------------------------------
ALTER TABLE players
    ADD CONSTRAINT players_city_id_fkey
    FOREIGN KEY (city_id) REFERENCES cities (id);

COMMENT ON COLUMN players.city_id IS
    'Current city. Foreign key to cities, added by migration 0002_phase1.';

-- ---------------------------------------------------------------------------
-- player_stats — the player's volatile numbers. Split out of players because
-- its write rate is far higher: energy and health tick constantly, while a
-- players row is nearly static.
-- player_id is both the primary key and the foreign key, which makes the
-- one-row-per-player relationship a schema fact rather than a convention.
-- ---------------------------------------------------------------------------
CREATE TABLE player_stats (
    player_id   uuid        PRIMARY KEY REFERENCES players (id),
    level       int         NOT NULL DEFAULT 1,
    xp          bigint      NOT NULL DEFAULT 0,
    health      int         NOT NULL,
    max_health  int         NOT NULL,
    energy      int         NOT NULL,
    max_energy  int         NOT NULL,
    happiness   int         NOT NULL,
    stamina     int         NOT NULL,
    reputation  int         NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL
);

COMMENT ON COLUMN player_stats.xp IS
    'Lifetime experience points. bigint because it only ever grows and is never reset.';

-- ---------------------------------------------------------------------------
-- player_skills — one row per (player, skill). Skill codes are config-driven
-- text ('programming', 'mechanics', …) rather than an enum, so that adding a
-- skill is a configuration change and not a migration.
-- ---------------------------------------------------------------------------
CREATE TABLE player_skills (
    id         uuid        PRIMARY KEY,
    player_id  uuid        NOT NULL REFERENCES players (id),
    skill_code text        NOT NULL,
    level      int         NOT NULL DEFAULT 0,
    xp         bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL,

    -- A player holds each skill exactly once: training is an UPDATE of this
    -- row, never a second row. Without this, two concurrent training commands
    -- would split one skill into two half-progressed rows.
    CONSTRAINT player_skills_player_skill_key UNIQUE (player_id, skill_code)
);

COMMENT ON COLUMN player_skills.skill_code IS
    'Config-driven skill identifier such as programming or mechanics. Deliberately text, not an enum, so new skills need no migration.';

-- ---------------------------------------------------------------------------
-- game_actions — every timed operation in the game. This table is the source
-- of truth for scheduling; any in-memory or Redis structure is only an
-- accelerator and may be rebuilt from here after a restart.
-- Created before travels: travels.game_action_id references it.
-- ---------------------------------------------------------------------------
CREATE TABLE game_actions (
    id             uuid        PRIMARY KEY,
    -- Open vocabulary ('travel', 'production', 'research', 'education', …).
    -- No CHECK here, unlike status: new action types appear with every phase,
    -- and a closed list would force a migration for each one.
    action_type    text        NOT NULL,
    actor_type     text        NOT NULL,
    -- Nullable because a 'system' action has no actor: a world tick or an
    -- auction expiry belongs to nobody.
    actor_id       uuid        NULL,
    -- Polymorphic pointer to whatever the action is about (a travel, a
    -- production order, …). Untyped by design, so game_actions does not need a
    -- foreign key to every future table in the schema.
    reference_type text        NULL,
    reference_id   uuid        NULL,
    payload        jsonb       NOT NULL DEFAULT '{}',
    status         text        NOT NULL,
    retry_count    int         NOT NULL DEFAULT 0,
    started_at     timestamptz NOT NULL,
    finish_at      timestamptz NOT NULL,  -- when the action becomes due
    completed_at   timestamptz NULL,      -- when it actually finished

    CONSTRAINT game_actions_status_check
        CHECK (status IN ('scheduled', 'running', 'completed', 'failed', 'cancelled')),
    CONSTRAINT game_actions_actor_type_check
        CHECK (actor_type IN ('player', 'company', 'system'))
);

COMMENT ON COLUMN game_actions.actor_id IS
    'Owner of the action. NULL when actor_type is system, because a world-level action has no owner.';
COMMENT ON COLUMN game_actions.reference_type IS
    'Name of the entity the action operates on, for example travel. Untyped pointer: no foreign key, so this table need not reference every domain table.';
COMMENT ON COLUMN game_actions.finish_at IS
    'Instant at which the action becomes due. The scheduler orders by this column.';

-- The scheduler index. Its partial predicate is the point: only rows still
-- waiting to run are indexed, so the index stays roughly the size of the
-- pending backlog no matter how many millions of completed actions pile up
-- behind it. This is what makes the project's explicit requirement — no tick
-- may ever scan the whole table — hold in practice rather than on paper. The
-- scheduler's query (status = 'scheduled' AND finish_at <= now() ORDER BY
-- finish_at LIMIT n) is answered by walking the front of this index and
-- stopping, with no sort and no visit to a single completed row.
CREATE INDEX game_actions_due_idx
    ON game_actions (finish_at)
    WHERE status = 'scheduled';

-- Supports "what is this entity currently doing?" lookups, which every domain
-- handler needs when resuming work after a restart.
CREATE INDEX game_actions_reference_idx ON game_actions (reference_type, reference_id);

-- ---------------------------------------------------------------------------
-- travels — a player moving between two cities. The journey's timing lives in
-- game_actions; this row holds what the journey is.
-- ---------------------------------------------------------------------------
CREATE TABLE travels (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id),
    from_city_id   uuid        NOT NULL REFERENCES cities (id),
    to_city_id     uuid        NOT NULL REFERENCES cities (id),
    -- docs/database.md declares vehicle_id as FK -> vehicles NULL. The vehicles
    -- table belongs to a later phase, so the column is created with no foreign
    -- key, following the same precedent as cities.treasury_account_id above.
    -- It is NULL-able in the document anyway, so nothing is weakened today.
    vehicle_id     uuid        NULL,
    cost           bigint      NOT NULL,  -- minor currency units; zero in phase 1, where travel is free
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    status         text        NOT NULL,
    departed_at    timestamptz NOT NULL,
    arrives_at     timestamptz NOT NULL,

    CONSTRAINT travels_status_check
        CHECK (status IN ('in_transit', 'arrived', 'cancelled'))
);

COMMENT ON COLUMN travels.vehicle_id IS
    'Vehicle used for the journey. Foreign key to vehicles is added in a later migration, once the vehicles table exists.';
COMMENT ON COLUMN travels.game_action_id IS
    'The scheduled action that completes this journey. Travel finishes because the scheduler fires, not because anyone polls.';

-- The invariant: a player is in at most one place at a time, and therefore on
-- at most one journey at a time. Expressed as a partial unique index rather
-- than in application code, because two concurrent travel commands would each
-- read "no active travel" and both proceed; only the database can serialise
-- that. Completed and cancelled journeys are excluded from the index, so a
-- player may travel any number of times in sequence.
CREATE UNIQUE INDEX travels_one_active_per_player_idx
    ON travels (player_id)
    WHERE status = 'in_transit';

-- ---------------------------------------------------------------------------
-- friendships — a directed social edge. Directed, not symmetric: (a -> b)
-- pending is a request from a to b, and a block by a must not imply a block
-- by b. A mutual friendship is two rows in the accepted state.
-- ---------------------------------------------------------------------------
CREATE TABLE friendships (
    id               uuid        PRIMARY KEY,
    player_id        uuid        NOT NULL REFERENCES players (id),
    friend_player_id uuid        NOT NULL REFERENCES players (id),
    status           text        NOT NULL,
    created_at       timestamptz NOT NULL,

    -- One edge per ordered pair: re-sending a request updates the existing row
    -- instead of stacking duplicates.
    CONSTRAINT friendships_player_friend_key UNIQUE (player_id, friend_player_id),
    -- Nobody befriends or blocks themselves. A self-edge is always a bug in
    -- the caller, so it is rejected at the lowest possible level.
    CONSTRAINT friendships_no_self_check
        CHECK (player_id <> friend_player_id),
    CONSTRAINT friendships_status_check
        CHECK (status IN ('pending', 'accepted', 'blocked'))
);

-- Answers "who has added me?", which the incoming-requests screen needs and
-- the UNIQUE index above (player_id first) cannot serve.
CREATE INDEX friendships_friend_player_id_idx ON friendships (friend_player_id);

COMMIT;
