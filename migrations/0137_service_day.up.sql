-- 0137_service_day: a service building is open on the days it is staffed and supplied (ADR 0052, prerequisite A4b).
--
-- A watch post was a coverage number; an inn had no building at all. Now, once a local day, every standing building whose
-- function is a daily service (watch_post: local security; teahouse_inn: lodging) is judged: its staff are seats of the
-- labour pool, paid by the treasury (service_wage, to the sink), and its upkeep (firewood, bread, water ...) is drawn from
-- the store (item reason service_upkeep). A post that was held gives its service that day; one without staff, wage or
-- supplies is idle and gives nothing.
--
--   service_days        the fence of a settlement's local day: one row per day judged, with the wage and the ledger tx
--   service_day_posts   one row per post that day: its service, held or the reason it was idle, the staff, the wage and
--                       what it used up
--
-- No foreign key to cities (as the other days). Conventions as 0114.
BEGIN;

CREATE TABLE service_days (
    settlement_id uuid        NOT NULL,
    day           bigint      NOT NULL,
    posts         bigint      NOT NULL DEFAULT 0,
    held          bigint      NOT NULL DEFAULT 0,
    staff         bigint      NOT NULL DEFAULT 0,
    wage          bigint      NOT NULL DEFAULT 0,
    wage_tx       uuid        NULL,
    at            timestamptz NOT NULL,

    CONSTRAINT service_days_pk PRIMARY KEY (settlement_id, day),
    CONSTRAINT service_days_check CHECK (posts >= 0 AND held >= 0 AND held <= posts AND staff >= 0 AND wage >= 0),
    CONSTRAINT service_days_wage_check CHECK ((wage > 0) = (wage_tx IS NOT NULL))
);

CREATE TABLE service_day_posts (
    settlement_id uuid    NOT NULL,
    day           bigint  NOT NULL,
    building_id   uuid    NOT NULL,
    service       text    NOT NULL,
    held          boolean NOT NULL,
    idle          text    NOT NULL DEFAULT '',
    staff         bigint  NOT NULL DEFAULT 0,
    wage          bigint  NOT NULL DEFAULT 0,
    used          jsonb   NOT NULL DEFAULT '{}'::jsonb,

    CONSTRAINT service_day_posts_pk PRIMARY KEY (settlement_id, day, building_id),
    CONSTRAINT service_day_posts_fk FOREIGN KEY (settlement_id, day) REFERENCES service_days (settlement_id, day) ON DELETE CASCADE,
    CONSTRAINT service_day_posts_check CHECK (staff >= 0 AND wage >= 0 AND length(btrim(service)) > 0
        AND (held AND idle = '' AND staff > 0 OR NOT held AND idle IN ('no_staff', 'no_wage', 'no_supplies') AND staff = 0 AND wage = 0 AND used = '{}'::jsonb))
);

COMMIT;
