-- 0029_finance, reversed. The finance tables go; companies are no longer
-- listed and shares no longer locked; the watch forgets wash trades. Money
-- finance moved stays where the ledger put it (its reasons stay in the closed
-- set's history); an account of a finance kind that never moved money is
-- dropped, and the old kinds are restored — which fails, on purpose, while a
-- finance account still holds entries.

BEGIN;

DELETE FROM watch_flags WHERE rule = 'wash_trade';
ALTER TABLE watch_flags DROP CONSTRAINT watch_flags_rule_check;
ALTER TABLE watch_flags ADD CONSTRAINT watch_flags_rule_check CHECK (rule IN ('one_way_transfers', 'off_market_trade',
    'single_partner', 'command_rate'));

DROP TABLE portfolio_marks;
DROP TABLE gold_trades;
DROP TABLE gold_holdings;
DROP TABLE gold_prices;
DROP TABLE gold_dealer;
DROP TABLE dividend_payments;
DROP TABLE dividends;
DROP TABLE share_trades;
DROP TABLE share_orders;
DROP TABLE stock_listings;
ALTER TABLE company_shareholders DROP CONSTRAINT company_shareholders_locked_check;
ALTER TABLE company_shareholders DROP COLUMN cost;
ALTER TABLE company_shareholders DROP COLUMN locked;
ALTER TABLE companies DROP COLUMN listed_at;

DROP TABLE insurance_claims;
DROP TABLE insurance_premiums;
DROP TABLE insurance_policies;
DROP TABLE savings_interest;
DROP TABLE savings_accounts;
DROP TABLE credit_events;
DROP TABLE loan_periods;
DROP TABLE loans;
DROP TABLE bank_fundings;
DROP TABLE finance_periods;
DROP TABLE finance_clock;

DELETE FROM accounts a WHERE a.kind IN ('national_bank', 'insurance_fund', 'player_savings')
   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.account_id = a.id);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund'));

COMMIT;
