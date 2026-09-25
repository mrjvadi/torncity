-- 0026_tariffs_and_achievements, reversed. The tariff records, achievements,
-- their progress and the consumer's inbox go; the money they moved stays
-- where the ledger put it (border_tariff, achievement_reward and fuel stay in
-- the closed set's history), and so do the reward grants of achievements.

BEGIN;

COMMENT ON COLUMN travels.vehicle_id IS
    'Vehicle used for the journey. Foreign key to vehicles is added in a later migration, once the vehicles table exists.';

DROP TABLE achievement_events;
DROP TABLE player_achievements;
DROP TABLE achievement_progress;
DROP TABLE border_tariffs;

COMMIT;
