-- 0021_military — the armed forces and diplomacy: a country's treasury and
-- defence fund, the equipment its state holds, what it bought and where it
-- is stationed, its defence periods, and the sanctions and treaties between
-- countries.
-- Rules: internal/domain/military, internal/domain/diplomacy. Content:
-- configs/content/military.yml, diplomacy.yml, defence_industry.yml, and the
-- defence offices, levers and actions of governance.yml. Decision:
-- docs/adr/0022-military-and-diplomacy.md. Money:
-- docs/adr/0009-economic-control.md (national_levy, defence_appropriation,
-- arms_procurement, military_upkeep).
--
-- THE STATE AS A HOLDER. A country's money is two accounts owned by its
-- jurisdiction: the national treasury (state_treasury) and the defence fund
-- (defence_fund). Its equipment is goods held by the organisation kind
-- 'state' (org_id = the country's jurisdiction) — one more value in the
-- owner-kind checks 0020 wrote for exactly this, not a new ownership model.
-- Every piece a state holds has one military_assets row: its class, branch
-- and garrison.
--
-- EXACTLY ONCE. A defence period is one scheduled game_action per country;
-- its clock row (military_clocks) names the action and period, and its
-- record (military_periods) has the country and the period as its primary
-- key. A procurement is one row per purchase under the listing's lock. A
-- move of equipment is one game_action; its row moves from 'moving' once.
-- A country has at most one standing sanction on another (partial unique
-- index), and a pair of countries at most one open treaty of a kind.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- A country's money.
-- ---------------------------------------------------------------------------
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund'));

-- ---------------------------------------------------------------------------
-- The state as a holder of goods.
-- ---------------------------------------------------------------------------
ALTER TABLE org_stacks DROP CONSTRAINT org_stacks_kind_check;
ALTER TABLE org_stacks ADD CONSTRAINT org_stacks_kind_check CHECK (org_kind IN ('company', 'state'));
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_org_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_org_check CHECK (
    (org_kind IS NULL) = (org_id IS NULL) AND (org_kind IS NULL OR org_kind IN ('company', 'state')));
ALTER TABLE item_movements DROP CONSTRAINT item_movements_org_check;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_org_check CHECK (
    (from_org_kind IS NULL) = (from_org IS NULL) AND (to_org_kind IS NULL) = (to_org IS NULL)
    AND (from_org_kind IS NULL OR from_org_kind IN ('company', 'state'))
    AND (to_org_kind IS NULL OR to_org_kind IN ('company', 'state')));

-- ---------------------------------------------------------------------------
-- military_clocks — each country's defence period: which one runs, since
-- when, the action that ends it, and the forces' readiness.
-- ---------------------------------------------------------------------------
CREATE TABLE military_clocks (
    country_id        uuid        PRIMARY KEY REFERENCES jurisdictions (id),
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL REFERENCES game_actions (id),
    readiness_bps     int         NOT NULL,
    updated_at        timestamptz NOT NULL,

    CONSTRAINT military_clocks_period_check CHECK (period_no >= 1),
    CONSTRAINT military_clocks_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL)),
    CONSTRAINT military_clocks_readiness_check CHECK (readiness_bps BETWEEN 0 AND 10000)
);

-- ---------------------------------------------------------------------------
-- military_periods — one settled defence period of one country. Append-only;
-- its primary key is what makes a settlement happen once.
-- ---------------------------------------------------------------------------
CREATE TABLE military_periods (
    country_id    uuid        NOT NULL REFERENCES jurisdictions (id),
    period_no     bigint      NOT NULL,
    started_at    timestamptz NOT NULL,
    ended_at      timestamptz NOT NULL,
    -- What the country's city treasuries took in during the period, and
    -- what they paid the national treasury of it.
    revenue       bigint      NOT NULL,
    levy          bigint      NOT NULL,
    -- What moved from the national treasury to the defence fund.
    appropriation bigint      NOT NULL,
    -- What the equipment cost, what the fund paid, and how many pieces.
    upkeep_due    bigint      NOT NULL,
    upkeep_paid   bigint      NOT NULL,
    pieces        bigint      NOT NULL,
    readiness_bps int         NOT NULL,

    CONSTRAINT military_periods_pkey PRIMARY KEY (country_id, period_no),
    CONSTRAINT military_periods_amounts_check CHECK (revenue >= 0 AND levy >= 0 AND levy <= revenue
        AND appropriation >= 0 AND appropriation <= levy AND upkeep_due >= 0 AND upkeep_paid >= 0
        AND upkeep_paid <= upkeep_due AND pieces >= 0),
    CONSTRAINT military_periods_readiness_check CHECK (readiness_bps BETWEEN 0 AND 10000),
    CONSTRAINT military_periods_span_check CHECK (ended_at >= started_at)
);

