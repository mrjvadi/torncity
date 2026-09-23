-- 0011_jobs_and_education — work and study become playable.
-- Rules: internal/domain/job and internal/domain/education. Content:
-- configs/content/jobs.yml and education.yml (docs/adr/0004-content-system.md).
-- Money: every wage, withholding and fee moves through the ledger of 0006
-- (docs/adr/0009-economic-control.md); nothing here holds a balance.
-- Schema: docs/database.md, sections 2 (employment) and 9 (education).
--
-- What is content and what is state:
--   * career_definitions, course_definitions — content, one set of rows per
--     content version, like city_routes and skill_definitions: re-activating
--     an old version needs no file. Each row holds the authored definition as
--     a document, because a career is a tree of tiers, requirements and
--     rewards whose shape is the content schema's, not a second one here.
--   * employments, work_shifts, enrollments, certifications — state. They
--     name a career or a course by its CODE, not by a definition row's id: a
--     code is the stable identity every version shares (the same choice
--     player_skills.skill_code made), so reloading content never orphans a
--     player's job or certificate.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- career_definitions — the careers of one content version.
-- ---------------------------------------------------------------------------
CREATE TABLE career_definitions (
    id                 uuid  PRIMARY KEY,
    code               text  NOT NULL,
    category           text  NOT NULL,
    -- The whole authored definition (content.CareerDef), tiers included.
    definition         jsonb NOT NULL,
    content_version_id uuid  NOT NULL REFERENCES content_versions (id),

    CONSTRAINT career_definitions_version_code_key UNIQUE (content_version_id, code)
);

CREATE INDEX career_definitions_content_version_id_idx
    ON career_definitions (content_version_id);

COMMENT ON COLUMN career_definitions.definition IS
    'The authored career (content.CareerDef) as a document. Validated by the loader before it is written; read back into the same type.';

-- ---------------------------------------------------------------------------
-- course_definitions — the courses of one content version.
-- ---------------------------------------------------------------------------
CREATE TABLE course_definitions (
    id                 uuid  PRIMARY KEY,
    code               text  NOT NULL,
    definition         jsonb NOT NULL,
    content_version_id uuid  NOT NULL REFERENCES content_versions (id),

    CONSTRAINT course_definitions_version_code_key UNIQUE (content_version_id, code)
);

CREATE INDEX course_definitions_content_version_id_idx
    ON course_definitions (content_version_id);

COMMENT ON COLUMN course_definitions.definition IS
    'The authored course (content.CourseDef) as a document. Validated by the loader before it is written; read back into the same type.';

-- ---------------------------------------------------------------------------
-- employments — a player's position, current and past.
--
-- One row per stint. The row with ended_at NULL is the current job, and the
-- partial unique index allows one: a player works one job at a time. Ended
-- rows stay as the job history (docs/database.md job_history).
--
-- company_id is absent on purpose: today every employer is the base (NPC)
-- employer of a city, which pays from system_source. The column arrives with
-- player companies, which will pay from their treasury instead.
-- ---------------------------------------------------------------------------
CREATE TABLE employments (
    id              uuid        PRIMARY KEY,
    player_id       uuid        NOT NULL REFERENCES players (id),
    career_code     text        NOT NULL,
    -- Where the job is. A shift is worked there.
    city_id         uuid        NOT NULL REFERENCES cities (id),
    -- Index into the career's tiers, from 0 (the most junior).
    tier            int         NOT NULL,
    -- Agreed pay per full-output shift, minor units.
    rate            bigint      NOT NULL,
    performance     int         NOT NULL DEFAULT 50,
    tier_since      timestamptz NOT NULL,
    shifts_in_tier  int         NOT NULL DEFAULT 0,
    total_shifts    int         NOT NULL DEFAULT 0,
    -- Start times of the shifts still inside the fatigue window. The domain
    -- prunes the list on every shift, so it stays a handful of entries.
    recent_shifts   timestamptz[] NOT NULL DEFAULT '{}',
    -- Gross pay earned in this stint, minor units, for the screen.
    total_earned    bigint      NOT NULL DEFAULT 0,
    hired_at        timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    ended_at        timestamptz NULL,
    end_reason      text        NULL,

    CONSTRAINT employments_tier_check CHECK (tier >= 0),
    CONSTRAINT employments_rate_check CHECK (rate >= 0),
    CONSTRAINT employments_performance_check CHECK (performance BETWEEN 0 AND 100),
    CONSTRAINT employments_counts_check CHECK (shifts_in_tier >= 0 AND total_shifts >= 0),
    CONSTRAINT employments_end_check CHECK ((ended_at IS NULL) = (end_reason IS NULL)),
    CONSTRAINT employments_end_reason_check
        CHECK (end_reason IS NULL OR end_reason IN ('resigned', 'fired', 'company_closed'))
);

