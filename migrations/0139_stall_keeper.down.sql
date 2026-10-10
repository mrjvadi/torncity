BEGIN;
ALTER TABLE market_trades DROP CONSTRAINT market_trades_keeper_cut_check;
ALTER TABLE market_trades DROP COLUMN keeper_cut;
DROP TABLE stall_keepers;
COMMIT;
