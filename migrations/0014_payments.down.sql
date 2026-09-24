-- 0014_payments, reversed. Ledger rows paid by card stay: they are ordinary
-- transactions on player_bank under the charge's own reason.

BEGIN;

ALTER TABLE transport_modes DROP COLUMN payment;
DROP TABLE content_documents;

COMMIT;
