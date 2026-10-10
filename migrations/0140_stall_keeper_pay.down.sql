BEGIN;
DROP TABLE stall_keeper_wages;
ALTER TABLE stall_keepers DROP CONSTRAINT stall_keepers_pay_check;
ALTER TABLE stall_keepers DROP COLUMN ended_reason;
ALTER TABLE stall_keepers DROP COLUMN daily_wage;
ALTER TABLE stall_keepers DROP COLUMN pay;
COMMIT;
