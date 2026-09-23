-- 0008_governance — player-held offices: levels, jurisdictions, levers,
-- offices and the policy values office holders set.
-- Authority: docs/adr/0015-player-held-offices.md (Accepted), sections 1–3 and
-- 6–8, with the schema findings of docs/adr/0016-offices-catalogue.md section
-- 6 that must not wait (G22: levels are content; G2: structured lever values;
-- section 1.4: one level per kind of overlay). Schema: docs/database.md,
-- section 15.
--
-- The principle, in one line: every political or economic decision is a LEVER
-- held by an OFFICE. A player may hold the office; when nobody does, or nobody
-- has ever moved the lever, the lever's content-defined default applies. The
-- operator defines only the bounds. No code reads a policy value from content
-- directly; it asks one resolver (application.PolicyReader).
--
-- What is content and what is state:
--   * jurisdiction_levels — content, a REGISTRY matched on code like cities:
--     a level keeps its row across loads, and every table naming a level
--     holds a real foreign key to it. No level is listed in any CHECK here, so
--     adding a province is a line of governance.yml and a load, never a
--     migration (ADR 0016 G22).
--   * lever_definitions, office_definitions — content, one set of rows per
--     content version, like city_routes: re-activating an old version needs
--     no file.
--   * jurisdictions — content AND state. Written by the loader, matched on
--     (kind, code) like cities, so a jurisdiction keeps its id across loads:
--     offices and policy values point at it.
--   * offices, policy_values, policy_changes — state. The loader creates the
--     vacant seats; nothing else here is written by a load.
--
-- Stored now, behaviour later: collective decisions, vetoes, appointment
-- chains, terms, removal, overlays and structured lever values are columns
-- the loader fills and validates, with no behaviour yet. Elections and
-- ballots are NOT here: ballots must be stored apart from voter identity
-- (ADR 0016 G21), which is the election phase's design to make.
--
-- ADR 0015 section 8, and where each invariant is enforced in the database
-- (the application refuses each one first, with a named error; these are the
-- last line against any writer that is not the application):
--   1. no value outside its lever's [min, max]       — policy_values trigger
--   2. no change before the previous one's cooldown  — policy_values trigger
--   3. no change takes effect before its notice      — policy_values trigger
--   4. every change has a public policy_changes row  — deferred trigger
--   5. a vacant office yields the default, never an error — the resolver; a
--      vacancy is a NULL holder, which nothing here refuses
--   6. only residents vote — elections, not in this migration
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now() — the application supplies every timestamp; CHECK
-- constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- jurisdiction_levels — the kinds of jurisdiction that may exist (village,
-- town, city, province, country, union, port_authority, free_zone …).
--
-- One row per level code, upserted by every load like a city, with the load
-- that last declared it. A level no longer declared keeps its row (and its
-- old content_version_id), because jurisdictions and definitions of earlier
-- versions still point at it.
--
-- 'world' is the one row no load writes: the root, the operator's level,
-- created below with the root jurisdiction. It is the tree's anchor, not a
-- level list.
-- ---------------------------------------------------------------------------
CREATE TABLE jurisdiction_levels (
    code               text    PRIMARY KEY,
    -- The levels a jurisdiction of this level may sit directly under;
    -- 'world' allows a top-level jurisdiction. Enforced by the loader.
    parents            text[]  NOT NULL,
    -- Overlays other jurisdictions (a port authority, a free zone) instead of
    -- partitioning its parent. Stored; no behaviour yet.
    overlay            boolean NOT NULL,
    content_version_id uuid    NULL REFERENCES content_versions (id),

    -- Every level but the root sits under something.
    CONSTRAINT jurisdiction_levels_parents_check CHECK ((code = 'world') = (cardinality(parents) = 0))
);

CREATE INDEX jurisdiction_levels_content_version_id_idx ON jurisdiction_levels (content_version_id);

INSERT INTO jurisdiction_levels (code, parents, overlay, content_version_id)
VALUES ('world', '{}', false, NULL);

