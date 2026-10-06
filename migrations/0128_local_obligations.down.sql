BEGIN;

ALTER TABLE village_currency_state DROP CONSTRAINT IF EXISTS village_currency_state_fee_check, DROP COLUMN IF EXISTS fx_fee_bps;
DROP TABLE IF EXISTS currency_desk_trades;
DROP TABLE IF EXISTS local_payments;

COMMIT;