-- One current job per player.
CREATE UNIQUE INDEX employments_one_current_idx
    ON employments (player_id) WHERE ended_at IS NULL;

-- The job history, most recent first.
CREATE INDEX employments_player_idx ON employments (player_id, hired_at DESC);

-- ---------------------------------------------------------------------------
-- work_shifts — one row per shift worked. Append-only: it is the payroll
-- record of the base employer, which settles every shift as it is worked,
-- and each row points at the ledger transaction that paid it.
-- ---------------------------------------------------------------------------
CREATE TABLE work_shifts (
    id                    uuid        PRIMARY KEY,
    employment_id         uuid        NOT NULL REFERENCES employments (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    tier                  int         NOT NULL,
    worked_at             timestamptz NOT NULL,
    gross                 bigint      NOT NULL,
    tax                   bigint      NOT NULL,
    net                   bigint      NOT NULL,
    xp                    bigint      NOT NULL,
    performance_delta     int         NOT NULL,
    fatigue_bps           int         NOT NULL,
    -- NULL only for a shift that paid nothing.
    ledger_transaction_id uuid        NULL,

    CONSTRAINT work_shifts_amounts_check CHECK (gross >= 0 AND tax >= 0 AND net >= 0 AND gross = net + tax),
    CONSTRAINT work_shifts_paid_check CHECK ((gross = 0) = (ledger_transaction_id IS NULL))
);

CREATE INDEX work_shifts_employment_idx ON work_shifts (employment_id, worked_at DESC);

CREATE TRIGGER work_shifts_append_only
    BEFORE UPDATE OR DELETE ON work_shifts
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER work_shifts_no_truncate
    BEFORE TRUNCATE ON work_shifts
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- enrollments — a player's place on a course.
-- ---------------------------------------------------------------------------
CREATE TABLE enrollments (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id),
    course_code    text        NOT NULL,
    -- The scheduled completion (game_actions action_type 'education').
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    status         text        NOT NULL,
    -- The fee charged, minor units, as it was at enrolment.
    fee            bigint      NOT NULL,
    started_at     timestamptz NOT NULL,
    completes_at   timestamptz NOT NULL,
    completed_at   timestamptz NULL,

    CONSTRAINT enrollments_status_check
        CHECK (status IN ('in_progress', 'completed', 'abandoned')),
    CONSTRAINT enrollments_fee_check CHECK (fee >= 0),
    CONSTRAINT enrollments_period_check CHECK (completes_at > started_at),
    CONSTRAINT enrollments_completed_check CHECK ((status = 'completed') = (completed_at IS NOT NULL))
);

-- One course at a time (education.ErrAlreadyEnrolled is the same rule).
CREATE UNIQUE INDEX enrollments_one_active_idx
    ON enrollments (player_id) WHERE status = 'in_progress';

-- Seats taken on a course.
CREATE INDEX enrollments_course_active_idx
    ON enrollments (course_code) WHERE status = 'in_progress';

CREATE INDEX enrollments_player_idx ON enrollments (player_id, started_at DESC);

-- ---------------------------------------------------------------------------
-- certifications — qualifications earned. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE certifications (
    id            uuid        PRIMARY KEY,
    player_id     uuid        NOT NULL REFERENCES players (id),
    -- The course that issued it; a career requires a certification by this
    -- same code.
    course_code   text        NOT NULL,
    enrollment_id uuid        NOT NULL REFERENCES enrollments (id),
    issued_at     timestamptz NOT NULL,

    CONSTRAINT certifications_player_course_key UNIQUE (player_id, course_code)
);

CREATE TRIGGER certifications_append_only
    BEFORE UPDATE OR DELETE ON certifications
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER certifications_no_truncate
    BEFORE TRUNCATE ON certifications
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
