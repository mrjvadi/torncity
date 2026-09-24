-- 0019_companies — player-owned businesses.
-- Rules: internal/domain/company. Content: configs/content/companies.yml.
-- Decision: docs/adr/0020-companies.md. Money: docs/adr/0009-economic-control.md.
-- Schema: docs/database.md, section 3.
--
-- A player founds a company in a city: it has its own treasury account in the
-- ledger (accounts kind 'company_treasury', owner_id = companies.id — a kind
-- 0006 already allowed and nothing could open until now), all its shares
-- belong to its founder, it posts job openings players apply to, and a shift
-- worked for it is paid from its treasury, not from system_source.
--
-- Every period (config company.period, game time) each city's companies are
-- settled ONCE: the city's NPC population pays them (a faucet bounded by the
-- city's budget) and they pay their upkeep. company_markets holds each city's
-- clock — the one scheduled action of the next settlement — and
-- company_market_periods / company_periods record each settlement; their
-- primary keys are what make a replayed settlement a no-op.
--
-- NO NEGATIVE BALANCE. The treasury is an account like any other
-- (accounts_balance_non_negative_check). On top of that, a shift reserves its
-- wage on shift_sessions.wage_reserved when it starts; everything a company
-- pays out by choice — a withdrawal, upkeep, a closing payout — is bounded by
-- its balance less the wages reserved, so a shift is always paid. Upkeep a
-- company cannot pay is owed (companies.debt), never overdrawn.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- companies — one row per company, active or dissolved. A dissolved company's
-- row stays as the record, and its name is free again in its city.
-- ---------------------------------------------------------------------------
CREATE TABLE companies (
    id                  uuid        PRIMARY KEY,
    -- The public code, like a player's: 7 characters of
    -- internal/shared/playercode's alphabet.
    code                text        NOT NULL,
    name                text        NOT NULL,
    -- The name as internal/domain/company.NameKey compares it.
    name_key            text        NOT NULL,
    -- A company_types code of the content.
    type_code           text        NOT NULL,
    city_id             uuid        NOT NULL REFERENCES cities (id),
    owner_player_id     uuid        NOT NULL REFERENCES players (id),
    manager_player_id   uuid        NULL REFERENCES players (id),
    status              text        NOT NULL,
    -- The price level, 10000 = the type's reference price.
    price_bps           int         NOT NULL,
    -- Applications to its openings are accepted as they arrive.
    auto_accept         boolean     NOT NULL DEFAULT false,
    total_shares        bigint      NOT NULL,
    -- Upkeep owed and unpaid, and the settlements in a row that ended so.
    debt                bigint      NOT NULL DEFAULT 0,
    arrears             int         NOT NULL DEFAULT 0,
    -- The quality of the last settled period, the public rating.
    rating_bps          int         NOT NULL DEFAULT 0,
    -- What its founder paid the city, and the ledger transaction.
    registration_fee    bigint      NOT NULL,
    registration_transaction_id uuid NULL,
    content_version     int         NOT NULL,
    founded_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    closed_at           timestamptz NULL,
    close_reason        text        NULL,

    CONSTRAINT companies_code_key UNIQUE (code),
    CONSTRAINT companies_status_check CHECK (status IN ('active', 'dissolved')),
    CONSTRAINT companies_closed_check CHECK ((status = 'dissolved') = (closed_at IS NOT NULL)),
    CONSTRAINT companies_close_reason_check CHECK (
        (closed_at IS NULL) = (close_reason IS NULL)
        AND (close_reason IS NULL OR close_reason IN ('closed', 'insolvent'))),
    CONSTRAINT companies_manager_check CHECK (manager_player_id IS NULL OR manager_player_id <> owner_player_id),
    CONSTRAINT companies_price_check CHECK (price_bps > 0),
    CONSTRAINT companies_shares_check CHECK (total_shares > 0),
    CONSTRAINT companies_debt_check CHECK (debt >= 0 AND arrears >= 0),
    CONSTRAINT companies_rating_check CHECK (rating_bps BETWEEN 0 AND 10000),
    CONSTRAINT companies_fee_check CHECK (registration_fee >= 0)
);

