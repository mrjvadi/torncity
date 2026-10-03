-- 0110_roads_open_land: a road opens the land it reaches (docs/adr/0044 5.5 and
-- the owner decision of 2026-10-03).
--
-- Until now a village owned a fixed grid and grew it by paying for an
-- expansion. The expansion is gone. The grid is the first block of land; the
-- head (or whoever holds road.draw) draws a road out into the territory, and
-- the lots along it open for sale. The road itself is only a PLAN until a lot
-- it serves is bought: the buyer pays for the stretch of road up to that lot
-- (ledger reason settlement_lot_road, as ADR 0043), and the stretch is laid
-- then.
--
--   settlement_road_plans   one drawn road: its class and surface, its quote at
--                           the time it was drawn, and who drew it. A cancelled
--                           plan keeps its row for the books.
--   settlement_road_cells   the lots of a plan, in order, each hanging from the
--                           one before (parent). built_at is NULL while the
--                           road is only planned; a cell is built, once, by the
--                           purchase of a lot it serves. The first cell of a
--                           plan has no parent: it touches the village's
--                           standing network (a road, or the civic hall).
--   settlement_open_lots    the frontage band of the plans: the lots that may
--                           be bought and built on, with the terrain they were
--                           sampled with (so a request never resamples the
--                           world) and the reason a lot cannot be used (water,
--                           steep ground).
--
-- Land has no edge any more: lot coordinates may be negative (west and south of
-- the first grid) and large, so the three non-negative checks are dropped.
-- Every row keeps the world tile it lies on, so another settlement's road can
-- be refused where this one already holds the ground (the claim a road carries
-- with it, ADR 0044 5.5).
--
-- Conventions as 0058/0100: instants are timestamptz in UTC, no DEFAULT now(),
-- named CHECK constraints, journals carry no foreign keys.
BEGIN;

ALTER TABLE settlement_buildings    DROP CONSTRAINT settlement_buildings_lot_check;
ALTER TABLE settlement_lots         DROP CONSTRAINT settlement_lots_coordinates_check;
ALTER TABLE settlement_road_reserve DROP CONSTRAINT settlement_road_reserve_coordinates_check;

CREATE TABLE settlement_road_plans (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    drawn_by      uuid        NOT NULL,
    class         text        NOT NULL,
    surface       text        NOT NULL,
    -- the quote the plan was drawn with
    lots          int         NOT NULL,
    crossing_lots int         NOT NULL,
    climb_m       int         NOT NULL,
    length_m      int         NOT NULL,
    -- the lot the road starts beside and the lot it was drawn to
    from_x        int         NOT NULL,
    from_y        int         NOT NULL,
    to_x          int         NOT NULL,
    to_y          int         NOT NULL,
    created_at    timestamptz NOT NULL,
    cancelled_at  timestamptz NULL,

    CONSTRAINT settlement_road_plans_counts_check
        CHECK (lots > 0 AND crossing_lots >= 0 AND crossing_lots <= lots AND climb_m >= 0 AND length_m > 0)
);

CREATE INDEX settlement_road_plans_settlement_idx ON settlement_road_plans (settlement_id) WHERE cancelled_at IS NULL;

CREATE TABLE settlement_road_cells (
    settlement_id uuid             NOT NULL,
    lot_x         int              NOT NULL,
    lot_y         int              NOT NULL,
    plan_id       uuid             NOT NULL,
    -- the cell's place in its plan, 0 first
    seq           int              NOT NULL,
    parent_x      int              NULL,
    parent_y      int              NULL,
    -- 0 dry, 1 brook (ford or culvert), 2 river (bridge)
    water         smallint         NOT NULL,
    elevation_m   double precision NOT NULL,
    tile_face     smallint         NOT NULL,
    tile_gx       int              NOT NULL,
    tile_gy       int              NOT NULL,
    -- NULL while only planned
    built_at      timestamptz      NULL,

    CONSTRAINT settlement_road_cells_pk PRIMARY KEY (settlement_id, lot_x, lot_y),
    CONSTRAINT settlement_road_cells_parent_check CHECK ((parent_x IS NULL) = (parent_y IS NULL)),
    CONSTRAINT settlement_road_cells_water_check CHECK (water BETWEEN 0 AND 2),
    CONSTRAINT settlement_road_cells_seq_check CHECK (seq >= 0)
);

CREATE INDEX settlement_road_cells_plan_idx ON settlement_road_cells (plan_id, seq);
CREATE INDEX settlement_road_cells_tile_idx ON settlement_road_cells (tile_face, tile_gx, tile_gy);

CREATE TABLE settlement_open_lots (
    settlement_id uuid             NOT NULL,
    lot_x         int              NOT NULL,
    lot_y         int              NOT NULL,
    plan_id       uuid             NOT NULL,
    -- the road cell that opened the lot, and how many lots away it is
    serves_x      int              NOT NULL,
    serves_y      int              NOT NULL,
    dist          smallint         NOT NULL,
    buildable     boolean          NOT NULL,
    -- '' when buildable; 'water' or 'steep' when not
    reason        text             NOT NULL,
    height_m      double precision NOT NULL,
    slope_m       double precision NOT NULL,
    biome         text             NOT NULL,
    water         text             NOT NULL,
    tags          text[]           NOT NULL,
    tile_face     smallint         NOT NULL,
    tile_gx       int              NOT NULL,
    tile_gy       int              NOT NULL,

    CONSTRAINT settlement_open_lots_pk PRIMARY KEY (settlement_id, lot_x, lot_y),
    CONSTRAINT settlement_open_lots_reason_check CHECK (reason IN ('', 'water', 'steep')),
    CONSTRAINT settlement_open_lots_buildable_check CHECK (buildable = (reason = '')),
    CONSTRAINT settlement_open_lots_dist_check CHECK (dist >= 1)
);

CREATE INDEX settlement_open_lots_plan_idx ON settlement_open_lots (plan_id);

COMMIT;
