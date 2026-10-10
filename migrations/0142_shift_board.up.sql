-- 0142_shift_board: the food an employer's hands ate out of his home store in a shift of his own workplace, by item (ADR 0066).
-- The verifier compares it with the item journal (reason board_eaten). Zero for every shift that ate from the village pot.
BEGIN;
ALTER TABLE settlement_shifts ADD COLUMN board jsonb NOT NULL DEFAULT '{}'::jsonb;
COMMIT;
