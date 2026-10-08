-- 0129_local_obligations_2b - the rest of a settlement's obligations settle in its own money, and a unit
-- paid to the NPC economy is burnt (roadmap 2.19 phase 2b, ADR 0033 sections 6.9, 6.10 and 6.11).
--
--   local_payments gains direction 'transfer': one player paying another in the local money (tuition,
--   rent, a face-to-face payment); payee_id is the receiving player and cut_units the share of the units
--   that went on to the settlement's treasury (the tax of a tuition fee).
--   currency_issuance_log gains the flow row a burn stands for (reference_type, reference_id: one burn per
--   row, the idempotency fence of a redelivered purchase) and basis_sup, the basis a burn took away.
BEGIN;

ALTER TABLE local_payments DROP CONSTRAINT local_payments_direction_check;
ALTER TABLE local_payments ADD CONSTRAINT local_payments_direction_check CHECK (direction IN ('pay', 'collect', 'transfer'));
ALTER TABLE local_payments
    ADD COLUMN payee_id  uuid,
    ADD COLUMN cut_units bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT local_payments_payee_check CHECK ((direction = 'transfer') = (payee_id IS NOT NULL)),
    ADD CONSTRAINT local_payments_cut_check CHECK (cut_units >= 0 AND cut_units < units);

ALTER TABLE currency_issuance_log
    ADD COLUMN reference_type text,
    ADD COLUMN reference_id   uuid,
    ADD COLUMN basis_sup      bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT currency_issuance_log_reference_check CHECK ((reference_type IS NULL) = (reference_id IS NULL));
CREATE UNIQUE INDEX currency_issuance_log_reference_key ON currency_issuance_log (reference_type, reference_id) WHERE reference_type IS NOT NULL;

COMMIT;
