-- Reverses 0006_ledger.up.sql, in reverse dependency order.
--
-- This destroys every account balance and the whole ledger history. It exists
-- for a development database; on any environment holding real players' money,
-- roll forward with a new migration instead.
--
-- DROP TABLE takes the triggers with it, so the append-only guard does not
-- stand in the way of the drop: a trigger refuses row changes, not the table's
-- removal.

BEGIN;

DROP TABLE IF EXISTS reward_grants;
DROP TABLE IF EXISTS ledger_entries;

DROP FUNCTION IF EXISTS ledger_transaction_must_balance();
DROP FUNCTION IF EXISTS refuse_append_only_change();

ALTER TABLE cities
    DROP CONSTRAINT IF EXISTS cities_treasury_account_id_fkey;

DROP TABLE IF EXISTS accounts;

COMMIT;
