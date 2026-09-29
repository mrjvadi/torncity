-- 0047_currencies — the currency registry, the renamed neutral currency, and
-- the account key that lets an owner or a system account exist in more than
-- one currency (docs/adr/0029-currencies-and-premium.md section 13, phase C1;
-- docs/adr/0032-support-merge.md section 4).
--
-- 1. currencies: one row per currency code. SUP is the neutral money of the
--    world (the old IRR, renamed one to one by the owner, ADR 0029 decision
--    1); NIL is the premium currency and has no account yet.
-- 2. Every IRR account and ledger entry is relabelled SUP. Only the LABEL
--    changes, never an amount: the ledger stays append-only in every other
--    respect. The relabel is the one deliberate act the append-only trigger's
--    own comment allows (migration 0006): the trigger is disabled and
--    re-enabled inside this transaction, so no other session ever sees the
--    ledger unguarded. The composite (account_id, currency) foreign key is
--    dropped around the two UPDATEs because the pair moves together.
-- 3. jurisdictions.currency_code: many-to-one, so both legacy countries share
--    one row (ADR 0029 section 2.1) while every future country gets its own.
-- 4. accounts.shard_id and the wider key: system_source / system_sink /
--    foreign_holding may exist once per currency and shard; every other kind
--    keeps exactly one row per owner, now enforced by a partial unique index
--    instead of by convention.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- CHECK constraints are named.

BEGIN;

CREATE TABLE currencies (
    code                       text        PRIMARY KEY,
    name                       text        NOT NULL,
    symbol                     text        NOT NULL,
    is_premium                 boolean     NOT NULL DEFAULT false,
    issued_by_jurisdiction_id  uuid        NULL REFERENCES jurisdictions (id),
    created_at                 timestamptz NOT NULL,

    CONSTRAINT currencies_code_check CHECK (code ~ '^[A-Z]{2,6}$')
);

COMMENT ON TABLE currencies IS
    'Every currency the ledger can hold. SUP is the neutral currency (renamed from IRR); NIL is the premium currency. name is a fallback, the display name lives in the locales.';
COMMENT ON COLUMN currencies.issued_by_jurisdiction_id IS
    'The country that issues the currency; NULL for the neutral and premium currencies.';

INSERT INTO currencies (code, name, symbol, is_premium, created_at) VALUES
    ('SUP', 'Sup',  'SUP', false, now()),
    ('NIL', 'Nil',  'NIL', true,  now());

-- Pre-flight: the ledger must be sound before its labels are touched, and the
-- only currency it may hold is the one being renamed (or SUP, on a rerun).
DO $$
DECLARE
    bad record;
BEGIN
    SELECT currency, SUM(amount) AS total INTO bad
      FROM ledger_entries GROUP BY currency HAVING SUM(amount) <> 0 LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION '0047 pre-flight: ledger currency % does not sum to zero (%)', bad.currency, bad.total;
    END IF;

    SELECT a.id, a.balance, COALESCE(s.total, 0) AS total INTO bad
      FROM accounts a
      LEFT JOIN (SELECT account_id, SUM(amount) AS total FROM ledger_entries GROUP BY account_id) s
             ON s.account_id = a.id
     WHERE a.balance <> COALESCE(s.total, 0) LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION '0047 pre-flight: account % balance % differs from its ledger sum %', bad.id, bad.balance, bad.total;
    END IF;

    SELECT currency INTO bad FROM accounts WHERE currency NOT IN ('IRR', 'SUP') LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION '0047 pre-flight: account currency % is neither IRR nor SUP', bad.currency;
    END IF;
END $$;

-- The relabel. Totals are captured first so the post-check can prove that
-- nothing but the label moved.
CREATE TEMP TABLE currency_relabel_totals ON COMMIT DROP AS
    SELECT (SELECT COALESCE(SUM(balance), 0) FROM accounts)                       AS balances,
           (SELECT COALESCE(SUM(ABS(amount)), 0) FROM ledger_entries)             AS volume,
           (SELECT COUNT(*) FROM ledger_entries)                                  AS entries,
           (SELECT COUNT(*) FROM accounts)                                        AS accounts;

ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_account_currency_fkey;

UPDATE accounts       SET currency = 'SUP' WHERE currency = 'IRR';
UPDATE ledger_entries SET currency = 'SUP' WHERE currency = 'IRR';

ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_account_currency_fkey
    FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency);
ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only;

DO $$
DECLARE
    t record;
    b record;
BEGIN
    SELECT * INTO t FROM currency_relabel_totals;
    SELECT (SELECT COALESCE(SUM(balance), 0) FROM accounts)           AS balances,
           (SELECT COALESCE(SUM(ABS(amount)), 0) FROM ledger_entries) AS volume,
           (SELECT COUNT(*) FROM ledger_entries)                      AS entries,
           (SELECT COUNT(*) FROM accounts)                            AS accounts INTO b;
    IF t.balances <> b.balances OR t.volume <> b.volume OR t.entries <> b.entries OR t.accounts <> b.accounts THEN
        RAISE EXCEPTION '0047 post-check: the relabel changed an amount or a row count';
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_entries WHERE currency = 'IRR')
       OR EXISTS (SELECT 1 FROM accounts WHERE currency = 'IRR') THEN
        RAISE EXCEPTION '0047 post-check: an IRR row survived the relabel';
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_entries GROUP BY currency HAVING SUM(amount) <> 0) THEN
        RAISE EXCEPTION '0047 post-check: a currency no longer sums to zero';
    END IF;
END $$;

ALTER TABLE accounts ALTER COLUMN currency SET DEFAULT 'SUP';
ALTER TABLE accounts
    ADD CONSTRAINT accounts_currency_fkey FOREIGN KEY (currency) REFERENCES currencies (code);

-- Countries and their money (many countries, one currency is allowed).
ALTER TABLE jurisdictions ADD COLUMN currency_code text NULL REFERENCES currencies (code);
COMMENT ON COLUMN jurisdictions.currency_code IS
    'The currency of a country jurisdiction; many countries may share one (the two content countries share SUP). NULL for every kind below country.';
UPDATE jurisdictions SET currency_code = 'SUP' WHERE kind = 'country';
ALTER TABLE jurisdictions
    ADD CONSTRAINT jurisdictions_currency_kind_check
    CHECK (currency_code IS NULL OR kind = 'country');

-- The account key. shard_id is part of the key of the few many-writer rows
-- (ADR 0029 section 5.3); it is 0 for everything that is not sharded.
ALTER TABLE accounts ADD COLUMN shard_id smallint NOT NULL DEFAULT 0;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_shard_check CHECK (shard_id >= 0);
COMMENT ON COLUMN accounts.shard_id IS
    'Which shard of a many-writer system row this is. 0 for every owner-scoped account; only system_source / system_sink may use more than one.';

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_owner_currency_key;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_kind_owner_currency_shard_key
    UNIQUE NULLS NOT DISTINCT (kind, owner_id, currency, shard_id);

-- Kinds that are NOT multi-currency keep one row per owner, enforced.
CREATE UNIQUE INDEX accounts_one_row_per_owner_idx
    ON accounts (kind, owner_id)
 WHERE kind NOT IN ('system_source', 'system_sink', 'foreign_holding');

COMMIT;
