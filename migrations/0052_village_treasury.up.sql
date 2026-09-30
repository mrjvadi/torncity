-- 0052_village_treasury — where a village's treasury money comes from.
--
-- A village has no income until its abstract sales exist (ADR 0028 section
-- 8.3), yet research and construction are paid from its treasury. Two
-- faucets fill it meanwhile, each recorded here so the ledger verifier can
-- prove the ledger and these rows say the same thing:
--
--   settlement_grants     the founding grant: system_source -> the
--                         village's city_treasury, EXACTLY ONCE per
--                         settlement (the primary key), whether it was made
--                         at founding or later by `admin settlement
--                         backfill-grants` for a village founded before the
--                         grant existed. Two replicas racing to backfill the
--                         same village cannot both insert the row.
--   settlement_topups     an operator's audited top-up (admin settlement grant).
--   settlement_donations  a resident's gift from their own cash to their
--                         village's treasury (settlement.donate).
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named. The ledger transaction id
-- is not a foreign key (ledger_entries.transaction_id is shared by several
-- rows and so is not unique); the row is written first with the id its
-- ledger transaction is then posted under.
BEGIN;

CREATE TABLE settlement_grants (
    settlement_id         uuid        PRIMARY KEY REFERENCES cities (id),
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    -- 'founding' for a grant made in the founding transaction, 'backfill'
    -- for one made later by the operator.
    source                text        NOT NULL,
    granted_by            text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_grants_amount_check CHECK (amount > 0),
    CONSTRAINT settlement_grants_source_check CHECK (source IN ('founding', 'backfill')),
    CONSTRAINT settlement_grants_granted_by_check CHECK (length(btrim(granted_by)) > 0)
);

CREATE TABLE settlement_donations (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_donations_amount_check CHECK (amount > 0)
);

-- An operator's top-up of a village's treasury (`admin settlement grant`):
-- as many as the operator makes, each with the reason it was made for.
CREATE TABLE settlement_topups (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    granted_by            text        NOT NULL,
    reason                text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_topups_amount_check CHECK (amount > 0),
    CONSTRAINT settlement_topups_granted_by_check CHECK (length(btrim(granted_by)) > 0),
    CONSTRAINT settlement_topups_reason_check CHECK (length(btrim(reason)) > 0)
);

CREATE INDEX settlement_donations_settlement_idx ON settlement_donations (settlement_id, created_at DESC);

COMMIT;
