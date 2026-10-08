BEGIN;

DROP INDEX IF EXISTS currency_issuance_log_reference_key;
ALTER TABLE currency_issuance_log
    DROP CONSTRAINT IF EXISTS currency_issuance_log_reference_check,
    DROP COLUMN IF EXISTS reference_type, DROP COLUMN IF EXISTS reference_id, DROP COLUMN IF EXISTS basis_sup;

DELETE FROM local_payments WHERE direction = 'transfer';
ALTER TABLE local_payments
    DROP CONSTRAINT IF EXISTS local_payments_payee_check, DROP CONSTRAINT IF EXISTS local_payments_cut_check,
    DROP COLUMN IF EXISTS payee_id, DROP COLUMN IF EXISTS cut_units;
ALTER TABLE local_payments DROP CONSTRAINT local_payments_direction_check;
ALTER TABLE local_payments ADD CONSTRAINT local_payments_direction_check CHECK (direction IN ('pay', 'collect'));

COMMIT;
