-- 0012_timed_shifts — a shift of work takes time.
-- Rules: internal/domain/job (StartShift, FinishShift) and the game clock,
-- internal/domain/gametime (docs/adr/0018-game-clock.md). Content: the
-- shift_duration of every tier in configs/content/jobs.yml.
-- Schema: docs/database.md, section 2 (shift_sessions).
--
-- Until now a shift paid on the press, so it could be pressed as fast as a
-- thumb moves. From here a shift is an ACTIVITY, like a journey or a course:
--
--   start   energy is charged, a shift_sessions row is written 'working' and
--           its end is scheduled as a game_actions row (action_type
--           'work_shift') at ends_at — all in one transaction;
--   working the player is at work: one shift at a time (the partial unique
--           index below), no travelling;
--   end     the scheduler dispatches job.finish_shift; the session moves to
--           'completed' under a row lock, and the pay, income tax, XP, skill
--           XP and performance are applied in that same transaction, with a
--           work_shifts payroll row whose id IS the session's id. A replayed
--           completion finds no working session, and a second payroll row
--           for the same session would collide on work_shifts' primary key:
--           a shift is paid exactly once.
--
-- Nothing existing is rewritten. work_shifts rows written before this
-- migration keep their own ids; employments are untouched. The production
-- database this ships to has no employments yet, so there is nothing to
-- carry over, and a database that does have some keeps every one of them:
-- their next shift is simply a timed one.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- shift_sessions — one row per shift started, from 'working' to its end.
-- ---------------------------------------------------------------------------
CREATE TABLE shift_sessions (
    id             uuid        PRIMARY KEY,
    employment_id  uuid        NOT NULL REFERENCES employments (id),
    player_id      uuid        NOT NULL REFERENCES players (id),
    -- The tier the shift was started in; its rewards are the ones paid.
    tier           int         NOT NULL,
    -- The scheduled end (game_actions action_type 'work_shift').
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    status         text        NOT NULL,
    -- The output multiplier the shift runs at, fixed when it starts from the
    -- shifts before it (10000 = full output).
    fatigue_bps    int         NOT NULL,
    -- The energy it cost, charged at the start, for the record.
    energy_cost    int         NOT NULL,
    started_at     timestamptz NOT NULL,
    -- started_at plus the tier's shift_duration on the game clock. Fixed
    -- here: a later change of the clock or of the content never moves it.
    ends_at        timestamptz NOT NULL,
    completed_at   timestamptz NULL,

    CONSTRAINT shift_sessions_status_check
        CHECK (status IN ('working', 'completed', 'abandoned')),
    CONSTRAINT shift_sessions_tier_check CHECK (tier >= 0),
    CONSTRAINT shift_sessions_fatigue_check CHECK (fatigue_bps BETWEEN 1 AND 10000),
    CONSTRAINT shift_sessions_energy_check CHECK (energy_cost >= 0),
    CONSTRAINT shift_sessions_period_check CHECK (ends_at > started_at),
    CONSTRAINT shift_sessions_completed_check CHECK ((status = 'working') = (completed_at IS NULL))
);

-- One shift at a time (job.ErrShiftInProgress is the same rule). Whatever
-- races — a double press, a redelivery, two devices — the second 'working'
-- row is refused here.
CREATE UNIQUE INDEX shift_sessions_one_working_idx
    ON shift_sessions (player_id) WHERE status = 'working';

CREATE INDEX shift_sessions_player_idx ON shift_sessions (player_id, started_at DESC);
CREATE INDEX shift_sessions_employment_idx ON shift_sessions (employment_id);

COMMENT ON COLUMN shift_sessions.ends_at IS
    'Real instant the shift ends: started_at plus the tier''s game-time shift_duration mapped through config game.time_scale.';

COMMIT;
