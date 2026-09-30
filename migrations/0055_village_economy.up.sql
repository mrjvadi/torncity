-- 0055_village_economy — the village's first economic loop (ADR 0033 section
-- 4.1, phase E1): materials bought from Support, and shifts worked at village
-- workplaces.
--
--   settlement_material_purchases  a village buys basic materials (timber,
--                                  stone, wool, ...) from Support's market with
--                                  SUP from its treasury: treasury -> system_sink
--                                  (reason settlement_material_purchase) and the
--                                  goods into the village stock (org_stacks,
--                                  item journal reason supplied). The row lets
--                                  the ledger verifier prove the two agree.
--   settlement_shifts              a resident works a timed shift at a standing
--                                  building that produces something. The shift
--                                  takes its inputs from the village stock when
--                                  it starts; when it ends (a scheduled action,
--                                  once) the goods enter the stock and the
--                                  treasury pays the wage to the resident's cash
--                                  (reason settlement_wage).
--
-- Both tables are journals like 0052's: they name their settlement and player
-- without a foreign key so they outlive the rows exactly as the ledger does.
-- Instants are timestamptz in UTC; no DEFAULT now(); CHECK constraints are
-- named.
BEGIN;

CREATE TABLE settlement_material_purchases (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    item_code             text        NOT NULL,
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    bought_by             uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT settlement_material_purchases_quantity_check CHECK (quantity > 0),
    CONSTRAINT settlement_material_purchases_price_check CHECK (unit_price > 0),
    CONSTRAINT settlement_material_purchases_total_check CHECK (total = quantity * unit_price)
);

CREATE INDEX settlement_material_purchases_settlement_idx
    ON settlement_material_purchases (settlement_id, created_at DESC);

CREATE TABLE settlement_shifts (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    building_id           uuid        NOT NULL REFERENCES settlement_buildings (id),
    player_id             uuid        NOT NULL,
    status                text        NOT NULL,
    wage                  bigint      NOT NULL,
    wage_paid             bigint      NOT NULL,
    produced              jsonb       NOT NULL,
    consumed              jsonb       NOT NULL,
    game_action_id        uuid        NOT NULL,
    ledger_transaction_id uuid,
    started_at            timestamptz NOT NULL,
    finish_at             timestamptz NOT NULL,
    finished_at           timestamptz,

    CONSTRAINT settlement_shifts_status_check CHECK (status IN ('working', 'done')),
    CONSTRAINT settlement_shifts_wage_check CHECK (wage >= 0 AND wage_paid >= 0 AND wage_paid <= wage),
    CONSTRAINT settlement_shifts_done_check CHECK ((status = 'done') = (finished_at IS NOT NULL))
);

-- One shift at a time per resident, whichever replica takes the command.
CREATE UNIQUE INDEX settlement_shifts_one_working_idx ON settlement_shifts (player_id) WHERE status = 'working';
CREATE INDEX settlement_shifts_building_idx ON settlement_shifts (building_id) WHERE status = 'working';
CREATE INDEX settlement_shifts_settlement_idx ON settlement_shifts (settlement_id, started_at DESC);

COMMIT;