-- ---------------------------------------------------------------------------
-- jurisdictions — the world and everything under it. A tree by parent_id.
-- ---------------------------------------------------------------------------
CREATE TABLE jurisdictions (
    id                 uuid PRIMARY KEY,
    kind               text NOT NULL REFERENCES jurisdiction_levels (code),
    -- For a city, the city's code; otherwise the code governance.yml
    -- declares. Unique per kind.
    code               text NOT NULL,
    name               text NOT NULL,
    parent_id          uuid NULL REFERENCES jurisdictions (id),
    -- The content load that last wrote this jurisdiction. NULL for the world,
    -- which no load writes. A jurisdiction no longer in the files keeps its
    -- row and its old version, exactly as a retired city does.
    content_version_id uuid NULL REFERENCES content_versions (id),

    -- The world is the root and the only root.
    CONSTRAINT jurisdictions_root_check CHECK ((kind = 'world') = (parent_id IS NULL)),
    CONSTRAINT jurisdictions_not_own_parent_check CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT jurisdictions_kind_code_key UNIQUE (kind, code)
);

-- Exactly one world.
CREATE UNIQUE INDEX jurisdictions_one_world_idx ON jurisdictions (kind) WHERE kind = 'world';
CREATE INDEX jurisdictions_parent_id_idx ON jurisdictions (parent_id);

COMMENT ON TABLE jurisdictions IS
    'The world and every jurisdiction under it, as a tree. The world is the operator''s and holds no offices; every other level may hold offices and levers (ADR 0015 section 1).';

-- The world. A fixed id, like the system accounts of 0006, so code can name
-- it without a lookup; mirrored as application.WorldJurisdictionID.
INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
VALUES ('00000000-0000-4000-8000-000000000100', 'world', 'world', 'World', NULL, NULL);

-- ---------------------------------------------------------------------------
-- cities.jurisdiction_id — the city's own jurisdiction. NULL-able because a
-- city row written by anything other than the loader (a test fixture, a row
-- from before this migration and before any backfill) has none yet.
-- ---------------------------------------------------------------------------
ALTER TABLE cities
    ADD COLUMN jurisdiction_id uuid NULL REFERENCES jurisdictions (id),
    ADD CONSTRAINT cities_jurisdiction_id_key UNIQUE (jurisdiction_id);

COMMENT ON COLUMN cities.jurisdiction_id IS
    'This city''s jurisdiction, whose parent is its country. Written by the content loader.';

-- ---------------------------------------------------------------------------
-- office_definitions — which offices exist, per content version.
--
-- Several columns describe mechanics that have no behaviour yet (appointment
-- chains, confirmation, terms, removal, vetoes). They are stored now so the
-- constitution can state them and no later feature has to break this table.
-- The loader validates every reference; the arrays cannot carry foreign keys.
-- ---------------------------------------------------------------------------
CREATE TABLE office_definitions (
    id                       uuid   PRIMARY KEY,
    content_version_id       uuid   NOT NULL REFERENCES content_versions (id),
    code                     text   NOT NULL,
    jurisdiction_kind        text   NOT NULL REFERENCES jurisdiction_levels (code),
    seats                    int    NOT NULL,
    -- How the office is normally obtained. An operator may still appoint.
    acquired_by              text   NOT NULL,
    -- Acts for this office while it is vacant (enforced by the resolver).
    deputy                   text   NULL,
    appointed_by             text   NULL,
    requires_confirmation_by text   NULL,
    -- NULL = at pleasure.
    term_seconds             bigint NULL,
    term_limit               int    NULL,
    -- Office codes, or 'residents' / 'operator'.
    can_be_removed_by        text[] NOT NULL,
    -- Lever and office codes.
    veto_over                text[] NOT NULL,
    -- Symmetric; enforced by `admin office appoint`.
    incompatible_with        text[] NOT NULL,

    CONSTRAINT office_definitions_version_code_key UNIQUE (content_version_id, code),
    -- Links to other offices of the SAME version. Deferred, so a load may
    -- insert an office before the deputy it names.
    CONSTRAINT office_definitions_deputy_fkey FOREIGN KEY (content_version_id, deputy)
        REFERENCES office_definitions (content_version_id, code) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT office_definitions_appointed_by_fkey FOREIGN KEY (content_version_id, appointed_by)
        REFERENCES office_definitions (content_version_id, code) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT office_definitions_confirmation_fkey FOREIGN KEY (content_version_id, requires_confirmation_by)
        REFERENCES office_definitions (content_version_id, code) DEFERRABLE INITIALLY DEFERRED,
    -- The root is the operator's: no office sits at it.
    CONSTRAINT office_definitions_not_world_check CHECK (jurisdiction_kind <> 'world'),
    CONSTRAINT office_definitions_seats_check CHECK (seats >= 1),
    CONSTRAINT office_definitions_acquired_by_check
        CHECK (acquired_by IN ('election', 'appointment', 'founding', 'conquest')),
    CONSTRAINT office_definitions_not_own_deputy_check CHECK (deputy IS NULL OR deputy <> code),
    CONSTRAINT office_definitions_appointment_check
        CHECK (appointed_by IS NULL OR acquired_by = 'appointment'),
    CONSTRAINT office_definitions_confirmation_check
        CHECK (requires_confirmation_by IS NULL OR appointed_by IS NOT NULL),
    CONSTRAINT office_definitions_term_check CHECK (term_seconds IS NULL OR term_seconds > 0),
    CONSTRAINT office_definitions_term_limit_check
        CHECK (term_limit IS NULL OR (term_limit >= 1 AND term_seconds IS NOT NULL))
);

