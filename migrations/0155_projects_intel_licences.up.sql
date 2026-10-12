-- Special projects, intelligence and sabotage between settlements, and the licence market (docs/adr/0077, plan B13).
--
--   special_projects, project_stages, project_stock, project_pledges, project_shifts, project_npc_days
--                              a long multi-stage public work, the stages done, the materials delivered, the fund's donors, the crews' shifts
--   project_contracts, project_contract_bids, project_contract_stages
--                              the tender of a project, generalised to the project's own stage count (docs/adr/0076 10)
--   intel_agents, intel_ops, intel_reports, intel_exposures, intel_expulsions, intel_shares
--                              the agent with a cover, an operation (one row per agent, target, kind and day), the banded report, the
--                              detectors' rolls on a blown operation, the expulsion, the sharing of a report
--   sabotage_acts, sabotage_effects   a capped, reversible act of sabotage and what it did (a delay, a blinded post, a skipped day)
--   licence_rules, licences, licence_auctions, licence_bids, licence_listings, licence_events
--                              the requirement a charter creates, the rights granted, the sealed-bid auctions, the exchange board, the audit
--   accounts                   three new kinds: project_fund, project_escrow, licence_escrow
BEGIN;

-- ---------------------------------------------------------------------------
-- special projects
-- ---------------------------------------------------------------------------
CREATE TABLE special_projects (
    id               uuid        PRIMARY KEY,
    kind             text        NOT NULL,
    settlement_id    uuid        NOT NULL REFERENCES cities (id),
    started_by       uuid        NOT NULL REFERENCES players (id),
    stages           integer     NOT NULL CHECK (stages BETWEEN 1 AND 20),
    stage            integer     NOT NULL DEFAULT 1 CHECK (stage >= 1),
    -- the crew-hours worked on the current stage
    stage_hours      bigint      NOT NULL DEFAULT 0 CHECK (stage_hours >= 0),
    stage_started_at timestamptz NULL,
    -- a stage cannot be accepted before this instant (a delay by sabotage moves it)
    delayed_until    timestamptz NULL,
    status           text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'finished', 'cancelled')),
    neglected        boolean     NOT NULL DEFAULT false,
    unpaid_periods   integer     NOT NULL DEFAULT 0 CHECK (unpaid_periods >= 0),
    upkeep_due_at    timestamptz NULL,
    started_at       timestamptz NOT NULL,
    finished_at      timestamptz NULL,
    cancelled_at     timestamptz NULL,
    CONSTRAINT special_projects_stage_check CHECK (stage <= stages),
    CONSTRAINT special_projects_finished_check CHECK ((status = 'finished') = (finished_at IS NOT NULL)),
    CONSTRAINT special_projects_cancelled_check CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);
CREATE INDEX special_projects_settlement_idx ON special_projects (settlement_id, started_at DESC);
CREATE INDEX special_projects_open_idx ON special_projects (status) WHERE status = 'active';

-- a stage done is never taken back
CREATE TABLE project_stages (
    project_id    uuid        NOT NULL REFERENCES special_projects (id),
    stage         integer     NOT NULL CHECK (stage >= 1),
    hours         bigint      NOT NULL CHECK (hours >= 0),
    completed_at  timestamptz NOT NULL,
    PRIMARY KEY (project_id, stage)
);

-- the materials delivered to the current stage (a stage done uses them up)
CREATE TABLE project_stock (
    project_id uuid   NOT NULL REFERENCES special_projects (id),
    item       text   NOT NULL,
    qty        bigint NOT NULL CHECK (qty >= 0),
    PRIMARY KEY (project_id, item)
);

-- what was given to the fund: a pledge of a resident or an outsider, or the treasury's allocation
CREATE TABLE project_pledges (
    id         uuid        PRIMARY KEY,
    project_id uuid        NOT NULL REFERENCES special_projects (id),
    player_id  uuid        NULL REFERENCES players (id),
    kind       text        NOT NULL CHECK (kind IN ('pledge', 'allocation')),
    amount     bigint      NOT NULL CHECK (amount > 0),
    anonymous  boolean     NOT NULL DEFAULT false,
    status     text        NOT NULL DEFAULT 'held' CHECK (status IN ('held', 'refunded')),
    ledger_tx  uuid        NULL,
    at         timestamptz NOT NULL,
    refunded_at timestamptz NULL,
    CONSTRAINT project_pledges_who_check CHECK ((kind = 'pledge') = (player_id IS NOT NULL)),
    CONSTRAINT project_pledges_refund_check CHECK ((status = 'refunded') = (refunded_at IS NOT NULL))
);
CREATE INDEX project_pledges_project_idx ON project_pledges (project_id, at);

