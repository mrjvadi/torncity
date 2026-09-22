-- Reverses 0002_phase1.up.sql.
-- Objects are dropped in reverse dependency order, so no DROP relies on
-- CASCADE:
--   friendships   -> players
--   travels       -> players, cities, game_actions
--   game_actions  -> (free-standing)
--   player_skills -> players
--   player_stats  -> players
--   players.city_id FK -> cities   (must go before cities itself)
--   cities        -> (free-standing once nothing references it)
--
-- Indexes and CHECK constraints defined inside the tables above disappear with
-- them; only the constraint added to the pre-existing players table has to be
-- dropped by hand, because players survives this migration.

BEGIN;

DROP TABLE IF EXISTS friendships;
DROP TABLE IF EXISTS travels;
DROP TABLE IF EXISTS game_actions;
DROP TABLE IF EXISTS player_skills;
DROP TABLE IF EXISTS player_stats;

-- Release players.city_id before cities is dropped, returning the column to
-- the bare uuid that 0001_init created, and restore the comment 0001 left on
-- it so the schema documents itself correctly at that version again.
ALTER TABLE players
    DROP CONSTRAINT IF EXISTS players_city_id_fkey;

COMMENT ON COLUMN players.city_id IS
    'Current city. Foreign key to cities is added in a later migration, once the cities table exists.';

DROP TABLE IF EXISTS cities;

COMMIT;
