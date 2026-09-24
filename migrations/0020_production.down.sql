-- 0020_production, reversed. Every company good, design, research, license,
-- order, reverse engineering, listing, sale and supply purchase goes with it,
-- and the item journal's company rows with them: the journal's checks of
-- 0017 cannot hold a movement to or from a company. The ledger rows those
-- flows wrote stay (their reasons stay in the closed set's history).

BEGIN;

ALTER TABLE company_periods DROP CONSTRAINT company_periods_stock_check;
ALTER TABLE company_periods DROP COLUMN stock_units;

DROP TABLE supply_purchases;
DROP TABLE company_sales;
DROP TABLE company_listings;
DROP TABLE reverse_jobs;
DROP TABLE production_orders;
DROP TABLE technology_licenses;
DROP TABLE company_technologies;
DROP FUNCTION refuse_unpublishing();
DROP TABLE company_research;

ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only;
DELETE FROM item_movements
 WHERE from_org IS NOT NULL OR to_org IS NOT NULL
    OR piece_id IN (SELECT id FROM item_pieces WHERE org_id IS NOT NULL OR design_id IS NOT NULL);
ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only;
DROP INDEX item_movements_org_idx;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_sides_check;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_from_check;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_to_check;
ALTER TABLE item_movements DROP CONSTRAINT item_movements_org_check;
ALTER TABLE item_movements DROP COLUMN from_org_kind;
ALTER TABLE item_movements DROP COLUMN from_org;
ALTER TABLE item_movements DROP COLUMN to_org_kind;
ALTER TABLE item_movements DROP COLUMN to_org;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_sides_check CHECK (from_player IS NOT NULL OR to_player IS NOT NULL);
ALTER TABLE item_movements ADD CONSTRAINT item_movements_from_check CHECK ((from_player IS NULL) = (from_holding IS NULL));
ALTER TABLE item_movements ADD CONSTRAINT item_movements_to_check CHECK ((to_player IS NULL) = (to_holding IS NULL));

UPDATE crimes SET stolen_piece_id = NULL
 WHERE stolen_piece_id IN (SELECT id FROM item_pieces WHERE org_id IS NOT NULL OR design_id IS NOT NULL);
DELETE FROM auction_bids WHERE auction_id IN (
    SELECT a.id FROM auctions a JOIN item_pieces p ON p.id = a.piece_id WHERE p.org_id IS NOT NULL OR p.design_id IS NOT NULL);
DELETE FROM auctions WHERE piece_id IN (SELECT id FROM item_pieces WHERE org_id IS NOT NULL OR design_id IS NOT NULL);
DELETE FROM item_pieces WHERE org_id IS NOT NULL OR design_id IS NOT NULL;
DROP INDEX item_pieces_org_idx;
DROP INDEX item_pieces_design_idx;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_owner_check;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_holding_check;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_org_check;
ALTER TABLE item_pieces DROP COLUMN org_kind;
ALTER TABLE item_pieces DROP COLUMN org_id;
ALTER TABLE item_pieces DROP COLUMN design_id;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_holding_check CHECK (holding IN ('carried', 'escrow', 'gone'));
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_owner_check CHECK ((holding = 'gone') = (owner_id IS NULL));

DROP TABLE org_stacks;
DROP TABLE product_designs;

COMMIT;
