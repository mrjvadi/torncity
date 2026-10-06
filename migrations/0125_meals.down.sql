BEGIN;

ALTER TABLE settlement_buildings DROP COLUMN IF EXISTS carry;
ALTER TABLE settlement_shifts
    DROP CONSTRAINT IF EXISTS settlement_shifts_meal_check,
    DROP COLUMN IF EXISTS output_bps,
    DROP COLUMN IF EXISTS fed,
    DROP COLUMN IF EXISTS meal_points;
DROP TABLE IF EXISTS settlement_meals;
DROP TABLE IF EXISTS settlement_kitchen;

COMMIT;
