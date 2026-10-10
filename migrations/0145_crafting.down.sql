BEGIN;
DROP TABLE craft_jobs;
UPDATE labor_jobs SET paused = NULL WHERE paused = 'no_recipe';
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_paused_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_paused_check CHECK (paused IS NULL OR paused IN
    ('no_staff', 'no_food', 'no_input', 'storage_full', 'employer_broke', 'budget_spent', 'needs_repair', 'no_trees', 'no_plot',
     'no_crop', 'crop_growing', 'no_grazing'));
ALTER TABLE labor_jobs DROP COLUMN recipe;
ALTER TABLE settlement_shifts DROP COLUMN recipe;
COMMIT;
