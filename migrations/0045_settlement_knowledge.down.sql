BEGIN;

DROP TABLE settlement_research;
DROP TABLE settlement_literacy;
DROP TABLE settlement_knowledge_owned;

ALTER TABLE item_movements DROP CONSTRAINT item_movements_org_check;
ALTER TABLE item_movements ADD CONSTRAINT item_movements_org_check CHECK (
    (from_org_kind IS NULL) = (from_org IS NULL) AND (to_org_kind IS NULL) = (to_org IS NULL)
    AND (from_org_kind IS NULL OR from_org_kind IN ('company', 'state'))
    AND (to_org_kind IS NULL OR to_org_kind IN ('company', 'state')));
ALTER TABLE item_pieces DROP CONSTRAINT item_pieces_org_check;
ALTER TABLE item_pieces ADD CONSTRAINT item_pieces_org_check CHECK (
    (org_kind IS NULL) = (org_id IS NULL) AND (org_kind IS NULL OR org_kind IN ('company', 'state')));
ALTER TABLE org_stacks DROP CONSTRAINT org_stacks_kind_check;
ALTER TABLE org_stacks ADD CONSTRAINT org_stacks_kind_check CHECK (org_kind IN ('company', 'state'));

COMMIT;
