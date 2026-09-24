-- 0015_places, reversed. Scheduled place_move rows in game_actions are left:
-- a row nobody routes is reported by the scheduler rather than lost.

BEGIN;

DROP INDEX players_city_place_idx;
DROP TABLE place_moves;
ALTER TABLE players DROP COLUMN place_since, DROP COLUMN place_code;

COMMIT;
