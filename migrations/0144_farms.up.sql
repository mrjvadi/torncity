-- 0144_farms: the farm cycle, the water works as daily services and the miller's toll (ADR 0067, plan B2).
--
--   farm_cycles            one crop on one farm: its soil at the order, the sowing, tending and harvest counters, the water factor
--                          samples, the yield fixed at the first harvest shift; stages are read from these and the clock, never ticked
--   settlement_shifts      the cycle and the phase a farm shift worked (farm_cycle, farm_phase: sow, tend, harvest) and the citizen
--                          whose own grain a mill shift ground (custom_for; farm_phase 'grind')
--   settlement_mill_policy the settlement's statute of the miller's toll, basis points of the grain
--
-- No foreign key to cities (as the other village day tables). Conventions as 0137.
BEGIN;

CREATE TABLE farm_cycles (
    id              uuid        PRIMARY KEY,
    settlement_id   uuid        NOT NULL,
    building_id     uuid        NOT NULL,
    rainfed         boolean     NOT NULL,
    soil_bps        int         NOT NULL,
    ordered_by      uuid        NULL,
    ordered_at      timestamptz NOT NULL,
    sow_started     int         NOT NULL DEFAULT 0,
    grow_from       timestamptz NULL,
    tended          int         NOT NULL DEFAULT 0,
    water_sum       bigint      NOT NULL DEFAULT 0,
    water_n         int         NOT NULL DEFAULT 0,
    harvest_started int         NOT NULL DEFAULT 0,
    yield_total     bigint      NOT NULL DEFAULT 0,
    seed_spent      bigint      NOT NULL DEFAULT 0,
    closed_at       timestamptz NULL,
    result          text        NOT NULL DEFAULT '',

    CONSTRAINT farm_cycles_counts_check CHECK (sow_started >= 0 AND tended >= 0 AND water_n >= 0 AND harvest_started >= 0
        AND yield_total >= 0 AND seed_spent >= 0 AND water_sum >= 0 AND soil_bps > 0),
    CONSTRAINT farm_cycles_result_check CHECK (result IN ('', 'harvested', 'rotted', 'replaced'))
);
-- one open crop per farm: the race of two sow orders is decided here
CREATE UNIQUE INDEX farm_cycles_open_idx ON farm_cycles (building_id) WHERE closed_at IS NULL;
CREATE INDEX farm_cycles_settlement_idx ON farm_cycles (settlement_id);
CREATE INDEX farm_cycles_building_idx ON farm_cycles (building_id, ordered_at);

ALTER TABLE settlement_shifts ADD COLUMN farm_cycle uuid NULL;
ALTER TABLE settlement_shifts ADD COLUMN farm_phase text NOT NULL DEFAULT '';
ALTER TABLE settlement_shifts ADD COLUMN custom_for uuid NULL;
ALTER TABLE settlement_shifts ADD CONSTRAINT settlement_shifts_farm_check CHECK (farm_phase IN ('', 'sow', 'tend', 'harvest', 'grind'));
CREATE INDEX settlement_shifts_farm_idx ON settlement_shifts (farm_cycle) WHERE farm_cycle IS NOT NULL;

CREATE TABLE settlement_mill_policy (
    settlement_id uuid        PRIMARY KEY,
    toll_bps      int         NOT NULL,
    set_by        uuid        NULL,
    set_at        timestamptz NOT NULL,

    CONSTRAINT settlement_mill_policy_check CHECK (toll_bps >= 0 AND toll_bps <= 10000)
);

-- The new charter permissions farm.sow (order the sowing of the treasury's farms) and mill.toll (set the statute of the miller's
-- toll): the head office of a stored charter that may draw roads gets them with the same hand. Offices created later get them from
-- the code.
UPDATE charter_offices
   SET grants = grants || '[{"p": "farm.sow"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "road.draw"}]'::jsonb AND NOT grants @> '[{"p": "farm.sow"}]'::jsonb;
UPDATE charter_offices
   SET grants = grants || '[{"p": "mill.toll"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "road.draw"}]'::jsonb AND NOT grants @> '[{"p": "mill.toll"}]'::jsonb;

COMMIT;
