-- 0135_levy_refund: the one-off refund of the national levy a founded settlement paid to a country nobody made.
--
-- Until the fix of 2026-10-09 the defence period of ADR 0022 levied every city under default_country, founded
-- settlements included: country.revenue_share (10 percent) of every credit to the treasury, donations and the
-- founding grant among them. A settlement belongs to a country only when its players create one (ADR 0044), so the
-- owner decided to give all of it back (operator command `admin settlement refund-national-levy`).
--
-- levy_refunds is the fence and the record: one row per settlement, written in the transaction that moves the money, so
-- the command is idempotent and safe on two machines at once. The refund is funded first from the country's state
-- treasury and defence fund (in proportion to what each holds) and the rest from system_source, all one ledger
-- transaction with reason levy_refund. No foreign key to cities: like the other journals it outlives a deleted test
-- settlement.
BEGIN;

CREATE TABLE levy_refunds (
    settlement_id         uuid        PRIMARY KEY,
    country_id            uuid        NOT NULL,
    levy                  bigint      NOT NULL,
    from_state_treasury   bigint      NOT NULL,
    from_defence_fund     bigint      NOT NULL,
    from_source           bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    reason                text        NOT NULL,
    operator              text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT levy_refunds_amounts_check CHECK (levy > 0 AND from_state_treasury >= 0 AND from_defence_fund >= 0 AND from_source >= 0),
    CONSTRAINT levy_refunds_sum_check CHECK (from_state_treasury + from_defence_fund + from_source = levy)
);

COMMIT;
