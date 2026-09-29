-- 0049_village_client — what a game client needs to draw a settlement, and
-- the cancel a leader may press on a building that is still going up.
--
-- Authority: docs/adr/0028-world-and-settlements.md section 6 (the placement
-- grid, permanence, cancelling) and section 9.4 (the layout view).
--
--  * rotated: the building's footprint was turned a quarter turn when the
--    leader placed it. Until now the turn only decided which lots the
--    footprint covered while it was validated and was not kept, so nothing
--    could draw the building the way it was placed.
--  * finish_at: when construction ends, on the real clock, written when the
--    placement is accepted. A client shows a countdown from it. NULL for a
--    row written before this migration and for the founding kit.
--  * damage_bps: 0..10000. No rule damages a building yet; the column is
--    here so the layout view already carries the field clients draw.
--  * status 'cancelled' + cancelled_at: a building still under construction
--    was cancelled; its spend is forfeited (ADR 0028 section 6.2) and its
--    lot is free again.
--  * The one-building-per-lot backstop becomes a partial unique index, so a
--    demolished or cancelled row (kept forever, "a status, not a deletion")
--    no longer holds its lot. The index keeps the constraint's old name,
--    which the writer matches a violation on.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

ALTER TABLE settlement_buildings ADD COLUMN rotated boolean NOT NULL DEFAULT false;
ALTER TABLE settlement_buildings ADD COLUMN finish_at timestamptz NULL;
ALTER TABLE settlement_buildings ADD COLUMN damage_bps int NOT NULL DEFAULT 0;
ALTER TABLE settlement_buildings ADD COLUMN cancelled_at timestamptz NULL;

ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_damage_check
    CHECK (damage_bps BETWEEN 0 AND 10000);

ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_status_check;
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_status_check
    CHECK (status IN ('queued', 'building', 'complete', 'demolished', 'cancelled'));
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_cancelled_shape_check
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL));

ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_lot_unique;
CREATE UNIQUE INDEX settlement_buildings_lot_unique ON settlement_buildings (settlement_id, lot_x, lot_y)
    WHERE status NOT IN ('demolished', 'cancelled');

COMMENT ON COLUMN settlement_buildings.rotated IS
    'The footprint was turned a quarter turn (width and height swapped) when the leader placed the building. Permanent, like the lot.';
COMMENT ON COLUMN settlement_buildings.finish_at IS
    'When construction ends on the real clock; NULL for the founding kit and for rows written before 0049.';

COMMIT;
