BEGIN;

DROP TABLE IF EXISTS fx_rate_history;
DROP TABLE IF EXISTS fx_clock;
DROP TABLE IF EXISTS fx_trades;
DROP TABLE IF EXISTS fx_orders;

DROP INDEX accounts_one_row_per_owner_idx;
CREATE UNIQUE INDEX accounts_one_row_per_owner_idx
    ON accounts (kind, owner_id)
 WHERE kind NOT IN ('system_source', 'system_sink', 'foreign_holding');
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot'));

COMMIT;