CREATE TRIGGER military_periods_append_only
    BEFORE UPDATE OR DELETE ON military_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER military_periods_no_truncate
    BEFORE TRUNCATE ON military_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- procurements — a state's purchase of arms from a company's listing, paid
-- from its defence fund (arms_procurement). Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE procurements (
    id                    uuid        PRIMARY KEY,
    no                    bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    country_id            uuid        NOT NULL REFERENCES jurisdictions (id),
    listing_id            uuid        NOT NULL REFERENCES company_listings (id),
    company_id            uuid        NOT NULL REFERENCES companies (id),
    item_code             text        NOT NULL,
    design_id             uuid        NULL REFERENCES product_designs (id),
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    -- Who bought, and from which office.
    bought_by             uuid        NOT NULL REFERENCES players (id),
    office_code           text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT procurements_amounts_check CHECK (quantity > 0 AND unit_price > 0 AND total = quantity * unit_price)
);
CREATE INDEX procurements_country_idx ON procurements (country_id, created_at DESC);

CREATE TRIGGER procurements_append_only
    BEFORE UPDATE OR DELETE ON procurements
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER procurements_no_truncate
    BEFORE TRUNCATE ON procurements
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- military_moves — equipment ordered to a garrison: pieces of one good (and
-- design) of one branch, travelling on the game clock until one scheduled
-- action lands them.
-- ---------------------------------------------------------------------------
CREATE TABLE military_moves (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    country_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    branch         text        NOT NULL,
    to_city_id     uuid        NOT NULL REFERENCES cities (id),
    item_code      text        NOT NULL,
    design_id      uuid        NULL REFERENCES product_designs (id),
    quantity       bigint      NOT NULL,
    status         text        NOT NULL,
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    ordered_by     uuid        NOT NULL REFERENCES players (id),
    office_code    text        NOT NULL,
    started_at     timestamptz NOT NULL,
    arrives_at     timestamptz NOT NULL,
    arrived_at     timestamptz NULL,

    CONSTRAINT military_moves_status_check CHECK (status IN ('moving', 'arrived')),
    CONSTRAINT military_moves_arrived_check CHECK ((status = 'arrived') = (arrived_at IS NOT NULL)),
    CONSTRAINT military_moves_quantity_check CHECK (quantity > 0)
);
CREATE INDEX military_moves_country_idx ON military_moves (country_id, status);

-- ---------------------------------------------------------------------------
-- military_assets — every piece a state holds: which country, class and
-- branch, and where it is stationed (NULL: the depot it was delivered to)
-- or where it is going.
-- ---------------------------------------------------------------------------
CREATE TABLE military_assets (
    piece_id         uuid        PRIMARY KEY REFERENCES item_pieces (id),
    country_id       uuid        NOT NULL REFERENCES jurisdictions (id),
    branch           text        NOT NULL,
    class_code       text        NOT NULL,
    status           text        NOT NULL,
    garrison_city_id uuid        NULL REFERENCES cities (id),
    move_id          uuid        NULL REFERENCES military_moves (id),
    procurement_id   uuid        NOT NULL REFERENCES procurements (id),
    acquired_at      timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,

    CONSTRAINT military_assets_status_check CHECK (status IN ('stationed', 'moving')),
    CONSTRAINT military_assets_move_check CHECK ((status = 'moving') = (move_id IS NOT NULL))
);
CREATE INDEX military_assets_country_idx ON military_assets (country_id, class_code);
CREATE INDEX military_assets_move_idx ON military_assets (move_id) WHERE move_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- sanctions — one country's sanction on another: its measures and ground,
-- who imposed it from which office, when it binds, and who lifted it.
-- ---------------------------------------------------------------------------
CREATE TABLE sanctions (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    imposer_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    target_id      uuid        NOT NULL REFERENCES jurisdictions (id),
    measures       text[]      NOT NULL,
    ground         text        NOT NULL,
    imposed_by     uuid        NOT NULL REFERENCES players (id),
    imposed_office text        NOT NULL,
    imposed_at     timestamptz NOT NULL,
    effective_at   timestamptz NOT NULL,
    lifted_by      uuid        NULL REFERENCES players (id),
    lifted_office  text        NULL,
    lifted_at      timestamptz NULL,

    CONSTRAINT sanctions_parties_check CHECK (imposer_id <> target_id),
    CONSTRAINT sanctions_measures_check CHECK (cardinality(measures) >= 1
        AND measures <@ ARRAY['trade', 'arms', 'technology', 'travel', 'financial']::text[]),
    CONSTRAINT sanctions_notice_check CHECK (effective_at >= imposed_at),
    CONSTRAINT sanctions_lifted_check CHECK ((lifted_at IS NULL) = (lifted_by IS NULL)
        AND (lifted_at IS NULL) = (lifted_office IS NULL) AND (lifted_at IS NULL OR lifted_at >= imposed_at))
);
-- One standing sanction per imposer and target.
CREATE UNIQUE INDEX sanctions_one_standing_idx ON sanctions (imposer_id, target_id) WHERE lifted_at IS NULL;
CREATE INDEX sanctions_target_idx ON sanctions (target_id) WHERE lifted_at IS NULL;

