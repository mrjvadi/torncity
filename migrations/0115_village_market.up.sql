-- 0115_village_market: the village book (storage and market audit 2026-10-03,
-- phase P3; docs/adr/0040 section 5).
--
-- A founded settlement's market is its own: a stall costs a listing fee when an
-- order is placed (not refunded when it is cancelled) and every trade pays dues,
-- both to the settlement treasury, at rates the market warden sets within
-- bounds (policy levers <level>.market_listing_fee_bps and market_dues_bps).
-- The dues are the trade's own fee column, now routed to the treasury instead of
-- the sink. The listing fee is new and is kept in its own table (one row per
-- order that paid one), so the orders table is unchanged and `admin economy
-- verify` can set the ledger against the rows.
--
-- Conventions as 0058. Additive; never a wipe.
BEGIN;

CREATE TABLE market_listing_fees (
    order_id uuid        PRIMARY KEY REFERENCES market_orders (id),
    city_id  uuid        NOT NULL REFERENCES cities (id),
    fee      bigint      NOT NULL,
    at       timestamptz NOT NULL,

    CONSTRAINT market_listing_fees_fee_check CHECK (fee > 0)
);

CREATE INDEX market_listing_fees_city_idx ON market_listing_fees (city_id);

COMMIT;
