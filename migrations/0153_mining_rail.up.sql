-- Mining, carriage and rail (docs/adr/0075-mining-rail-and-ratios.md, plan B11).
--
--   ore_deposits, ore_deposit_surveys, ore_extractions   a world deposit as a stored quantity taken (deltas only), what a settlement surveyed, what each shift took
--   mine_state, mine_levels                              a minehead's modules, the props and the water of each level
--   build_works                                          a timed order of materials and builders: a mine module, a road link, rail sections, a carrier
--   carriers, road_links, haul_orders, haul_loads        carts and wagons, the roads between settlements, a consignment and the goods on the road
--   rail_lines, rail_segments, rail_stops, rail_trains,
--   rail_schedules, rail_reservations                    the railway: track in sections, stations, trains, their stops, the sections a train holds
--   node_stats                                           an hour of a building's work (produced, consumed), for the ratio view
--   settlement_shifts                                    four columns: the staff role of a mine shift, what limited it, the deposit and the depth it dug
--   charter_offices                                      three more permissions on the head (mine.run, rail.draw, haul.order)
BEGIN;

CREATE TABLE ore_deposits (
    deposit_id text        PRIMARY KEY,
    resource   text        NOT NULL,
    taken      bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL,
    CONSTRAINT ore_deposits_taken_check CHECK (taken >= 0)
);

CREATE TABLE ore_deposit_surveys (
    deposit_id       text        NOT NULL REFERENCES ore_deposits (deposit_id),
    settlement_id    uuid        NOT NULL REFERENCES cities (id),
    surveyed_by      uuid        NOT NULL REFERENCES players (id),
    surveyed_at      timestamptz NOT NULL,
    grade_permille   integer     NOT NULL,
    reserve_estimate bigint      NOT NULL,
    fee              bigint      NOT NULL DEFAULT 0,
    fee_tx           uuid        NULL,
    PRIMARY KEY (deposit_id, settlement_id),
    CONSTRAINT ore_deposit_surveys_check CHECK (grade_permille BETWEEN 0 AND 1000 AND reserve_estimate >= 0 AND fee >= 0)
);

