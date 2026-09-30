-- 0059_labor_market - construction done by workers and the hiring board
-- (docs/adr/0037-labor-market.md).
--
--   settlement_buildings   gains work_required / work_done (worker-minutes): a
--                          building placed under the labour rules is finished
--                          only by the work of shifts, never by a timer. A row
--                          with work_required = 0 is a building of the older
--                          timer (finish_at) and is unchanged. employer_player_id
--                          names the citizen who builds and hires for a private
--                          building; NULL means the village treasury does.
--   settlement_shifts      is generalised, not duplicated: a shift now has a kind
--                          (production, as before, or construction), may belong
--                          to a hiring-board job, may be worked by an NPC
--                          labourer (no player), and records the work it adds,
--                          who pays and the village's levy.
--   labor_jobs             the hiring board: a job at a building with a wage per
--                          shift, a budget of shifts, and an NPC crew size.
--   labor_workers          a player's experience: shifts worked and money
--                          earned, from which the skill level is read.
--
-- Journals name their settlement and player without a foreign key so they
-- outlive the rows exactly as the ledger does. Instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.
BEGIN;

ALTER TABLE settlement_buildings
    ADD COLUMN work_required      bigint NOT NULL DEFAULT 0,
    ADD COLUMN work_done          bigint NOT NULL DEFAULT 0,
    ADD COLUMN employer_player_id uuid,
    ADD CONSTRAINT settlement_buildings_work_check CHECK (work_done >= 0 AND work_done <= work_required);

ALTER TABLE settlement_shifts
    ALTER COLUMN player_id DROP NOT NULL,
    ADD COLUMN kind        text   NOT NULL DEFAULT 'production',
    ADD COLUMN job_id      uuid,
    ADD COLUMN worker_kind text   NOT NULL DEFAULT 'player',
    ADD COLUMN work_points bigint NOT NULL DEFAULT 0,
    ADD COLUMN payer_kind  text   NOT NULL DEFAULT 'settlement',
    ADD COLUMN payer_id    uuid,
    ADD COLUMN fee         bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT settlement_shifts_kind_check CHECK (kind IN ('production', 'construction')),
    ADD CONSTRAINT settlement_shifts_worker_check CHECK (worker_kind IN ('player', 'npc')),
    ADD CONSTRAINT settlement_shifts_worker_player_check CHECK ((worker_kind = 'player') = (player_id IS NOT NULL)),
    ADD CONSTRAINT settlement_shifts_payer_check CHECK (payer_kind IN ('settlement', 'player')),
    ADD CONSTRAINT settlement_shifts_fee_check CHECK (fee >= 0 AND fee <= wage_paid),
    ADD CONSTRAINT settlement_shifts_points_check CHECK (work_points >= 0);

CREATE INDEX settlement_shifts_job_idx ON settlement_shifts (job_id) WHERE job_id IS NOT NULL;
CREATE INDEX settlement_shifts_npc_idx ON settlement_shifts (settlement_id) WHERE worker_kind = 'npc' AND status = 'working';

CREATE TABLE labor_jobs (
    id              uuid        PRIMARY KEY,
    settlement_id   uuid        NOT NULL,
    building_id     uuid        NOT NULL,
    kind            text        NOT NULL,
    employer_kind   text        NOT NULL,
    employer_id     uuid        NOT NULL,
    wage            bigint      NOT NULL,
    shifts_total    int         NOT NULL,
    shifts_started  int         NOT NULL,
    npc_crew        int         NOT NULL,
    status          text        NOT NULL,
    created_by      uuid        NOT NULL,
    created_at      timestamptz NOT NULL,
    closed_at       timestamptz,

    CONSTRAINT labor_jobs_kind_check CHECK (kind IN ('construction', 'production')),
    CONSTRAINT labor_jobs_employer_check CHECK (employer_kind IN ('settlement', 'player')),
    CONSTRAINT labor_jobs_status_check CHECK (status IN ('open', 'closed')),
    CONSTRAINT labor_jobs_wage_check CHECK (wage >= 0),
    CONSTRAINT labor_jobs_budget_check CHECK (shifts_total >= 0 AND shifts_started >= 0 AND shifts_started <= shifts_total),
    CONSTRAINT labor_jobs_crew_check CHECK (npc_crew >= 0),
    CONSTRAINT labor_jobs_closed_check CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);

-- One open job per building: the board lists a site once.
CREATE UNIQUE INDEX labor_jobs_one_open_idx ON labor_jobs (building_id) WHERE status = 'open';
CREATE INDEX labor_jobs_board_idx ON labor_jobs (settlement_id, created_at DESC) WHERE status = 'open';

CREATE TABLE labor_workers (
    player_id  uuid        PRIMARY KEY,
    shifts     bigint      NOT NULL,
    earned     bigint      NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT labor_workers_check CHECK (shifts >= 0 AND earned >= 0)
);

COMMIT;
