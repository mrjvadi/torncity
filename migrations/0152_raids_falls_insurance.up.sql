-- Death, insurance, raids and detection (docs/adr/0074-death-insurance-raids-detection.md, plan B10).
--
--   settlement_raids and its participants, detections and loot   a raid is a charter act of one settlement against another
--   falls                                                         a player who fell in a raid, what they lost (once per cause)
--   building_sight                                                the cached sight of a lookout, a tower or a beacon (territory.Compute)
--   settlement_insurers                                           a settlement's mutual insurance fund
--   insurance_policies                                            more covers (death_loss, raid_damage), an issuer that is not a country
--   hospital_stays.cause                                          one more cause: raid
BEGIN;

CREATE TABLE settlement_raids (
    id            uuid        PRIMARY KEY,
    attacker_id   uuid        NOT NULL REFERENCES cities (id),
    defender_id   uuid        NOT NULL REFERENCES cities (id),
    filed_by      uuid        NOT NULL REFERENCES players (id),
    filed_at      timestamptz NOT NULL,
    start_at      timestamptz NOT NULL,
    end_at        timestamptz NOT NULL,
    status        text        NOT NULL,
    seed          bigint      NOT NULL,
    fee           bigint      NOT NULL,
    fee_tx        uuid        NULL,
    tribute       bigint      NULL,
    looted        boolean     NOT NULL DEFAULT false,
    resolved_at   timestamptz NULL,
    outcome       jsonb       NULL,
    CONSTRAINT settlement_raids_status_check CHECK (status IN ('filed', 'open', 'resolved', 'settled')),
    CONSTRAINT settlement_raids_time_check CHECK (start_at > filed_at AND end_at > start_at),
    CONSTRAINT settlement_raids_pair_check CHECK (attacker_id <> defender_id),
    CONSTRAINT settlement_raids_fee_check CHECK (fee >= 0 AND (tribute IS NULL OR tribute > 0)),
    CONSTRAINT settlement_raids_done_check CHECK ((status IN ('resolved', 'settled')) = (resolved_at IS NOT NULL))
);
CREATE INDEX settlement_raids_defender_idx ON settlement_raids (defender_id, filed_at DESC);
CREATE INDEX settlement_raids_pair_idx ON settlement_raids (attacker_id, defender_id, filed_at DESC);

CREATE TABLE settlement_raid_participants (
    raid_id   uuid        NOT NULL REFERENCES settlement_raids (id),
    player_id uuid        NOT NULL REFERENCES players (id),
    side      text        NOT NULL,
    joined_at timestamptz NOT NULL,
    PRIMARY KEY (raid_id, player_id),
    CONSTRAINT settlement_raid_participants_side_check CHECK (side IN ('attacker', 'defender'))
);
CREATE INDEX settlement_raid_participants_player_idx ON settlement_raid_participants (player_id);

CREATE TABLE settlement_raid_detections (
    raid_id     uuid        NOT NULL REFERENCES settlement_raids (id),
    building_id uuid        NOT NULL,
    code        text        NOT NULL,
    chance_bps  int         NOT NULL,
    roll        int         NOT NULL,
    detected    boolean     NOT NULL,
    sight_m     bigint      NOT NULL,
    hops        int         NOT NULL,
    lead_secs   bigint      NOT NULL,
    at          timestamptz NOT NULL,
    PRIMARY KEY (raid_id, building_id),
    CONSTRAINT settlement_raid_detections_chance_check CHECK (chance_bps BETWEEN 0 AND 10000 AND roll BETWEEN 0 AND 9999)
);

