-- 0133_building_functions, reversed.
BEGIN;

DELETE FROM settlement_shifts WHERE kind = 'fitout';
DELETE FROM labor_jobs WHERE kind = 'fitout';
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_kind_check;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_kind_check CHECK (kind IN ('production', 'construction', 'repair'));
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_kind_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_kind_check CHECK (kind IN ('construction', 'production', 'repair'));

DROP TABLE plan_templates;
DROP TABLE function_conversions;
DROP TABLE building_works;
DROP TABLE building_looks;
DROP TABLE building_modules;
DROP TABLE building_functions;

COMMIT;
