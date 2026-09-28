-- 0042_world_and_settlements, reversed.

BEGIN;

ALTER TABLE cities
    DROP CONSTRAINT cities_founded_shape_check,
    DROP CONSTRAINT cities_tier_check,
    DROP CONSTRAINT cities_origin_check;

DROP INDEX cities_founded_by_group_unique_idx;
DROP INDEX cities_world_cell_unique_idx;

ALTER TABLE cities
    DROP COLUMN protected_until,
    DROP COLUMN founded_at,
    DROP COLUMN founded_by_group_id,
    DROP COLUMN world_cell_id,
    DROP COLUMN world_id,
    DROP COLUMN tier,
    DROP COLUMN origin;

DROP TABLE worlds;

COMMIT;
