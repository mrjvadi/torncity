-- Teachers of courses (activities follow-up, docs/research/2026-10-03-activities-audit.md section 7).
--
-- A course in a founded settlement is taught only while somebody teaches it:
--   * kind 'npc': a teacher the settlement hires from its labour pool; employer
--     'settlement' (paid from the treasury, per student, into the sink);
--   * kind 'player', employer 'settlement': a certificate holder who took the
--     school's post (paid from the treasury, per student);
--   * kind 'player', employer 'self': a certificate holder teaching at home; the
--     student pays them directly, the settlement's income tax comes off.
-- class_seats ties an enrolment to its teacher and the wage the class owes,
-- paid exactly once when the course completes (wage_paid_at).
CREATE TABLE course_teachers (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    course_code   text        NOT NULL,
    kind          text        NOT NULL,
    player_id     uuid        NULL REFERENCES players (id),
    employer      text        NOT NULL,
    started_at    timestamptz NOT NULL,
    ended_at      timestamptz NULL,
    CONSTRAINT course_teachers_kind_check CHECK (kind IN ('npc', 'player')),
    CONSTRAINT course_teachers_employer_check CHECK (employer IN ('settlement', 'self')),
    CONSTRAINT course_teachers_who_check CHECK ((kind = 'player') = (player_id IS NOT NULL)),
    CONSTRAINT course_teachers_self_check CHECK (employer <> 'self' OR kind = 'player')
);

-- One NPC teacher per course and settlement; one post per player per course and settlement.
CREATE UNIQUE INDEX course_teachers_npc_idx
    ON course_teachers (settlement_id, course_code) WHERE kind = 'npc' AND ended_at IS NULL;
CREATE UNIQUE INDEX course_teachers_player_idx
    ON course_teachers (settlement_id, course_code, player_id) WHERE kind = 'player' AND ended_at IS NULL;
CREATE INDEX course_teachers_player_active_idx
    ON course_teachers (player_id) WHERE ended_at IS NULL AND player_id IS NOT NULL;

CREATE TABLE class_seats (
    enrollment_id uuid        PRIMARY KEY REFERENCES enrollments (id),
    teacher_id    uuid        NOT NULL REFERENCES course_teachers (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    -- What the class owes the teacher from the treasury; 0 for a private
    -- teacher, who was paid by the student at enrolment.
    wage          bigint      NOT NULL,
    wage_paid_at  timestamptz NULL,
    -- What the treasury paid in the end (at most wage; less when it was short).
    wage_paid     bigint      NOT NULL DEFAULT 0,
    CONSTRAINT class_seats_wage_check CHECK (wage >= 0 AND wage_paid >= 0 AND wage_paid <= wage)
);
CREATE INDEX class_seats_teacher_idx ON class_seats (teacher_id);
