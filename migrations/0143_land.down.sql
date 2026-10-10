BEGIN;
UPDATE labor_jobs SET paused = NULL WHERE paused IN ('no_trees', 'no_plot');
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_paused_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_paused_check CHECK (paused IS NULL OR paused IN
    ('no_staff', 'no_food', 'no_input', 'storage_full', 'employer_broke', 'budget_spent', 'needs_repair'));
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_land_check;
ALTER TABLE settlement_shifts DROP COLUMN land_owner, DROP COLUMN land_kind, DROP COLUMN land_y, DROP COLUMN land_x;
DROP TABLE settlement_saplings;
DROP TABLE settlement_land;
COMMIT;
