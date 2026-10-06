BEGIN;

DELETE FROM settlement_shifts WHERE kind = 'repair';
DELETE FROM labor_jobs WHERE kind = 'repair';
DROP INDEX IF EXISTS labor_jobs_one_open_idx;
CREATE UNIQUE INDEX labor_jobs_one_open_idx ON labor_jobs (building_id) WHERE status = 'open';
ALTER TABLE settlement_shifts
    DROP CONSTRAINT IF EXISTS settlement_shifts_gain_check,
    DROP COLUMN IF EXISTS condition_gain;
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_kind_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_kind_check CHECK (kind IN ('production', 'construction'));
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_kind_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_kind_check CHECK (kind IN ('construction', 'production'));
ALTER TABLE settlement_buildings DROP COLUMN IF EXISTS damage_at;

COMMIT;
