-- Reverse of 0047_currencies: SUP goes back to IRR, the account key narrows to
-- (kind, owner_id, currency), and the registry goes away. Refuses when any
-- account holds a currency other than SUP, because a second currency cannot
-- be folded back into the single legacy one without losing what it meant.

BEGIN;

DO $$
DECLARE
    bad record;
BEGIN
    SELECT currency INTO bad FROM accounts WHERE currency <> 'SUP' LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION '0047 down: an account exists in currency %, which cannot be folded back into IRR', bad.currency;
    END IF;
    IF EXISTS (SELECT 1 FROM accounts WHERE shard_id <> 0) THEN
        RAISE EXCEPTION '0047 down: a sharded account exists; merge the shards first';
    END IF;
END $$;

DROP INDEX accounts_one_row_per_owner_idx;
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_owner_currency_shard_key;
ALTER TABLE accounts
    ADD CONSTRAINT accounts_kind_owner_currency_key
    UNIQUE NULLS NOT DISTINCT (kind, owner_id, currency);
ALTER TABLE accounts DROP CONSTRAINT accounts_shard_check;
ALTER TABLE accounts DROP COLUMN shard_id;

ALTER TABLE jurisdictions DROP CONSTRAINT jurisdictions_currency_kind_check;
ALTER TABLE jurisdictions DROP COLUMN currency_code;

ALTER TABLE accounts DROP CONSTRAINT accounts_currency_fkey;
ALTER TABLE accounts ALTER COLUMN currency SET DEFAULT 'IRR';

ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_account_currency_fkey;
UPDATE accounts       SET currency = 'IRR' WHERE currency = 'SUP';
UPDATE ledger_entries SET currency = 'IRR' WHERE currency = 'SUP';
ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_account_currency_fkey
    FOREIGN KEY (account_id, currency) REFERENCES accounts (id, currency);
ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only;

DROP TABLE currencies;

COMMIT;
