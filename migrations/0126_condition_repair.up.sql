-- 0126_condition_repair - workplaces wear and are repaired in kind (roadmap 2.2 phase 5,
-- ADR 0041 6.10; owner decision 2 of 2026-10-01).
--
--   settlement_buildings  damage_bps (0 intact, 10000 a ruin) already exists; damage_at is the
--                         instant it was last true: the wear since then is read lazily, whole
--                         local days at a time, so no tick writes a row per building. The
--                         condition of the ADR is 10000 - damage_bps.
--   labor_jobs            gain the kind 'repair'.
--   settlement_shifts     gain the kind 'repair' and condition_gain, the bps a finished repair
--                         shift restored.
BEGIN;

ALTER TABLE settlement_buildings ADD COLUMN damage_at timestamptz;

-- a workplace may have its production job and a repair job open together
DROP INDEX labor_jobs_one_open_idx;
CREATE UNIQUE INDEX labor_jobs_one_open_idx ON labor_jobs (building_id, kind) WHERE status = 'open';

ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_kind_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_kind_check CHECK (kind IN ('construction', 'production', 'repair'));

ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_kind_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_kind_check CHECK (kind IN ('production', 'construction', 'repair'));
ALTER TABLE settlement_shifts
    ADD COLUMN condition_gain int NOT NULL DEFAULT 0,
    ADD CONSTRAINT settlement_shifts_gain_check CHECK (condition_gain >= 0);

COMMIT;
