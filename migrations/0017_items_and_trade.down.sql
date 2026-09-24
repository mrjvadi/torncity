-- 0017_items_and_trade, reversed. Ledger rows posted under the trade
-- reasons stay: the ledger is append-only and balanced on its own. The
-- escrow accounts are removed with their kind; a verifier run before this
-- down migration shows whether any still held money (it must not).

BEGIN;

ALTER TABLE crimes
    DROP COLUMN stolen_qty,
    DROP COLUMN stolen_piece_id,
    DROP COLUMN stolen_item,
    DROP COLUMN gear_solve_bps;

ALTER TABLE auctions DROP CONSTRAINT auctions_high_bid_fkey;
DROP TABLE auction_bids;
DROP TABLE auctions;
DROP TABLE market_trades;
DROP TABLE market_orders;
DROP TABLE shop_sales;
DROP TABLE shop_shelves;
DROP TABLE item_cooldowns;
DROP TABLE item_movements;
DROP TABLE item_pieces;
DROP TABLE item_stacks;

-- An escrow account with ledger history cannot be removed (the ledger is
-- append-only and references it), and then the old kind list cannot be
-- restored either: this down migration refuses rather than lose history.
DELETE FROM accounts a WHERE a.kind = 'player_escrow'
   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.account_id = a.id);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source'));

COMMIT;
