BEGIN;
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_land_check;
ALTER TABLE settlement_shifts DROP COLUMN land_owner, DROP COLUMN land_kind, DROP COLUMN land_y, DROP COLUMN land_x;
DROP TABLE settlement_saplings;
DROP TABLE settlement_land;
COMMIT;
