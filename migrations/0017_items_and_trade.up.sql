-- 0017_items_and_trade — goods, the city shops, the player market and the
-- auction house.
-- Rules: internal/domain/inventory, shop, market, auction. Content:
-- configs/content/items.yml and shops.yml (stored in content_documents).
-- Policy: city.sales_tax and city.market_fee (governance.yml, through the
-- resolver). Money: every price, tax, escrow, trade and fee moves through the
-- ledger of 0006 under its own reason (docs/adr/0009-economic-control.md).
--
-- THE ITEM JOURNAL. Goods move the way money does: item_movements is
-- append-only, one row per movement, and names why. A unit enters the world
-- only from a recorded origin (a shop's sale, a crime's loot, a reward) and
-- leaves it only by a recorded end (used, worn out, sold back, confiscated,
-- dropped). The holdings — item_stacks and item_pieces — are what the
-- journal adds up to; `admin economy verify` checks that they do.
--
-- Escrow: a buy order's money and a standing bid sit in the player's
-- player_escrow account (new account kind below); a sell order's goods and an
-- auctioned piece sit in the holding 'escrow'. Both are still the player's,
-- and both are checked against the open orders and bids by the verifier.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- A player's escrow account for the market and the auction house.
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow'));

-- ---------------------------------------------------------------------------
-- item_stacks — units of one good one player holds in one place.
-- ---------------------------------------------------------------------------
CREATE TABLE item_stacks (
    player_id  uuid   NOT NULL REFERENCES players (id),
    item_code  text   NOT NULL,
    holding    text   NOT NULL,
    quantity   bigint NOT NULL,

    CONSTRAINT item_stacks_pkey PRIMARY KEY (player_id, item_code, holding),
    CONSTRAINT item_stacks_holding_check CHECK (holding IN ('carried', 'escrow')),
    -- An empty stack is deleted, never kept at zero.
    CONSTRAINT item_stacks_quantity_check CHECK (quantity > 0)
);