-- a shift of a player on the site: hours counted when it ends, a wage paid from the fund (or by the contractor)
CREATE TABLE project_shifts (
    id          uuid        PRIMARY KEY,
    project_id  uuid        NOT NULL REFERENCES special_projects (id),
    stage       integer     NOT NULL,
    player_id   uuid        NOT NULL REFERENCES players (id),
    hours       bigint      NOT NULL CHECK (hours > 0),
    wage        bigint      NOT NULL CHECK (wage >= 0),
    payer_kind  text        NOT NULL CHECK (payer_kind IN ('fund', 'company')),
    payer_id    uuid        NULL,
    status      text        NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done')),
    started_at  timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    ledger_tx   uuid        NULL
);
-- one running shift per player, wherever it is
CREATE UNIQUE INDEX project_shifts_one_running_idx ON project_shifts (player_id) WHERE status = 'running';
CREATE INDEX project_shifts_project_idx ON project_shifts (project_id, started_at DESC);

-- the hours of hired labour of the settlement's pool, one row a project and day (the fence)
CREATE TABLE project_npc_days (
    project_id uuid   NOT NULL REFERENCES special_projects (id),
    day        bigint NOT NULL,
    stage      integer NOT NULL,
    hours      bigint NOT NULL CHECK (hours > 0),
    wage       bigint NOT NULL CHECK (wage >= 0),
    PRIMARY KEY (project_id, day)
);

-- the tender of a project, paid in the project's own stages
CREATE TABLE project_contracts (
    id             uuid        PRIMARY KEY,
    project_id     uuid        NOT NULL REFERENCES special_projects (id),
    posted_by      uuid        NOT NULL REFERENCES players (id),
    budget_cap     bigint      NOT NULL CHECK (budget_cap >= 0),
    rule           text        NOT NULL CHECK (rule IN ('lowest_price', 'scored')),
    bond_bps       bigint      NOT NULL,
    retention_bps  bigint      NOT NULL,
    defects_days   integer     NOT NULL,
    opens_at       timestamptz NOT NULL,
    closes_at      timestamptz NOT NULL,
    due_days       integer     NOT NULL CHECK (due_days > 0),
    status         text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'awarded', 'done', 'failed', 'cancelled')),
    bid_id         uuid        NULL,
    company_id     uuid        NULL REFERENCES companies (id),
    price          bigint      NULL,
    awarded_at     timestamptz NULL,
    due_at         timestamptz NULL,
    stages_paid    integer     NOT NULL DEFAULT 0 CHECK (stages_paid >= 0),
    retention_held bigint      NOT NULL DEFAULT 0,
    defect_raised  boolean     NOT NULL DEFAULT false,
    last_stage_at  timestamptz NULL,
    closed_at      timestamptz NULL
);
CREATE UNIQUE INDEX project_contracts_one_idx ON project_contracts (project_id) WHERE status IN ('open', 'awarded');

CREATE TABLE project_contract_bids (
    id          uuid        PRIMARY KEY,
    contract_id uuid        NOT NULL REFERENCES project_contracts (id),
    company_id  uuid        NOT NULL REFERENCES companies (id),
    bid_by      uuid        NOT NULL REFERENCES players (id),
    price       bigint      NOT NULL CHECK (price > 0),
    days        integer     NOT NULL CHECK (days > 0),
    record      integer     NOT NULL DEFAULT 0,
    bond        bigint      NOT NULL CHECK (bond >= 0),
    status      text        NOT NULL DEFAULT 'standing' CHECK (status IN ('standing', 'won', 'lost', 'withdrawn')),
    at          timestamptz NOT NULL,
    UNIQUE (contract_id, company_id)
);

CREATE TABLE project_contract_stages (
    contract_id uuid        NOT NULL REFERENCES project_contracts (id),
    stage       integer     NOT NULL CHECK (stage >= 1),
    paid        bigint      NOT NULL CHECK (paid >= 0),
    held        bigint      NOT NULL CHECK (held >= 0),
    ledger_tx   uuid        NULL,
    at          timestamptz NOT NULL,
    PRIMARY KEY (contract_id, stage)
);

