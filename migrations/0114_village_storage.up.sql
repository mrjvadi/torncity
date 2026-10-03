-- 0114_village_storage: the working storehouse (storage and market audit
-- 2026-10-03, phase P2; docs/adr/0041 6.4, building_functions.yml storage rows).
--
-- A settlement's stock is kept in three CLASSES (bulk, food, goods) and a
-- storehouse or granary gives its room only while a storekeeper keeps it. The
-- keeper is an NPC citizen of the labour pool, paid a day's wage from the
-- treasury (ledger reason storekeeper_wage, treasury to the sink). Food in the
-- stock spoils a little each game day, less when a granary is kept.
--
-- village_storage_days is the fence of that day, like village_shop_days is of the
-- morning delivery: one row per settlement and game day, written by the one
-- transaction that settles the day (two replicas, a tick and a player's first look
-- cannot do it twice). It records how many storage buildings stand, how many had
-- a keeper, the wage paid, and the units of food that spoiled; spoil_carry is the
-- remainder (in ten-thousandths of a unit) carried to the next day so rounding
-- never favours either side.
--
-- Conventions as 0058. Additive; never a wipe.
BEGIN;

CREATE TABLE village_storage_days (
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    day                   bigint      NOT NULL,
    buildings             integer     NOT NULL,
    kept                  integer     NOT NULL,
    wage                  bigint      NOT NULL DEFAULT 0,
    ledger_transaction_id uuid        NULL,
    spoiled_units         bigint      NOT NULL DEFAULT 0,
    spoil_carry           bigint      NOT NULL DEFAULT 0,
    at                    timestamptz NOT NULL,

    CONSTRAINT village_storage_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT village_storage_days_counts_check CHECK (buildings >= 0 AND kept >= 0 AND kept <= buildings),
    CONSTRAINT village_storage_days_amounts_check CHECK (wage >= 0 AND spoiled_units >= 0 AND spoil_carry >= 0),
    CONSTRAINT village_storage_days_wage_check CHECK ((wage > 0) = (ledger_transaction_id IS NOT NULL))
);

COMMIT;
