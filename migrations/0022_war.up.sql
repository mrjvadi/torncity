-- 0022_war — war between countries: its declaration, its parties, the
-- ceasefires and peaces proposed in it, the operations fought in it, what
-- they did to cities, which cities changed hands, and its public record.
-- Rules: internal/domain/war. Content: configs/content/military.yml (war,
-- and each force class's combat and repair), governance.yml (country.war,
-- country.war_levy, military_governor). Decision:
-- docs/adr/0022-military-and-diplomacy.md, part two. Money:
-- docs/adr/0009-economic-control.md (war_levy, military_repair).
--
-- EXACTLY ONCE. A declaration is one row per pair of countries while it is
-- not over (partial unique index on the ordered pair). An operation is one
-- scheduled game_action; its row moves from 'launched' once, under its lock.
-- A city changes hands once per operation: the city's control row names the
-- operation that took it, and the city's jurisdiction moves under the war's
-- lock in the same transaction.
--
-- EQUIPMENT LOST. A piece destroyed in battle, or a munition or missile
-- spent, leaves the world through the item journal ('destroyed',
-- 'expended'); its military_assets row stays, as the record, with the status
-- saying so and the operation that lost it. A damaged piece stays in service
-- but cannot fight until a defence period pays its repair.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- wars — one war: who declared it on whom, on what ground, from which
-- office, when it may be fought, and how it ended.
-- ---------------------------------------------------------------------------
CREATE TABLE wars (
    id               uuid        PRIMARY KEY,
    no               bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    attacker_id      uuid        NOT NULL REFERENCES jurisdictions (id),
    defender_id      uuid        NOT NULL REFERENCES jurisdictions (id),
    ground           text        NOT NULL,
    -- 'declared' until active_at (read as active from then), 'ceasefire',
    -- 'ended'.
    status           text        NOT NULL,
    declared_by      uuid        NOT NULL REFERENCES players (id),
    declared_office  text        NOT NULL,
    declared_at      timestamptz NOT NULL,
    active_at        timestamptz NOT NULL,
    -- Whether journeys between the parties' cities are closed (content
    -- war.economy.close_border, fixed at the declaration).
    border_closed    boolean     NOT NULL,
    -- The treaties between the two the declaration broke.
    broke_treaties   bigint[]    NOT NULL,
    ended_at         timestamptz NULL,
    updated_at       timestamptz NOT NULL,

    CONSTRAINT wars_parties_check CHECK (attacker_id <> defender_id),
    CONSTRAINT wars_status_check CHECK (status IN ('declared', 'ceasefire', 'ended')),
    CONSTRAINT wars_notice_check CHECK (active_at >= declared_at),
    CONSTRAINT wars_ended_check CHECK ((status = 'ended') = (ended_at IS NOT NULL))
);
-- One war not over per pair of principals, whoever declared it.
CREATE UNIQUE INDEX wars_one_open_idx ON wars
    (LEAST(attacker_id, defender_id), GREATEST(attacker_id, defender_id)) WHERE status <> 'ended';

-- ---------------------------------------------------------------------------
-- war_parties — every country in a war and its side: the two principals
-- from the declaration, allies as they join.
-- ---------------------------------------------------------------------------
CREATE TABLE war_parties (
    war_id      uuid        NOT NULL REFERENCES wars (id),
    country_id  uuid        NOT NULL REFERENCES jurisdictions (id),
    side        text        NOT NULL,
    joined_by   uuid        NOT NULL REFERENCES players (id),
    office_code text        NOT NULL,
    joined_at   timestamptz NOT NULL,

    CONSTRAINT war_parties_pkey PRIMARY KEY (war_id, country_id),
    CONSTRAINT war_parties_side_check CHECK (side IN ('attacker', 'defender'))
);
CREATE INDEX war_parties_country_idx ON war_parties (country_id);