-- ---------------------------------------------------------------------------
-- intelligence
-- ---------------------------------------------------------------------------
CREATE TABLE intel_agents (
    id          uuid        PRIMARY KEY,
    player_id   uuid        NOT NULL REFERENCES players (id),
    sponsor_id  uuid        NOT NULL REFERENCES cities (id),
    target_id   uuid        NOT NULL REFERENCES cities (id),
    status      text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expelled', 'recalled')),
    cover_since timestamptz NOT NULL,
    created_at  timestamptz NOT NULL,
    ended_at    timestamptz NULL,
    CONSTRAINT intel_agents_apart_check CHECK (sponsor_id <> target_id),
    CONSTRAINT intel_agents_ended_check CHECK ((status = 'active') = (ended_at IS NULL))
);
-- one active assignment per agent and target
CREATE UNIQUE INDEX intel_agents_active_idx ON intel_agents (player_id, target_id) WHERE status = 'active';
CREATE INDEX intel_agents_sponsor_idx ON intel_agents (sponsor_id, created_at DESC);

CREATE TABLE intel_ops (
    id          uuid        PRIMARY KEY,
    agent_id    uuid        NOT NULL REFERENCES intel_agents (id),
    sponsor_id  uuid        NOT NULL REFERENCES cities (id),
    target_id   uuid        NOT NULL REFERENCES cities (id),
    kind        text        NOT NULL,
    day         bigint      NOT NULL,
    chance_bps  integer     NOT NULL CHECK (chance_bps BETWEEN 0 AND 10000),
    roll        integer     NOT NULL CHECK (roll BETWEEN 0 AND 9999),
    outcome     text        NOT NULL CHECK (outcome IN ('clean', 'partial', 'blown')),
    fee         bigint      NOT NULL CHECK (fee >= 0),
    wage        bigint      NOT NULL CHECK (wage >= 0),
    ledger_tx   uuid        NULL,
    at          timestamptz NOT NULL,
    UNIQUE (agent_id, target_id, kind, day)
);
CREATE INDEX intel_ops_sponsor_idx ON intel_ops (sponsor_id, at DESC);

-- a report holds bands and codes, never a count or a name of a person
CREATE TABLE intel_reports (
    id          uuid        PRIMARY KEY,
    op_id       uuid        NOT NULL UNIQUE REFERENCES intel_ops (id),
    sponsor_id  uuid        NOT NULL REFERENCES cities (id),
    target_id   uuid        NOT NULL REFERENCES cities (id),
    kind        text        NOT NULL,
    fields      jsonb       NOT NULL,
    confidence  text        NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    taken_at    timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL
);
CREATE INDEX intel_reports_sponsor_idx ON intel_reports (sponsor_id, taken_at DESC);

CREATE TABLE intel_exposures (
    op_id       uuid        NOT NULL REFERENCES intel_ops (id),
    detector_id uuid        NOT NULL,
    chance_bps  integer     NOT NULL CHECK (chance_bps BETWEEN 0 AND 10000),
    roll        integer     NOT NULL CHECK (roll BETWEEN 0 AND 9999),
    detected    boolean     NOT NULL,
    at          timestamptz NOT NULL,
    PRIMARY KEY (op_id, detector_id)
);

CREATE TABLE intel_expulsions (
    id          uuid        PRIMARY KEY,
    op_id       uuid        NOT NULL UNIQUE REFERENCES intel_ops (id),
    agent_id    uuid        NOT NULL REFERENCES intel_agents (id),
    player_id   uuid        NOT NULL REFERENCES players (id),
    target_id   uuid        NOT NULL REFERENCES cities (id),
    sponsor_id  uuid        NOT NULL REFERENCES cities (id),
    attributed  boolean     NOT NULL,
    until       timestamptz NOT NULL,
    at          timestamptz NOT NULL
);
CREATE INDEX intel_expulsions_player_idx ON intel_expulsions (player_id, target_id, until DESC);

CREATE TABLE intel_shares (
    report_id  uuid        NOT NULL REFERENCES intel_reports (id),
    to_kind    text        NOT NULL CHECK (to_kind IN ('charter', 'settlement')),
    to_id      uuid        NOT NULL,
    shared_by  uuid        NOT NULL REFERENCES players (id),
    at         timestamptz NOT NULL,
    PRIMARY KEY (report_id, to_kind, to_id)
);

