-- 0053_settlement_grid_shift - where a village's lot grid sits on its world cell
-- (docs/adr/0028-world-and-settlements.md section 4).
--
-- A village's 5x5 lot grid used to be centred exactly on its cell's centre.
-- Site selection now judges the grid the village would really get (lake, sea
-- and river lots are not buildable) and may slide the grid a few whole lots
-- from the centre to find dry ground. The slide is stored here: lots east and
-- north of the cell centre (negative = west and south). Rows written before
-- this migration keep 0,0 - exactly the grid they always had.

BEGIN;

ALTER TABLE cities
    ADD COLUMN grid_shift_x smallint NOT NULL DEFAULT 0,
    ADD COLUMN grid_shift_y smallint NOT NULL DEFAULT 0;

COMMENT ON COLUMN cities.grid_shift_x IS
    'Whole lots the village grid is slid east of its world cell centre (negative: west). 0 for a village founded before 0053.';
COMMENT ON COLUMN cities.grid_shift_y IS
    'Whole lots the village grid is slid north of its world cell centre (negative: south).';

COMMIT;
