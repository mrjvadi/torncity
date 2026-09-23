-- Reverses 0008_governance.up.sql, in reverse dependency order.
--
-- This destroys every office holder and the whole public history of policy
-- changes. It exists for a development database; on any environment with real
-- office holders, roll forward with a new migration instead.
--
-- DROP TABLE takes the triggers with it: an append-only trigger refuses row
-- changes, not the table's removal.

BEGIN;

DROP TABLE IF EXISTS policy_changes;
DROP TABLE IF EXISTS policy_values;
DROP TABLE IF EXISTS offices;
DROP TABLE IF EXISTS lever_definitions;
DROP TABLE IF EXISTS office_definitions;

DROP FUNCTION IF EXISTS policy_value_must_be_public();
DROP FUNCTION IF EXISTS policy_value_must_respect_lever();
DROP FUNCTION IF EXISTS refuse_policy_history_change();

ALTER TABLE cities
    DROP CONSTRAINT IF EXISTS cities_jurisdiction_id_key,
    DROP COLUMN IF EXISTS jurisdiction_id;

DROP TABLE IF EXISTS jurisdictions;
DROP TABLE IF EXISTS jurisdiction_levels;

COMMIT;
