BEGIN;

-- The two new holdings are folded back into 'carried' first, so the old checks hold.
UPDATE item_stacks s SET holding = 'carried' WHERE holding IN ('claim', 'home')
    AND NOT EXISTS (SELECT 1 FROM item_stacks c WHERE c.player_id = s.player_id AND c.item_code = s.item_code AND c.holding = 'carried');
UPDATE item_stacks c SET quantity = c.quantity + x.q FROM (
    SELECT player_id, item_code, SUM(quantity) AS q FROM item_stacks WHERE holding IN ('claim', 'home') GROUP BY 1, 2) x
    WHERE c.player_id = x.player_id AND c.item_code = x.item_code AND c.holding = 'carried';
DELETE FROM item_stacks WHERE holding IN ('claim', 'home');
UPDATE item_pieces SET holding = 'carried' WHERE holding IN ('claim', 'home');

ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_owner_check;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_holding_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_holding_check
    CHECK (holding IN ('carried', 'escrow', 'gone', 'warehouse', 'listed'));
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_owner_check CHECK (
       (holding = 'gone' AND owner_id IS NULL AND org_id IS NULL)
    OR (holding IN ('carried', 'escrow') AND owner_id IS NOT NULL AND org_id IS NULL)
    OR (holding IN ('warehouse', 'listed') AND owner_id IS NULL AND org_id IS NOT NULL));
ALTER TABLE item_stacks DROP CONSTRAINT item_stacks_holding_check;
ALTER TABLE item_stacks ADD CONSTRAINT item_stacks_holding_check CHECK (holding IN ('carried', 'escrow'));

COMMIT;
