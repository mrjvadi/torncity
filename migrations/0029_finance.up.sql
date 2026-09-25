-- 0029_finance — stage G2: finance. The national banks (their capital, the
-- loans they make and the credit record a score is worked out from),
-- savings, insurance, the stock exchange (a company's listing, its order
-- book, its trades and its dividends) and the gold dealer, with the one
-- clock they are settled on. Rules: internal/domain/finance and the one
-- matcher of internal/domain/market. Content: configs/content/finance.yml
-- and governance.yml (country.base_interest_rate, country.bank_reserve_ratio,
-- country.bank_funding; office central_banker). Decision:
-- docs/adr/0026-finance.md. Money: docs/adr/0009-economic-control.md.
--
-- NO MONEY FROM NOTHING. A loan is lent from the national bank's account
-- (national_bank, owned by the country), which the national treasury funds
-- (bank_capital) and repayments and interest refill: lending moves money,
-- it never creates it. A savings account (player_savings) is the saver's own
-- money; its interest comes from the bank. An insurance fund
-- (insurance_fund, owned by the country) pays claims from the premiums it
-- was paid, never more than it holds. Only the gold dealer is the NPC
-- economy: gold bought leaves the economy (gold_purchase, a drain), gold sold
-- back brings money in (gold_sale, a faucet), the spread keeping the round
-- trip a drain and a finite reserve bounding it.
--
-- EXACTLY ONCE. The finance clock is one row locked by its settlement; a
-- period is recorded by its number (finance_periods' primary key); every
-- loan, premium and savings interest of a period by its (thing, period)
-- primary key; a claim by (policy, source); a dividend's payments by
-- (dividend, holder).
--
-- SHARES ARE CONSERVED. company_shareholders still holds every share of a
-- company (Σ shares = total_shares); a share offered in a sell order is
-- `locked` on its holder's row, never moved elsewhere, until it trades or
-- the order ends.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- The accounts of finance.
-- ---------------------------------------------------------------------------
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings'));

-- ---------------------------------------------------------------------------
-- finance_clock — the one clock of finance (finance.yml period, GAME time).
-- ---------------------------------------------------------------------------
CREATE TABLE finance_clock (
    id                int         PRIMARY KEY,
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL REFERENCES game_actions (id),
    updated_at        timestamptz NOT NULL,

    CONSTRAINT finance_clock_one_check CHECK (id = 1),
    CONSTRAINT finance_clock_period_check CHECK (period_no >= 1),
    CONSTRAINT finance_clock_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- finance_periods — each period settled, once.
-- Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE finance_periods (
    period_no  bigint      PRIMARY KEY,
    settled_at timestamptz NOT NULL,

    CONSTRAINT finance_periods_no_check CHECK (period_no >= 1)
);

CREATE TRIGGER finance_periods_append_only
    BEFORE UPDATE OR DELETE ON finance_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER finance_periods_no_truncate
    BEFORE TRUNCATE ON finance_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- bank_fundings — what a national treasury moved into its national bank in
-- a period (country.bank_funding), once. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE bank_fundings (
    country_id            uuid        NOT NULL REFERENCES jurisdictions (id),
    period_no             bigint      NOT NULL,
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    funded_at             timestamptz NOT NULL,

    CONSTRAINT bank_fundings_pkey PRIMARY KEY (country_id, period_no),
    CONSTRAINT bank_fundings_amount_check CHECK (amount >= 0 AND (amount > 0) = (ledger_transaction_id IS NOT NULL))
);

CREATE TRIGGER bank_fundings_append_only
    BEFORE UPDATE OR DELETE ON bank_fundings
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER bank_fundings_no_truncate
    BEFORE TRUNCATE ON bank_fundings
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- loans — a loan of a national bank: to a player, or to a company through
-- its owner (player_id is who answers for it), secured by a property or not,
-- lent at a yearly rate fixed when taken and repaid in `periods` equal
-- instalments, one per finance period from first_period. principal_paid and
-- interest_paid follow the ledger to the unit; a default writes off what the
-- collateral did not recover.
-- ---------------------------------------------------------------------------
CREATE TABLE loans (
    id                          uuid        PRIMARY KEY,
    no                          bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    product                     text        NOT NULL,
    borrower_kind               text        NOT NULL,
    player_id                   uuid        NOT NULL REFERENCES players (id),
    company_id                  uuid        NULL REFERENCES companies (id),
    country_id                  uuid        NOT NULL REFERENCES jurisdictions (id),
    property_id                 uuid        NULL REFERENCES properties (id),
    principal                   bigint      NOT NULL,
    rate_bps                    int         NOT NULL,
    interest                    bigint      NOT NULL,
    periods                     int         NOT NULL,
    first_period                bigint      NOT NULL,
    paid_periods                int         NOT NULL DEFAULT 0,
    arrears                     int         NOT NULL DEFAULT 0,
    missed_total                int         NOT NULL DEFAULT 0,
    principal_paid              bigint      NOT NULL DEFAULT 0,
    interest_paid               bigint      NOT NULL DEFAULT 0,
    fees_due                    bigint      NOT NULL DEFAULT 0,
    fees_paid                   bigint      NOT NULL DEFAULT 0,
    status                      text        NOT NULL,
    recovered                   bigint      NOT NULL DEFAULT 0,
    written_off                 bigint      NOT NULL DEFAULT 0,
    disbursement_transaction_id uuid        NOT NULL,
    opened_at                   timestamptz NOT NULL,
    closed_at                   timestamptz NULL,
    updated_at                  timestamptz NOT NULL,

    CONSTRAINT loans_borrower_check CHECK (borrower_kind IN ('player', 'company')
        AND (borrower_kind = 'company') = (company_id IS NOT NULL)),
    CONSTRAINT loans_status_check CHECK (status IN ('active', 'repaid', 'defaulted')),
    CONSTRAINT loans_closed_check CHECK ((status = 'active') = (closed_at IS NULL)),
    CONSTRAINT loans_terms_check CHECK (principal > 0 AND rate_bps >= 0 AND interest >= 0 AND periods >= 1
        AND first_period >= 1),
    CONSTRAINT loans_progress_check CHECK (paid_periods BETWEEN 0 AND periods AND arrears >= 0
        AND paid_periods + arrears <= periods AND missed_total >= 0
        AND principal_paid BETWEEN 0 AND principal AND interest_paid BETWEEN 0 AND interest
        AND fees_due >= 0 AND fees_paid >= 0 AND recovered >= 0 AND written_off >= 0
        AND principal_paid + recovered + written_off <= principal),
    CONSTRAINT loans_settled_check CHECK (status <> 'repaid'
        OR (principal_paid = principal AND interest_paid = interest AND fees_due = 0)),
    CONSTRAINT loans_default_check CHECK (status <> 'defaulted' OR principal_paid + recovered + written_off = principal)
);

CREATE INDEX loans_player_idx ON loans (player_id, status);
CREATE INDEX loans_active_idx ON loans (country_id) WHERE status = 'active';
-- A property secures one running loan at a time.
CREATE UNIQUE INDEX loans_collateral_idx ON loans (property_id) WHERE status = 'active' AND property_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- loan_periods — one period of one loan, once: what fell due, what was paid
-- of it (principal, interest, late fees), the late fee charged, whether it
-- was missed and whether the loan defaulted. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE loan_periods (
    loan_id               uuid        NOT NULL REFERENCES loans (id),
    period_no             bigint      NOT NULL,
    due                   int         NOT NULL,
    paid                  int         NOT NULL,
    principal             bigint      NOT NULL,
    interest              bigint      NOT NULL,
    fees                  bigint      NOT NULL,
    new_fee               bigint      NOT NULL,
    missed                boolean     NOT NULL,
    defaulted             boolean     NOT NULL,
    at                    timestamptz NOT NULL,

    CONSTRAINT loan_periods_pkey PRIMARY KEY (loan_id, period_no),
    CONSTRAINT loan_periods_amounts_check CHECK (due >= 0 AND paid BETWEEN 0 AND due AND principal >= 0
        AND interest >= 0 AND fees >= 0 AND new_fee >= 0)
);

CREATE TRIGGER loan_periods_append_only
    BEFORE UPDATE OR DELETE ON loan_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER loan_periods_no_truncate
    BEFORE TRUNCATE ON loan_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- credit_events — a player's credit record, which the score is worked out
-- from: a loan opened, a period paid on time or missed, a loan repaid or
-- defaulted. Once per loan, kind and period. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE credit_events (
    id        uuid        PRIMARY KEY,
    player_id uuid        NOT NULL REFERENCES players (id),
    loan_id   uuid        NOT NULL REFERENCES loans (id),
    kind      text        NOT NULL,
    period_no bigint      NOT NULL,
    at        timestamptz NOT NULL,

    CONSTRAINT credit_events_kind_check CHECK (kind IN ('opened', 'on_time', 'missed', 'repaid', 'default')),
    CONSTRAINT credit_events_once_key UNIQUE (loan_id, kind, period_no)
);
CREATE INDEX credit_events_player_idx ON credit_events (player_id, at DESC);

CREATE TRIGGER credit_events_append_only
    BEFORE UPDATE OR DELETE ON credit_events
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER credit_events_no_truncate
    BEFORE TRUNCATE ON credit_events
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- savings_accounts — what a savings account held at the end of the last
-- period it was settled in: interest is paid on the least of that and what
-- it holds now, so money parked for a moment earns nothing.
-- ---------------------------------------------------------------------------
CREATE TABLE savings_accounts (
    player_id      uuid        PRIMARY KEY REFERENCES players (id),
    marked_balance bigint      NOT NULL,
    marked_period  bigint      NOT NULL,
    updated_at     timestamptz NOT NULL,

    CONSTRAINT savings_accounts_mark_check CHECK (marked_balance >= 0 AND marked_period >= 0)
);

-- ---------------------------------------------------------------------------
-- savings_interest — a period's interest on one savings account, once.
-- Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE savings_interest (
    player_id             uuid        NOT NULL REFERENCES players (id),
    period_no             bigint      NOT NULL,
    country_id            uuid        NOT NULL REFERENCES jurisdictions (id),
    balance               bigint      NOT NULL,
    rate_bps              int         NOT NULL,
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    paid_at               timestamptz NOT NULL,

    CONSTRAINT savings_interest_pkey PRIMARY KEY (player_id, period_no),
    CONSTRAINT savings_interest_amount_check CHECK (balance >= 0 AND rate_bps >= 0 AND amount >= 0
        AND (amount > 0) = (ledger_transaction_id IS NOT NULL))
);

CREATE TRIGGER savings_interest_append_only
    BEFORE UPDATE OR DELETE ON savings_interest
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER savings_interest_no_truncate
    BEFORE TRUNCATE ON savings_interest
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- insurance_policies — a player's policy with their country's insurance
-- fund: what it covers (and which property, for a property's policy), from
-- when it pays claims, and whether it still runs.
-- ---------------------------------------------------------------------------
CREATE TABLE insurance_policies (
    id          uuid        PRIMARY KEY,
    no          bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    player_id   uuid        NOT NULL REFERENCES players (id),
    product     text        NOT NULL,
    covers      text        NOT NULL,
    country_id  uuid        NOT NULL REFERENCES jurisdictions (id),
    property_id uuid        NULL REFERENCES properties (id),
    status      text        NOT NULL,
    started_at  timestamptz NOT NULL,
    claims_from timestamptz NOT NULL,
    ended_at    timestamptz NULL,
    end_reason  text        NULL,
    updated_at  timestamptz NOT NULL,

    CONSTRAINT insurance_policies_covers_check CHECK (covers IN ('hospital', 'war_damage')
        AND (covers = 'war_damage') = (property_id IS NOT NULL)),
    CONSTRAINT insurance_policies_status_check CHECK (status IN ('active', 'ended')
        AND (status = 'ended') = (ended_at IS NOT NULL) AND (ended_at IS NULL) = (end_reason IS NULL)
        AND (end_reason IS NULL OR end_reason IN ('cancelled', 'lapsed', 'property_gone')))
);
CREATE UNIQUE INDEX insurance_policies_one_idx ON insurance_policies
    (player_id, product, COALESCE(property_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE status = 'active';
CREATE INDEX insurance_policies_property_idx ON insurance_policies (property_id) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- insurance_premiums — a policy's premium for one period, once. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE insurance_premiums (
    policy_id             uuid        NOT NULL REFERENCES insurance_policies (id),
    period_no             bigint      NOT NULL,
    amount                bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    paid_at               timestamptz NOT NULL,

    CONSTRAINT insurance_premiums_pkey PRIMARY KEY (policy_id, period_no),
    CONSTRAINT insurance_premiums_amount_check CHECK (amount > 0)
);

CREATE TRIGGER insurance_premiums_append_only
    BEFORE UPDATE OR DELETE ON insurance_premiums
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER insurance_premiums_no_truncate
    BEFORE TRUNCATE ON insurance_premiums
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- insurance_claims — a claim a real loss made on a policy, once per source
-- (a hospital stay, a strike): the loss, what was due and what the fund
-- could pay. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE insurance_claims (
    id                    uuid        PRIMARY KEY,
    policy_id             uuid        NOT NULL REFERENCES insurance_policies (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    source                text        NOT NULL,
    loss                  bigint      NOT NULL,
    due                   bigint      NOT NULL,
    paid                  bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    claimed_at            timestamptz NOT NULL,

    CONSTRAINT insurance_claims_once_key UNIQUE (policy_id, source),
    CONSTRAINT insurance_claims_amount_check CHECK (loss >= 0 AND due BETWEEN 0 AND loss AND paid BETWEEN 0 AND due
        AND (paid > 0) = (ledger_transaction_id IS NOT NULL))
);

CREATE TRIGGER insurance_claims_append_only
    BEFORE UPDATE OR DELETE ON insurance_claims
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER insurance_claims_no_truncate
    BEFORE TRUNCATE ON insurance_claims
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- The stock exchange. A listed company's shares trade; a share offered is
-- locked on its holder's row; cost is what the holder paid for the shares
-- they hold (a founder's cost nothing).
-- ---------------------------------------------------------------------------
ALTER TABLE companies ADD COLUMN listed_at timestamptz NULL;
ALTER TABLE company_shareholders
    ADD COLUMN locked bigint NOT NULL DEFAULT 0,
    ADD COLUMN cost   bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT company_shareholders_locked_check CHECK (locked >= 0 AND locked <= shares AND cost >= 0);

-- stock_listings — a company's listing, once: who listed it, the shares
-- offered, at what price, and the fee. Append-only.
CREATE TABLE stock_listings (
    company_id            uuid        PRIMARY KEY REFERENCES companies (id),
    listed_by             uuid        NOT NULL REFERENCES players (id),
    float_shares          bigint      NOT NULL,
    price                 bigint      NOT NULL,
    book_per_share        bigint      NOT NULL,
    fee                   bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    listed_at             timestamptz NOT NULL,

    CONSTRAINT stock_listings_amounts_check CHECK (float_shares > 0 AND price > 0 AND book_per_share > 0
        AND fee >= 0 AND (fee > 0) = (ledger_transaction_id IS NOT NULL))
);

CREATE TRIGGER stock_listings_append_only
    BEFORE UPDATE OR DELETE ON stock_listings
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER stock_listings_no_truncate
    BEFORE TRUNCATE ON stock_listings
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- share_orders — an order on a company's book. A buy's money is set aside
-- from its owner's bank (share_escrow); a sell's shares are locked.
CREATE TABLE share_orders (
    id         uuid        PRIMARY KEY,
    no         bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id uuid        NOT NULL REFERENCES companies (id),
    side       text        NOT NULL,
    quantity   bigint      NOT NULL,
    filled     bigint      NOT NULL,
    unit_price bigint      NOT NULL,
    owner_id   uuid        NOT NULL REFERENCES players (id),
    status     text        NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    closed_at  timestamptz NULL,

    CONSTRAINT share_orders_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT share_orders_status_check CHECK (status IN ('open', 'filled', 'cancelled', 'expired')),
    CONSTRAINT share_orders_fill_check CHECK (quantity > 0 AND filled >= 0 AND filled <= quantity),
    CONSTRAINT share_orders_price_check CHECK (unit_price > 0),
    CONSTRAINT share_orders_closed_check CHECK ((status = 'open') = (closed_at IS NULL))
);
CREATE INDEX share_orders_book_idx ON share_orders (company_id, side) WHERE status = 'open';
CREATE INDEX share_orders_owner_idx ON share_orders (owner_id, created_at DESC);
CREATE INDEX share_orders_expiry_idx ON share_orders (expires_at) WHERE status = 'open';

-- share_trades — every fill. Append-only.
CREATE TABLE share_trades (
    id                    uuid        PRIMARY KEY,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    buy_order_id          uuid        NOT NULL REFERENCES share_orders (id),
    sell_order_id         uuid        NOT NULL REFERENCES share_orders (id),
    buyer_id              uuid        NOT NULL REFERENCES players (id),
    seller_id             uuid        NOT NULL REFERENCES players (id),
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    notional              bigint      NOT NULL,
    fee                   bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT share_trades_amounts_check CHECK (quantity > 0 AND unit_price > 0
        AND notional = quantity * unit_price AND fee >= 0 AND fee <= notional),
    CONSTRAINT share_trades_parties_check CHECK (buyer_id <> seller_id)
);
CREATE INDEX share_trades_company_idx ON share_trades (company_id, created_at DESC);
CREATE INDEX share_trades_buyer_idx ON share_trades (buyer_id);
CREATE INDEX share_trades_seller_idx ON share_trades (seller_id);

CREATE TRIGGER share_trades_append_only
    BEFORE UPDATE OR DELETE ON share_trades
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER share_trades_no_truncate
    BEFORE TRUNCATE ON share_trades
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- dividends — a dividend declared and paid at once (the record date is the
-- declaration: the company's book is locked while it is paid): the amount
-- taken from the company, the corporate tax on it, what each share received
-- and what all of them did. Append-only.
CREATE TABLE dividends (
    id                    uuid        PRIMARY KEY,
    no                    bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    company_id            uuid        NOT NULL REFERENCES companies (id),
    declared_by           uuid        NOT NULL REFERENCES players (id),
    amount                bigint      NOT NULL,
    tax                   bigint      NOT NULL,
    per_share             bigint      NOT NULL,
    total_shares          bigint      NOT NULL,
    paid                  bigint      NOT NULL,
    tax_transaction_id    uuid        NULL,
    declared_at           timestamptz NOT NULL,

    CONSTRAINT dividends_amounts_check CHECK (amount > 0 AND tax >= 0 AND per_share > 0 AND total_shares > 0
        AND paid = per_share * total_shares AND paid + tax <= amount
        AND (tax > 0) = (tax_transaction_id IS NOT NULL))
);
CREATE INDEX dividends_company_idx ON dividends (company_id, declared_at DESC);

CREATE TRIGGER dividends_append_only
    BEFORE UPDATE OR DELETE ON dividends
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER dividends_no_truncate
    BEFORE TRUNCATE ON dividends
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- dividend_payments — one holder's part of a dividend. Append-only.
CREATE TABLE dividend_payments (
    dividend_id           uuid   NOT NULL REFERENCES dividends (id),
    player_id             uuid   NOT NULL REFERENCES players (id),
    shares                bigint NOT NULL,
    amount                bigint NOT NULL,
    ledger_transaction_id uuid   NOT NULL,

    CONSTRAINT dividend_payments_pkey PRIMARY KEY (dividend_id, player_id),
    CONSTRAINT dividend_payments_amount_check CHECK (shares > 0 AND amount > 0)
);
CREATE INDEX dividend_payments_player_idx ON dividend_payments (player_id);

CREATE TRIGGER dividend_payments_append_only
    BEFORE UPDATE OR DELETE ON dividend_payments
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER dividend_payments_no_truncate
    BEFORE TRUNCATE ON dividend_payments
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- The gold dealer: its mid price, what it holds, and all the gold there is
-- (stock + Σ holdings = reserve, always); each period's price; each
-- player's gold; each trade.
-- ---------------------------------------------------------------------------
CREATE TABLE gold_dealer (
    id         int         PRIMARY KEY,
    price      bigint      NOT NULL,
    stock      bigint      NOT NULL,
    reserve    bigint      NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT gold_dealer_one_check CHECK (id = 1),
    CONSTRAINT gold_dealer_amounts_check CHECK (price > 0 AND stock >= 0 AND stock <= reserve)
);

CREATE TABLE gold_prices (
    period_no bigint      PRIMARY KEY,
    price     bigint      NOT NULL,
    net_grams bigint      NOT NULL,
    set_at    timestamptz NOT NULL,

    CONSTRAINT gold_prices_price_check CHECK (price > 0)
);

CREATE TRIGGER gold_prices_append_only
    BEFORE UPDATE OR DELETE ON gold_prices
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER gold_prices_no_truncate
    BEFORE TRUNCATE ON gold_prices
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

CREATE TABLE gold_holdings (
    player_id  uuid        PRIMARY KEY REFERENCES players (id),
    grams      bigint      NOT NULL,
    cost       bigint      NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT gold_holdings_amounts_check CHECK (grams >= 0 AND cost >= 0)
);

CREATE TABLE gold_trades (
    id                    uuid        PRIMARY KEY,
    player_id             uuid        NOT NULL REFERENCES players (id),
    side                  text        NOT NULL,
    grams                 bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    method                text        NULL,
    ledger_transaction_id uuid        NOT NULL,
    traded_at             timestamptz NOT NULL,

    CONSTRAINT gold_trades_side_check CHECK (side IN ('buy', 'sell') AND (side = 'buy') = (method IS NOT NULL)),
    CONSTRAINT gold_trades_amounts_check CHECK (grams > 0 AND unit_price > 0 AND total = grams * unit_price),
    CONSTRAINT gold_trades_method_check CHECK (method IS NULL OR method IN ('cash', 'card'))
);
CREATE INDEX gold_trades_player_idx ON gold_trades (player_id, traded_at DESC);
CREATE INDEX gold_trades_time_idx ON gold_trades (traded_at);

CREATE TRIGGER gold_trades_append_only
    BEFORE UPDATE OR DELETE ON gold_trades
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER gold_trades_no_truncate
    BEFORE TRUNCATE ON gold_trades
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- portfolio_marks — a player's portfolio gain (what their shares, gold and
-- savings are worth less what they put into them) at the last leaderboard,
-- which the investors' board measures the next one from.
-- ---------------------------------------------------------------------------
CREATE TABLE portfolio_marks (
    player_id uuid        PRIMARY KEY REFERENCES players (id),
    gain      bigint      NOT NULL,
    value     bigint      NOT NULL,
    marked_at timestamptz NOT NULL
);

-- ---------------------------------------------------------------------------
-- The watch notices wash trades on the exchange: one pair trading a company's
-- shares back and forth.
-- ---------------------------------------------------------------------------
ALTER TABLE watch_flags DROP CONSTRAINT watch_flags_rule_check;
ALTER TABLE watch_flags ADD CONSTRAINT watch_flags_rule_check CHECK (rule IN ('one_way_transfers', 'off_market_trade',
    'single_partner', 'command_rate', 'wash_trade'));

COMMIT;
