-- 0049_village_client, reversed. Rows that no longer hold a lot and would
-- collide with the old plain unique constraint are dropped first; a
-- cancelled building is folded into demolished so the old checks hold.

BEGIN;

DROP INDEX settlement_buildings_lot_unique;
ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_cancelled_shape_check;
ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_status_check;
DELETE FROM settlement_buildings d WHERE d.status IN ('demolished', 'cancelled') AND EXISTS (
    SELECT 1 FROM settlement_buildings o
     WHERE o.settlement_id = d.settlement_id AND o.lot_x = d.lot_x AND o.lot_y = d.lot_y AND o.id <> d.id);
UPDATE settlement_buildings SET status = 'demolished', demolished_at = cancelled_at, completed_at = cancelled_at
 WHERE status = 'cancelled';
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_lot_unique UNIQUE (settlement_id, lot_x, lot_y);

ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_status_check
    CHECK (status IN ('queued', 'building', 'complete', 'demolished'));
ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_damage_check;

ALTER TABLE settlement_buildings DROP COLUMN cancelled_at;
ALTER TABLE settlement_buildings DROP COLUMN damage_bps;
ALTER TABLE settlement_buildings DROP COLUMN finish_at;
ALTER TABLE settlement_buildings DROP COLUMN rotated;

COMMIT;
