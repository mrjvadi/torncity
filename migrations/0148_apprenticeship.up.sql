-- 0148_apprenticeship: the teaching network (ADR 0071, plan B7).
--
--   apprenticeships     a master (a player at his workshop, or the pool's craftsman of a public workshop) trains an apprentice for a
--                       term; the fee, the wage share and the counters are on the row
--   apprentice_exams    the journeyman exam of an apprenticeship: the piece (finished shifts) and who passed it
--   certifications      a certificate now says who issued it and by which route (a class, or an apprenticeship); a certificate of
--                       an apprenticeship has no enrolment
--
-- No foreign key to cities or buildings (as the other village tables). Conventions as 0137.
BEGIN;

CREATE TABLE apprenticeships (
    id             uuid        PRIMARY KEY,
    settlement_id  uuid        NOT NULL,
    workplace_id   uuid        NOT NULL,
    craft          text        NOT NULL,
    master_kind    text        NOT NULL,
    master_id      uuid        NULL,
    apprentice_id  uuid        NOT NULL,
    term_days      integer     NOT NULL,
    fee            bigint      NOT NULL DEFAULT 0,
    wage_share_bps integer     NOT NULL,
    status         text        NOT NULL DEFAULT 'offered',
    reason         text        NOT NULL DEFAULT '',
    offered_at     timestamptz NOT NULL,
    started_at     timestamptz NULL,
    ends_at        timestamptz NULL,
    ended_at       timestamptz NULL,
    shifts         integer     NOT NULL DEFAULT 0,
    xp_extra       bigint      NOT NULL DEFAULT 0,
    master_xp_day  bigint       NULL,
    fee_paid       bigint      NOT NULL DEFAULT 0,
    refunded       bigint      NOT NULL DEFAULT 0,
    certified_at   timestamptz NULL,
    CONSTRAINT apprenticeships_check CHECK (master_kind IN ('player', 'npc') AND (master_kind = 'npc' OR master_id IS NOT NULL)
        AND status IN ('offered', 'active', 'completed', 'left', 'dismissed', 'declined')
        AND term_days >= 1 AND fee >= 0 AND wage_share_bps BETWEEN 0 AND 10000 AND shifts >= 0 AND xp_extra >= 0
        AND fee_paid >= 0 AND refunded >= 0 AND refunded <= fee_paid
        AND (status NOT IN ('active', 'completed') OR started_at IS NOT NULL))
);
CREATE UNIQUE INDEX apprenticeships_one_active_idx ON apprenticeships (apprentice_id) WHERE status = 'active';
CREATE INDEX apprenticeships_master_idx ON apprenticeships (master_id) WHERE status = 'active';
CREATE INDEX apprenticeships_apprentice_idx ON apprenticeships (apprentice_id, offered_at DESC);
CREATE INDEX apprenticeships_settlement_idx ON apprenticeships (settlement_id, status);

CREATE TABLE apprentice_exams (
    id                uuid        PRIMARY KEY,
    apprenticeship_id uuid        NOT NULL,
    candidate_id      uuid        NOT NULL,
    craft             text        NOT NULL,
    examiner_kind     text        NOT NULL,
    examiner_id       uuid        NULL,
    shifts            integer     NOT NULL,
    passed            boolean     NOT NULL,
    at                timestamptz NOT NULL,
    CONSTRAINT apprentice_exams_check CHECK (examiner_kind IN ('player', 'npc') AND (examiner_kind = 'npc' OR examiner_id IS NOT NULL) AND shifts >= 0)
);

CREATE UNIQUE INDEX apprentice_exams_pass_idx ON apprentice_exams (apprenticeship_id) WHERE passed;

ALTER TABLE certifications ALTER COLUMN enrollment_id DROP NOT NULL;
ALTER TABLE certifications ADD COLUMN issued_by uuid NULL;
ALTER TABLE certifications ADD COLUMN route text NOT NULL DEFAULT 'class';
ALTER TABLE certifications ADD COLUMN apprenticeship_id uuid NULL;
ALTER TABLE certifications ADD CONSTRAINT certifications_route_check CHECK (
    (route = 'class' AND enrollment_id IS NOT NULL AND apprenticeship_id IS NULL)
    OR (route = 'apprenticeship' AND enrollment_id IS NULL AND apprenticeship_id IS NOT NULL));

COMMIT;
