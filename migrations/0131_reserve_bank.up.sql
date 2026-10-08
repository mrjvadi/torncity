-- 0131_reserve_bank - the head's tools over the reserve and the macro readings (roadmap 2.19 phase 4,
-- docs/adr/0033 sections 6.3 to 6.8, 6.11 to 6.13, 7).
--
--   village_currency_state     gains the wind-down fields and the two intervention counters, so the pot's identity is
--                              pot = deposited - released - intervention_out + intervention_in.
--   fx_orders                  gain `purpose`: 'intervention' orders are the treasury's, funded from (and paying into)
--                              the reserve pot.
--   currency_interventions     the head's intervention requests, public, executed one period after they are posted.
--   currency_withdrawals       excess withdrawals: announced, executed after the Reserve Bank's notice.
--   currency_claims            a holder's claim of a pro-rata share of the pot in a wind-down.
--   village_macro_periods      the macro readings of each period (append-only): price indices, coverage, supply growth.
BEGIN;

ALTER TABLE village_currency_state
    ADD COLUMN wind_down_at       timestamptz NULL,
    ADD COLUMN wind_down_ends_at  timestamptz NULL,
    ADD COLUMN wind_down_reason   text        NULL,
    ADD COLUMN retired_at         timestamptz NULL,
    ADD COLUMN intervention_out   bigint      NOT NULL DEFAULT 0,
    ADD COLUMN intervention_in    bigint      NOT NULL DEFAULT 0,
    ADD CONSTRAINT village_currency_state_flows_check CHECK (intervention_out >= 0 AND intervention_in >= 0),
    ADD CONSTRAINT village_currency_state_wind_check CHECK ((status = 'chartered') = (wind_down_at IS NULL));

ALTER TABLE fx_orders
    ADD COLUMN purpose text NOT NULL DEFAULT 'trade',
    ADD CONSTRAINT fx_orders_purpose_check CHECK (purpose IN ('trade', 'intervention') AND (purpose = 'trade' OR owner_kind = 'settlement'));

CREATE TABLE currency_interventions (
    id               uuid        PRIMARY KEY,
    settlement_id    uuid        NOT NULL,
    side             text        NOT NULL,
    units            bigint      NOT NULL,
    price            bigint      NOT NULL,
    posted_by        uuid        NOT NULL,
    posted_at        timestamptz NOT NULL,
    execute_after    timestamptz NOT NULL,
    status           text        NOT NULL DEFAULT 'pending',
    executed_at      timestamptz NULL,
    order_id         uuid        NULL,
    sup_used         bigint      NOT NULL DEFAULT 0,
    refusal          text        NULL,

    CONSTRAINT currency_interventions_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT currency_interventions_status_check CHECK (status IN ('pending', 'done', 'refused', 'cancelled')),
    CONSTRAINT currency_interventions_amounts_check CHECK (units > 0 AND price > 0 AND sup_used >= 0),
    CONSTRAINT currency_interventions_done_check CHECK ((status = 'pending') = (executed_at IS NULL))
);
CREATE INDEX currency_interventions_due_idx ON currency_interventions (execute_after) WHERE status = 'pending';
CREATE INDEX currency_interventions_settlement_idx ON currency_interventions (settlement_id, posted_at DESC);

CREATE TABLE currency_withdrawals (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    sup                   bigint      NOT NULL,
    requested_by          uuid        NOT NULL,
    requested_at          timestamptz NOT NULL,
    execute_after         timestamptz NOT NULL,
    status                text        NOT NULL DEFAULT 'pending',
    executed_at           timestamptz NULL,
    ledger_transaction_id uuid        NULL UNIQUE,
    refusal               text        NULL,

    CONSTRAINT currency_withdrawals_status_check CHECK (status IN ('pending', 'done', 'refused', 'cancelled')),
    CONSTRAINT currency_withdrawals_amount_check CHECK (sup > 0),
    CONSTRAINT currency_withdrawals_done_check CHECK ((status = 'pending') = (executed_at IS NULL))
);
CREATE INDEX currency_withdrawals_due_idx ON currency_withdrawals (execute_after) WHERE status = 'pending';

CREATE TABLE currency_claims (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    player_id             uuid        NOT NULL,
    units                 bigint      NOT NULL,
    sup                   bigint      NOT NULL,
    pot_before            bigint      NOT NULL,
    claimable_before      bigint      NOT NULL,
    sup_transaction_id    uuid        NULL UNIQUE,
    burn_transaction_id   uuid        NOT NULL UNIQUE,
    created_at            timestamptz NOT NULL,

    CONSTRAINT currency_claims_amounts_check CHECK (units > 0 AND sup >= 0 AND pot_before >= 0 AND claimable_before >= units AND sup <= pot_before)
);
CREATE INDEX currency_claims_settlement_idx ON currency_claims (settlement_id, created_at);

CREATE TABLE village_macro_periods (
    settlement_id        uuid        NOT NULL,
    period_no            bigint      NOT NULL,
    supply_units         bigint      NOT NULL,
    stabilisation_units  bigint      NOT NULL,
    m_sup                bigint      NOT NULL,
    y_sup                bigint      NOT NULL,
    x_ref_ppm            bigint      NOT NULL,
    tradable_ppm         bigint      NOT NULL,
    nontradable_ppm      bigint      NOT NULL,
    price_ppm            bigint      NOT NULL,
    pi_local_bps         bigint      NOT NULL,
    coverage_bps         bigint      NULL,
    supply_growth_bps    bigint      NOT NULL,
    pot_sup              bigint      NOT NULL,
    basis_sup            bigint      NOT NULL,
    created_at           timestamptz NOT NULL,

    PRIMARY KEY (settlement_id, period_no),
    CONSTRAINT village_macro_periods_amounts_check CHECK (supply_units >= 0 AND stabilisation_units >= 0 AND m_sup >= 0 AND y_sup >= 0
        AND x_ref_ppm > 0 AND tradable_ppm > 0 AND nontradable_ppm > 0 AND price_ppm > 0 AND pot_sup >= 0 AND basis_sup >= 0)
);
CREATE TRIGGER village_macro_periods_append_only BEFORE UPDATE OR DELETE ON village_macro_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER village_macro_periods_no_truncate BEFORE TRUNCATE ON village_macro_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
