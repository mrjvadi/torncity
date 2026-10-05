BEGIN;

DROP INDEX IF EXISTS settlement_shifts_npc_building_idx;
ALTER TABLE labor_jobs
    DROP CONSTRAINT IF EXISTS labor_jobs_paused_check,
    DROP CONSTRAINT IF EXISTS labor_jobs_priority_check,
    DROP COLUMN IF EXISTS paused,
    DROP COLUMN IF EXISTS priority;

COMMIT;