-- One active company of a name per city: the second of two racing founders
-- is refused here.
CREATE UNIQUE INDEX companies_city_name_idx ON companies (city_id, name_key) WHERE status = 'active';
CREATE INDEX companies_city_idx ON companies (city_id, status);
CREATE INDEX companies_owner_idx ON companies (owner_player_id) WHERE status = 'active';
CREATE INDEX companies_manager_idx ON companies (manager_player_id) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- company_shareholders — who holds a company's shares. At founding the
-- founder holds all of them. Kept for the stock market (ADR 0006); nothing
-- trades a share yet.
-- ---------------------------------------------------------------------------
CREATE TABLE company_shareholders (
    company_id  uuid        NOT NULL REFERENCES companies (id),
    player_id   uuid        NOT NULL REFERENCES players (id),
    shares      bigint      NOT NULL,
    acquired_at timestamptz NOT NULL,

    CONSTRAINT company_shareholders_pkey PRIMARY KEY (company_id, player_id),
    CONSTRAINT company_shareholders_shares_check CHECK (shares > 0)
);

CREATE INDEX company_shareholders_player_idx ON company_shareholders (player_id);

-- ---------------------------------------------------------------------------
-- company_openings — jobs a company offers: one career, at a wage per shift
-- (never under the city's minimum wage when posted), for some positions.
-- ---------------------------------------------------------------------------
CREATE TABLE company_openings (
    id          uuid        PRIMARY KEY,
    -- A short public number for buttons.
    no          bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id  uuid        NOT NULL REFERENCES companies (id),
    career_code text        NOT NULL,
    wage        bigint      NOT NULL,
    positions   int         NOT NULL,
    status      text        NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,

    CONSTRAINT company_openings_status_check CHECK (status IN ('open', 'closed')),
    CONSTRAINT company_openings_wage_check CHECK (wage >= 0),
    CONSTRAINT company_openings_positions_check CHECK (positions >= 1)
);

CREATE INDEX company_openings_company_idx ON company_openings (company_id, status);

-- ---------------------------------------------------------------------------
-- company_applications — a player's application to an opening.
-- ---------------------------------------------------------------------------
CREATE TABLE company_applications (
    id          uuid        PRIMARY KEY,
    no          bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    opening_id  uuid        NOT NULL REFERENCES company_openings (id),
    company_id  uuid        NOT NULL REFERENCES companies (id),
    player_id   uuid        NOT NULL REFERENCES players (id),
    status      text        NOT NULL,
    applied_at  timestamptz NOT NULL,
    decided_at  timestamptz NULL,
    decided_by  uuid        NULL REFERENCES players (id),

    CONSTRAINT company_applications_status_check
        CHECK (status IN ('pending', 'accepted', 'rejected', 'withdrawn')),
    CONSTRAINT company_applications_decided_check CHECK ((status = 'pending') = (decided_at IS NULL))
);

-- One pending application per player and opening: a double press applies once.
CREATE UNIQUE INDEX company_applications_one_pending_idx
    ON company_applications (opening_id, player_id) WHERE status = 'pending';
CREATE INDEX company_applications_company_idx ON company_applications (company_id, status);

-- ---------------------------------------------------------------------------
-- Work at a company. A job at a company names it (and the opening it was
-- taken from); a shift worked for it names it and the wage it reserved; the
-- payroll row names it, which is what a settlement counts the period's
-- shifts and wages by.
-- ---------------------------------------------------------------------------
ALTER TABLE employments
    ADD COLUMN company_id uuid NULL REFERENCES companies (id),
    ADD COLUMN opening_id uuid NULL REFERENCES company_openings (id);
CREATE INDEX employments_company_idx ON employments (company_id) WHERE ended_at IS NULL;

ALTER TABLE shift_sessions
    ADD COLUMN company_id    uuid   NULL REFERENCES companies (id),
    ADD COLUMN wage_reserved bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT shift_sessions_wage_reserved_check CHECK (wage_reserved >= 0);
CREATE INDEX shift_sessions_company_working_idx ON shift_sessions (company_id) WHERE status = 'working';

ALTER TABLE work_shifts
    ADD COLUMN company_id uuid NULL REFERENCES companies (id);
CREATE INDEX work_shifts_company_idx ON work_shifts (company_id, worked_at) WHERE company_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- company_markets — each city's settlement clock: the period being run, when
-- it began, and the scheduled action (game_actions action_type
-- 'company_period') that settles it. action_id is NULL while the city has no
-- active company: the first company founded starts the clock again.
-- ---------------------------------------------------------------------------
CREATE TABLE company_markets (
    city_id           uuid        PRIMARY KEY REFERENCES cities (id),
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL REFERENCES game_actions (id),
    updated_at        timestamptz NOT NULL,

    CONSTRAINT company_markets_period_check CHECK (period_no >= 1),
    CONSTRAINT company_markets_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- company_market_periods — one settled period of one city. Append-only; its
-- primary key is what makes a settlement happen once.
-- ---------------------------------------------------------------------------
CREATE TABLE company_market_periods (
    city_id     uuid        NOT NULL REFERENCES cities (id),
    period_no   bigint      NOT NULL,
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz NOT NULL,
    population  bigint      NOT NULL,
    -- What the population could spend, what the companies asked at their
    -- prices, and what they were paid (never above budget).
    budget      bigint      NOT NULL,
    asked       bigint      NOT NULL,
    paid        bigint      NOT NULL,
    companies   int         NOT NULL,
    settled_at  timestamptz NOT NULL,

    CONSTRAINT company_market_periods_pkey PRIMARY KEY (city_id, period_no),
    CONSTRAINT company_market_periods_paid_check CHECK (paid >= 0 AND paid <= budget),
    CONSTRAINT company_market_periods_span_check CHECK (ended_at >= started_at)
);

-- ---------------------------------------------------------------------------
-- company_periods — one settled period of one company: its books for the
-- period, the report its owner reads. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE company_periods (
    company_id      uuid        NOT NULL REFERENCES companies (id),
    period_no       bigint      NOT NULL,
    city_id         uuid        NOT NULL REFERENCES cities (id),
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz NOT NULL,
    presence_bps    int         NOT NULL,
    price_bps       int         NOT NULL,
    quality_bps     int         NOT NULL,
    shifts          int         NOT NULL,
    wanted_units    bigint      NOT NULL,
    capacity_units  bigint      NOT NULL,
    sold_units      bigint      NOT NULL,
    revenue         bigint      NOT NULL,
    sales_tax       bigint      NOT NULL,
    wages           bigint      NOT NULL,
    upkeep_due      bigint      NOT NULL,
    upkeep_paid     bigint      NOT NULL,
    debt            bigint      NOT NULL,
    balance_after   bigint      NOT NULL,
    insolvent       boolean     NOT NULL,
    settled_at      timestamptz NOT NULL,

    CONSTRAINT company_periods_pkey PRIMARY KEY (company_id, period_no),
    CONSTRAINT company_periods_amounts_check CHECK (revenue >= 0 AND sales_tax >= 0 AND sales_tax <= revenue
        AND wages >= 0 AND upkeep_paid >= 0 AND upkeep_paid <= upkeep_due AND debt >= 0 AND balance_after >= 0),
    CONSTRAINT company_periods_units_check CHECK (sold_units >= 0 AND sold_units <= capacity_units AND sold_units <= wanted_units)
);

CREATE INDEX company_periods_city_idx ON company_periods (city_id, period_no);

CREATE TRIGGER company_market_periods_append_only
    BEFORE UPDATE OR DELETE ON company_market_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER company_market_periods_no_truncate
    BEFORE TRUNCATE ON company_market_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER company_periods_append_only
    BEFORE UPDATE OR DELETE ON company_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER company_periods_no_truncate
    BEFORE TRUNCATE ON company_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
