BEGIN;
DROP TABLE settlement_mill_policy;
DROP INDEX settlement_shifts_farm_idx;
ALTER TABLE settlement_shifts DROP CONSTRAINT settlement_shifts_farm_check;
ALTER TABLE settlement_shifts DROP COLUMN custom_for, DROP COLUMN farm_phase, DROP COLUMN farm_cycle;
DROP TABLE farm_cycles;
COMMIT;
