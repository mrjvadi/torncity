-- 0019_companies, reversed: every table and column 0019 added, in reverse
-- order of their dependencies. Company treasury accounts are left in accounts:
-- they hold ledger history, which is never deleted.

BEGIN;

DROP TABLE company_periods;
DROP TABLE company_market_periods;
DROP TABLE company_markets;

DROP INDEX work_shifts_company_idx;
ALTER TABLE work_shifts DROP COLUMN company_id;

DROP INDEX shift_sessions_company_working_idx;
ALTER TABLE shift_sessions
    DROP CONSTRAINT shift_sessions_wage_reserved_check,
    DROP COLUMN wage_reserved,
    DROP COLUMN company_id;

DROP INDEX employments_company_idx;
ALTER TABLE employments
    DROP COLUMN opening_id,
    DROP COLUMN company_id;

DROP TABLE company_applications;
DROP TABLE company_openings;
DROP TABLE company_shareholders;
DROP TABLE companies;

COMMIT;
