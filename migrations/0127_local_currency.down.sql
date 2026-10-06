BEGIN;

DROP TABLE IF EXISTS currency_issuance_log;
DROP TABLE IF EXISTS village_currency_state;
DELETE FROM accounts WHERE kind IN ('foreign_holding', 'reserve_pot') AND balance = 0 AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.account_id = accounts.id);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings'));

COMMIT;
