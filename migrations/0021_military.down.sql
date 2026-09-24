-- 0021_military, reversed. Every defence period, procurement, move,
-- military asset, sanction, treaty and diplomacy event goes with it, and so
-- does every piece a state holds, with its journal rows: 0020's checks
-- cannot hold a state as a holder. The ledger rows the defence periods and
-- procurements wrote stay (their reasons stay in the closed set's history).
-- A state treasury or defence fund with ledger history cannot be removed —
-- the ledger is append-only and references it — and then the old kind list
-- cannot be restored either: this down migration refuses rather than lose
-- money's history, as 0017's did for escrow.

BEGIN;

DROP TABLE diplomacy_events;
DROP TABLE treaties;
DROP TABLE sanctions;
DROP TABLE military_assets;
DROP TABLE military_moves;
DROP TABLE procurements;
DROP TABLE military_periods;
DROP TABLE military_clocks;

ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only;
DELETE FROM item_movements
 WHERE from_org_kind = 'state' OR to_org_kind = 'state'
    OR piece_id IN (SELECT id FROM item_pieces WHERE org_kind = 'state');
ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only;
DELETE FROM item_pieces WHERE org_kind = 'state';
DELETE FROM org_stacks WHERE org_kind = 'state';

ALTER TABLE item_movements DROP CONSTRAINT item_movements_org_check;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_org_check CHECK (
    (from_org_kind IS NULL) = (from_org IS NULL) AND (to_org_kind IS NULL) = (to_org IS NULL)
    AND (from_org_kind IS NULL OR from_org_kind IN ('company'))
    AND (to_org_kind IS NULL OR to_org_kind IN ('company')));
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_org_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_org_check CHECK (
    (org_kind IS NULL) = (org_id IS NULL) AND (org_kind IS NULL OR org_kind IN ('company')));
ALTER TABLE org_stacks DROP CONSTRAINT org_stacks_kind_check;
ALTER TABLE org_stacks ADD CONSTRAINT org_stacks_kind_check CHECK (org_kind IN ('company'));

DELETE FROM accounts a WHERE a.kind IN ('state_treasury', 'defence_fund')
   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.account_id = a.id);
ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow'));

COMMIT;
