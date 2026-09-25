-- 0028_life_and_legacy, reversed. The lives, their histories, the nights
-- slept, the kept photos and the leaderboards go; energy regenerates at the
-- one rate again. Money a night's lodging moved stays where the ledger put it
-- (lodging_fee stays in the closed set's history).

BEGIN;

DROP TABLE leaderboard_lines;
DROP TABLE leaderboard_periods;
DROP TABLE leaderboard_clock;
DROP TABLE player_photos;
DROP TABLE life_sleeps;
DROP TABLE life_events;
DROP TABLE life_history;
DROP TABLE player_life;

ALTER TABLE player_stats DROP CONSTRAINT player_stats_regen_check;
ALTER TABLE player_stats DROP COLUMN regen_bps;

COMMIT;
