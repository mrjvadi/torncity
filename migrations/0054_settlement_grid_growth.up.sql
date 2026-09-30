-- 0054_settlement_grid_growth - how far a village's lot grid has grown.
--
-- A village owns as much land as it pays for (docs/adr/0033 section 8.4, the
-- owner's correction: land is never capped by tier). Each expansion adds one
-- column on the grid's east edge and one row on its north edge, so the side
-- is the tier's base side plus this number and every building's stored lot
-- coordinate stays where it was (lot (0,0) is the south-west corner and never
-- moves). Rows written before this migration keep 0: the grid they had.

BEGIN;

ALTER TABLE cities
    ADD COLUMN grid_growth smallint NOT NULL DEFAULT 0 CHECK (grid_growth >= 0);

COMMENT ON COLUMN cities.grid_growth IS
    'Expansions bought: the grid side is the tier base plus this. East and north edges grow; lot (0,0) never moves.';

COMMIT;
