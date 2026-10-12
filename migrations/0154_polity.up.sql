-- Unions, countries, treaties of settlements, public tenders and sovereign loans (docs/adr/0076, plan B12).
--
--   unions, union_members     a league or a country founded by agreement of settlements; its jurisdiction, its members, what they delegated
--   union_offices, union_seats the union's charter: the same offices the settlements write (docs/adr/0044 6), kept apart because their
--                              scope is a union; union_audit is their append-only audit (rail R3)
--   union_proposals, union_invites, union_ballots, union_ballot_votes, union_ballot_signatures
--                              the proposal, each settlement's own ratification, the residents' ballots (assembly approval, recall)
--   union_motions             decisions between the members (admit, expel, dissolve, consent to an exit)
--   union_dues_periods        one row per (union, member, period)
--   tenders, tender_bids, tender_stages, tender_debarments   public works by tender, the escrow and its stages
--   sovereign_loans, sovereign_loan_periods, sovereign_loan_events   loans between polities
--   treaties.ended_party      which party ended a treaty (the cooling-off after a broken alliance)
--   accounts                  a new kind: works_escrow
BEGIN;

INSERT INTO jurisdiction_levels (code, parents, overlay, content_version_id)
VALUES ('league', '{world}', false, NULL)
ON CONFLICT (code) DO NOTHING;

ALTER TABLE treaties ADD COLUMN ended_party uuid NULL REFERENCES jurisdictions (id);

CREATE TABLE unions (
    id              uuid        PRIMARY KEY,
    jurisdiction_id uuid        NOT NULL UNIQUE REFERENCES jurisdictions (id),
    kind            text        NOT NULL CHECK (kind IN ('league', 'country')),
    name            text        NOT NULL,
    vote_mode       text        NOT NULL CHECK (vote_mode IN ('one_member', 'weighted', 'double_majority')),
    exit_rule       text        NOT NULL CHECK (exit_rule IN ('free', 'consent')),
    delegations     text[]      NOT NULL DEFAULT '{}',
    dues_fixed      bigint      NOT NULL DEFAULT 0 CHECK (dues_fixed >= 0),
    dues_share_bps  bigint      NOT NULL DEFAULT 0 CHECK (dues_share_bps >= 0),
    status          text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dissolved')),
    created_at      timestamptz NOT NULL,
    dissolved_at    timestamptz NULL,
    CONSTRAINT unions_dissolved_check CHECK ((status = 'dissolved') = (dissolved_at IS NOT NULL))
);
CREATE UNIQUE INDEX unions_name_idx ON unions (lower(name)) WHERE status = 'active';

CREATE TABLE union_members (
    union_id       uuid        NOT NULL REFERENCES unions (id),
    settlement_id  uuid        NOT NULL REFERENCES cities (id),
    union_kind     text        NOT NULL,
    status         text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'notice', 'left', 'expelled')),
    joined_at      timestamptz NOT NULL,
    notice_at      timestamptz NULL,
    left_at        timestamptz NULL,
    -- the jurisdiction the settlement sat under before it joined a country (it goes back there on leaving)
    prev_parent_id uuid        NULL REFERENCES jurisdictions (id),
    arrears        integer     NOT NULL DEFAULT 0 CHECK (arrears >= 0),
    PRIMARY KEY (union_id, settlement_id),
    CONSTRAINT union_members_left_check CHECK ((status IN ('left', 'expelled')) = (left_at IS NOT NULL))
);
-- a settlement belongs to at most one country at a time, and to many leagues
CREATE UNIQUE INDEX union_members_one_country_idx ON union_members (settlement_id) WHERE union_kind = 'country' AND status IN ('active', 'notice');
CREATE INDEX union_members_settlement_idx ON union_members (settlement_id) WHERE status IN ('active', 'notice');