CREATE INDEX office_definitions_content_version_id_idx ON office_definitions (content_version_id);

-- ---------------------------------------------------------------------------
-- lever_definitions — the constitution: every lever, its bounds, its default,
-- the office deciding it and how, per content version.
--
-- value_type is a CLOSED set, unlike levels, because code interprets each
-- type. value_kind says where a value of the type lives: 'scalar' types
-- (bps, money, int) are one bounded integer; 'structured' types (bool, enum,
-- map, allocation) are a JSON document (ADR 0016 G2). Only scalars have
-- behaviour today.
-- ---------------------------------------------------------------------------
CREATE TABLE lever_definitions (
    id                      uuid   PRIMARY KEY,
    content_version_id      uuid   NOT NULL REFERENCES content_versions (id),
    code                    text   NOT NULL,
    jurisdiction_kind       text   NOT NULL REFERENCES jurisdiction_levels (code),
    value_type              text   NOT NULL,
    value_kind              text   NOT NULL,
    -- Scalar levers: the default and the operator's bounds.
    default_value           bigint NULL,
    min_value               bigint NULL,
    max_value               bigint NULL,
    -- Structured levers: the default document and the type's parameters.
    default_json            jsonb  NULL,
    options                 text[] NOT NULL,
    map_key                 text   NULL,
    categories              text[] NOT NULL,
    -- The office (one person or a body) that decides the lever.
    held_by                 text   NOT NULL,
    -- single has behaviour; the others are a vote of held_by, stored until
    -- voting exists. Fractions are 'p/q' text, validated by the loader.
    decision_rule           text   NOT NULL,
    threshold               text   NULL,
    quorum                  text   NULL,
    veto_by                 text[] NOT NULL,
    override_rule           text   NULL,
    override_threshold      text   NULL,
    change_cooldown_seconds bigint NOT NULL,
    notice_seconds          bigint NOT NULL,
    -- A per-city default source, or NULL. 'tax_rate_bps' makes each city's
    -- default its own cities.tax_rate_bps; see content.CityDefaultTaxRate.
    city_default            text   NULL,

    CONSTRAINT lever_definitions_version_code_key UNIQUE (content_version_id, code),
    -- held_by names an office of the SAME version.
    CONSTRAINT lever_definitions_held_by_fkey FOREIGN KEY (content_version_id, held_by)
        REFERENCES office_definitions (content_version_id, code),
    CONSTRAINT lever_definitions_not_world_check CHECK (jurisdiction_kind <> 'world'),
    CONSTRAINT lever_definitions_type_check CHECK (
        (value_kind = 'scalar'     AND value_type IN ('bps', 'money', 'int')) OR
        (value_kind = 'structured' AND value_type IN ('bool', 'enum', 'map', 'allocation'))),
    -- A scalar has a bounded integer default and no document; a structured
    -- lever the reverse.
    CONSTRAINT lever_definitions_value_shape_check CHECK (
        (value_kind = 'scalar'
            AND default_value IS NOT NULL AND min_value IS NOT NULL AND max_value IS NOT NULL
            AND default_json IS NULL
            AND min_value <= max_value AND default_value BETWEEN min_value AND max_value) OR
        (value_kind = 'structured'
            AND default_value IS NULL AND min_value IS NULL AND max_value IS NULL
            AND default_json IS NOT NULL)),
    CONSTRAINT lever_definitions_bps_check
        CHECK (value_type <> 'bps' OR (min_value >= 0 AND max_value <= 10000)),
    CONSTRAINT lever_definitions_options_check
        CHECK ((value_type IN ('enum', 'map')) = (cardinality(options) > 0)),
    CONSTRAINT lever_definitions_map_key_check CHECK ((value_type = 'map') = (map_key IS NOT NULL)),
    CONSTRAINT lever_definitions_categories_check
        CHECK ((value_type = 'allocation') = (cardinality(categories) > 0)),
    -- Decision rules are a RULE the code interprets: a closed list.
    CONSTRAINT lever_definitions_decision_rule_check
        CHECK (decision_rule IN ('single', 'majority', 'supermajority', 'unanimous')),
    CONSTRAINT lever_definitions_threshold_check
        CHECK ((decision_rule = 'supermajority') = (threshold IS NOT NULL)),
    CONSTRAINT lever_definitions_quorum_check CHECK (quorum IS NULL OR decision_rule <> 'single'),
    CONSTRAINT lever_definitions_override_rule_check
        CHECK (override_rule IS NULL OR (override_rule IN ('majority', 'supermajority', 'unanimous')
                                         AND cardinality(veto_by) > 0)),
    CONSTRAINT lever_definitions_override_threshold_check
        CHECK ((override_rule IS NOT DISTINCT FROM 'supermajority') = (override_threshold IS NOT NULL)),
    CONSTRAINT lever_definitions_durations_check
        CHECK (change_cooldown_seconds >= 0 AND notice_seconds >= 0),
    CONSTRAINT lever_definitions_city_default_check
        CHECK (city_default IS NULL OR (city_default = 'tax_rate_bps' AND value_kind = 'scalar'))
);

