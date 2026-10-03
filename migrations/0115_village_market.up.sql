-- 0115_village_market: the village book (storage and market audit 2026-10-03,
-- phase P3; docs/adr/0040 section 5).
--
-- A founded settlement's market is its own: a stall costs a listing fee when an
-- order is placed (not refunded when it is cancelled) and every trade pays dues,
-- both to the settlement treasury, at rates the market warden sets within
-- bounds (policy levers city.market_listing_fee_bps and city.market_dues_bps).
-- The dues are the trade's own fee column, now routed to the treasury instead of
-- the sink; the listing fee is new, and is kept on the order so `admin economy
-- verify` can set the ledger against the rows.
--
-- Conventions as 0058. Additive; never a wipe.
BEGIN;

ALTER TABLE market_orders
    ADD COLUMN listing_fee bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT market_orders_listing_fee_check CHECK (listing_fee >= 0);

COMMIT;
