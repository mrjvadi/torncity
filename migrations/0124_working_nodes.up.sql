-- 0124_working_nodes - NPC crews work production jobs (roadmap 2.2 phase 2, ADR 0041).
--
--   labor_jobs       gains a priority (1 first .. 4 last: when the pool is short the crews of
--                    the lower numbers are filled first) and the reason a crew is paused: the
--                    job could not start a shift, so it waits instead of retrying; a refilled
--                    crew clears it.
--   settlement_shifts  an index for "how many NPC shifts did this building start today".
BEGIN;

ALTER TABLE labor_jobs
    ADD COLUMN priority smallint NOT NULL DEFAULT 4,
    ADD COLUMN paused   text,
    ADD CONSTRAINT labor_jobs_priority_check CHECK (priority BETWEEN 1 AND 4),
    ADD CONSTRAINT labor_jobs_paused_check CHECK (paused IS NULL OR paused IN
        ('no_staff', 'no_food', 'no_input', 'storage_full', 'employer_broke', 'budget_spent', 'needs_repair'));

CREATE INDEX settlement_shifts_npc_building_idx ON settlement_shifts (building_id, started_at) WHERE worker_kind = 'npc';

COMMIT;