CREATE TABLE union_offices (
    id                  uuid        PRIMARY KEY,
    union_id            uuid        NOT NULL REFERENCES unions (id),
    title               text        NOT NULL,
    seats               integer     NOT NULL CHECK (seats BETWEEN 1 AND 50),
    grants              jsonb       NOT NULL DEFAULT '[]',
    acquisition         text        NOT NULL CHECK (acquisition IN ('head', 'appointment', 'election')),
    appointer_office_id uuid        NULL REFERENCES union_offices (id),
    term_days           integer     NOT NULL DEFAULT 0 CHECK (term_days >= 0),
    created_by          uuid        NULL,
    created_at          timestamptz NOT NULL,
    closed_at           timestamptz NULL
);
CREATE UNIQUE INDEX union_offices_title_idx ON union_offices (union_id, lower(title)) WHERE closed_at IS NULL;
CREATE INDEX union_offices_union_idx ON union_offices (union_id);

CREATE TABLE union_seats (
    id           uuid        PRIMARY KEY,
    office_id    uuid        NOT NULL REFERENCES union_offices (id),
    holder_id    uuid        NOT NULL REFERENCES players (id),
    appointed_by uuid        NULL,
    since        timestamptz NOT NULL,
    until        timestamptz NULL,
    end_reason   text        NULL
);
CREATE UNIQUE INDEX union_seats_holder_idx ON union_seats (office_id, holder_id) WHERE until IS NULL;
CREATE INDEX union_seats_player_idx ON union_seats (holder_id) WHERE until IS NULL;

CREATE TABLE union_audit (
    id         uuid        PRIMARY KEY,
    union_id   uuid        NULL REFERENCES unions (id),
    actor_id   uuid        NULL,
    action     text        NOT NULL,
    office_id  uuid        NULL,
    detail     jsonb       NOT NULL DEFAULT '{}',
    at         timestamptz NOT NULL
);
CREATE INDEX union_audit_union_idx ON union_audit (union_id, at DESC);

-- rail R3: the audit is append-only.
CREATE FUNCTION union_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'union_audit is append-only';
END;
$$;
CREATE TRIGGER union_audit_no_change BEFORE UPDATE OR DELETE ON union_audit
    FOR EACH ROW EXECUTE FUNCTION union_audit_append_only();

CREATE TABLE union_proposals (
    id              uuid        PRIMARY KEY,
    proposer_id     uuid        NOT NULL REFERENCES cities (id),
    proposed_by     uuid        NOT NULL REFERENCES players (id),
    name            text        NOT NULL,
    kind            text        NOT NULL CHECK (kind IN ('league', 'country')),
    vote_mode       text        NOT NULL,
    exit_rule       text        NOT NULL,
    delegations     text[]      NOT NULL DEFAULT '{}',
    dues_fixed      bigint      NOT NULL DEFAULT 0,
    dues_share_bps  bigint      NOT NULL DEFAULT 0,
    offices         jsonb       NOT NULL DEFAULT '[]',
    fee             bigint      NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL,
    deadline        timestamptz NOT NULL,
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_force', 'lapsed', 'refused', 'withdrawn')),
    union_id        uuid        NULL REFERENCES unions (id),
    decided_at      timestamptz NULL
);
CREATE INDEX union_proposals_open_idx ON union_proposals (status, deadline) WHERE status = 'pending';
-- one open proposal per proposing settlement
CREATE UNIQUE INDEX union_proposals_one_open_idx ON union_proposals (proposer_id) WHERE status = 'pending';

