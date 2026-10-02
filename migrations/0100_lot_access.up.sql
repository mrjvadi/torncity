-- 0100_lot_access: a lot is only sold when a road can reach it (docs/adr/0043).
--
-- Real land is platted with its streets before the parcels are sold: every
-- parcel fronts a public road or a recorded right of way, and a buyer of a
-- landlocked parcel is owed access or his money back. This migration gives
-- the village its road reserve and the books that go with it.
--
--   settlement_road_reserve    right-of-way lots: never sold, never built on
--                              except by road. 'plan' lots are streets platted
--                              ahead; 'corridor' lots are the road that serves a
--                              sold lot (reserved in the same transaction as the
--                              sale, so no later sale can landlock it).
--   settlement_lots            gains the release columns: a lot given back to the
--                              village (rescission, refund) or dedicated to the
--                              road keeps its row, so the sale stays in the
--                              books and `admin economy verify` still balances.
--   settlement_lot_connections the journal of the roads a buyer or owner paid
--                              for to reach a lot (ledger reason settlement_lot_road).
--
-- Conventions as 0058: instants are timestamptz in UTC, no DEFAULT now(),
-- named CHECK constraints, journals carry no foreign keys.
BEGIN;

CREATE TABLE settlement_road_reserve (
    settlement_id uuid        NOT NULL,
    lot_x         int         NOT NULL,
    lot_y         int         NOT NULL,
    -- plan: a street platted ahead. corridor: the access of one sold lot.
    kind          text        NOT NULL,
    -- the lot a corridor serves; NULL for a planned street
    serves_x      int         NULL,
    serves_y      int         NULL,
    created_at    timestamptz NOT NULL,

    CONSTRAINT settlement_road_reserve_pk PRIMARY KEY (settlement_id, lot_x, lot_y),
    CONSTRAINT settlement_road_reserve_kind_check CHECK (kind IN ('plan', 'corridor')),
    CONSTRAINT settlement_road_reserve_coordinates_check CHECK (lot_x >= 0 AND lot_y >= 0),
    CONSTRAINT settlement_road_reserve_serves_check
        CHECK ((kind = 'corridor') = (serves_x IS NOT NULL AND serves_y IS NOT NULL))
);

-- A lot is released when the village takes it back: a refund (rescission, the
-- price returns to the owner from the treasury) or a dedication (the owner
-- turns it into road). The row stays; only a live row (released_at IS NULL)
-- holds the lot. The unique constraint becomes a partial unique index with
-- the same name, so the buyer race is still decided there.
ALTER TABLE settlement_lots
    ADD COLUMN released_at                  timestamptz NULL,
    ADD COLUMN release_kind                 text        NULL,
    ADD COLUMN refund_amount                bigint      NULL,
    ADD COLUMN refund_ledger_transaction_id uuid        NULL UNIQUE,
    ADD CONSTRAINT settlement_lots_release_check CHECK (
        (released_at IS NULL AND release_kind IS NULL AND refund_amount IS NULL AND refund_ledger_transaction_id IS NULL)
        OR (released_at IS NOT NULL AND release_kind = 'refund' AND refund_amount > 0 AND refund_ledger_transaction_id IS NOT NULL)
        OR (released_at IS NOT NULL AND release_kind = 'dedicated' AND refund_amount IS NULL AND refund_ledger_transaction_id IS NULL));

ALTER TABLE settlement_lots DROP CONSTRAINT settlement_lots_lot_unique;
CREATE UNIQUE INDEX settlement_lots_lot_unique ON settlement_lots (settlement_id, lot_x, lot_y) WHERE released_at IS NULL;

-- The roads paid for to reach a lot. fee is what left the payer's cash for
-- good (reason settlement_lot_road, into the system sink, like every
-- construction cost); it is 0 for a connection that cost nothing, and then
-- there is no ledger transaction.
CREATE TABLE settlement_lot_connections (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    player_id             uuid        NOT NULL,
    lot_x                 int         NOT NULL,
    lot_y                 int         NOT NULL,
    -- buy: included in a purchase; repair: a lot bought before the rule.
    origin                text        NOT NULL,
    road_lots             int         NOT NULL,
    crossing_lots         int         NOT NULL,
    carved_lots           int         NOT NULL,
    fee                   bigint      NOT NULL,
    ledger_transaction_id uuid        NULL UNIQUE,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_lot_connections_origin_check CHECK (origin IN ('buy', 'repair')),
    CONSTRAINT settlement_lot_connections_counts_check
        CHECK (road_lots >= 0 AND crossing_lots >= 0 AND carved_lots >= 0 AND fee >= 0),
    CONSTRAINT settlement_lot_connections_ledger_check CHECK ((fee > 0) = (ledger_transaction_id IS NOT NULL))
);

CREATE INDEX settlement_lot_connections_settlement_idx ON settlement_lot_connections (settlement_id);

COMMIT;