CREATE INDEX lever_definitions_content_version_id_idx ON lever_definitions (content_version_id);

-- ---------------------------------------------------------------------------
-- offices — the seats that exist, and who holds each. One row per office,
-- jurisdiction and seat; the loader creates them vacant.
-- ---------------------------------------------------------------------------
CREATE TABLE offices (
    id               uuid        PRIMARY KEY,
    -- Not a foreign key: office_definitions is per content version, and a
    -- seat outlives the version that created it.
    office_code      text        NOT NULL,
    jurisdiction_id  uuid        NOT NULL REFERENCES jurisdictions (id),
    seat             int         NOT NULL,
    -- NULL = vacant = the deputy, or else the default behaviour, runs.
    holder_player_id uuid        NULL REFERENCES players (id),
    term_ends_at     timestamptz NULL,
    -- How the CURRENT holder obtained the seat; NULL while vacant.
    acquired_by      text        NULL,
    -- When the current state began: the appointment, or the vacancy.
    since            timestamptz NOT NULL,

    CONSTRAINT offices_seat_key UNIQUE (office_code, jurisdiction_id, seat),
    -- One player holds at most one seat of one office in one jurisdiction.
    -- Vacant seats (NULL holder) are distinct, so they never collide. One
    -- player MAY hold different offices, unless content declares them
    -- incompatible.
    CONSTRAINT offices_one_seat_per_holder_key UNIQUE (office_code, jurisdiction_id, holder_player_id),
    CONSTRAINT offices_seat_check CHECK (seat >= 1),
    CONSTRAINT offices_acquired_by_check
        CHECK (acquired_by IS NULL OR acquired_by IN ('election', 'appointment', 'founding', 'conquest')),
    CONSTRAINT offices_holder_acquired_check CHECK ((holder_player_id IS NULL) = (acquired_by IS NULL)),
    CONSTRAINT offices_term_needs_holder_check CHECK (term_ends_at IS NULL OR holder_player_id IS NOT NULL)
);

CREATE INDEX offices_jurisdiction_id_idx ON offices (jurisdiction_id);
CREATE INDEX offices_holder_player_id_idx ON offices (holder_player_id) WHERE holder_player_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- policy_values — every value an office holder has set, with when it takes
-- effect. The resolver reads the latest row whose effective_at has passed;
-- with none, the lever's default applies.
--
-- A scalar value is `value`, a structured one `value_json`; exactly one is
-- set, by value_kind. JSON beside the integer rather than a child table of
-- entries: a map or an allocation is decided, announced and superseded as a
-- WHOLE — one decision, one row, one public record of old and new — and the
-- resolver reads one row per lever either way. Per-entry bound checks, the
-- child table's advantage, are expressible over jsonb in the trigger below
-- when those types gain behaviour.
-- ---------------------------------------------------------------------------
CREATE TABLE policy_values (
    id               uuid        PRIMARY KEY,
    jurisdiction_id  uuid        NOT NULL REFERENCES jurisdictions (id),
    lever_code       text        NOT NULL,
    value_kind       text        NOT NULL,
    value            bigint      NULL,
    value_json       jsonb       NULL,
    set_by_player_id uuid        NOT NULL REFERENCES players (id),
    -- The seat the change was made from.
    office_id        uuid        NOT NULL REFERENCES offices (id),
    set_at           timestamptz NOT NULL,
    effective_at     timestamptz NOT NULL,

    CONSTRAINT policy_values_value_kind_check CHECK (
        (value_kind = 'scalar'     AND value IS NOT NULL AND value_json IS NULL) OR
        (value_kind = 'structured' AND value IS NULL     AND value_json IS NOT NULL)),
    CONSTRAINT policy_values_effective_after_set_check CHECK (effective_at >= set_at)
);

