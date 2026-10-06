-- 0128_local_obligations - a settlement's obligations settle in its own money, and its desk lets a
-- player convert (roadmap 2.19 phase 2, ADR 0033 sections 6.9 and 6.10).
--
--   local_payments        one row for every obligation paid in the local money instead of SUP: who,
--                         the SUP amount it stands for, the units, the rate it used and the ledger
--                         transaction. The flow's own row (a shift, a class seat, a training session)
--                         keeps its SUP amount; the verifier leaves a flow it finds here out of its SUP
--                         totals and checks it against this table instead.
--   currency_desk_trades  the village desk's conversions (a player buys or sells local units for SUP at
--                         the reference rate less the desk's fee), each with its two ledger transactions.
--   village_currency_state.fx_fee_bps  the desk's fee, set by the head within 10..300 (default 30).
BEGIN;

CREATE TABLE local_payments (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    player_id             uuid        NOT NULL,
    direction             text        NOT NULL,
    flow                  text        NOT NULL,
    sup_amount            bigint      NOT NULL,
    units                 bigint      NOT NULL,
    r0                    bigint      NOT NULL,
    x_ref_ppm             bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    reference_type        text        NOT NULL,
    reference_id          uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT local_payments_direction_check CHECK (direction IN ('pay', 'collect')),
    CONSTRAINT local_payments_amounts_check CHECK (sup_amount > 0 AND units > 0 AND r0 > 0 AND x_ref_ppm > 0),
    -- one local payment per flow row and direction: the idempotency fence of a redelivered finish
    CONSTRAINT local_payments_reference_key UNIQUE (reference_type, reference_id, direction)
);
CREATE INDEX local_payments_settlement_idx ON local_payments (settlement_id, created_at);

CREATE TABLE currency_desk_trades (
    id                uuid        PRIMARY KEY,
    settlement_id     uuid        NOT NULL,
    player_id         uuid        NOT NULL,
    side              text        NOT NULL,
    sup_amount        bigint      NOT NULL,
    units             bigint      NOT NULL,
    fee_bps           int         NOT NULL,
    x_ref_ppm         bigint      NOT NULL,
    r0                bigint      NOT NULL,
    sup_transaction_id   uuid     NOT NULL UNIQUE,
    local_transaction_id uuid     NOT NULL UNIQUE,
    created_at        timestamptz NOT NULL,

    CONSTRAINT currency_desk_trades_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT currency_desk_trades_amounts_check CHECK (sup_amount > 0 AND units > 0 AND fee_bps BETWEEN 0 AND 10000 AND x_ref_ppm > 0 AND r0 > 0)
);
CREATE INDEX currency_desk_trades_settlement_idx ON currency_desk_trades (settlement_id, created_at);

ALTER TABLE village_currency_state
    ADD COLUMN fx_fee_bps int NOT NULL DEFAULT 30,
    ADD CONSTRAINT village_currency_state_fee_check CHECK (fx_fee_bps BETWEEN 10 AND 300);

COMMIT;
