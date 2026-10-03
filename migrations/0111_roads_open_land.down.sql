-- Down: the land tables go; the non-negative lot checks come back only when no
-- row lies outside the old range (a lot west or south of the first grid, or a
-- building on one, would make the checks fail: that is the honest refusal).
BEGIN;

DROP TABLE IF EXISTS settlement_open_lots;
DROP TABLE IF EXISTS settlement_road_cells;
DROP TABLE IF EXISTS settlement_road_plans;

ALTER TABLE settlement_buildings    ADD CONSTRAINT settlement_buildings_lot_check CHECK (lot_x >= 0 AND lot_y >= 0);
ALTER TABLE settlement_lots         ADD CONSTRAINT settlement_lots_coordinates_check CHECK (lot_x >= 0 AND lot_y >= 0);
ALTER TABLE settlement_road_reserve ADD CONSTRAINT settlement_road_reserve_coordinates_check CHECK (lot_x >= 0 AND lot_y >= 0);

COMMIT;