-- The resolver's query: the latest effective row for one lever in one place.
CREATE INDEX policy_values_lookup_idx
    ON policy_values (jurisdiction_id, lever_code, effective_at DESC, set_at DESC);

-- ---------------------------------------------------------------------------
-- policy_changes — the public history of every change, with the holder's
-- name. Append-only (ADR 0015 section 4: a public record that can be edited is
-- not a public record).
-- ---------------------------------------------------------------------------
CREATE TABLE policy_changes (
    id               uuid        PRIMARY KEY,
    policy_value_id  uuid        NOT NULL UNIQUE REFERENCES policy_values (id),
    jurisdiction_id  uuid        NOT NULL REFERENCES jurisdictions (id),
    lever_code       text        NOT NULL,
    office_id        uuid        NOT NULL REFERENCES offices (id),
    office_code      text        NOT NULL,
    set_by_player_id uuid        NOT NULL REFERENCES players (id),
    value_kind       text        NOT NULL,
    -- The value in force when the change was made, and the value announced:
    -- the integer pair for a scalar lever, the document pair otherwise.
    old_value        bigint      NULL,
    new_value        bigint      NULL,
    old_value_json   jsonb       NULL,
    new_value_json   jsonb       NULL,
    set_at           timestamptz NOT NULL,
    effective_at     timestamptz NOT NULL,

    CONSTRAINT policy_changes_value_kind_check CHECK (
        (value_kind = 'scalar'
            AND old_value IS NOT NULL AND new_value IS NOT NULL
            AND old_value_json IS NULL AND new_value_json IS NULL) OR
        (value_kind = 'structured'
            AND old_value IS NULL AND new_value IS NULL
            AND old_value_json IS NOT NULL AND new_value_json IS NOT NULL)),
    CONSTRAINT policy_changes_effective_after_set_check CHECK (effective_at >= set_at)
);

CREATE INDEX policy_changes_jurisdiction_idx ON policy_changes (jurisdiction_id, lever_code, set_at DESC);
CREATE INDEX policy_changes_player_idx ON policy_changes (set_by_player_id, set_at DESC);

COMMENT ON TABLE policy_changes IS
    'Public, append-only history of every lever change, naming the office holder. UPDATE, DELETE and TRUNCATE are refused by trigger.';

-- Append-only, by trigger rather than REVOKE; see 0006_ledger for why. Its own
-- function, not the ledger's, so the hint names the right remedy.
CREATE FUNCTION refuse_policy_history_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation',
              HINT = 'The public record of a policy change is never edited; a new change supersedes it.';
END;
$$;

CREATE TRIGGER policy_changes_append_only
    BEFORE UPDATE OR DELETE ON policy_changes
    FOR EACH ROW EXECUTE FUNCTION refuse_policy_history_change();
CREATE TRIGGER policy_changes_no_truncate
    BEFORE TRUNCATE ON policy_changes
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_policy_history_change();

-- ---------------------------------------------------------------------------
-- Invariants 1–3, checked against the ACTIVE lever definition.
--
-- A CHECK cannot read another table, so this is a trigger. The application
-- refuses each case first with a named error (application.SetPolicy) and
-- serialises changes of one lever in one place under an advisory lock; this
-- is for any writer that bypassed both.
-- ---------------------------------------------------------------------------
CREATE FUNCTION policy_value_must_respect_lever() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lever record;
    place text;
