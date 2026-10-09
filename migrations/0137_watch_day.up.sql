-- 0137_watch_day: the night watch costs wood and men (ADR 0052, prerequisite A4b).
--
-- A watch post used to be a coverage number with no guard, no fuel and no night. Now, once a local day, each standing
-- watch post is judged: its watchmen (seats of the labour pool) are paid by the treasury (watch_wage, to the sink) and
-- its fire burns firewood (item reason watch_fuel). A post that was held counts its security; one without guards, wage
-- or fuel stands idle and gives nothing that day.
--
--   watch_days        the fence of a settlement's local day: one row per day judged, with the wage and the ledger tx
--   watch_day_posts   one row per post that day: held or the reason it was idle, the guards, the wage and the fuel
--
-- No foreign key to cities (as the other days). Conventions as 0114.
BEGIN;

CREATE TABLE watch_days (
    settlement_id uuid        NOT NULL,
    day           bigint      NOT NULL,
    posts         bigint      NOT NULL DEFAULT 0,
    held          bigint      NOT NULL DEFAULT 0,
    guards        bigint      NOT NULL DEFAULT 0,
    wage          bigint      NOT NULL DEFAULT 0,
    fuel          bigint      NOT NULL DEFAULT 0,
    wage_tx       uuid        NULL,
    at            timestamptz NOT NULL,

    CONSTRAINT watch_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT watch_days_check CHECK (posts >= 0 AND held >= 0 AND held <= posts AND guards >= 0 AND wage >= 0 AND fuel >= 0),
    CONSTRAINT watch_days_wage_check CHECK ((wage > 0) = (wage_tx IS NOT NULL))
);

CREATE TABLE watch_day_posts (
    settlement_id uuid   NOT NULL,
    day           bigint NOT NULL,
    building_id   uuid   NOT NULL,
    held          boolean NOT NULL,
    idle          text   NOT NULL DEFAULT '',
    guards        bigint NOT NULL DEFAULT 0,
    wage          bigint NOT NULL DEFAULT 0,
    fuel          bigint NOT NULL DEFAULT 0,

    CONSTRAINT watch_day_posts_pk PRIMARY KEY (settlement_id, day, building_id),
    CONSTRAINT watch_day_posts_fk FOREIGN KEY (settlement_id, day) REFERENCES watch_days (settlement_id, day) ON DELETE CASCADE,
    CONSTRAINT watch_day_posts_check CHECK (guards >= 0 AND wage >= 0 AND fuel >= 0
        AND (held AND idle = '' AND guards > 0 AND fuel > 0 OR NOT held AND idle IN ('no_guard', 'no_wage', 'no_fuel') AND guards = 0 AND wage = 0 AND fuel = 0))
);

COMMIT;
