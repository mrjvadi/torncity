-- Reverses 0009_transport_modes.up.sql, in reverse dependency order.
--
-- This drops every transport mode of every content version and forgets which
-- mode each journey used and which ledger transaction paid it. The ledger
-- entries themselves are untouched: they are append-only and still name the
-- journey through reference_id.

BEGIN;

DROP INDEX IF EXISTS travels_demand_idx;

ALTER TABLE travels
    DROP COLUMN IF EXISTS content_version,
    DROP COLUMN IF EXISTS ledger_transaction_id,
    DROP COLUMN IF EXISTS mode;

ALTER TABLE city_routes
    DROP COLUMN IF EXISTS modes;

ALTER TABLE cities
    DROP COLUMN IF EXISTS facilities;

DROP TABLE IF EXISTS transport_modes;
DROP TABLE IF EXISTS transport_facilities;

COMMIT;
