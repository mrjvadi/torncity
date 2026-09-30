-- 0058_citizen_loop — a resident owns land and builds a private house in the
-- village (docs/adr/0033 sections 4.4 and 4.5, phase H1).
--
-- Every lot of a village grid is COMMONS (the village's own) unless a row of
-- settlement_lots says otherwise: there is no row per commons lot. A lot
-- bought from the village is freehold; the price goes to the village
-- treasury (ledger reason settlement_lot_sale). A private building is a
-- normal settlement_buildings row (so the grid, the layout and the
-- construction timer keep working unchanged) plus one row of
-- settlement_private_buildings that says who owns it and what it was paid
-- with. The property tax is one row per owner and period.
--
-- The money side is provable: each of the ledger reasons below has exactly
-- one journal row per ledger transaction, and `admin economy verify`
-- compares the two (internal/infrastructure/postgres/ledger_admin_citizen.go).
-- Like migration 0052, the journals name their settlement and player
-- without a foreign key, so they outlive both exactly as the ledger does.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.
BEGIN;

CREATE TABLE settlement_lots (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    lot_x                 int         NOT NULL,
    lot_y                 int         NOT NULL,
    -- freehold: the owner holds it outright. leased: reserved for a
    -- lease (the head's offer of a lease is a later phase; the vocabulary is
    -- here so the layout never has to change).
    tenure                text        NOT NULL,
    owner_id              uuid        NOT NULL,
    price                 bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    acquired_at           timestamptz NOT NULL,

    CONSTRAINT settlement_lots_tenure_check CHECK (tenure IN ('freehold', 'leased')),
    CONSTRAINT settlement_lots_coordinates_check CHECK (lot_x >= 0 AND lot_y >= 0),
    CONSTRAINT settlement_lots_price_check CHECK (price > 0),
    -- One owner per lot: the race of two buyers is decided here.
    CONSTRAINT settlement_lots_lot_unique UNIQUE (settlement_id, lot_x, lot_y)
);

CREATE INDEX settlement_lots_owner_idx ON settlement_lots (owner_id, settlement_id);

-- The head's levers, each inside the bounds of config settlement.citizen_*;
-- a NULL is "the default of the configuration".
CREATE TABLE settlement_lot_terms (
    settlement_id uuid        PRIMARY KEY,
    lot_price     bigint      NULL,
    permit_fee    bigint      NULL,
    tax_bps       int         NULL,
    updated_by    uuid        NOT NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT settlement_lot_terms_lot_price_check CHECK (lot_price IS NULL OR lot_price > 0),
    CONSTRAINT settlement_lot_terms_permit_fee_check CHECK (permit_fee IS NULL OR permit_fee >= 0),
    CONSTRAINT settlement_lot_terms_tax_bps_check CHECK (tax_bps IS NULL OR tax_bps >= 0)
);

-- A private building: the settlement_buildings row is the building, this row
-- is its owner and its bill. Like every journal here it names its building
-- without a foreign key, so it outlives the building and the verifier can
-- always compare it with the ledger. permit_fee went to the treasury (reason
-- settlement_permit_fee), construction_paid and materials_paid left the
-- owner's cash for good (reasons citizen_construction, citizen_materials).
CREATE TABLE settlement_private_buildings (
    building_id           uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    owner_id              uuid        NOT NULL,
    permit_fee            bigint      NOT NULL,
    construction_paid     bigint      NOT NULL,
    materials_paid        bigint      NOT NULL,
    -- What the property tax is assessed on: the building's cost (money and
    -- materials at their reference price). The lot is assessed on its price.
    assessed_value        bigint      NOT NULL,
    -- The permit fee's ledger transaction; NULL when the fee was waived.
    ledger_transaction_id uuid        NULL UNIQUE,
    -- When the owner last rested at home (the house's small comfort effect).
    last_rest_at          timestamptz NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_private_buildings_amounts_check
        CHECK (permit_fee >= 0 AND construction_paid >= 0 AND materials_paid >= 0 AND assessed_value >= 0)
);

CREATE INDEX settlement_private_buildings_owner_idx ON settlement_private_buildings (owner_id, settlement_id);
CREATE INDEX settlement_private_buildings_settlement_idx ON settlement_private_buildings (settlement_id);

-- The property tax, one row per owner and period (period_no is the number of
-- whole tax periods since the epoch, so every replica names the same one).
-- due is what the period asked; paid_at and ledger_transaction_id are set
-- when the owner's cash paid it. Unpaid rows are the owner's debt.
CREATE TABLE settlement_property_tax (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    player_id             uuid        NOT NULL,
    period_no             bigint      NOT NULL,
    assessed_value        bigint      NOT NULL,
    tax_bps               int         NOT NULL,
    due                   bigint      NOT NULL,
    paid_at               timestamptz NULL,
    ledger_transaction_id uuid        NULL UNIQUE,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_property_tax_due_check CHECK (due > 0),
    CONSTRAINT settlement_property_tax_paid_shape_check
        CHECK ((paid_at IS NULL) = (ledger_transaction_id IS NULL)),
    -- The fence that keeps a period from being charged twice.
    CONSTRAINT settlement_property_tax_period_unique UNIQUE (settlement_id, player_id, period_no)
);

CREATE INDEX settlement_property_tax_debt_idx ON settlement_property_tax (settlement_id, player_id) WHERE paid_at IS NULL;

COMMIT;
