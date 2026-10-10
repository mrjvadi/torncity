-- 0145_crafting: recipes at the stations, tool tiers and crafting at home (ADR 0068, plan B4).
--
--   settlement_shifts.recipe  the recipe a workshop shift made ('' is the station's standard shift)
--   labor_jobs.recipe         the recipe a posted job's crew makes ('' the standard shift)
--   craft_jobs                a citizen's timed craft at his home station: the goods leave his home store when it starts and
--                             the made goods come into it when it ends (settlement.craft.done, exactly once)
--
-- No foreign key to cities (as the other village day tables). Conventions as 0137.
BEGIN;

ALTER TABLE settlement_shifts ADD COLUMN recipe text NOT NULL DEFAULT '';
ALTER TABLE labor_jobs ADD COLUMN recipe text NOT NULL DEFAULT '';

-- A crew that cannot start for want of the recipe's research is paused with that reason.
ALTER TABLE labor_jobs DROP CONSTRAINT labor_jobs_paused_check;
ALTER TABLE labor_jobs ADD CONSTRAINT labor_jobs_paused_check CHECK (paused IS NULL OR paused IN
    ('no_staff', 'no_food', 'no_input', 'storage_full', 'employer_broke', 'budget_spent', 'needs_repair', 'no_trees', 'no_plot',
     'no_crop', 'crop_growing', 'no_grazing', 'no_recipe'));

CREATE TABLE craft_jobs (
    id             uuid        PRIMARY KEY,
    settlement_id  uuid        NOT NULL,
    player_id      uuid        NOT NULL,
    building_id    uuid        NOT NULL,
    recipe         text        NOT NULL,
    batches        int         NOT NULL,
    consumed       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    planned        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    made           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    output_bps     int         NOT NULL,
    tool_item      text        NOT NULL DEFAULT '',
    status         text        NOT NULL DEFAULT 'working',
    game_action_id uuid        NULL,
    started_at     timestamptz NOT NULL,
    finish_at      timestamptz NOT NULL,
    finished_at    timestamptz NULL,

    CONSTRAINT craft_jobs_check CHECK (batches >= 1 AND output_bps > 0 AND finish_at > started_at),
    CONSTRAINT craft_jobs_status_check CHECK (status IN ('working', 'done'))
);
CREATE INDEX craft_jobs_player_idx ON craft_jobs (player_id, status);
CREATE INDEX craft_jobs_settlement_idx ON craft_jobs (settlement_id, status);

COMMIT;