-- what a raid took and broke: append-only
CREATE TABLE settlement_raid_loot (
    id          uuid        PRIMARY KEY,
    raid_id     uuid        NOT NULL REFERENCES settlement_raids (id),
    kind        text        NOT NULL,
    item        text        NULL,
    qty         bigint      NOT NULL,
    building_id uuid        NULL,
    ledger_tx   uuid        NULL,
    at          timestamptz NOT NULL,
    CONSTRAINT settlement_raid_loot_kind_check CHECK (kind IN ('cash', 'item', 'damage', 'tribute')),
    CONSTRAINT settlement_raid_loot_qty_check CHECK (qty > 0 AND (kind = 'item') = (item IS NOT NULL))
);
CREATE INDEX settlement_raid_loot_raid_idx ON settlement_raid_loot (raid_id);
CREATE TRIGGER settlement_raid_loot_append_only BEFORE UPDATE OR DELETE ON settlement_raid_loot
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER settlement_raid_loot_no_truncate BEFORE TRUNCATE ON settlement_raid_loot
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- a fall: the one event that costs a player assets, once per cause
CREATE TABLE falls (
    id         uuid        PRIMARY KEY,
    player_id  uuid        NOT NULL REFERENCES players (id),
    cause      text        NOT NULL,
    cause_ref  uuid        NOT NULL,
    lost_cash  bigint      NOT NULL,
    lost_goods jsonb       NOT NULL DEFAULT '{}'::jsonb,
    capped     boolean     NOT NULL DEFAULT false,
    cooling    boolean     NOT NULL DEFAULT false,
    net_assets bigint      NOT NULL DEFAULT 0,
    lost_worth bigint      NOT NULL DEFAULT 0,
    stay_id    uuid        NULL,
    cash_tx    uuid        NULL,
    at         timestamptz NOT NULL,
    CONSTRAINT falls_once_key UNIQUE (player_id, cause_ref),
    CONSTRAINT falls_cause_check CHECK (cause IN ('raid', 'war')),
    CONSTRAINT falls_loss_check CHECK (lost_cash >= 0 AND (lost_cash > 0) = (cash_tx IS NOT NULL))
);
CREATE INDEX falls_player_idx ON falls (player_id, at DESC);

-- the sight of a standing detector, computed once per revision of the building and the world
CREATE TABLE building_sight (
    building_id uuid        PRIMARY KEY,
    revision    text        NOT NULL,
    sight       integer[]   NOT NULL,
    computed_at timestamptz NOT NULL
);

-- a settlement's own insurance fund (a friendly society): the office opens it, residents buy cover from it
CREATE TABLE settlement_insurers (
    settlement_id uuid        PRIMARY KEY REFERENCES cities (id),
    opened_by     uuid        NOT NULL REFERENCES players (id),
    opened_at     timestamptz NOT NULL,
    scale_bps     int         NOT NULL DEFAULT 10000,
    CONSTRAINT settlement_insurers_scale_check CHECK (scale_bps BETWEEN 5000 AND 20000)
);

-- policies: an issuer that is a country, a settlement or a company (country_id holds the issuer's id), the new covers, a building
ALTER TABLE insurance_policies DROP CONSTRAINT insurance_policies_country_id_fkey;
ALTER TABLE insurance_policies ADD COLUMN issuer_kind text NOT NULL DEFAULT 'country';
ALTER TABLE insurance_policies ADD COLUMN building_id uuid NULL;
ALTER TABLE insurance_policies ADD CONSTRAINT insurance_policies_issuer_check CHECK (issuer_kind IN ('country', 'settlement', 'company'));
ALTER TABLE insurance_policies DROP CONSTRAINT insurance_policies_covers_check;
ALTER TABLE insurance_policies ADD CONSTRAINT insurance_policies_covers_check CHECK (
    covers IN ('hospital', 'war_damage', 'death_loss', 'raid_damage')
    AND (covers = 'war_damage') = (property_id IS NOT NULL)
    AND (covers = 'raid_damage') = (building_id IS NOT NULL));
DROP INDEX insurance_policies_one_idx;
CREATE UNIQUE INDEX insurance_policies_one_idx ON insurance_policies
    (player_id, product, COALESCE(property_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(building_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'active';

ALTER TABLE hospital_stays DROP CONSTRAINT hospital_stays_cause_check;
ALTER TABLE hospital_stays ADD CONSTRAINT hospital_stays_cause_check
    CHECK (cause IN ('crime', 'war', 'work', 'faction_crime', 'exposure', 'dehydration', 'raid'));

-- The permissions raid.declare and insurance.run become active: the head office of a stored charter that may put goods on sale gets
-- them with the same hand (a settlement's raids and its own insurance fund are the head's to start, ADR 0074).
UPDATE charter_offices
   SET grants = grants || '[{"p": "raid.declare"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "raid.declare"}]'::jsonb;
UPDATE charter_offices
   SET grants = grants || '[{"p": "insurance.run"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "insurance.run"}]'::jsonb;

COMMIT;
