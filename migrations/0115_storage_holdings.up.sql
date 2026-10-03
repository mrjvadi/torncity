-- 0115_storage_holdings: the holding slot and the home store (storage and market
-- audit 2026-10-03, phase P1; docs/adr/0040 section 6).
--
-- Until now a player's goods sat in two holdings, 'carried' and 'escrow'. Two
-- more places are needed once carrying has a limit:
--   'claim' - the holding slot: goods that arrived (a reward, a delivery, a
--             gift, a won auction) when the bags were full. They wait there,
--             still the player's, and are claimed when there is room. Nothing
--             that arrives is ever refused and lost.
--   'home'  - «انبار من»: what the player keeps in their own house, cottage or
--             shed. Not carried, so it takes no room in the bags.
-- Both are the player's own holdings, so the stack and piece tables only need
-- their checks widened; the journal already takes any holding text.
--
-- Conventions as 0058. Additive; never a wipe.
BEGIN;

ALTER TABLE item_stacks DROP CONSTRAINT item_stacks_holding_check;
ALTER TABLE item_stacks ADD CONSTRAINT item_stacks_holding_check
    CHECK (holding IN ('carried', 'escrow', 'claim', 'home'));

ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_holding_check;
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_owner_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_holding_check
    CHECK (holding IN ('carried', 'escrow', 'claim', 'home', 'gone', 'warehouse', 'listed'));
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_owner_check CHECK (
       (holding = 'gone' AND owner_id IS NULL AND org_id IS NULL)
    OR (holding IN ('carried', 'escrow', 'claim', 'home') AND owner_id IS NOT NULL AND org_id IS NULL)
    OR (holding IN ('warehouse', 'listed') AND owner_id IS NULL AND org_id IS NOT NULL));

COMMIT;