-- ---------------------------------------------------------------------------
-- war_proposals — a ceasefire or a peace one principal offered the other.
-- ---------------------------------------------------------------------------
CREATE TABLE war_proposals (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    war_id          uuid        NOT NULL REFERENCES wars (id),
    kind            text        NOT NULL,
    proposer_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    partner_id      uuid        NOT NULL REFERENCES jurisdictions (id),
    status          text        NOT NULL,
    proposed_by     uuid        NOT NULL REFERENCES players (id),
    proposed_office text        NOT NULL,
    proposed_at     timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    decided_by      uuid        NULL REFERENCES players (id),
    decided_office  text        NULL,
    decided_at      timestamptz NULL,

    CONSTRAINT war_proposals_kind_check CHECK (kind IN ('ceasefire', 'peace')),
    CONSTRAINT war_proposals_status_check CHECK (status IN ('proposed', 'accepted', 'declined', 'withdrawn', 'expired')),
    CONSTRAINT war_proposals_decided_check CHECK ((status IN ('accepted', 'declined', 'withdrawn')) = (decided_at IS NOT NULL)
        AND (decided_at IS NULL) = (decided_by IS NULL)),
    CONSTRAINT war_proposals_expiry_check CHECK (expires_at > proposed_at)
);
CREATE UNIQUE INDEX war_proposals_one_open_idx ON war_proposals (war_id, kind) WHERE status = 'proposed';

-- ---------------------------------------------------------------------------
-- war_operations — one operation: a strike or an assault, launched by a
-- branch's commander from a garrison at an enemy city, resolved once on the
-- game clock. Its report is the result; its public face the bands.
-- ---------------------------------------------------------------------------
CREATE TABLE war_operations (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    war_id          uuid        NOT NULL REFERENCES wars (id),
    kind            text        NOT NULL,
    objective       text        NOT NULL,
    country_id      uuid        NOT NULL REFERENCES jurisdictions (id),
    target_country_id uuid      NOT NULL REFERENCES jurisdictions (id),
    from_city_id    uuid        NOT NULL REFERENCES cities (id),
    target_city_id  uuid        NOT NULL REFERENCES cities (id),
    class_code      text        NOT NULL,
    committed       int         NOT NULL,
    munitions       int         NOT NULL,
    status          text        NOT NULL,
    game_action_id  uuid        NOT NULL REFERENCES game_actions (id),
    ordered_by      uuid        NOT NULL REFERENCES players (id),
    office_code     text        NOT NULL,
    -- The seed the battle's dice are rolled from: an operation resolved
    -- again rolls the same.
    seed            bigint      NOT NULL,
    launched_at     timestamptz NOT NULL,
    strikes_at      timestamptz NOT NULL,
    resolved_at     timestamptz NULL,
    -- The outcome, filled at resolution.
    attacker_lost   int         NOT NULL DEFAULT 0,
    attacker_damaged int        NOT NULL DEFAULT 0,
    defender_lost   int         NOT NULL DEFAULT 0,
    defender_damaged int        NOT NULL DEFAULT 0,
    munitions_used  int         NOT NULL DEFAULT 0,
    hits            int         NOT NULL DEFAULT 0,
    damage_bps      int         NOT NULL DEFAULT 0,
    captured        boolean     NOT NULL DEFAULT false,
    report          jsonb       NOT NULL DEFAULT '{}',

    CONSTRAINT war_operations_kind_check CHECK (kind IN ('air', 'missile', 'ground')),
    CONSTRAINT war_operations_objective_check CHECK (objective IN ('city', 'defences', 'take')),
    CONSTRAINT war_operations_status_check CHECK (status IN ('launched', 'resolved', 'called_off')),
    CONSTRAINT war_operations_resolved_check CHECK ((status = 'launched') = (resolved_at IS NULL)),
    CONSTRAINT war_operations_counts_check CHECK (committed > 0 AND munitions >= 0 AND attacker_lost >= 0
        AND defender_lost >= 0 AND munitions_used >= 0 AND munitions_used <= munitions AND hits >= 0
        AND damage_bps BETWEEN 0 AND 10000)
);
CREATE INDEX war_operations_war_idx ON war_operations (war_id, launched_at DESC);
CREATE INDEX war_operations_target_idx ON war_operations (target_city_id, launched_at DESC);

-- ---------------------------------------------------------------------------
-- The equipment an operation takes, and what it loses.
-- ---------------------------------------------------------------------------
ALTER TABLE military_assets DROP CONSTRAINT military_assets_status_check;
ALTER TABLE military_assets ADD CONSTRAINT military_assets_status_check
    CHECK (status IN ('stationed', 'moving', 'committed', 'destroyed', 'expended'));
