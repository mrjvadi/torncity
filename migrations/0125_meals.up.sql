-- 0125_meals - workers eat (roadmap 2.2 phase 3, ADR 0041 section 6.5).
--
--   settlement_kitchen  the village pot: food points opened from the stock and eaten by shifts.
--                       pot = opened_points - eaten_points, never negative. One row per settlement.
--   settlement_meals    one row per opening of food units into the pot (append-only): the
--                       item journal reason meal_eaten references it.
--   settlement_shifts   meal_points eaten at the start, whether the worker was fed, and the
--                       productivity (bps) the shift's output is scaled by.
--   settlement_buildings  carry: the fraction of an item a workplace has made but not yet
--                       delivered, in ten-thousandths of a unit, so the totals stay exact.
BEGIN;

CREATE TABLE settlement_kitchen (
    settlement_id uuid   PRIMARY KEY,
    opened_points bigint NOT NULL DEFAULT 0,
    eaten_points  bigint NOT NULL DEFAULT 0,
    pot           bigint NOT NULL DEFAULT 0,
    CONSTRAINT settlement_kitchen_check CHECK (opened_points >= 0 AND eaten_points >= 0 AND pot >= 0 AND pot = opened_points - eaten_points)
);

CREATE TABLE settlement_meals (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    shift_id      uuid        NOT NULL,
    item_code     text        NOT NULL,
    units         bigint      NOT NULL,
    points_each   bigint      NOT NULL,
    created_at    timestamptz NOT NULL,
    CONSTRAINT settlement_meals_check CHECK (units > 0 AND points_each > 0)
);
CREATE INDEX settlement_meals_settlement_idx ON settlement_meals (settlement_id, created_at);

ALTER TABLE settlement_shifts
    ADD COLUMN meal_points int     NOT NULL DEFAULT 0,
    ADD COLUMN fed         boolean NOT NULL DEFAULT true,
    ADD COLUMN output_bps  int     NOT NULL DEFAULT 10000,
    ADD CONSTRAINT settlement_shifts_meal_check CHECK (meal_points >= 0 AND output_bps >= 0);

ALTER TABLE settlement_buildings
    ADD COLUMN carry jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMIT;
