-- 0031_specialist_recruitment — companies recruit NPC specialists. Rules:
-- internal/domain/recruit. Content: configs/content/recruitment.yml. Tuning:
-- configs/config.yml company.recruit_*. Decision:
-- docs/adr/0027-specialist-recruitment.md. Money:
-- docs/adr/0009-economic-control.md (recruitment_ad, specialist_signing,
-- specialist_relocation, specialist_salary, specialist_equity).
--
-- Each city has a finite pool of specialists of each skill and level
-- (specialist_pools), refilled on the game clock. A company runs a campaign
-- (recruit_campaigns) — a job advertisement in the cities it chooses, with a
-- package — and pays each city's treasury its advertising fee
-- (recruit_ad_fees). At each check the specialists it reaches weigh the offer;
-- those who apply are candidates (recruit_candidates). A hired candidate is a
-- specialist of the company (npc_staff), paid every company period
-- (npc_staff_payments).
--
-- EXACTLY ONCE. A campaign's check is one game_actions row ('recruit_check');
-- the campaign row is locked and names the action and the check it expects,
-- and (campaign, check, seq) is the candidates' key, so a repeated delivery
-- adds nobody. A candidate is hired from 'pending' only, under the company's
-- lock, and at most one specialist comes of one candidate. A specialist is
-- paid at most once per period of their company's city (primary key).
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- specialist_pools — how many specialists of a skill and level a city has
-- left to hire, as last counted. The capacity is content; a row is created
-- full the first time the pool is read.
-- ---------------------------------------------------------------------------
CREATE TABLE specialist_pools (
    city_id     uuid        NOT NULL REFERENCES cities (id),
    skill       text        NOT NULL,
    level       int         NOT NULL,
    available   bigint      NOT NULL,
    refilled_at timestamptz NOT NULL,

    CONSTRAINT specialist_pools_pkey PRIMARY KEY (city_id, skill, level),
    CONSTRAINT specialist_pools_numbers_check CHECK (level BETWEEN 1 AND 100 AND available >= 0)
);

-- ---------------------------------------------------------------------------
-- recruit_campaigns — a company's recruitment campaign: a draft being built,
-- then running on its checks until it has hired its positions or run them
-- all out. cities are content codes.
-- ---------------------------------------------------------------------------
CREATE TABLE recruit_campaigns (
    id            uuid        PRIMARY KEY,
    no            bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id    uuid        NOT NULL REFERENCES companies (id),
    status        text        NOT NULL,
    skill         text        NOT NULL,
    min_level     int         NOT NULL,
    cities        text[]      NOT NULL,
    positions     int         NOT NULL,
    hired         int         NOT NULL,
    salary        bigint      NOT NULL,
    housing       bigint      NOT NULL,
    signing       bigint      NOT NULL,
    relocation    bigint      NOT NULL,
    term_periods  int         NOT NULL,
    shares        bigint      NOT NULL,
    auto_accept   boolean     NOT NULL,
    ad_fee        bigint      NOT NULL,
    checks_total  int         NOT NULL,
    checks_done   int         NOT NULL,
    action_id     uuid        NULL REFERENCES game_actions (id),
    next_check_at timestamptz NULL,
    created_by    uuid        NOT NULL REFERENCES players (id),
    created_at    timestamptz NOT NULL,
    posted_at     timestamptz NULL,
    ended_at      timestamptz NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT recruit_campaigns_status_check
        CHECK (status IN ('draft', 'running', 'filled', 'ended', 'cancelled')),
    CONSTRAINT recruit_campaigns_numbers_check CHECK (
        min_level BETWEEN 1 AND 100 AND positions >= 1 AND hired BETWEEN 0 AND positions
        AND salary >= 0 AND housing >= 0 AND signing >= 0 AND relocation >= 0 AND shares >= 0
        AND term_periods >= 1 AND ad_fee >= 0 AND checks_done BETWEEN 0 AND checks_total),
    CONSTRAINT recruit_campaigns_posted_check CHECK ((status = 'draft') = (posted_at IS NULL)),
    CONSTRAINT recruit_campaigns_ended_check CHECK ((status IN ('draft', 'running')) = (ended_at IS NULL))
);

