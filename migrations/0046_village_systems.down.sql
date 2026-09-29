BEGIN;

DROP TABLE settlement_knowledge_holder_counts;

ALTER TABLE settlement_literacy DROP COLUMN pending_action_id;

ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_demolished_shape_check;
ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_completed_shape_check;
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_completed_shape_check
    CHECK ((status = 'complete') = (completed_at IS NOT NULL));
ALTER TABLE settlement_buildings DROP COLUMN demolished_at;

ALTER TABLE settlement_buildings DROP CONSTRAINT settlement_buildings_status_check;
ALTER TABLE settlement_buildings ADD CONSTRAINT settlement_buildings_status_check
    CHECK (status IN ('queued', 'building', 'complete'));

COMMIT;
