-- 0003_content — the content system's storage.
-- Scope: the tables that make a YAML content load durable, versioned and
-- auditable. Authority: docs/adr/0004-content-system.md (option C), which
-- makes the files in configs/content/ the authoring format and this database
-- the source of truth every running service reads.
--
-- The shape follows directly from the ADR's mandatory rules:
--   * rule 1 (validate before writing) means nothing here re-validates content
--     at read time, so the CHECK constraints below are a last line of defence
--     against a writer that bypassed the loader, not the primary check;
--   * rule 2 (atomic swap) means a version is either wholly present or wholly
--     absent, which is why every content row carries content_version_id and a
--     load is one transaction;
--   * rule 4 (rollback is as cheap as load) means old versions are KEPT, never
--     deleted, so re-activating one is a status update and nothing else;
--   * rule 5 (every load leaves an audit row) is why audit_logs is created
--     here: docs/database.md declares it and nothing had created it yet, and
--     the first writer of an audit row is this migration's own loader.
--
-- Conventions inherited from 0001_init and 0002_phase1:
--   * all instants are timestamptz, stored in UTC;
--   * no DEFAULT now() anywhere — the application supplies every timestamp, so
--     a replayed command produces the same row it produced the first time;
--   * CHECK constraints are named, so a violation names the rule it broke.
--
-- Creation order below follows the foreign keys:
--   content_versions -> cities.content_version_id -> city_routes
--   -> skill_definitions, then the free-standing audit_logs.

BEGIN;

-- ---------------------------------------------------------------------------
-- content_versions — one row per successful `admin content load`.
--
-- This is the spine of the whole system: every other table here points at it,
-- and "which content is the world running on" is answered by the single row
-- with status = 'active'. Versions are never deleted, so a load can be undone
-- by activating an earlier row rather than by re-running a file that may no
-- longer exist in that form.
-- ---------------------------------------------------------------------------
CREATE TABLE content_versions (
    id              uuid        PRIMARY KEY,
    -- Monotonic and human-quotable. The uuid above is what foreign keys use;
    -- this is what an operator says out loud ("we are on 7") and what a domain
    -- event carries, per ADR 0004 rule 3. UNIQUE rather than a sequence: the
    -- loader derives it inside the transaction under an advisory lock, so a
    -- gap can never appear from a rolled-back load.
    version         int         NOT NULL UNIQUE,
    loaded_at       timestamptz NOT NULL,
    -- Who ran the loader. Free text, because the operator identity at this
    -- stage is an operating-system account and not a row in players.
    loaded_by       text        NOT NULL,
    -- Digest of the source files this version was built from. It is what lets
    -- someone six months later ask "is the checkout in front of me the content
    -- production is running?" and get an answer that does not depend on the
    -- files still being reachable.
    source_checksum text        NOT NULL,
    status          text        NOT NULL,
    -- Why this load happened. NOT NULL with no default on purpose: ADR 0009
    -- treats a change whose reason was not recorded as a change nobody can
    -- understand later, so the loader refuses to run without one.
    notes           text        NOT NULL,

    CONSTRAINT content_versions_status_check
        CHECK (status IN ('active', 'superseded')),
    CONSTRAINT content_versions_version_positive_check
        CHECK (version > 0)
);

-- Exactly one active version, enforced by the database rather than by the
-- loader. Two active rows would mean two services could each pick a different
-- "current" world and both be reading a row that says it is the live one —
-- precisely the split-brain ADR 0004 rejected option A to avoid. A partial
-- unique index is the only way to say "at most one row satisfying a predicate"
-- in PostgreSQL; a plain UNIQUE on status would allow only one superseded row
-- as well, which is the opposite of what is wanted.
CREATE UNIQUE INDEX content_versions_one_active_idx
    ON content_versions (status)
    WHERE status = 'active';

COMMENT ON COLUMN content_versions.version IS
    'Monotonic content version. Carried on every domain event produced while it was active, so a past outcome can be explained by the content that produced it.';
COMMENT ON COLUMN content_versions.source_checksum IS
    'Digest of the content files this version was built from, for matching a running world against a git checkout.';
COMMENT ON COLUMN content_versions.notes IS
    'Operator-supplied reason for the load. Required: an unexplained content change is unreadable six months later.';

-- ---------------------------------------------------------------------------
-- cities gains the load that produced it. Nullable, because 0002 created the
-- table and any row written before the content system existed legitimately has
-- no version to point at.
--
-- This column is also how a city is REMOVED from the world without deleting
-- the row. players.city_id and travels reference cities, so a real DELETE is
-- refused by those foreign keys (and rightly: ADR 0004 rule 7). Instead, a
-- city that a load no longer mentions simply keeps its old content_version_id
-- and is therefore not part of the new active set. The historical row survives
-- for the events and travels that point at it.
-- ---------------------------------------------------------------------------
ALTER TABLE cities
    ADD COLUMN content_version_id uuid NULL REFERENCES content_versions (id);

COMMENT ON COLUMN cities.content_version_id IS
    'The content load that last wrote this city. A city whose value is not the active version is not part of the current world, which is how a city is retired without deleting a row other tables reference.';

-- Answers "which cities belong to the active version", which is the first
-- query every service runs at boot.
CREATE INDEX cities_content_version_id_idx ON cities (content_version_id);

