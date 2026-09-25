-- 0027_defence_licences — who may make arms: the defence licence of a
-- defence company and of a civilian defence contractor. Rules:
-- internal/domain/military (licence.go). Content: configs/content/military.yml
-- (defence_licence), jobs.yml (armed_forces), governance.yml
-- (country.defence_licence). Decision: docs/adr/0022-military-and-diplomacy.md,
-- section 2.14. Money: docs/adr/0009-economic-control.md (military_wage — a
-- soldier's pay from the defence fund needs no table of its own: the shift
-- is work_shifts, the payment a ledger transaction).
--
-- A company of the defence sector is founded by a serving officer of the
-- armed forces or by the owner of a licensed contractor; a civilian company
-- applies for a contractor licence and the defence minister decides it. Every
-- licence is public; the minister may revoke one with notice, and it stays in
-- force until effective_at (nothing runs on a clock for it: the rules read
-- the status against now).
--
-- EXACTLY ONCE. A company has at most one licence open — pending, active or
-- revoking — (a partial unique index); a decision and a revocation update the
-- row only from the status the rules expect, under its lock.
--
-- LIVE DATA. Every defence company already running keeps working: it is
-- given an active licence on the basis 'grandfathered' below. The kinds of
-- business of the defence sector are named here because a migration cannot
-- read the content; they are the five of configs/content/defence_industry.yml
-- at the time of writing.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- defence_licences — a company's defence licence, or its application for
-- one: its public number, its kind (a defence company's own, or a civilian
-- contractor's), what it rests on, where it stands, who applied, who decided
-- from which office, who revoked it and when the revocation takes effect.
-- ---------------------------------------------------------------------------
CREATE TABLE defence_licences (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY,
    company_id      uuid        NOT NULL REFERENCES companies (id),
    kind            text        NOT NULL,
    basis           text        NOT NULL,
    status          text        NOT NULL,
    applied_by      uuid        NULL REFERENCES players (id),
    applied_at      timestamptz NOT NULL,
    decided_by      uuid        NULL REFERENCES players (id),
    decided_office  text        NULL,
    decided_at      timestamptz NULL,
    revoked_by      uuid        NULL REFERENCES players (id),
    revoked_office  text        NULL,
    revoked_at      timestamptz NULL,
    effective_at    timestamptz NULL,
    updated_at      timestamptz NOT NULL,

    CONSTRAINT defence_licences_no_key UNIQUE (no),
    CONSTRAINT defence_licences_kind_check CHECK (kind IN ('manufacturer', 'contractor')),
    CONSTRAINT defence_licences_basis_check CHECK (basis IN ('rank', 'contractor', 'minister', 'grandfathered')),
    CONSTRAINT defence_licences_status_check CHECK (status IN ('pending', 'active', 'revoking', 'revoked', 'rejected')),
    -- A contractor's licence is the minister's; a defence company's rests on
    -- its founder or on history.
    CONSTRAINT defence_licences_basis_kind_check CHECK (
        (kind = 'contractor') = (basis = 'minister')),
    -- Only an application waits, and it waits undecided; an application
    -- that left pending was decided.
    CONSTRAINT defence_licences_pending_check CHECK (
        status <> 'pending' OR (basis = 'minister' AND decided_at IS NULL)),
    CONSTRAINT defence_licences_decided_check CHECK (
        basis <> 'minister' OR status = 'pending' OR decided_at IS NOT NULL),
    CONSTRAINT defence_licences_revoked_check CHECK (
        (status IN ('revoking', 'revoked')) = (revoked_at IS NOT NULL AND effective_at IS NOT NULL))
);

CREATE UNIQUE INDEX defence_licences_open_key ON defence_licences (company_id)
    WHERE status IN ('pending', 'active', 'revoking');
CREATE INDEX defence_licences_status_idx ON defence_licences (status, applied_at);

-- The defence companies already running keep their business.
INSERT INTO defence_licences (id, company_id, kind, basis, status, applied_at, updated_at)
SELECT gen_random_uuid(), c.id, 'manufacturer', 'grandfathered', 'active', now(), now()
  FROM companies c
 WHERE c.status = 'active'
   AND c.type_code IN ('aerospace', 'land_systems', 'missile_works', 'shipyard', 'defence_electronics');

COMMIT;