CREATE UNIQUE INDEX recruit_campaigns_one_draft_idx ON recruit_campaigns (company_id) WHERE status = 'draft';
CREATE INDEX recruit_campaigns_company_idx ON recruit_campaigns (company_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- recruit_ad_fees — what a campaign paid each city it advertised in, to the
-- city's treasury.
-- ---------------------------------------------------------------------------
CREATE TABLE recruit_ad_fees (
    campaign_id           uuid        NOT NULL REFERENCES recruit_campaigns (id),
    city_id               uuid        NOT NULL REFERENCES cities (id),
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    paid_at               timestamptz NOT NULL,

    CONSTRAINT recruit_ad_fees_pkey PRIMARY KEY (campaign_id, city_id),
    CONSTRAINT recruit_ad_fees_amount_check CHECK (amount >= 0)
);

-- ---------------------------------------------------------------------------
-- recruit_candidates — a specialist who applied to a campaign at one of its
-- checks, with what they expect and how they weighed the offer.
-- ---------------------------------------------------------------------------
CREATE TABLE recruit_candidates (
    id           uuid        PRIMARY KEY,
    no           bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    campaign_id  uuid        NOT NULL REFERENCES recruit_campaigns (id),
    company_id   uuid        NOT NULL REFERENCES companies (id),
    home_city_id uuid        NOT NULL REFERENCES cities (id),
    skill        text        NOT NULL,
    level        int         NOT NULL,
    preference   text        NOT NULL,
    name_seed    int         NOT NULL,
    expected     bigint      NOT NULL,
    move_cost    bigint      NOT NULL,
    value        bigint      NOT NULL,
    chance_bps   int         NOT NULL,
    check_no     int         NOT NULL,
    seq          int         NOT NULL,
    status       text        NOT NULL,
    applied_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    decided_at   timestamptz NULL,
    decided_by   uuid        NULL REFERENCES players (id),

    CONSTRAINT recruit_candidates_slot_key UNIQUE (campaign_id, check_no, seq),
    CONSTRAINT recruit_candidates_status_check
        CHECK (status IN ('pending', 'hired', 'rejected', 'expired', 'withdrawn')),
    CONSTRAINT recruit_candidates_decided_check CHECK ((status = 'pending') = (decided_at IS NULL)),
    CONSTRAINT recruit_candidates_numbers_check CHECK (
        level BETWEEN 1 AND 100 AND expected >= 0 AND move_cost >= 0 AND value >= 0
        AND chance_bps BETWEEN 0 AND 10000 AND name_seed >= 0)
);

CREATE INDEX recruit_candidates_campaign_idx ON recruit_candidates (campaign_id, no);
CREATE INDEX recruit_candidates_pending_idx ON recruit_candidates (home_city_id, skill, level) WHERE status = 'pending';

-- ---------------------------------------------------------------------------
-- npc_staff — a specialist working for a company: their skill, their
-- contract and where it stands, and what was paid at hire.
-- ---------------------------------------------------------------------------
CREATE TABLE npc_staff (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id      uuid        NOT NULL REFERENCES companies (id),
    candidate_id    uuid        NOT NULL REFERENCES recruit_candidates (id),
    campaign_id     uuid        NOT NULL REFERENCES recruit_campaigns (id),
    home_city_id    uuid        NOT NULL REFERENCES cities (id),
    skill           text        NOT NULL,
    level           int         NOT NULL,
    preference      text        NOT NULL,
    name_seed       int         NOT NULL,
    salary          bigint      NOT NULL,
    housing         bigint      NOT NULL,
    accepted_bps    bigint      NOT NULL,
    term_periods    int         NOT NULL,
    served          int         NOT NULL,
    expiring        boolean     NOT NULL,
    unpaid_run      int         NOT NULL,
    underpaid_run   int         NOT NULL,
    shares          bigint      NOT NULL,
    signing_paid    bigint      NOT NULL,
    relocation_paid bigint      NOT NULL,
    equity_paid     bigint      NOT NULL,
    status          text        NOT NULL,
    leave_reason    text        NULL,
    hired_at        timestamptz NOT NULL,
    left_at         timestamptz NULL,
    updated_at      timestamptz NOT NULL,

    CONSTRAINT npc_staff_candidate_key UNIQUE (candidate_id),
    CONSTRAINT npc_staff_status_check CHECK (status IN ('active', 'left')),
    CONSTRAINT npc_staff_left_check CHECK ((status = 'left') = (left_at IS NOT NULL AND leave_reason IS NOT NULL)),
    CONSTRAINT npc_staff_reason_check CHECK (leave_reason IS NULL OR leave_reason IN
        ('unpaid', 'poached', 'contract_end', 'dismissed', 'company_closed')),
    CONSTRAINT npc_staff_numbers_check CHECK (
        level BETWEEN 1 AND 100 AND salary >= 0 AND housing >= 0 AND accepted_bps >= 0 AND term_periods >= 1
        AND served >= 0 AND unpaid_run >= 0 AND underpaid_run >= 0 AND shares >= 0
        AND signing_paid >= 0 AND relocation_paid >= 0 AND equity_paid >= 0)
);

CREATE INDEX npc_staff_active_idx ON npc_staff (company_id, no) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- npc_staff_payments — a specialist's pay for one period of their company's
-- city: salary and housing, one ledger transaction. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE npc_staff_payments (
    staff_id              uuid        NOT NULL REFERENCES npc_staff (id),
    period_no             bigint      NOT NULL,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    paid_at               timestamptz NOT NULL,

    CONSTRAINT npc_staff_payments_pkey PRIMARY KEY (staff_id, period_no),
    CONSTRAINT npc_staff_payments_amount_check CHECK (amount > 0)
);

COMMIT;