CREATE TABLE ore_extractions (
    shift_id      uuid        PRIMARY KEY REFERENCES settlement_shifts (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    building_id   uuid        NOT NULL REFERENCES settlement_buildings (id),
    deposit_id    text        NOT NULL REFERENCES ore_deposits (deposit_id),
    item_code     text        NOT NULL,
    quantity      bigint      NOT NULL,
    depth         smallint    NOT NULL,
    at            timestamptz NOT NULL,
    CONSTRAINT ore_extractions_check CHECK (quantity > 0 AND depth BETWEEN 0 AND 3)
);
CREATE INDEX ore_extractions_deposit_idx ON ore_extractions (deposit_id);
CREATE INDEX ore_extractions_building_idx ON ore_extractions (building_id, at DESC);

ALTER TABLE settlement_shifts
    ADD COLUMN staff_role      text     NOT NULL DEFAULT '',
    ADD COLUMN mine_limit      text     NOT NULL DEFAULT '',
    ADD COLUMN deposit_id      text     NOT NULL DEFAULT '',
    ADD COLUMN deposit_depth   smallint NOT NULL DEFAULT 0,
    ADD COLUMN deposit_reserve bigint   NOT NULL DEFAULT 0;
CREATE INDEX settlement_shifts_role_idx ON settlement_shifts (building_id, staff_role, finished_at DESC);

CREATE TABLE mine_state (
    building_id   uuid        PRIMARY KEY REFERENCES settlement_buildings (id),
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    shafts        smallint    NOT NULL DEFAULT 0,
    hoist         smallint    NOT NULL DEFAULT -1,
    ore_bin       boolean     NOT NULL DEFAULT false,
    adit_m        integer     NOT NULL DEFAULT 0,
    pump          smallint    NOT NULL DEFAULT -1,
    vent          boolean     NOT NULL DEFAULT false,
    day_done      bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT mine_state_check CHECK (shafts BETWEEN 0 AND 2 AND hoist BETWEEN -1 AND 1 AND pump BETWEEN -1 AND 1 AND adit_m >= 0)
);
CREATE INDEX mine_state_settlement_idx ON mine_state (settlement_id);

CREATE TABLE mine_levels (
    building_id   uuid        NOT NULL REFERENCES settlement_buildings (id),
    depth         smallint    NOT NULL,
    condition_bps integer     NOT NULL DEFAULT 10000,
    water_bps     integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (building_id, depth),
    CONSTRAINT mine_levels_check CHECK (depth BETWEEN 1 AND 2 AND condition_bps BETWEEN 0 AND 10000 AND water_bps BETWEEN 0 AND 10000)
);

CREATE TABLE build_works (
    id             uuid        PRIMARY KEY,
    settlement_id  uuid        NOT NULL REFERENCES cities (id),
    kind           text        NOT NULL,
    ref_id         uuid        NOT NULL,
    param          jsonb       NOT NULL DEFAULT '{}',
    status         text        NOT NULL DEFAULT 'working',
    materials      jsonb       NOT NULL DEFAULT '{}',
    wage           bigint      NOT NULL DEFAULT 0,
    wage_tx        uuid        NULL,
    ordered_by     uuid        NULL REFERENCES players (id),
    started_at     timestamptz NOT NULL,
    due_at         timestamptz NOT NULL,
    done_at        timestamptz NULL,
    game_action_id uuid        NULL,
    CONSTRAINT build_works_kind_check CHECK (kind IN ('mine_module', 'road_link', 'rail_sections', 'carrier')),
    CONSTRAINT build_works_status_check CHECK (status IN ('working', 'done') AND (status = 'done') = (done_at IS NOT NULL)),
    CONSTRAINT build_works_wage_check CHECK (wage >= 0 AND due_at >= started_at)
);
CREATE INDEX build_works_ref_idx ON build_works (kind, ref_id) WHERE status = 'working';
CREATE INDEX build_works_settlement_idx ON build_works (settlement_id, started_at DESC);

CREATE TABLE carriers (
    id            uuid        PRIMARY KEY,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    owner_kind    text        NOT NULL,
    owner_id      uuid        NOT NULL,
    mode          text        NOT NULL,
    kind          text        NOT NULL,
    capacity_kg   integer     NOT NULL,
    condition_bps integer     NOT NULL DEFAULT 10000,
    status        text        NOT NULL DEFAULT 'building',
    created_at    timestamptz NOT NULL,
    built_at      timestamptz NULL,
    CONSTRAINT carriers_owner_check CHECK (owner_kind IN ('settlement', 'player')),
    CONSTRAINT carriers_kind_check CHECK (kind IN ('cart', 'rail')),
    CONSTRAINT carriers_status_check CHECK (status IN ('building', 'idle', 'moving')),
    CONSTRAINT carriers_check CHECK (capacity_kg > 0 AND condition_bps BETWEEN 0 AND 10000)
);
CREATE INDEX carriers_settlement_idx ON carriers (settlement_id, status);

CREATE TABLE road_links (
    id               uuid        PRIMARY KEY,
    a_settlement     uuid        NOT NULL REFERENCES cities (id),
    b_settlement     uuid        NOT NULL REFERENCES cities (id),
    owner_settlement uuid        NOT NULL REFERENCES cities (id),
    class            text        NOT NULL,
    length_m         integer     NOT NULL,
    condition_bps    integer     NOT NULL DEFAULT 10000,
    toll             bigint      NOT NULL DEFAULT 0,
    status           text        NOT NULL DEFAULT 'building',
    created_by       uuid        NULL REFERENCES players (id),
    created_at       timestamptz NOT NULL,
    built_at         timestamptz NULL,
    CONSTRAINT road_links_pair_check CHECK (a_settlement <> b_settlement AND length_m > 0 AND toll >= 0 AND condition_bps BETWEEN 0 AND 10000),
    CONSTRAINT road_links_status_check CHECK (status IN ('building', 'open', 'closed'))
);
CREATE UNIQUE INDEX road_links_pair_idx ON road_links (LEAST(a_settlement, b_settlement), GREATEST(a_settlement, b_settlement)) WHERE status <> 'closed';

CREATE TABLE haul_orders (
    id              uuid        PRIMARY KEY,
    from_settlement uuid        NOT NULL REFERENCES cities (id),
    to_settlement   uuid        NOT NULL REFERENCES cities (id),
    ordered_by      uuid        NOT NULL REFERENCES players (id),
    payer_kind      text        NOT NULL,
    payer_id        uuid        NOT NULL,
    item_code       text        NOT NULL,
    quantity        bigint      NOT NULL,
    mode            text        NOT NULL,
    carrier_id      uuid        NULL REFERENCES carriers (id),
    line_id         uuid        NULL,
    from_stop       uuid        NULL,
    to_stop         uuid        NULL,
    fee             bigint      NOT NULL DEFAULT 0,
    fee_tx          uuid        NULL,
    status          text        NOT NULL DEFAULT 'open',
    created_at      timestamptz NOT NULL,
    delivered_at    timestamptz NULL,
    CONSTRAINT haul_orders_check CHECK (quantity > 0 AND fee >= 0 AND from_settlement <> to_settlement AND mode IN ('road', 'rail')),
    CONSTRAINT haul_orders_payer_check CHECK (payer_kind IN ('settlement', 'player')),
    CONSTRAINT haul_orders_status_check CHECK (status IN ('open', 'moving', 'delivered', 'cancelled') AND (status = 'delivered') = (delivered_at IS NOT NULL))
);
CREATE INDEX haul_orders_open_idx ON haul_orders (from_settlement, status) WHERE status IN ('open', 'moving');

CREATE TABLE haul_loads (
    id              uuid        PRIMARY KEY,
    order_id        uuid        NULL REFERENCES haul_orders (id),
    carrier_kind    text        NOT NULL,
    carrier_id      uuid        NOT NULL,
    item_code       text        NOT NULL,
    quantity        bigint      NOT NULL,
    kg              bigint      NOT NULL DEFAULT 0,
    from_settlement uuid        NOT NULL REFERENCES cities (id),
    to_settlement   uuid        NOT NULL REFERENCES cities (id),
    departed_at     timestamptz NOT NULL,
    due_at          timestamptz NOT NULL,
    arrived_at      timestamptz NULL,
    game_action_id  uuid        NULL,
    CONSTRAINT haul_loads_check CHECK (quantity > 0 AND kg >= 0 AND carrier_kind IN ('cart', 'rail') AND due_at >= departed_at)
);
CREATE INDEX haul_loads_open_idx ON haul_loads (to_settlement) WHERE arrived_at IS NULL;
CREATE INDEX haul_loads_carrier_idx ON haul_loads (carrier_id) WHERE arrived_at IS NULL;

CREATE TABLE rail_lines (
    id                uuid        PRIMARY KEY,
    name              text        NOT NULL,
    owner_kind        text        NOT NULL,
    owner_id          uuid        NOT NULL,
    home_settlement   uuid        NOT NULL REFERENCES cities (id),
    class             text        NOT NULL,
    rate_per_tonne_km bigint      NOT NULL DEFAULT 0,
    status            text        NOT NULL DEFAULT 'planned',
    sections_total    integer     NOT NULL,
    sections_built    integer     NOT NULL DEFAULT 0,
    length_m          integer     NOT NULL,
    created_by        uuid        NOT NULL REFERENCES players (id),
    created_at        timestamptz NOT NULL,
    cancelled_at      timestamptz NULL,
    CONSTRAINT rail_lines_owner_check CHECK (owner_kind IN ('settlement', 'player')),
    CONSTRAINT rail_lines_status_check CHECK (status IN ('planned', 'building', 'open', 'closed')),
    CONSTRAINT rail_lines_check CHECK (rate_per_tonne_km >= 0 AND sections_total > 0 AND sections_built BETWEEN 0 AND sections_total AND length_m > 0)
);
CREATE INDEX rail_lines_home_idx ON rail_lines (home_settlement, status);

CREATE TABLE rail_segments (
    id            uuid        PRIMARY KEY,
    line_id       uuid        NOT NULL REFERENCES rail_lines (id),
    seq           integer     NOT NULL,
    tile          bigint      NOT NULL,
    length_m      integer     NOT NULL,
    grade_bps     integer     NOT NULL DEFAULT 0,
    loop          boolean     NOT NULL DEFAULT false,
    condition_bps integer     NOT NULL DEFAULT 10000,
    wear_day      bigint      NOT NULL DEFAULT 0,
    built_at      timestamptz NULL,
    UNIQUE (line_id, seq),
    CONSTRAINT rail_segments_check CHECK (seq >= 1 AND length_m > 0 AND condition_bps BETWEEN 0 AND 10000)
);

CREATE TABLE rail_stops (
    id            uuid    PRIMARY KEY,
    line_id       uuid    NOT NULL REFERENCES rail_lines (id),
    after_seq     integer NOT NULL,
    building_id   uuid    NOT NULL REFERENCES settlement_buildings (id),
    settlement_id uuid    NOT NULL REFERENCES cities (id),
    UNIQUE (line_id, after_seq),
    UNIQUE (building_id),
    CONSTRAINT rail_stops_check CHECK (after_seq >= 0)
);

CREATE TABLE rail_trains (
    id             uuid        PRIMARY KEY,
    line_id        uuid        NOT NULL REFERENCES rail_lines (id),
    owner_kind     text        NOT NULL,
    owner_id       uuid        NOT NULL,
    name           text        NOT NULL,
    wagons         integer     NOT NULL,
    status         text        NOT NULL DEFAULT 'idle',
    at_stop        uuid        NULL REFERENCES rail_stops (id),
    heading        smallint    NOT NULL DEFAULT 1,
    leg_no         bigint      NOT NULL DEFAULT 0,
    sched_pos      integer     NOT NULL DEFAULT 0,
    next_event_at  timestamptz NULL,
    game_action_id uuid        NULL,
    waiting_since  timestamptz NULL,
    stuck_notified boolean     NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL,
    CONSTRAINT rail_trains_owner_check CHECK (owner_kind IN ('settlement', 'player')),
    CONSTRAINT rail_trains_status_check CHECK (status IN ('idle', 'running', 'waiting')),
    CONSTRAINT rail_trains_check CHECK (wagons > 0 AND heading IN (-1, 1) AND leg_no >= 0 AND sched_pos >= 0)
);
CREATE INDEX rail_trains_line_idx ON rail_trains (line_id, status);

CREATE TABLE rail_schedules (
    train_id     uuid    NOT NULL REFERENCES rail_trains (id),
    seq          integer NOT NULL,
    stop_id      uuid    NOT NULL REFERENCES rail_stops (id),
    op           text    NOT NULL,
    item_code    text    NOT NULL DEFAULT '',
    stock_floor  bigint  NOT NULL DEFAULT 0,
    wait_minutes integer NOT NULL DEFAULT 0,
    PRIMARY KEY (train_id, seq),
    CONSTRAINT rail_schedules_check CHECK (op IN ('load', 'unload', 'pass') AND stock_floor >= 0 AND wait_minutes >= 0 AND seq BETWEEN 0 AND 11)
);

-- One train holds a section, a passing loop holds two: the primary key is the rule, the trigger keeps the second slot to loops.
CREATE TABLE rail_reservations (
    segment_id uuid        NOT NULL REFERENCES rail_segments (id),
    slot       smallint    NOT NULL,
    train_id   uuid        NOT NULL REFERENCES rail_trains (id),
    leg_no     bigint      NOT NULL,
    since      timestamptz NOT NULL,
    PRIMARY KEY (segment_id, slot),
    UNIQUE (segment_id, train_id),
    CONSTRAINT rail_reservations_slot_check CHECK (slot IN (0, 1))
);
CREATE INDEX rail_reservations_train_idx ON rail_reservations (train_id);

CREATE FUNCTION rail_reservations_loop_only() RETURNS trigger AS $$
BEGIN
    IF NEW.slot = 1 AND NOT EXISTS (SELECT 1 FROM rail_segments WHERE id = NEW.segment_id AND loop) THEN
        RAISE EXCEPTION 'only a passing loop holds a second train' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER rail_reservations_loop_only BEFORE INSERT ON rail_reservations FOR EACH ROW EXECUTE FUNCTION rail_reservations_loop_only();

CREATE TABLE node_stats (
    building_id   uuid        NOT NULL REFERENCES settlement_buildings (id),
    bucket        timestamptz NOT NULL,
    settlement_id uuid        NOT NULL REFERENCES cities (id),
    produced      jsonb       NOT NULL DEFAULT '{}',
    consumed      jsonb       NOT NULL DEFAULT '{}',
    shifts        integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (building_id, bucket),
    CONSTRAINT node_stats_check CHECK (shifts >= 0)
);
CREATE INDEX node_stats_settlement_idx ON node_stats (settlement_id, bucket DESC);

UPDATE charter_offices
   SET grants = grants || '[{"p": "mine.run"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "mine.run"}]'::jsonb;
UPDATE charter_offices
   SET grants = grants || '[{"p": "rail.draw"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "rail.draw"}]'::jsonb;
UPDATE charter_offices
   SET grants = grants || '[{"p": "haul.order"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "haul.order"}]'::jsonb;

COMMIT;
