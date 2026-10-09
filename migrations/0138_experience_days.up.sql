-- 0138_experience_days: practice in a field counts once a day per source (ADR 0054, plan A6).
--
-- A settlement's experience in a field (settlement_experience) is what real work earns: finished production and
-- construction shifts, a held watch or health day, a market day that sold, a staffed research day, a teaching day.
-- The day sources are fenced here: one row per settlement, field, source and local day, so the points are added once
-- however often and by whoever settles the day.
BEGIN;

CREATE TABLE experience_days (
    settlement_id uuid        NOT NULL,
    field         text        NOT NULL,
    source        text        NOT NULL,
    day           bigint      NOT NULL,
    points        bigint      NOT NULL,
    at            timestamptz NOT NULL,

    CONSTRAINT experience_days_pk PRIMARY KEY (settlement_id, field, source, day),
    CONSTRAINT experience_days_check CHECK (points > 0 AND length(btrim(field)) > 0 AND length(btrim(source)) > 0)
);

COMMIT;
