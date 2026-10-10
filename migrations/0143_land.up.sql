-- 0143_land: trees and rocks as land (ADR 0065, plan B1). What stands on a lot is a pure function of the world's seed
-- (domain/land); only what has been done to a lot is stored, one row for a lot ever touched.
--
--   settlement_land      cut trees, broken rocks (and the shifts spent on the rock in hand), the clearing orders, the woodlot
--                        mark, the instant natural regrowth counts from
--   settlement_saplings  a tree planted by a forester; it is a tree from ready_at on
--   settlement_shifts    the lot a land shift worked (land_x, land_y), what it did to it (land_kind: tree, rock or sapling)
--                        and, when the lot was a citizen's, whose it was (land_owner: the yield goes to him)
--
-- No foreign key to cities (as the other village day tables). Conventions as 0137.
BEGIN;

CREATE TABLE settlement_land (
    settlement_id uuid        NOT NULL,
    lot_x         int         NOT NULL,
    lot_y         int         NOT NULL,
    trees_cut     int         NOT NULL DEFAULT 0,
    rocks_cut     int         NOT NULL DEFAULT 0,
    rock_work     int         NOT NULL DEFAULT 0,
    regrow_anchor timestamptz NULL,
    clear_trees   boolean     NOT NULL DEFAULT false,
    clear_rocks   boolean     NOT NULL DEFAULT false,
    woodlot       boolean     NOT NULL DEFAULT false,
    ordered_by    uuid        NULL,
    ordered_at    timestamptz NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT settlement_land_pk PRIMARY KEY (settlement_id, lot_x, lot_y),
    CONSTRAINT settlement_land_check CHECK (trees_cut >= 0 AND rocks_cut >= 0 AND rock_work >= 0)
);

CREATE TABLE settlement_saplings (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    lot_x         int         NOT NULL,
    lot_y         int         NOT NULL,
    planted_at    timestamptz NOT NULL,
    ready_at      timestamptz NOT NULL,
    shift_id      uuid        NULL,

    CONSTRAINT settlement_saplings_check CHECK (ready_at > planted_at)
);
CREATE INDEX settlement_saplings_settlement_idx ON settlement_saplings (settlement_id);

ALTER TABLE settlement_shifts ADD COLUMN land_x int NULL;
ALTER TABLE settlement_shifts ADD COLUMN land_y int NULL;
ALTER TABLE settlement_shifts ADD COLUMN land_kind text NOT NULL DEFAULT '';
ALTER TABLE settlement_shifts ADD COLUMN land_owner uuid NULL;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_land_check CHECK (land_kind IN ('', 'tree', 'rock', 'sapling'));

-- The new charter permission land.clear (order the clearing of commons and treasury lots): the head office of a stored charter
-- that may draw roads gets it with the same hand (the founder's office holds every power). Offices created later get it from
-- the code.
UPDATE charter_offices
   SET grants = grants || '[{"p": "land.clear"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "road.draw"}]'::jsonb AND NOT grants @> '[{"p": "land.clear"}]'::jsonb;

COMMIT;