-- ---------------------------------------------------------------------------
-- treaties — a treaty between two countries: proposed by one, answered by
-- the other, ended by either.
-- ---------------------------------------------------------------------------
CREATE TABLE treaties (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
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
    ended_by        uuid        NULL REFERENCES players (id),
    ended_office    text        NULL,
    ended_at        timestamptz NULL,

    CONSTRAINT treaties_parties_check CHECK (proposer_id <> partner_id),
    CONSTRAINT treaties_status_check CHECK (status IN ('proposed', 'active', 'declined', 'withdrawn', 'expired', 'terminated')),
    CONSTRAINT treaties_decided_check CHECK ((status IN ('active', 'declined', 'terminated')) = (decided_at IS NOT NULL)
        AND (decided_at IS NULL) = (decided_by IS NULL)),
    CONSTRAINT treaties_ended_check CHECK ((status IN ('withdrawn', 'terminated')) = (ended_at IS NOT NULL)
        AND (ended_at IS NULL) = (ended_by IS NULL)),
    CONSTRAINT treaties_expiry_check CHECK (expires_at > proposed_at)
);
-- One open treaty of a kind per pair of countries, whichever proposed it.
CREATE UNIQUE INDEX treaties_one_open_idx ON treaties
    (kind, LEAST(proposer_id, partner_id), GREATEST(proposer_id, partner_id)) WHERE status IN ('proposed', 'active');
CREATE INDEX treaties_partner_idx ON treaties (partner_id, status);
CREATE INDEX treaties_proposer_idx ON treaties (proposer_id, status);

-- ---------------------------------------------------------------------------
-- diplomacy_events — the public record of diplomacy, append-only: every
-- sanction imposed and lifted, every treaty proposed, answered and ended,
-- with the country that acted and the office it acted from.
-- ---------------------------------------------------------------------------
CREATE TABLE diplomacy_events (
    id               uuid        PRIMARY KEY,
    kind             text        NOT NULL,
    country_id       uuid        NOT NULL REFERENCES jurisdictions (id),
    other_country_id uuid        NOT NULL REFERENCES jurisdictions (id),
    sanction_id      uuid        NULL REFERENCES sanctions (id),
    treaty_id        uuid        NULL REFERENCES treaties (id),
    player_id        uuid        NULL REFERENCES players (id),
    office_code      text        NULL,
    created_at       timestamptz NOT NULL,

    CONSTRAINT diplomacy_events_kind_check CHECK (kind IN ('sanction_imposed', 'sanction_lifted',
        'treaty_proposed', 'treaty_signed', 'treaty_declined', 'treaty_withdrawn', 'treaty_terminated')),
    CONSTRAINT diplomacy_events_subject_check CHECK ((sanction_id IS NULL) <> (treaty_id IS NULL))
);
CREATE INDEX diplomacy_events_country_idx ON diplomacy_events (country_id, created_at DESC);
CREATE INDEX diplomacy_events_other_idx ON diplomacy_events (other_country_id, created_at DESC);

CREATE TRIGGER diplomacy_events_append_only
    BEFORE UPDATE OR DELETE ON diplomacy_events
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER diplomacy_events_no_truncate
    BEFORE TRUNCATE ON diplomacy_events
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
