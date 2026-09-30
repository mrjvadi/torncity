BEGIN;

ALTER TABLE cities
    DROP COLUMN grid_shift_x,
    DROP COLUMN grid_shift_y;

COMMIT;