-- ---------------------------------------------------------------------------
-- sabotage
-- ---------------------------------------------------------------------------
CREATE TABLE sabotage_acts (
    id          uuid        PRIMARY KEY,
    sponsor_id  uuid        NOT NULL REFERENCES cities (id),
    target_id   uuid        NOT NULL REFERENCES cities (id),
    agent_id    uuid        NOT NULL REFERENCES intel_agents (id),
    kind        text        NOT NULL CHECK (kind IN ('delay_work', 'spoil_stock', 'blind_post', 'skip_service_day')),
    ref_type    text        NOT NULL,
    ref_id      text        NOT NULL,
    amount      bigint      NOT NULL DEFAULT 0 CHECK (amount >= 0),
    unit        text        NOT NULL DEFAULT '',
    cost        bigint      NOT NULL CHECK (cost >= 0),
    ledger_tx   uuid        NULL,
    repair      text        NOT NULL DEFAULT 'passes' CHECK (repair IN ('passes', 'replace', 'recovers', 'catch_up')),
    repaired_at timestamptz NULL,
    at          timestamptz NOT NULL,
    CONSTRAINT sabotage_acts_apart_check CHECK (sponsor_id <> target_id)
);
CREATE INDEX sabotage_acts_sponsor_idx ON sabotage_acts (sponsor_id, at DESC);
CREATE INDEX sabotage_acts_target_idx ON sabotage_acts (target_id, kind, ref_id, at DESC);

-- what an act does while it lasts: a post blinded until, a building's service skipped on a day
CREATE TABLE sabotage_effects (
    act_id      uuid        PRIMARY KEY REFERENCES sabotage_acts (id),
    kind        text        NOT NULL,
    building_id uuid        NOT NULL,
    day         bigint      NULL,
    until       timestamptz NULL,
    bps         integer     NOT NULL DEFAULT 0
);
CREATE INDEX sabotage_effects_building_idx ON sabotage_effects (building_id, kind);

-- the stays of an expelled agent are in intel_expulsions; a charter's vetting policy is a column of the settlement's rules
CREATE TABLE intel_vetting (
    settlement_id uuid        PRIMARY KEY REFERENCES cities (id),
    probation_days integer    NOT NULL CHECK (probation_days BETWEEN 0 AND 60),
    set_by        uuid        NOT NULL REFERENCES players (id),
    at            timestamptz NOT NULL
);

-- ---------------------------------------------------------------------------
-- licences
-- ---------------------------------------------------------------------------
-- the requirement a charter creates: from effective_at nobody does the thing without the licence
CREATE TABLE licence_rules (
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    kind          text        NOT NULL,
    mode          text        NOT NULL CHECK (mode IN ('fixed', 'auction')),
    fee           bigint      NOT NULL CHECK (fee >= 0),
    reserve       bigint      NOT NULL DEFAULT 0 CHECK (reserve >= 0),
    term_days     integer     NOT NULL CHECK (term_days > 0),
    transferable  boolean     NOT NULL DEFAULT false,
    set_by        uuid        NOT NULL REFERENCES players (id),
    set_at        timestamptz NOT NULL,
    effective_at  timestamptz NOT NULL,
    closed_at     timestamptz NULL,
    PRIMARY KEY (settlement_id, kind)
);

CREATE SEQUENCE licence_no_seq;
CREATE TABLE licences (
    id           uuid        PRIMARY KEY,
    no           bigint      NOT NULL DEFAULT nextval('licence_no_seq') UNIQUE,
    kind         text        NOT NULL,
    issuer_id    uuid        NOT NULL REFERENCES cities (id),
    holder_kind  text        NOT NULL CHECK (holder_kind IN ('player', 'company')),
    holder_id    uuid        NOT NULL,
    target_ref   text        NOT NULL DEFAULT '',
    via          text        NOT NULL CHECK (via IN ('fixed', 'auction', 'first_grant', 'transfer')),
    paid         bigint      NOT NULL DEFAULT 0 CHECK (paid >= 0),
    transferable boolean     NOT NULL DEFAULT false,
    status       text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'notice', 'revoked', 'expired', 'transferred')),
    term_start   timestamptz NOT NULL,
    term_end     timestamptz NOT NULL,
    last_used_at timestamptz NULL,
    notice_until timestamptz NULL,
    cause        text        NULL,
    transferred_at timestamptz NULL,
    from_licence uuid        NULL REFERENCES licences (id),
    created_at   timestamptz NOT NULL,
    ended_at     timestamptz NULL,
    CONSTRAINT licences_term_check CHECK (term_end > term_start),
    CONSTRAINT licences_ended_check CHECK ((status IN ('revoked', 'expired', 'transferred')) = (ended_at IS NOT NULL))
);
CREATE INDEX licences_issuer_idx ON licences (issuer_id, kind) WHERE status IN ('active', 'notice');
CREATE INDEX licences_holder_idx ON licences (holder_kind, holder_id) WHERE status IN ('active', 'notice');
-- an exclusive right (a target) has at most one live holder
CREATE UNIQUE INDEX licences_one_target_idx ON licences (issuer_id, kind, target_ref) WHERE target_ref <> '' AND status IN ('active', 'notice');

