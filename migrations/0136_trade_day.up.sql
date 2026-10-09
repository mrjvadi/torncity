-- 0136_trade_day: the settlement earns from its surplus (ADR 0049, prerequisite A1).
--
-- A settlement's treasury used to live on its residents' gifts and taxes and its founding grant; its own work never
-- reached the ledger. Now, once a local day, when the clerk of the market is at his post and the head has put goods on
-- sale, a travelling trader buys the surplus above what the head keeps, at a price below the reference price and up to
-- what one visit carries. The goods leave the stock (item reason exported); the money is the faucet export_sale, bounded
-- by that cap; the clerk is paid by the treasury (market_clerk_wage, to the sink).
--
--   trade_orders      the head's standing orders: for an item, keep this much, sell the rest (on or off)
--   trade_days        the fence of a settlement's local day: one row per day a visit was judged, with its cap, its gross,
--                     the clerk's wage and the two ledger transactions
--   trade_day_lines   what was sold, at which price and which reference price (the arbitrage guard of the verifier)
--
-- No foreign key to cities: like the other days they outlive a deleted test settlement. Conventions as 0114.
BEGIN;

CREATE TABLE trade_orders (
    settlement_id uuid        NOT NULL,
    item          text        NOT NULL,
    keep          bigint      NOT NULL DEFAULT 0,
    on_sale       boolean     NOT NULL DEFAULT true,
    updated_by    uuid        NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT trade_orders_pk PRIMARY KEY (settlement_id, item),
    CONSTRAINT trade_orders_keep_check CHECK (keep >= 0 AND length(btrim(item)) > 0)
);

CREATE TABLE trade_days (
    settlement_id   uuid        NOT NULL,
    day             bigint      NOT NULL,
    outcome         text        NOT NULL,
    residents       bigint      NOT NULL DEFAULT 0,
    cap             bigint      NOT NULL DEFAULT 0,
    gross           bigint      NOT NULL DEFAULT 0,
    wage            bigint      NOT NULL DEFAULT 0,
    sale_tx         uuid        NULL,
    wage_tx         uuid        NULL,
    at              timestamptz NOT NULL,

    CONSTRAINT trade_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT trade_days_outcome_check CHECK (outcome IN ('sold', 'no_clerk', 'no_wage', 'nothing', 'too_little', 'no_road')),
    CONSTRAINT trade_days_amounts_check CHECK (cap >= 0 AND gross >= 0 AND wage >= 0 AND gross <= cap),
    CONSTRAINT trade_days_sold_check CHECK ((outcome = 'sold') = (gross > 0 AND sale_tx IS NOT NULL)),
    CONSTRAINT trade_days_wage_check CHECK ((wage > 0) = (wage_tx IS NOT NULL))
);

CREATE TABLE trade_day_lines (
    settlement_id   uuid   NOT NULL,
    day             bigint NOT NULL,
    item            text   NOT NULL,
    qty             bigint NOT NULL,
    unit_price      bigint NOT NULL,
    reference_price bigint NOT NULL,

    CONSTRAINT trade_day_lines_pk PRIMARY KEY (settlement_id, day, item),
    CONSTRAINT trade_day_lines_fk FOREIGN KEY (settlement_id, day) REFERENCES trade_days (settlement_id, day) ON DELETE CASCADE,
    CONSTRAINT trade_day_lines_check CHECK (qty > 0 AND unit_price > 0 AND unit_price <= reference_price)
);

-- The new charter permission trade.export (put goods on sale): the head office of a stored charter that may start
-- research gets it with the same hand (a charter that took research.start from it keeps that choice).
UPDATE charter_offices
   SET grants = grants || '[{"p": "trade.export"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "research.start"}]'::jsonb AND NOT grants @> '[{"p": "trade.export"}]'::jsonb;

COMMIT;
