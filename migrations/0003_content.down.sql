-- Reverses 0003_content.up.sql.
-- Objects are dropped in reverse dependency order, so no DROP relies on
-- CASCADE:
--   audit_logs          -> (free-standing)
--   skill_definitions   -> content_versions
--   city_routes         -> cities, content_versions
--   cities.content_version_id -> content_versions  (must go before the table)
--   content_versions    -> (free-standing once nothing references it)
--
-- Indexes and CHECK constraints defined with the tables above disappear with
-- them; only the column added to the pre-existing cities table has to be
-- dropped by hand, because cities survives this migration.
--
-- Rolling this back DELETES EVERY LOADED CONTENT VERSION. The authoring files
-- in configs/content/ are unaffected and a single `admin content load` rebuilds
-- the current version from them, but the history of past loads and the audit
-- rows that explain them do not come back. That is the price of reversibility
-- here, and it is stated rather than hidden.

BEGIN;

DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS skill_definitions;
DROP TABLE IF EXISTS city_routes;

-- Release cities.content_version_id before content_versions is dropped,
-- returning the table to exactly the shape 0002_phase1 left it in.
DROP INDEX IF EXISTS cities_content_version_id_idx;

ALTER TABLE cities
    DROP COLUMN IF EXISTS content_version_id;

DROP TABLE IF EXISTS content_versions;

COMMIT;
