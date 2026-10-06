-- 0127_local_currency - a settlement's own money exists (roadmap 2.19 phase 1, ADR 0033 section 6,
-- ADR 0029 section 5).
--
--   accounts                 gain the kinds 'foreign_holding' (a holder's balance in a currency that is not
--                            the neutral one: one row per owner AND currency, the owner a player, a
--                            settlement or a company) and 'reserve_pot' (a settlement's reserve at the
--                            Reserve Bank, SUP, one per settlement).
--   village_currency_state   one row per chartered settlement: its currency code, the charter rate r0
--                            (units per SUP at charter, the scale of the numbers and not a peg), the
--                            reference rate x_ref in parts per million (1000000 = 1.00 until a book
--                            trades), and the counters the pot and the supply are verified against.
--   currency_issuance_log    every mint (and later burn), one row each, keyed by its ledger transaction.
--
-- Nothing is converted and no balance moves here: the charter itself is a command (auto at founding, once
-- at deploy for the old settlements, or by the head).
BEGIN;

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot'));

CREATE TABLE village_currency_state (
    settlement_id  uuid        PRIMARY KEY,
    currency_code  text        NOT NULL UNIQUE REFERENCES currencies (code),
    status         text        NOT NULL DEFAULT 'chartered',
    r0             bigint      NOT NULL,
    x_ref_ppm      bigint      NOT NULL DEFAULT 1000000,
    minted_units   bigint      NOT NULL DEFAULT 0,
    burnt_units    bigint      NOT NULL DEFAULT 0,
    basis_sup      bigint      NOT NULL DEFAULT 0,
    deposited_sup  bigint      NOT NULL DEFAULT 0,
    released_sup   bigint      NOT NULL DEFAULT 0,
    chartered_at   timestamptz NOT NULL,
    chartered_by   text        NOT NULL,

    CONSTRAINT village_currency_state_status_check CHECK (status IN ('chartered', 'wind_down', 'retired')),
    CONSTRAINT village_currency_state_rate_check CHECK (r0 > 0 AND x_ref_ppm > 0),
    CONSTRAINT village_currency_state_counters_check CHECK (
        minted_units >= 0 AND burnt_units >= 0 AND burnt_units <= minted_units
        AND basis_sup >= 0 AND deposited_sup >= 0 AND released_sup >= 0 AND released_sup <= deposited_sup)
);

COMMENT ON TABLE village_currency_state IS
    'A chartered settlement currency (ADR 0033 6). supply = minted_units - burnt_units; the reserve pot account holds deposited_sup - released_sup.';

CREATE TABLE currency_issuance_log (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL,
    kind                  text        NOT NULL,
    deposit_sup           bigint      NOT NULL,
    units                 bigint      NOT NULL,
    x_ref_ppm             bigint      NOT NULL,
    mint_fee_bps          int         NOT NULL,
    ledger_transaction_id uuid        NOT NULL UNIQUE,
    issued_by             text        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT currency_issuance_log_kind_check CHECK (kind IN ('mint', 'burn')),
    CONSTRAINT currency_issuance_log_amounts_check CHECK (deposit_sup >= 0 AND units > 0 AND x_ref_ppm > 0 AND mint_fee_bps BETWEEN 0 AND 10000)
);
CREATE INDEX currency_issuance_log_settlement_idx ON currency_issuance_log (settlement_id, created_at);

COMMIT;