ALTER TABLE military_assets ADD COLUMN condition text NOT NULL DEFAULT 'ready';
ALTER TABLE military_assets ADD CONSTRAINT military_assets_condition_check CHECK (condition IN ('ready', 'damaged'));
ALTER TABLE military_assets ADD COLUMN operation_id uuid NULL REFERENCES war_operations (id);
ALTER TABLE military_assets ADD CONSTRAINT military_assets_operation_check
    CHECK ((status IN ('committed', 'destroyed', 'expended')) = (operation_id IS NOT NULL));
-- Equipment that came to the state otherwise than by purchase (an
-- operator's grant for an event) has no procurement.
ALTER TABLE military_assets ALTER COLUMN procurement_id DROP NOT NULL;
CREATE INDEX military_assets_operation_idx ON military_assets (operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX military_assets_garrison_idx ON military_assets (garrison_city_id, status);

-- The war economy: what a defence period levied for the war and paid for
-- repairs.
ALTER TABLE military_periods ADD COLUMN war_levy bigint NOT NULL DEFAULT 0;
ALTER TABLE military_periods ADD COLUMN repairs bigint NOT NULL DEFAULT 0;
ALTER TABLE military_periods ADD COLUMN repaired bigint NOT NULL DEFAULT 0;
ALTER TABLE military_periods ADD CONSTRAINT military_periods_war_check CHECK (war_levy >= 0 AND repairs >= 0 AND repaired >= 0);

-- ---------------------------------------------------------------------------
-- city_war_damage — a city's war damage as last stored; the damage at any
-- later instant heals at the content's rate (read, never scheduled).
-- ---------------------------------------------------------------------------
CREATE TABLE city_war_damage (
    city_id        uuid        PRIMARY KEY REFERENCES cities (id),
    damage_bps     int         NOT NULL,
    as_of          timestamptz NOT NULL,
    last_struck_at timestamptz NOT NULL,
    -- Journeys into the city are refused until then (content
    -- war.city.closed_for).
    closed_until   timestamptz NOT NULL,

    CONSTRAINT city_war_damage_check CHECK (damage_bps BETWEEN 0 AND 10000)
);

-- ---------------------------------------------------------------------------
-- city_control — a city held by a country other than the one the content
-- puts it in: the row exists exactly while it is occupied. The city's
-- jurisdiction is moved under the controller (so every country-level rule
-- follows it); a content load leaves such a city where it stands.
-- ---------------------------------------------------------------------------
CREATE TABLE city_control (
    city_id               uuid        PRIMARY KEY REFERENCES cities (id),
    de_jure_country_id    uuid        NOT NULL REFERENCES jurisdictions (id),
    controller_country_id uuid        NOT NULL REFERENCES jurisdictions (id),
    war_id                uuid        NOT NULL REFERENCES wars (id),
    operation_id          uuid        NOT NULL REFERENCES war_operations (id),
    since                 timestamptz NOT NULL,

    CONSTRAINT city_control_occupied_check CHECK (de_jure_country_id <> controller_country_id)
);

-- ---------------------------------------------------------------------------
-- war_events — the public record of war, append-only: declarations,
-- treaties a declaration broke, allies joining, ceasefires and peaces,
-- operations, and cities changing hands — with who acted from which office.
-- ---------------------------------------------------------------------------
CREATE TABLE war_events (
    id               uuid        PRIMARY KEY,
    kind             text        NOT NULL,
    war_id           uuid        NOT NULL REFERENCES wars (id),
    country_id       uuid        NOT NULL REFERENCES jurisdictions (id),
    other_country_id uuid        NULL REFERENCES jurisdictions (id),
    city_id          uuid        NULL REFERENCES cities (id),
    operation_id     uuid        NULL REFERENCES war_operations (id),
    proposal_id      uuid        NULL REFERENCES war_proposals (id),
    player_id        uuid        NULL REFERENCES players (id),
    office_code      text        NULL,
    created_at       timestamptz NOT NULL,

    CONSTRAINT war_events_kind_check CHECK (kind IN ('declared', 'treaty_broken', 'joined', 'proposed',
        'ceasefire', 'peace', 'declined', 'resumed', 'operation', 'captured', 'liberated'))
);
CREATE INDEX war_events_war_idx ON war_events (war_id, created_at DESC);
CREATE INDEX war_events_country_idx ON war_events (country_id, created_at DESC);

CREATE TRIGGER war_events_append_only
    BEFORE UPDATE OR DELETE ON war_events
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER war_events_no_truncate
    BEFORE TRUNCATE ON war_events
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
