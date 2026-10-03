BEGIN;
ALTER TABLE market_orders DROP CONSTRAINT IF EXISTS market_orders_listing_fee_check, DROP COLUMN IF EXISTS listing_fee;
COMMIT;
