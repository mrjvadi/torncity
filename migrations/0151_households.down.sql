BEGIN;

UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g WHERE g->>'p' <> 'citizen.admit'), '[]'::jsonb)
 WHERE grants @> '[{"p": "citizen.admit"}]'::jsonb;

DELETE FROM village_shop_sales WHERE household_id IS NOT NULL;
ALTER TABLE village_shop_sales DROP CONSTRAINT village_shop_sales_buyer_check;
ALTER TABLE village_shop_sales DROP COLUMN household_id;
ALTER TABLE village_shop_sales ALTER COLUMN player_id SET NOT NULL;

DELETE FROM accounts WHERE kind = 'household_wallet';
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow'));

DROP TABLE household_rentals;
DROP TABLE household_events;
DROP TABLE household_ticks;
DROP TABLE household_days;
DROP TABLE households;
DROP TABLE household_settlements;
COMMIT;
