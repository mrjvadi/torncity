-- Reverses 0005_spawn_and_residence.up.sql.
-- The index goes first because it is defined on a column this file drops;
-- dropping the column would take the index with it, but naming it keeps the
-- reversal explicit rather than relying on that side effect.
--
-- Rolling this back does not move any player. city_id is untouched, so
-- everybody stays where they are; only the record of where they live, and the
-- weights that decide where newcomers start, are lost.

BEGIN;

DROP INDEX IF EXISTS players_residence_city_id_idx;

ALTER TABLE players
    DROP COLUMN IF EXISTS residence_city_id;

ALTER TABLE cities
    DROP CONSTRAINT IF EXISTS cities_spawn_weight_check,
    DROP COLUMN IF EXISTS spawn_weight;

COMMIT;