CREATE TABLE union_invites (
    proposal_id   uuid        NOT NULL REFERENCES union_proposals (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    decision      text        NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending', 'assembly', 'yes', 'no')),
    decided_by    uuid        NULL REFERENCES players (id),
    decided_at    timestamptz NULL,
    ballot_id     uuid        NULL,
    PRIMARY KEY (proposal_id, settlement_id)
);
CREATE INDEX union_invites_settlement_idx ON union_invites (settlement_id) WHERE decision IN ('pending', 'assembly');

CREATE TABLE union_ballots (
    id            uuid        PRIMARY KEY,
    kind          text        NOT NULL CHECK (kind IN ('ratify', 'recall')),
    -- ratify: the settlement whose assembly decides and the invite it decides; recall: the union and the office
    settlement_id uuid        NULL REFERENCES cities (id),
    proposal_id   uuid        NULL REFERENCES union_proposals (id),
    union_id      uuid        NULL REFERENCES unions (id),
    office_id     uuid        NULL REFERENCES union_offices (id),
    target_id     uuid        NULL REFERENCES players (id),
    opened_by     uuid        NULL,
    opens_at      timestamptz NOT NULL,
    closes_at     timestamptz NOT NULL,
    eligible      integer     NOT NULL,
    -- a recall collects signatures first
    status        text        NOT NULL DEFAULT 'open' CHECK (status IN ('petition', 'open', 'passed', 'failed', 'void')),
    result        jsonb       NULL,
    settled_at    timestamptz NULL
);
CREATE UNIQUE INDEX union_ballots_one_ratify_idx ON union_ballots (proposal_id, settlement_id) WHERE kind = 'ratify' AND status = 'open';
CREATE UNIQUE INDEX union_ballots_one_recall_idx ON union_ballots (office_id, target_id) WHERE kind = 'recall' AND status IN ('petition', 'open');

CREATE TABLE union_ballot_votes (
    ballot_id uuid        NOT NULL REFERENCES union_ballots (id),
    voter_id  uuid        NOT NULL REFERENCES players (id),
    yes       boolean     NOT NULL,
    at        timestamptz NOT NULL,
    PRIMARY KEY (ballot_id, voter_id)
);
CREATE TABLE union_ballot_signatures (
    ballot_id uuid        NOT NULL REFERENCES union_ballots (id),
    signer_id uuid        NOT NULL REFERENCES players (id),
    at        timestamptz NOT NULL,
    PRIMARY KEY (ballot_id, signer_id)
);

CREATE TABLE union_motions (
    id           uuid        PRIMARY KEY,
    union_id     uuid        NOT NULL REFERENCES unions (id),
    kind         text        NOT NULL CHECK (kind IN ('admit', 'expel', 'dissolve', 'exit', 'charter')),
    -- admit/expel/exit: the settlement; charter: the change (jsonb in detail)
    subject_id   uuid        NULL,
    detail       jsonb       NOT NULL DEFAULT '{}',
    opened_by    uuid        NOT NULL REFERENCES players (id),
    opened_at    timestamptz NOT NULL,
    closes_at    timestamptz NOT NULL,
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'passed', 'failed', 'void')),
    result       jsonb       NULL,
    settled_at   timestamptz NULL
);
CREATE UNIQUE INDEX union_motions_one_open_idx ON union_motions (union_id, kind, COALESCE(subject_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE status = 'open';
CREATE TABLE union_motion_votes (
    motion_id     uuid        NOT NULL REFERENCES union_motions (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    yes           boolean     NOT NULL,
    cast_by       uuid        NOT NULL REFERENCES players (id),
    at            timestamptz NOT NULL,
    PRIMARY KEY (motion_id, settlement_id)
);

CREATE TABLE union_dues_periods (
    union_id      uuid        NOT NULL REFERENCES unions (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    period_no     bigint      NOT NULL CHECK (period_no >= 0),
    due           bigint      NOT NULL CHECK (due >= 0),
    paid          bigint      NOT NULL CHECK (paid >= 0 AND paid <= due),
    ledger_tx     uuid        NULL,
    at            timestamptz NOT NULL,
    PRIMARY KEY (union_id, settlement_id, period_no)
);

-- exits and dissolutions: what each member took of the purse
CREATE TABLE union_settlements (
    id            uuid        PRIMARY KEY,
    union_id      uuid        NOT NULL REFERENCES unions (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    reason        text        NOT NULL CHECK (reason IN ('exit', 'expelled', 'dissolution')),
    amount        bigint      NOT NULL CHECK (amount >= 0),
    ledger_tx     uuid        NULL,
    at            timestamptz NOT NULL,
    UNIQUE (union_id, settlement_id, reason)
);

-- ---------------------------------------------------------------------------
-- public works by tender
-- ---------------------------------------------------------------------------
CREATE TABLE tenders (
    id             uuid        PRIMARY KEY,
    payer_kind     text        NOT NULL CHECK (payer_kind IN ('settlement', 'union')),
    payer_id       uuid        NOT NULL,
    settlement_id  uuid        NOT NULL REFERENCES cities (id),
    building_id    uuid        NOT NULL,
    posted_by      uuid        NOT NULL REFERENCES players (id),
    posted_office  uuid        NULL,
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
    stages_paid    integer     NOT NULL DEFAULT 0 CHECK (stages_paid BETWEEN 0 AND 3),
    retention_held bigint      NOT NULL DEFAULT 0,
    last_stage_at  timestamptz NULL,
    closed_at      timestamptz NULL
);
CREATE UNIQUE INDEX tenders_one_per_building_idx ON tenders (building_id) WHERE status IN ('open', 'awarded');
CREATE INDEX tenders_settlement_idx ON tenders (settlement_id, opens_at DESC);

CREATE TABLE tender_bids (
    id         uuid        PRIMARY KEY,
    tender_id  uuid        NOT NULL REFERENCES tenders (id),
    company_id uuid        NOT NULL REFERENCES companies (id),
    bid_by     uuid        NOT NULL REFERENCES players (id),
    price      bigint      NOT NULL CHECK (price > 0),
    days       integer     NOT NULL CHECK (days > 0),
    record     integer     NOT NULL DEFAULT 0,
    bond       bigint      NOT NULL CHECK (bond >= 0),
    status     text        NOT NULL DEFAULT 'standing' CHECK (status IN ('standing', 'won', 'lost', 'withdrawn')),
    at         timestamptz NOT NULL,
    UNIQUE (tender_id, company_id)
);

CREATE TABLE tender_stages (
    tender_id uuid        NOT NULL REFERENCES tenders (id),
    stage     integer     NOT NULL CHECK (stage BETWEEN 1 AND 3),
    paid      bigint      NOT NULL CHECK (paid >= 0),
    held      bigint      NOT NULL CHECK (held >= 0),
    ledger_tx uuid        NULL,
    at        timestamptz NOT NULL,
    PRIMARY KEY (tender_id, stage)
);

CREATE TABLE tender_debarments (
    company_id uuid        PRIMARY KEY REFERENCES companies (id),
    until      timestamptz NOT NULL,
    reason     text        NOT NULL,
    tender_id  uuid        NULL,
    at         timestamptz NOT NULL
);

-- the company is an employer of a construction job (the contractor's crew is paid by the company)
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_employer_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_employer_check CHECK (employer_kind IN ('settlement', 'player', 'company'));
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_payer_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_payer_check CHECK (payer_kind IN ('settlement', 'player', 'company'));

-- ---------------------------------------------------------------------------
-- sovereign loans
-- ---------------------------------------------------------------------------
CREATE TABLE sovereign_loans (
    id              uuid        PRIMARY KEY,
    lender_kind     text        NOT NULL CHECK (lender_kind IN ('settlement', 'union')),
    lender_id       uuid        NOT NULL,
    borrower_kind   text        NOT NULL CHECK (borrower_kind IN ('settlement', 'union')),
    borrower_id     uuid        NOT NULL,
    principal       bigint      NOT NULL CHECK (principal > 0),
    rate_bps        bigint      NOT NULL CHECK (rate_bps >= 0),
    periods         integer     NOT NULL CHECK (periods > 0),
    periods_per_year integer    NOT NULL CHECK (periods_per_year > 0),
    paid            integer     NOT NULL DEFAULT 0 CHECK (paid >= 0),
    arrears         integer     NOT NULL DEFAULT 0 CHECK (arrears >= 0),
    fees_due        bigint      NOT NULL DEFAULT 0 CHECK (fees_due >= 0),
    status          text        NOT NULL DEFAULT 'offered' CHECK (status IN ('offered', 'active', 'repaid', 'defaulted', 'written_off', 'restructured', 'declined', 'withdrawn', 'expired')),
    stream          text        NULL,
    offered_by      uuid        NOT NULL REFERENCES players (id),
    accepted_by     uuid        NULL REFERENCES players (id),
    offered_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    accepted_at     timestamptz NULL,
    next_due_at     timestamptz NULL,
    replaced_by     uuid        NULL,
    replaces        uuid        NULL,
    closed_at       timestamptz NULL,
    CONSTRAINT sovereign_loans_parties_check CHECK (NOT (lender_kind = borrower_kind AND lender_id = borrower_id)),
    CONSTRAINT sovereign_loans_paid_check CHECK (paid <= periods)
);
CREATE INDEX sovereign_loans_lender_idx ON sovereign_loans (lender_kind, lender_id);
CREATE INDEX sovereign_loans_borrower_idx ON sovereign_loans (borrower_kind, borrower_id);
CREATE INDEX sovereign_loans_due_idx ON sovereign_loans (next_due_at) WHERE status = 'active';

CREATE TABLE sovereign_loan_periods (
    loan_id    uuid        NOT NULL REFERENCES sovereign_loans (id),
    k          integer     NOT NULL CHECK (k > 0),
    principal  bigint      NOT NULL CHECK (principal >= 0),
    interest   bigint      NOT NULL CHECK (interest >= 0),
    fee        bigint      NOT NULL DEFAULT 0 CHECK (fee >= 0),
    ledger_tx  uuid        NULL,
    at         timestamptz NOT NULL,
    PRIMARY KEY (loan_id, k)
);
-- the period rows that did not pay (a missed run), one per attempt
CREATE TABLE sovereign_loan_events (
    id        uuid        PRIMARY KEY,
    loan_id   uuid        NOT NULL REFERENCES sovereign_loans (id),
    kind      text        NOT NULL,
    detail    jsonb       NOT NULL DEFAULT '{}',
    at        timestamptz NOT NULL
);
CREATE INDEX sovereign_loan_events_loan_idx ON sovereign_loan_events (loan_id, at);
CREATE FUNCTION sovereign_loan_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'sovereign_loan_events is append-only';
END;
$$;
CREATE TRIGGER sovereign_loan_events_no_change BEFORE UPDATE OR DELETE ON sovereign_loan_events
    FOR EACH ROW EXECUTE FUNCTION sovereign_loan_events_append_only();

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet', 'works_escrow'));

-- The head office of a stored settlement charter gets the new settlement-level powers with the same hand (docs/adr/0076 9.1): it may
-- ratify unions, sign and end treaties, borrow, lend and tender public works.
UPDATE charter_offices
   SET grants = grants
       || CASE WHEN grants @> '[{"p": "union.propose"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "union.propose"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "treaty.propose"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "treaty.propose"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "union.ratify"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "union.ratify"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "treaty.sign"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "treaty.sign"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "treaty.end"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "treaty.end"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "debt.borrow"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "debt.borrow"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "debt.lend"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "debt.lend"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "debt.restructure"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "debt.restructure"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "works.tender"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "works.tender"}]'::jsonb END
       || CASE WHEN grants @> '[{"p": "works.accept"}]'::jsonb THEN '[]'::jsonb ELSE '[{"p": "works.accept"}]'::jsonb END
 WHERE acquisition = 'head' AND closed_at IS NULL;

COMMIT;
