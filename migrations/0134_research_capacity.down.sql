BEGIN;

DROP TABLE research_pacts;
DROP TABLE settlement_experience;
DROP TABLE research_posts;
DROP TABLE research_day_buildings;
DROP TABLE research_days;

ALTER TABLE settlement_research
    DROP CONSTRAINT settlement_research_share_check,
    DROP CONSTRAINT settlement_research_discount_check,
    DROP CONSTRAINT settlement_research_ahead_check,
    DROP CONSTRAINT settlement_research_speed_check,
    DROP CONSTRAINT settlement_research_slot_check,
    DROP COLUMN spent_points,
    DROP COLUMN share_bps,
    DROP COLUMN discount_bps,
    DROP COLUMN ahead_bps,
    DROP COLUMN speed_bps,
    DROP COLUMN slot_ref;

DROP INDEX settlement_research_running_idx;
CREATE UNIQUE INDEX settlement_research_one_running_idx ON settlement_research (settlement_id) WHERE status = 'running';

COMMIT;
