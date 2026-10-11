-- 0150_land_registry: parcels, zones, tile claims and foundations (ADR 0072, plan B8).
--
--   parcels, parcel_cells   the lots one owner holds, grouped. Every live row of settlement_lots is a cell of exactly one parcel (the
--                           backfill makes each a parcel of one cell, with the lot's id); the treasury may hold parcels too (the
--                           commons), which have no lot row.
--   zones, zone_cells       a land-use rule opened on cells by a holder of zone.open; closing frees the cells.
--   settlement_claims       a tile of the world (the 305 m tile of ADR 0042) held by one settlement, one owner for the whole world.
--   lot_foundations         a foundation laid on a lot too steep for the building (ADR 0072 3.5).
--   land_audit              every registry act, append-only.
--
-- No foreign key to cities, lots or players (as the other village tables). Conventions as 0137.
BEGIN;

CREATE TABLE parcels (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    owner_kind    text        NOT NULL,
    owner_id      uuid        NULL,
    use           text        NOT NULL,
    zone_id       uuid        NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT parcels_owner_kind_check CHECK (owner_kind IN ('treasury', 'player')),
    CONSTRAINT parcels_owner_check CHECK ((owner_kind = 'treasury') = (owner_id IS NULL))
);
CREATE INDEX parcels_settlement_idx ON parcels (settlement_id, owner_id);

CREATE TABLE parcel_cells (
    settlement_id uuid NOT NULL,
    lot_x         int  NOT NULL,
    lot_y         int  NOT NULL,
    parcel_id     uuid NOT NULL REFERENCES parcels (id),
    CONSTRAINT parcel_cells_pk PRIMARY KEY (settlement_id, lot_x, lot_y)
);
CREATE INDEX parcel_cells_parcel_idx ON parcel_cells (parcel_id);

INSERT INTO parcels (id, settlement_id, owner_kind, owner_id, use, created_at, updated_at)
SELECT l.id, l.settlement_id, 'player', l.owner_id, '', l.acquired_at, l.acquired_at
  FROM settlement_lots l WHERE l.released_at IS NULL;

INSERT INTO parcel_cells (settlement_id, lot_x, lot_y, parcel_id)
SELECT l.settlement_id, l.lot_x, l.lot_y, l.id
  FROM settlement_lots l WHERE l.released_at IS NULL;

CREATE TABLE zones (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    name          text        NOT NULL,
    kind          text        NOT NULL,
    opened_by     uuid        NOT NULL,
    opened_at     timestamptz NOT NULL,
    effective_at  timestamptz NOT NULL,
    closed_at     timestamptz NULL,
    closed_by     uuid        NULL,
    cell_count    int         NOT NULL,
    CONSTRAINT zones_name_check CHECK (char_length(name) BETWEEN 1 AND 40),
    CONSTRAINT zones_kind_check CHECK (char_length(kind) BETWEEN 1 AND 30),
    CONSTRAINT zones_cells_check CHECK (cell_count >= 1),
    CONSTRAINT zones_closed_check CHECK ((closed_at IS NULL) = (closed_by IS NULL))
);
CREATE INDEX zones_settlement_idx ON zones (settlement_id) WHERE closed_at IS NULL;

CREATE TABLE zone_cells (
    zone_id       uuid NOT NULL REFERENCES zones (id),
    settlement_id uuid NOT NULL,
    lot_x         int  NOT NULL,
    lot_y         int  NOT NULL,
    CONSTRAINT zone_cells_pk PRIMARY KEY (zone_id, lot_x, lot_y)
);
-- one open zone per cell: closing a zone deletes its cells
CREATE UNIQUE INDEX zone_cells_cell_unique ON zone_cells (settlement_id, lot_x, lot_y);

CREATE TABLE settlement_claims (
    id             uuid        PRIMARY KEY,
    settlement_id  uuid        NOT NULL,
    tile_face      smallint    NOT NULL,
    tile_gx        int         NOT NULL,
    tile_gy        int         NOT NULL,
    claimed_by     uuid        NOT NULL,
    claimed_at     timestamptz NOT NULL,
    paid_through   timestamptz NOT NULL,
    ledger_transaction_id uuid NOT NULL,
    -- one owner for a tile of the world
    CONSTRAINT settlement_claims_tile_unique UNIQUE (tile_face, tile_gx, tile_gy)
);
CREATE INDEX settlement_claims_settlement_idx ON settlement_claims (settlement_id);

CREATE TABLE lot_foundations (
    settlement_id uuid        NOT NULL,
    lot_x         int         NOT NULL,
    lot_y         int         NOT NULL,
    steps         smallint    NOT NULL,
    laid_by       uuid        NOT NULL,
    laid_at       timestamptz NOT NULL,
    CONSTRAINT lot_foundations_pk PRIMARY KEY (settlement_id, lot_x, lot_y),
    CONSTRAINT lot_foundations_steps_check CHECK (steps = 1)
);

CREATE TABLE land_audit (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    kind          text        NOT NULL,
    actor_id      uuid        NOT NULL,
    detail        jsonb       NOT NULL,
    at            timestamptz NOT NULL
);
CREATE INDEX land_audit_settlement_idx ON land_audit (settlement_id, at);

COMMIT;
