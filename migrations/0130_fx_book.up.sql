-- 0130_fx_book - the floating VC/SUP order book of a settlement's own money (roadmap 2.19 phase 3,
-- docs/adr/0033 sections 6.6 to 6.9, docs/adr/0029 section 4).
--
--   accounts        gain the kind 'fx_escrow': what an order on the book has set aside, one row per owner AND
--                   currency (SUP for a buy, the settlement's units for a sell); owner a player or a settlement.
--   fx_orders       the book: limit orders of players and settlements, partial fills, cancel. A separate book on
--                   the shared matcher (internal/domain/market), not market_orders: that table is built around
--                   item escrow, a city key and player owners (docs/adr/0033 as built, phase 3).
--   fx_trades       every fill, append-only, with its two ledger transactions.
--   fx_clock        the one clock of the book's periods (a scheduled action ends each one, like finance_clock).
--   fx_rate_history one row per currency and period, append-only: the readings the reference rate x_ref
--                   was set from. village_currency_state.x_ref_ppm is always the last row's x_ref_after.
BEGIN;

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow'));
DROP INDEX accounts_one_row_per_owner_idx;
CREATE UNIQUE INDEX accounts_one_row_per_owner_idx
    ON accounts (kind, owner_id)
 WHERE kind NOT IN ('system_source', 'system_sink', 'foreign_holding', 'fx_escrow');

CREATE TABLE fx_orders (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY,
    settlement_id  uuid        NOT NULL,
    owner_kind     text        NOT NULL,
    owner_id       uuid        NOT NULL,
    side           text        NOT NULL,
    quantity       bigint      NOT NULL,
    filled         bigint      NOT NULL DEFAULT 0,
    price          bigint      NOT NULL,
    notional_micro bigint      NOT NULL DEFAULT 0,
    escrow_left    bigint      NOT NULL,
    fee_bps        int         NOT NULL,
    kind           text        NOT NULL DEFAULT 'limit',
    status         text        NOT NULL DEFAULT 'open',
    created_at     timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    closed_at      timestamptz NULL,

    CONSTRAINT fx_orders_no_key UNIQUE (no),
    CONSTRAINT fx_orders_owner_check CHECK (owner_kind IN ('player', 'settlement')),
    CONSTRAINT fx_orders_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT fx_orders_kind_check CHECK (kind IN ('limit', 'market')),
    CONSTRAINT fx_orders_status_check CHECK (status IN ('open', 'filled', 'cancelled', 'expired')),
    CONSTRAINT fx_orders_amounts_check CHECK (quantity > 0 AND filled >= 0 AND filled <= quantity AND price > 0
        AND notional_micro >= 0 AND escrow_left >= 0 AND fee_bps BETWEEN 0 AND 10000),
    CONSTRAINT fx_orders_closed_check CHECK ((status = 'open') = (closed_at IS NULL)),
    -- a closed order holds nothing
    CONSTRAINT fx_orders_released_check CHECK (status = 'open' OR escrow_left = 0)
);
CREATE INDEX fx_orders_book_idx ON fx_orders (settlement_id, side, price, created_at, id) WHERE status = 'open';
CREATE INDEX fx_orders_owner_idx ON fx_orders (owner_id, created_at DESC);
CREATE INDEX fx_orders_expiry_idx ON fx_orders (expires_at) WHERE status = 'open';

CREATE TABLE fx_trades (
    id                 uuid        PRIMARY KEY,
    settlement_id      uuid        NOT NULL,
    buy_order_id       uuid        NOT NULL REFERENCES fx_orders (id),
    sell_order_id      uuid        NOT NULL REFERENCES fx_orders (id),
    buyer_kind         text        NOT NULL,
    buyer_id           uuid        NOT NULL,
    seller_kind        text        NOT NULL,
    seller_id          uuid        NOT NULL,
    quantity           bigint      NOT NULL,
    price              bigint      NOT NULL,
    sup_amount         bigint      NOT NULL,
    sup_fee            bigint      NOT NULL,
    units_fee          bigint      NOT NULL,
    sup_transaction_id uuid        NULL UNIQUE,
    vc_transaction_id  uuid        NOT NULL UNIQUE,
    created_at         timestamptz NOT NULL,

    CONSTRAINT fx_trades_amounts_check CHECK (quantity > 0 AND price > 0 AND sup_amount >= 0 AND sup_fee >= 0
        AND units_fee >= 0 AND units_fee <= quantity),
    CONSTRAINT fx_trades_parties_check CHECK (buy_order_id <> sell_order_id)
);
CREATE INDEX fx_trades_book_idx ON fx_trades (settlement_id, created_at DESC);
CREATE TRIGGER fx_trades_append_only BEFORE UPDATE OR DELETE ON fx_trades
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();

CREATE TABLE fx_clock (
    id                int         PRIMARY KEY,
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL,
    updated_at        timestamptz NOT NULL,

    CONSTRAINT fx_clock_one_check CHECK (id = 1),
    CONSTRAINT fx_clock_period_check CHECK (period_no >= 1),
    CONSTRAINT fx_clock_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL))
);

CREATE TABLE fx_rate_history (
    settlement_id  uuid        NOT NULL,
    period_no      bigint      NOT NULL,
    trades         bigint      NOT NULL,
    volume_units   bigint      NOT NULL,
    notional_micro bigint      NOT NULL,
    value_ppm      bigint      NOT NULL,
    window_trades  bigint      NOT NULL,
    x_ref_before   bigint      NOT NULL,
    x_ref_after    bigint      NOT NULL,
    created_at     timestamptz NOT NULL,

    PRIMARY KEY (settlement_id, period_no),
    CONSTRAINT fx_rate_history_amounts_check CHECK (trades >= 0 AND volume_units >= 0 AND notional_micro >= 0
        AND value_ppm > 0 AND window_trades >= 0 AND x_ref_before > 0 AND x_ref_after > 0)
);
CREATE TRIGGER fx_rate_history_append_only BEFORE UPDATE OR DELETE ON fx_rate_history
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER fx_rate_history_no_truncate BEFORE TRUNCATE ON fx_rate_history
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
