-- 0109_village_shop: the village shop's shelves, its morning deliveries and its
-- sales (docs/adr/0046 section 5, phase M2).
--
-- A village has a shop from the day it is founded: a counter that sells a few
-- basic goods in limited quantity (configs/content/village_shop.yml), and
-- more once it has built a shop building. The shop only sells; it never buys
-- anything back, so there is no buy-back column anywhere in these tables.
--
-- THE SHELF. village_shop_lines holds one row per settlement and line: the
-- stock now, and the running totals the verifier checks it against (stock =
-- delivered - sold - trimmed). A delivery adds the day's units up to the shelf's
-- cap; what the cap refuses is `trimmed`, so no unit is unaccounted for.
-- sold_today is the units sold since the last delivery; it is what a price
-- rises with inside a day, and it is reset at each delivery.
--
-- THE MORNING DELIVERY. village_shop_days is the idempotency fence of the
-- once-per-game-day delivery at merchant.restock_hour: (settlement, day) is
-- the primary key, so the village's tick, a player's first look of the day and
-- a second replica can all try, and exactly one delivery happens. outcome says
-- what happened: delivered, no_shopkeeper (nobody to keep the shop: it stays
-- frozen) or unpaid (the treasury could not pay the shopkeeper's wage: the
-- shopkeeper stayed home). delivered_value is the reference value of the goods
-- delivered and budget the most the supply rule allowed that day, both in minor
-- units: the verifier checks the first against the second.
--
-- THE SALE. village_shop_sales is one row per purchase: the unit price paid and
-- the reference price it was set against (so the verifier can prove every sale
-- was at or over the reference price and never over the ceiling), the tax and
-- the ledger transaction. village_shop_player_day is one player's purchases of
-- a line in a restock day, against the per-player daily cap.
--
-- THE HEAD'S TERMS. village_shop_terms holds what the village's head has set for
-- the shop, each inside the bounds of the config and each null until set: the
-- price ceiling, in basis points of the reference price, and the village's sales
-- tax on a purchase, in basis points of the price. (A village has no sales-tax
-- lever of the city kind, so the shop carries its own; the tax pays the
-- shopkeeper's wage.)
--
-- Conventions as 0058: instants are timestamptz in UTC, no DEFAULT now(), named
-- CHECK constraints, additive; never a wipe.
BEGIN;

CREATE TABLE village_shop_lines (
    settlement_id   uuid   NOT NULL REFERENCES cities (id),
    line            text   NOT NULL,
    stock           bigint NOT NULL,
    delivered_day   bigint NOT NULL,
    sold_today      bigint NOT NULL,
    delivered_total bigint NOT NULL,
    sold_total      bigint NOT NULL,
    trimmed_total   bigint NOT NULL,
    CONSTRAINT village_shop_lines_pk PRIMARY KEY (settlement_id, line),
    CONSTRAINT village_shop_lines_stock_check CHECK (stock >= 0 AND sold_today >= 0),
    CONSTRAINT village_shop_lines_totals_check CHECK (delivered_total >= 0 AND sold_total >= 0 AND trimmed_total >= 0)
);

CREATE TABLE village_shop_days (
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    day                   bigint      NOT NULL,
    outcome               text        NOT NULL,
    wage                  bigint      NOT NULL,
    delivered_value       bigint      NOT NULL,
    budget                bigint      NOT NULL,
    ledger_transaction_id uuid        NULL,
    at                    timestamptz NOT NULL,
    CONSTRAINT village_shop_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT village_shop_days_outcome_check CHECK (outcome IN ('delivered', 'no_shopkeeper', 'unpaid')),
    CONSTRAINT village_shop_days_amounts_check CHECK (wage >= 0 AND delivered_value >= 0 AND budget >= 0),
    -- Only a delivery pays a wage and moves goods; a frozen day moves nothing.
    CONSTRAINT village_shop_days_frozen_check CHECK (outcome = 'delivered' OR (wage = 0 AND delivered_value = 0 AND ledger_transaction_id IS NULL))
);

CREATE TABLE village_shop_sales (
    id                    uuid        PRIMARY KEY,
    settlement_id         uuid        NOT NULL REFERENCES cities (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    line                  text        NOT NULL,
    day                   bigint      NOT NULL,
    quantity              bigint      NOT NULL,
    unit_price            bigint      NOT NULL,
    reference_price       bigint      NOT NULL,
    total                 bigint      NOT NULL,
    tax                   bigint      NOT NULL,
    method                text        NOT NULL,
    ledger_transaction_id uuid        NOT NULL,
    created_at            timestamptz NOT NULL,
    CONSTRAINT village_shop_sales_amounts_check CHECK (quantity > 0 AND unit_price >= reference_price AND reference_price > 0
        AND total = unit_price * quantity AND tax >= 0)
);

CREATE INDEX village_shop_sales_by_day ON village_shop_sales (settlement_id, day, line);

CREATE TABLE village_shop_player_day (
    player_id     uuid   NOT NULL REFERENCES players (id),
    settlement_id uuid   NOT NULL REFERENCES cities (id),
    line          text   NOT NULL,
    day           bigint NOT NULL,
    quantity      bigint NOT NULL,
    CONSTRAINT village_shop_player_day_pk PRIMARY KEY (player_id, settlement_id, line, day),
    CONSTRAINT village_shop_player_day_check CHECK (quantity > 0)
);

CREATE TABLE village_shop_terms (
    settlement_id uuid        PRIMARY KEY REFERENCES cities (id),
    price_cap_bps bigint      NULL,
    tax_bps       bigint      NULL,
    set_by        uuid        NULL REFERENCES players (id),
    updated_at    timestamptz NOT NULL,
    CONSTRAINT village_shop_terms_cap_check CHECK (price_cap_bps IS NULL OR price_cap_bps >= 10000),
    CONSTRAINT village_shop_terms_tax_check CHECK (tax_bps IS NULL OR tax_bps >= 0)
);

COMMIT;