-- ---------------------------------------------------------------------------
-- item_pieces — unique items, each with a serial and a provenance.
-- ---------------------------------------------------------------------------
CREATE TABLE item_pieces (
    id          uuid        PRIMARY KEY,
    serial      text        NOT NULL,
    item_code   text        NOT NULL,
    archetype   text        NOT NULL,
    quality     int         NOT NULL,
    uses_left   int         NOT NULL,
    owner_id    uuid        NULL REFERENCES players (id),
    holding     text        NOT NULL,
    -- Provenance: supply (a shop's sale), loot (a crime), grant (a reward).
    origin      text        NOT NULL,
    origin_ref  uuid        NOT NULL,
    created_at  timestamptz NOT NULL,
    gone_at     timestamptz NULL,

    CONSTRAINT item_pieces_serial_key UNIQUE (serial),
    CONSTRAINT item_pieces_holding_check CHECK (holding IN ('carried', 'escrow', 'gone')),
    CONSTRAINT item_pieces_owner_check CHECK ((holding = 'gone') = (owner_id IS NULL)),
    CONSTRAINT item_pieces_gone_check CHECK ((holding = 'gone') = (gone_at IS NOT NULL)),
    CONSTRAINT item_pieces_origin_check CHECK (origin IN ('supply', 'loot', 'grant', 'production')),
    CONSTRAINT item_pieces_quality_check CHECK (quality BETWEEN 0 AND 100),
    CONSTRAINT item_pieces_uses_check CHECK (uses_left >= 0)
);

CREATE INDEX item_pieces_owner_idx ON item_pieces (owner_id, holding) WHERE owner_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- item_movements — the append-only item journal.
-- ---------------------------------------------------------------------------
CREATE TABLE item_movements (
    id             uuid        PRIMARY KEY,
    item_code      text        NOT NULL,
    piece_id       uuid        NULL REFERENCES item_pieces (id),
    quantity       bigint      NOT NULL,
    from_player    uuid        NULL REFERENCES players (id),
    from_holding   text        NULL,
    to_player      uuid        NULL REFERENCES players (id),
    to_holding     text        NULL,
    reason         text        NOT NULL,
    reference_type text        NULL,
    reference_id   uuid        NULL,
    created_at     timestamptz NOT NULL,

    CONSTRAINT item_movements_quantity_check CHECK (quantity > 0),
    CONSTRAINT item_movements_piece_quantity_check CHECK (piece_id IS NULL OR quantity = 1),
    -- A movement goes somewhere or comes from somewhere, and its sides are
    -- complete.
    CONSTRAINT item_movements_sides_check CHECK (from_player IS NOT NULL OR to_player IS NOT NULL),
    CONSTRAINT item_movements_from_check CHECK ((from_player IS NULL) = (from_holding IS NULL)),
    CONSTRAINT item_movements_to_check CHECK ((to_player IS NULL) = (to_holding IS NULL)),
    CONSTRAINT item_movements_reference_check CHECK ((reference_type IS NULL) = (reference_id IS NULL))
);

CREATE INDEX item_movements_piece_idx ON item_movements (piece_id, created_at) WHERE piece_id IS NOT NULL;
CREATE INDEX item_movements_player_idx ON item_movements (to_player, created_at DESC);
CREATE INDEX item_movements_reference_idx ON item_movements (reference_type, reference_id);

CREATE TRIGGER item_movements_append_only
    BEFORE UPDATE OR DELETE ON item_movements
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER item_movements_no_truncate
    BEFORE TRUNCATE ON item_movements
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- item_cooldowns — when a player last used a good of a cooldown group.
-- ---------------------------------------------------------------------------
CREATE TABLE item_cooldowns (
    player_id  uuid        NOT NULL REFERENCES players (id),
    cool_group text        NOT NULL,
    used_at    timestamptz NOT NULL,

    CONSTRAINT item_cooldowns_pkey PRIMARY KEY (player_id, cool_group)
);

-- ---------------------------------------------------------------------------
-- shop_shelves and shop_sales — the city shops.
-- ---------------------------------------------------------------------------
CREATE TABLE shop_shelves (
    city_id      uuid        NOT NULL REFERENCES cities (id),
    shop_code    text        NOT NULL,
    item_code    text        NOT NULL,
    stock        bigint      NOT NULL,
    restocked_at timestamptz NOT NULL,

    CONSTRAINT shop_shelves_pkey PRIMARY KEY (city_id, shop_code, item_code),
    CONSTRAINT shop_shelves_stock_check CHECK (stock >= 0)
);

CREATE TABLE shop_sales (
    id                    uuid        PRIMARY KEY,
    player_id             uuid        NOT NULL REFERENCES players (id),
    city_id               uuid        NOT NULL REFERENCES cities (id),
    shop_code             text        NOT NULL,
    item_code             text        NOT NULL,
    direction             text        NOT NULL,
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    total                 bigint      NOT NULL,
    tax                   bigint      NOT NULL,
    method                text        NULL,
    ledger_transaction_id uuid        NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT shop_sales_direction_check CHECK (direction IN ('buy', 'sell')),
    CONSTRAINT shop_sales_amounts_check CHECK (quantity > 0 AND unit_price >= 0 AND total >= 0 AND tax >= 0)
);

-- Demand pricing counts a shelf's recent sales, like travels_demand_idx.
CREATE INDEX shop_sales_demand_idx ON shop_sales (city_id, shop_code, item_code, created_at) WHERE direction = 'buy';

-- ---------------------------------------------------------------------------
-- market_orders and market_trades — the player market, per city and good.
-- ---------------------------------------------------------------------------
CREATE TABLE market_orders (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY,
    city_id        uuid        NOT NULL REFERENCES cities (id),
    item_code      text        NOT NULL,
    side           text        NOT NULL,
    order_type     text        NOT NULL,
    quantity       bigint      NOT NULL,
    filled         bigint      NOT NULL,
    unit_price     bigint      NOT NULL,
    owner_id       uuid        NOT NULL REFERENCES players (id),
    funding        text        NULL,
    status         text        NOT NULL,
    game_action_id uuid        NULL,
    created_at     timestamptz NOT NULL,
    expires_at     timestamptz NOT NULL,
    closed_at      timestamptz NULL,

    CONSTRAINT market_orders_no_key UNIQUE (no),
    CONSTRAINT market_orders_side_check CHECK (side IN ('buy', 'sell')),
    CONSTRAINT market_orders_type_check CHECK (order_type IN ('limit', 'market')),
    CONSTRAINT market_orders_status_check CHECK (status IN ('open', 'filled', 'cancelled', 'expired')),
    CONSTRAINT market_orders_fill_check CHECK (quantity > 0 AND filled >= 0 AND filled <= quantity),
    CONSTRAINT market_orders_price_check CHECK (unit_price > 0),
    CONSTRAINT market_orders_funding_check CHECK ((side = 'buy') = (funding IS NOT NULL)),
    CONSTRAINT market_orders_closed_check CHECK ((status = 'open') = (closed_at IS NULL))
);

CREATE INDEX market_orders_book_idx ON market_orders (city_id, item_code, side) WHERE status = 'open';
CREATE INDEX market_orders_owner_idx ON market_orders (owner_id, created_at DESC);

CREATE TABLE market_trades (
    id                    uuid        PRIMARY KEY,
    city_id               uuid        NOT NULL REFERENCES cities (id),
    item_code             text        NOT NULL,
    buy_order_id          uuid        NOT NULL REFERENCES market_orders (id),
    sell_order_id         uuid        NOT NULL REFERENCES market_orders (id),
    buyer_id              uuid        NOT NULL REFERENCES players (id),
    seller_id             uuid        NOT NULL REFERENCES players (id),
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    notional              bigint      NOT NULL,
    fee                   bigint      NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT market_trades_amounts_check CHECK (quantity > 0 AND unit_price > 0
        AND notional = quantity * unit_price AND fee >= 0 AND fee <= notional),
    CONSTRAINT market_trades_parties_check CHECK (buyer_id <> seller_id)
);

CREATE INDEX market_trades_book_idx ON market_trades (city_id, item_code, created_at DESC);

CREATE TRIGGER market_trades_append_only
    BEFORE UPDATE OR DELETE ON market_trades
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER market_trades_no_truncate
    BEFORE TRUNCATE ON market_trades
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- auctions and auction_bids — the auction house, for unique items.
-- ---------------------------------------------------------------------------
CREATE TABLE auctions (
    id             uuid        PRIMARY KEY,
    no             bigint      GENERATED ALWAYS AS IDENTITY,
    city_id        uuid        NOT NULL REFERENCES cities (id),
    seller_id      uuid        NOT NULL REFERENCES players (id),
    piece_id       uuid        NOT NULL REFERENCES item_pieces (id),
    item_code      text        NOT NULL,
    reserve        bigint      NOT NULL,
    step_bps       int         NOT NULL,
    min_step       bigint      NOT NULL,
    status         text        NOT NULL,
    high_bid_id    uuid        NULL,
    game_action_id uuid        NOT NULL,
    opens_at       timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    closed_at      timestamptz NULL,
    fee            bigint      NOT NULL,

    CONSTRAINT auctions_no_key UNIQUE (no),
    CONSTRAINT auctions_status_check CHECK (status IN ('open', 'sold', 'unsold')),
    CONSTRAINT auctions_terms_check CHECK (reserve > 0 AND step_bps >= 0 AND min_step > 0 AND ends_at > opens_at),
    CONSTRAINT auctions_closed_check CHECK ((status = 'open') = (closed_at IS NULL)),
    CONSTRAINT auctions_fee_check CHECK (fee >= 0)
);

-- One open auction per piece, whatever races to open a second.
CREATE UNIQUE INDEX auctions_one_open_per_piece_idx ON auctions (piece_id) WHERE status = 'open';
CREATE INDEX auctions_city_open_idx ON auctions (city_id, ends_at) WHERE status = 'open';
CREATE INDEX auctions_seller_idx ON auctions (seller_id, opens_at DESC);

CREATE TABLE auction_bids (
    id                    uuid        PRIMARY KEY,
    auction_id            uuid        NOT NULL REFERENCES auctions (id),
    bidder_id             uuid        NOT NULL REFERENCES players (id),
    amount                bigint      NOT NULL,
    method                text        NOT NULL,
    status                text        NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT auction_bids_amount_check CHECK (amount > 0),
    CONSTRAINT auction_bids_method_check CHECK (method IN ('cash', 'card')),
    CONSTRAINT auction_bids_status_check CHECK (status IN ('standing', 'outbid', 'won'))
);

-- One standing bid per auction.
CREATE UNIQUE INDEX auction_bids_one_standing_idx ON auction_bids (auction_id) WHERE status = 'standing';
CREATE INDEX auction_bids_bidder_idx ON auction_bids (bidder_id, created_at DESC);

ALTER TABLE auctions ADD CONSTRAINT auctions_high_bid_fkey FOREIGN KEY (high_bid_id) REFERENCES auction_bids (id);

-- ---------------------------------------------------------------------------
-- crimes: what gear did, and what was taken beside money.
-- ---------------------------------------------------------------------------
ALTER TABLE crimes
    ADD COLUMN gear_solve_bps  int    NOT NULL DEFAULT 0,
    ADD COLUMN stolen_item     text   NULL,
    ADD COLUMN stolen_piece_id uuid   NULL REFERENCES item_pieces (id),
    ADD COLUMN stolen_qty      bigint NULL;

COMMENT ON COLUMN crimes.gear_solve_bps IS
    'What the thief''s gear added to the solve chance of a report of this attempt (gloves: negative), fixed when the crime was committed.';

COMMIT;
