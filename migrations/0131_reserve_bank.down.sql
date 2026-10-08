BEGIN;

DROP TABLE IF EXISTS village_macro_periods;
DROP TABLE IF EXISTS currency_claims;
DROP TABLE IF EXISTS currency_withdrawals;
DROP TABLE IF EXISTS currency_interventions;
ALTER TABLE fx_orders DROP CONSTRAINT IF EXISTS fx_orders_purpose_check, DROP COLUMN IF EXISTS purpose;
ALTER TABLE village_currency_state
    DROP CONSTRAINT IF EXISTS village_currency_state_wind_check,
    DROP CONSTRAINT IF EXISTS village_currency_state_flows_check,
    DROP COLUMN IF EXISTS intervention_in, DROP COLUMN IF EXISTS intervention_out, DROP COLUMN IF EXISTS retired_at,
    DROP COLUMN IF EXISTS wind_down_reason, DROP COLUMN IF EXISTS wind_down_ends_at, DROP COLUMN IF EXISTS wind_down_at;

COMMIT;
