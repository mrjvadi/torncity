BEGIN;

DROP TABLE labor_workers;
DROP TABLE labor_jobs;

DROP INDEX settlement_shifts_npc_idx;
DROP INDEX settlement_shifts_job_idx;
DELETE FROM settlement_shifts WHERE worker_kind = 'npc' OR kind <> 'production';
ALTER TABLE settlement_shifts
    DROP CONSTRAINT settlement_shifts_points_check,
    DROP CONSTRAINT settlement_shifts_fee_check,
    DROP CONSTRAINT settlement_shifts_payer_check,
    DROP CONSTRAINT settlement_shifts_worker_player_check,
    DROP CONSTRAINT settlement_shifts_worker_check,
    DROP CONSTRAINT settlement_shifts_kind_check,
    DROP COLUMN fee, DROP COLUMN payer_id, DROP COLUMN payer_kind, DROP COLUMN work_points,
    DROP COLUMN worker_kind, DROP COLUMN job_id, DROP COLUMN kind,
    ALTER COLUMN player_id SET NOT NULL;

ALTER TABLE settlement_buildings
    DROP CONSTRAINT settlement_buildings_work_check,
    DROP COLUMN employer_player_id, DROP COLUMN work_done, DROP COLUMN work_required;

COMMIT;