-- ---------------------------------------------------------------------------
-- city_routes — the edges of the travel graph, per version.
--
-- The rows are content: which cities connect and how far apart they are. The
-- rule that turns a distance into a duration and a fare stays in
-- internal/domain/travel and is not represented here at all.
--
-- Rows are written per version rather than updated in place. That is what
-- makes rule 4 work: the previous version's edges are still on disk, so
-- re-activating it needs no file and no deploy.
-- ---------------------------------------------------------------------------
CREATE TABLE city_routes (
    id                 uuid    PRIMARY KEY,
    from_city_id       uuid    NOT NULL REFERENCES cities (id),
    to_city_id         uuid    NOT NULL REFERENCES cities (id),
    -- Abstract distance units. int, like every other content number, so that
    -- no travel calculation ever starts from a float.
    distance           int     NOT NULL,
    -- When true, the reverse edge is implied and is not stored. Storing both
    -- directions would let the two drift apart, which is the single most
    -- likely authoring mistake in a hand-edited route file.
    bidirectional      boolean NOT NULL,
    content_version_id uuid    NOT NULL REFERENCES content_versions (id),

    -- One edge per ordered pair per version. Without it, a load that listed a
    -- pair twice would store both and the shortest-path build would silently
    -- pick one.
    CONSTRAINT city_routes_version_pair_key
        UNIQUE (content_version_id, from_city_id, to_city_id),
    -- Zero would make two cities the same place and travel between them
    -- instant; negative would let a path get shorter the further it goes.
    CONSTRAINT city_routes_distance_positive_check
        CHECK (distance > 0),
    -- The distance from a city to itself is zero by definition, so an explicit
    -- self route is either a typo or an assertion that cannot be true.
    CONSTRAINT city_routes_no_self_check
        CHECK (from_city_id <> to_city_id)
);

COMMENT ON COLUMN city_routes.bidirectional IS
    'True when the reverse edge is implied. The reverse row is deliberately not stored, so the two directions cannot drift apart.';
COMMENT ON COLUMN city_routes.distance IS
    'Abstract distance units. The travel rule derives duration and cost from this; the number itself is content.';

-- The service boot query: every edge of one version.
CREATE INDEX city_routes_content_version_id_idx ON city_routes (content_version_id);

-- ---------------------------------------------------------------------------
-- skill_definitions — the display data attached to a skill code.
--
-- The SET of skill codes is a RULE and lives in internal/domain/player: other
-- packages branch on individual members of it, so a code no package knows
-- about would be a skill a player can train and never use. What is content is
-- everything ABOUT a skill that is naming and grouping rather than meaning,
-- and that is what this table holds. The loader rejects a code that the domain
-- does not declare.
-- ---------------------------------------------------------------------------
CREATE TABLE skill_definitions (
    id                 uuid PRIMARY KEY,
    -- Matches player_skills.skill_code, which 0002 deliberately left as text.
    code               text NOT NULL,
    name               text NOT NULL,
    -- Grouping for presentation ('technical', 'social', …). Open vocabulary
    -- and no CHECK: a new grouping is a content decision, and a closed list
    -- here would force a migration for each one.
    category           text NOT NULL,
    content_version_id uuid NOT NULL REFERENCES content_versions (id),

    CONSTRAINT skill_definitions_version_code_key
        UNIQUE (content_version_id, code)
);

COMMENT ON COLUMN skill_definitions.code IS
    'Skill identifier, which must be one of the codes internal/domain/player declares. The closed set is a rule; the name and category here are content.';

CREATE INDEX skill_definitions_content_version_id_idx
    ON skill_definitions (content_version_id);

-- ---------------------------------------------------------------------------
-- audit_logs — append-only record of every operator change.
--
-- Declared by docs/database.md and required by ADR 0004 rule 5, and created
-- here because the content loader is the first operator action in the system
-- that must leave a trace. Nothing in this table is ever updated or deleted:
-- an audit trail that can be edited is not an audit trail.
--
-- Columns follow docs/database.md exactly, including the two jsonb value
-- columns, which a content load leaves NULL on the old side (there is no
-- previous value for "a new version appeared") and fills on the new side.
-- ---------------------------------------------------------------------------
CREATE TABLE audit_logs (
    id          bigserial   PRIMARY KEY,
    actor       text        NOT NULL,  -- operator identity
    action      text        NOT NULL,  -- what they did, e.g. content.load
    target_type text        NOT NULL,  -- entity kind the action touched
    target_id   uuid        NULL,      -- NULL when the target has no uuid
    old_value   jsonb       NULL,
    new_value   jsonb       NULL,
    reason      text        NOT NULL,
    created_at  timestamptz NOT NULL
);

-- "What happened to this thing, most recent first" — the question asked when
-- investigating one entity.
CREATE INDEX audit_logs_target_idx
    ON audit_logs (target_type, target_id, created_at DESC);

-- "What has this operator been doing" — the question asked when investigating
-- a person rather than a thing.
CREATE INDEX audit_logs_actor_idx
    ON audit_logs (actor, created_at DESC);

COMMENT ON COLUMN audit_logs.reason IS
    'Why the operator made the change. Required, because a change whose reason was not recorded cannot be evaluated later.';

COMMIT;
