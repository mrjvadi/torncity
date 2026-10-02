BEGIN;
DROP TABLE IF EXISTS settlement_lot_connections;
DROP INDEX IF EXISTS settlement_lots_lot_unique;
DELETE FROM settlement_lots WHERE released_at IS NOT NULL;
ALTER TABLE settlement_lots ADD CONSTRAINT settlement_lots_lot_unique UNIQUE (settlement_id, lot_x, lot_y);
ALTER TABLE settlement_lots
    DROP CONSTRAINT IF EXISTS settlement_lots_release_check,
    DROP COLUMN IF EXISTS refund_ledger_transaction_id,
    DROP COLUMN IF EXISTS refund_amount,
    DROP COLUMN IF EXISTS release_kind,
    DROP COLUMN IF EXISTS released_at;
DROP TABLE IF EXISTS settlement_road_reserve;
COMMIT;