CREATE TABLE licence_auctions (
    id           uuid        PRIMARY KEY,
    issuer_id    uuid        NOT NULL REFERENCES cities (id),
    kind         text        NOT NULL,
    target_ref   text        NOT NULL DEFAULT '',
    posted_by    uuid        NOT NULL REFERENCES players (id),
    reserve      bigint      NOT NULL CHECK (reserve > 0),
    term_days    integer     NOT NULL CHECK (term_days > 0),
    transferable boolean     NOT NULL DEFAULT false,
    opens_at     timestamptz NOT NULL,
    closes_at    timestamptz NOT NULL,
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'awarded', 'void', 'cancelled')),
    bid_id       uuid        NULL,
    price        bigint      NULL,
    licence_id   uuid        NULL REFERENCES licences (id),
    closed_at    timestamptz NULL,
    CONSTRAINT licence_auctions_window_check CHECK (closes_at > opens_at)
);
CREATE UNIQUE INDEX licence_auctions_one_open_idx ON licence_auctions (issuer_id, kind, target_ref) WHERE status = 'open';

CREATE TABLE licence_bids (
    id          uuid        PRIMARY KEY,
    auction_id  uuid        NOT NULL REFERENCES licence_auctions (id),
    bidder_kind text        NOT NULL CHECK (bidder_kind IN ('player', 'company')),
    bidder_id   uuid        NOT NULL,
    bid_by      uuid        NOT NULL REFERENCES players (id),
    amount      bigint      NOT NULL CHECK (amount > 0),
    status      text        NOT NULL DEFAULT 'standing' CHECK (status IN ('standing', 'won', 'lost', 'withdrawn')),
    at          timestamptz NOT NULL,
    UNIQUE (auction_id, bidder_kind, bidder_id)
);

CREATE TABLE licence_listings (
    id         uuid        PRIMARY KEY,
    licence_id uuid        NOT NULL REFERENCES licences (id),
    seller_id  uuid        NOT NULL REFERENCES players (id),
    price      bigint      NOT NULL CHECK (price > 0),
    status     text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'sold', 'cancelled')),
    at         timestamptz NOT NULL,
    closed_at  timestamptz NULL
);
CREATE UNIQUE INDEX licence_listings_one_open_idx ON licence_listings (licence_id) WHERE status = 'open';

CREATE TABLE licence_events (
    id         uuid        PRIMARY KEY,
    issuer_id  uuid        NOT NULL,
    licence_id uuid        NULL,
    actor_id   uuid        NULL,
    kind       text        NOT NULL,
    detail     jsonb       NOT NULL DEFAULT '{}',
    at         timestamptz NOT NULL
);
CREATE INDEX licence_events_issuer_idx ON licence_events (issuer_id, at DESC);
CREATE FUNCTION licence_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'licence_events is append-only';
END;
$$;
CREATE TRIGGER licence_events_no_change BEFORE UPDATE OR DELETE ON licence_events
    FOR EACH ROW EXECUTE FUNCTION licence_events_append_only();

-- the same for what the saboteur did and what the agents learned: the acts are never deleted
CREATE FUNCTION sabotage_acts_no_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'sabotage_acts is never deleted';
END;
$$;
CREATE TRIGGER sabotage_acts_keep BEFORE DELETE ON sabotage_acts
    FOR EACH ROW EXECUTE FUNCTION sabotage_acts_no_delete();

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet', 'works_escrow',
    'project_fund', 'project_escrow', 'licence_escrow'));

-- The head office of a stored settlement charter gets the new settlement-level powers with the same hand (docs/adr/0077 8).
UPDATE charter_offices
   SET grants = grants
       || CASE WHEN grants @> '[{"p": "project.start"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "project.start"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "project.fund"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "project.fund"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "project.accept"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "project.accept"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "intel.run"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "intel.run"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "intel.read"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "intel.read"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "intel.share"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "intel.share"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "counter.vet"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "counter.vet"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "licence.set_kind"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "licence.set_kind"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "licence.issue"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "licence.issue"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "licence.auction"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "licence.auction"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "licence.revoke"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "licence.revoke"}]'::jsonb END
 WHERE acquisition = 'head' AND closed_at IS NULL;

COMMIT;
