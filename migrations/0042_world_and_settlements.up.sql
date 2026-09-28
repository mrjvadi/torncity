-- 0042_world_and_settlements — the world registry, and letting a Telegram
-- group found its own village on the generated planet.
--
-- Authority: docs/adr/0028-world-and-settlements.md (Proposed), sections 2, 3
-- and 9. Depends on the world generator (internal/domain/worldgen, already on
-- main) and on migration 0008 (jurisdictions, offices) and 0016
-- (city_group_links).
--
-- SEED-FIRST (ADR 0028 section 2): a world's terrain is never a row. worlds
-- holds only the seed, the generator version and a hash of the parameters it
-- was generated with — a few hundred bytes, from which every replica
-- regenerates the identical planet in memory (internal/domain/worldgen.
-- Generate). Nothing about a cell's elevation, biome or river is ever stored
-- here; only what a player subsequently changes is (a settlement's claim on
-- a cell, its buildings — this migration and 0043).
--
-- cities/jurisdictions are EXTENDED, not replaced (ADR 0028 section 9.1): the
-- content loader keeps owning every origin = 'content' row exactly as before
-- (0002_phase1, 0008_governance); gameplay may now also write an
-- origin = 'founded' row, with the extra columns a founded settlement needs
-- and a legacy content city never has.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now() — the application supplies every timestamp; CHECK
-- constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- worlds — the registry of generated planets. Immutable once created: a
-- generator version bump or a parameter change is a NEW row, never an
-- update to an existing one (ADR 0028 section 2's "a generator version
-- travels with every seed... never what an existing seed WAS"). At most one
-- row is active at a time — "the one live world" the owner chooses a
-- production seed for, later, by `admin world create`.
-- ---------------------------------------------------------------------------
CREATE TABLE worlds (
    id                 uuid        PRIMARY KEY,
    -- The world seed. worldgen.Generate takes a uint64; stored as bigint
    -- (Postgres has no unsigned type) — the same bit pattern round-trips
    -- through a Go uint64<->int64 conversion, so nothing is lost, only the
    -- decimal rendering of a seed above 2^63 looks negative in a `SELECT`.
    seed               bigint      NOT NULL,
    generator_version  int         NOT NULL,
    -- A hex digest of the worldgen.Params (and world-gen content) this seed
    -- was generated under: every replica must regenerate the SAME planet, so
    -- this is what a replica checks its own configured Params against before
    -- trusting its in-memory World for this world_id, rather than silently
    -- serving a different planet under the same id because two replicas'
    -- configs/config.yml drifted.
    params_hash        text        NOT NULL,
    active             boolean     NOT NULL,
    created_at         timestamptz NOT NULL,
    created_by         text        NOT NULL,

    CONSTRAINT worlds_created_by_check CHECK (length(btrim(created_by)) > 0),
    CONSTRAINT worlds_params_hash_check CHECK (length(btrim(params_hash)) > 0)
);

-- At most one active world. `admin world create` refuses to create a second
-- one while this holds a row — the owner's "the one live world"
-- (ADR 0028 status: "the owner chooses the production seed later").
CREATE UNIQUE INDEX worlds_one_active_idx ON worlds (active) WHERE active;

COMMENT ON TABLE worlds IS
    'The world registry (ADR 0028 section 2): seed, generator version and a params hash only. A world is regenerated from these per replica, in memory, cached, never stored tile by tile. Immutable once created; at most one row is active.';

-- ---------------------------------------------------------------------------
-- cities — extended with a founded settlement's own columns. A row the
-- content loader wrote keeps origin = 'content' and every new column NULL,
-- forever; the loader never touches a 'founded' row (ADR 0004's "content in
-- use is never clobbered", applied to origin here).
-- ---------------------------------------------------------------------------
ALTER TABLE cities
    ADD COLUMN origin              text        NOT NULL DEFAULT 'content',
    ADD COLUMN tier                text        NULL,
    ADD COLUMN world_id            uuid        NULL REFERENCES worlds (id),
    -- The world cell (internal/domain/worldgen.World.Cells index) this
    -- settlement stands on. Not a chunk address: everything this phase
    -- needs (spacing, a claim, a deposit) is already expressed per cell in
    -- ADR 0028 section 9.1's own data model; a settlement's local lot grid
    -- (section 6) is sampled straight from the cell's lat/lon, never from a
    -- stored chunk coordinate.
    ADD COLUMN world_cell_id       int         NULL,
    -- The Telegram chat that founded this settlement (ADR 0028 section
    -- 3.1). Kept on the row itself, not only in city_group_links, so
    -- "has this chat already founded a settlement" is one indexed lookup
    -- and the idempotent-founding guarantee below is a single constraint.
    ADD COLUMN founded_by_group_id bigint      NULL,
    ADD COLUMN founded_at          timestamptz NULL,
    -- The beginner-protection window (ADR 0028 section 3.1, config
    -- settlement.protection_window): the settlement's territory cannot be
    -- claimed, contested or struck until this instant. Set at founding to
    -- founded_at + the window; moved to founded_at itself (in the past,
    -- immediately) the moment the settlement's own side commits an
    -- aggressive act, which is how protection "ends early" without a
    -- second boolean to keep in step with this timestamp.
    ADD COLUMN protected_until      timestamptz NULL;

ALTER TABLE cities
    ADD CONSTRAINT cities_origin_check CHECK (origin IN ('content', 'founded')),
    ADD CONSTRAINT cities_tier_check CHECK (tier IS NULL OR tier IN ('village', 'town', 'city')),
    -- A content city has none of the founding columns; a founded one has
    -- all of them. One CHECK keeps the two shapes from ever drifting apart.
    ADD CONSTRAINT cities_founded_shape_check CHECK (
        (origin = 'content' AND tier IS NULL AND world_id IS NULL AND world_cell_id IS NULL
            AND founded_by_group_id IS NULL AND founded_at IS NULL AND protected_until IS NULL)
        OR
        (origin = 'founded' AND tier IS NOT NULL AND world_id IS NOT NULL AND world_cell_id IS NOT NULL
            AND founded_by_group_id IS NOT NULL AND founded_at IS NOT NULL AND protected_until IS NOT NULL)
    );

-- Two settlements never stand on the same cell of the same world (the
-- spawn algorithm's own minimum-spacing guarantee, backstopped here against
-- a race between replicas: internal/domain/settlement.FindSpawn is pure and
-- reads no lock, so the actual guarantee against two concurrent foundings
-- landing on the very same cell is this constraint, with the inserting
-- transaction retrying the next lattice point on conflict).
CREATE UNIQUE INDEX cities_world_cell_unique_idx ON cities (world_id, world_cell_id) WHERE world_cell_id IS NOT NULL;

-- One Telegram group founds at most one settlement this way (ADR 0028
-- section 3.1: "No group founds more than one settlement... a group GROWS
-- by expedition, never by registering twice"). This is also what makes
-- founding idempotent under at-least-once command delivery: a redelivered
-- founding command reaches this same constraint and is refused, not
-- duplicated.
CREATE UNIQUE INDEX cities_founded_by_group_unique_idx ON cities (founded_by_group_id) WHERE founded_by_group_id IS NOT NULL;

COMMENT ON COLUMN cities.origin IS
    'content (the content loader owns this row) or founded (a Telegram group founded it, ADR 0028 section 3.1). The loader never writes or touches a founded row.';
COMMENT ON COLUMN cities.tier IS
    'village, town or city — a founded settlement''s own growth stage (ADR 0028 section 4). NULL for a content city, which is always effectively city tier with the full governance.yml city.* catalogue.';
COMMENT ON COLUMN cities.world_cell_id IS
    'The generated world cell (worldgen.World.Cells index) this settlement stands on. The cell''s terrain is never stored; it is regenerated from worlds.seed on every read.';

-- ---------------------------------------------------------------------------
-- jurisdictions — no schema change: content_version_id is already NULLable
-- (it is NULL for the root world row created by 0008), which is exactly
-- what a runtime-inserted village/town/province jurisdiction needs — see
-- ADR 0028 section 9.1: "Runtime code may now insert/update a row... the
-- loader still owns every content-origin row." Noted here, not migrated,
-- because there is nothing to migrate.
-- ---------------------------------------------------------------------------

COMMIT;