BEGIN
    SELECT ld.* INTO lever
      FROM lever_definitions ld
      JOIN content_versions cv ON cv.id = ld.content_version_id AND cv.status = 'active'
     WHERE ld.code = NEW.lever_code;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'policy value for unknown lever %', NEW.lever_code
            USING ERRCODE = 'foreign_key_violation', CONSTRAINT = 'policy_values_lever_exists';
    END IF;

    SELECT kind INTO place FROM jurisdictions WHERE id = NEW.jurisdiction_id;
    IF place IS DISTINCT FROM lever.jurisdiction_kind THEN
        RAISE EXCEPTION 'lever % is a % lever, not a % one', NEW.lever_code, lever.jurisdiction_kind, place
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_jurisdiction_kind';
    END IF;

    IF NEW.value_kind IS DISTINCT FROM lever.value_kind THEN
        RAISE EXCEPTION 'lever % holds % values, not %', NEW.lever_code, lever.value_kind, NEW.value_kind
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_lever_kind';
    END IF;

    IF lever.value_kind = 'scalar' AND (NEW.value < lever.min_value OR NEW.value > lever.max_value) THEN
        RAISE EXCEPTION 'policy value % for % is outside [%, %]', NEW.value, NEW.lever_code,
                lever.min_value, lever.max_value
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_bounds';
    END IF;

    IF NEW.effective_at < NEW.set_at + make_interval(secs => lever.notice_seconds) THEN
        RAISE EXCEPTION 'policy value for % takes effect before its notice of % seconds',
                NEW.lever_code, lever.notice_seconds
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_notice';
    END IF;

    IF TG_OP = 'INSERT' AND EXISTS (
        SELECT 1 FROM policy_values pv
         WHERE pv.jurisdiction_id = NEW.jurisdiction_id
           AND pv.lever_code = NEW.lever_code
           AND pv.set_at > NEW.set_at - make_interval(secs => lever.change_cooldown_seconds)
    ) THEN
        RAISE EXCEPTION 'policy value for % is within the cooldown of % seconds since the last change',
                NEW.lever_code, lever.change_cooldown_seconds
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_cooldown';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER policy_values_respect_lever
    BEFORE INSERT OR UPDATE ON policy_values
    FOR EACH ROW EXECUTE FUNCTION policy_value_must_respect_lever();

-- Invariant 4: a policy value committed without its public record is refused
-- at COMMIT. Deferred, so the value may be written before the record that
-- references it, in one transaction.
CREATE FUNCTION policy_value_must_be_public() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM policy_changes WHERE policy_value_id = NEW.id) THEN
        RAISE EXCEPTION 'policy value % has no public policy_changes row', NEW.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_public_record';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER policy_values_public_record
    AFTER INSERT ON policy_values
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION policy_value_must_be_public();

-- ---------------------------------------------------------------------------
-- Backfill for a database already running content.
--
-- Once this migration exists, a content version must place every city in a
-- country on declared levels, and a service boots by rebuilding the active
-- version from these tables. Without this block a database whose active
-- version predates 0008 would hold cities with no country, and every service
-- would refuse to boot until somebody ran the loader. So the active version
-- is given the two levels its cities need and each existing city a
-- jurisdiction under 'default_country' — exactly what the shipped
-- governance.yml declares for them. This is data for rows that already
-- exist, not a constraint: the next `admin content load` matches all of it on
-- code and takes it over, keeping the ids, and nothing here limits which
-- levels may exist. Nothing happens on a database with no content.
-- ---------------------------------------------------------------------------
INSERT INTO jurisdiction_levels (code, parents, overlay, content_version_id)
SELECT l.code, l.parents, false, cv.id
  FROM content_versions cv
 CROSS JOIN (VALUES ('country', ARRAY['world']), ('city', ARRAY['country'])) AS l(code, parents)
 WHERE cv.status = 'active';

INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
SELECT gen_random_uuid(), 'country', 'default_country', 'The Commonwealth',
       '00000000-0000-4000-8000-000000000100',
       (SELECT id FROM content_versions WHERE status = 'active')
 WHERE EXISTS (SELECT 1 FROM content_versions WHERE status = 'active');

INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
SELECT gen_random_uuid(), 'city', c.code, c.name,
       (SELECT id FROM jurisdictions WHERE kind = 'country' AND code = 'default_country'),
       c.content_version_id
  FROM cities c
 WHERE EXISTS (SELECT 1 FROM jurisdictions WHERE kind = 'country' AND code = 'default_country');

UPDATE cities c
   SET jurisdiction_id = j.id
  FROM jurisdictions j
 WHERE j.kind = 'city' AND j.code = c.code;

COMMIT;
