BEGIN;
UPDATE labor_jobs SET paused = NULL WHERE paused IN ('no_crop', 'crop_growing', 'no_grazing');
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_paused_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_paused_check CHECK (paused IS NULL OR paused IN
    ('no_staff', 'no_food', 'no_input', 'storage_full', 'employer_broke', 'budget_spent', 'needs_repair', 'no_trees', 'no_plot'));
DROP TABLE settlement_mill_policy;
DROP INDEX settlement_shifts_farm_idx;
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_farm_check;
ALTER TABLE settlement_shifts DROP COLUMN custom_for, DROP COLUMN farm_phase, DROP COLUMN farm_cycle;
DROP TABLE farm_cycles;
COMMIT;
