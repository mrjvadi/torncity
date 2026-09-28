-- 0043_settlement_buildings — what a settlement has built on its own lot
-- grid (docs/adr/0028-world-and-settlements.md section 6, section 9.1).
--
-- Only the founding kit (one road, one civic hall, ADR 0028 section 3.1/7)
-- is written by this phase (W4). The full construction queue — placing a
-- building, its cost, its build time, demolition — is section 6's own later
-- phase (W5); this table's shape already fits it (status, queued_at,
-- completed_at) so that phase adds behaviour, not columns.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE settlement_buildings (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    -- The building catalogue's code (ADR 0028 section 7: road, civic_hall,
    -- …). Not a foreign key: the catalogue is content (a future
    -- buildings.yml), and a building outlives any one content version, the
    -- same way a company's kind code does.
    type_code     text        NOT NULL,
    -- The settlement's own local lot grid (never a world cell): 0-based,
    -- origin at the grid's own corner. Legality (footprint, terrain,
    -- overlap) is checked by the writer; this table only records the
    -- result.
    lot_x         int         NOT NULL,
    lot_y         int         NOT NULL,
    -- queued: paid for, not yet started (may be cancelled for a refund).
    -- building: under construction on the game clock. complete: finished
    -- and producing. The founding kit is written directly as complete — a
    -- free starting gift, never queued or waited on.
    status        text        NOT NULL,
    queued_at     timestamptz NOT NULL,
    completed_at  timestamptz NULL,

    CONSTRAINT settlement_buildings_status_check CHECK (status IN ('queued', 'building', 'complete')),
    CONSTRAINT settlement_buildings_completed_shape_check
        CHECK ((status = 'complete') = (completed_at IS NOT NULL)),
    CONSTRAINT settlement_buildings_lot_check CHECK (lot_x >= 0 AND lot_y >= 0),
    -- Two buildings never occupy the same lot; the writer checks the whole
    -- footprint, this backstops one lot of it exactly like a row lock
    -- backstops every other placement rule in this codebase.
    CONSTRAINT settlement_buildings_lot_unique UNIQUE (settlement_id, lot_x, lot_y)
);

CREATE INDEX settlement_buildings_settlement_id_idx ON settlement_buildings (settlement_id);

COMMENT ON TABLE settlement_buildings IS
    'One placed (or queued) building on a settlement''s own lot grid (ADR 0028 section 6). This phase (W4) writes only the founding kit, as complete; the construction queue is a later phase.';

COMMIT;
