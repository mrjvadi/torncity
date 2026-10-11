-- 0147_survival: thirst, warmth, garments and sickness (ADR 0070, plan B6).
--
--   player_survival   a player's thirst and warmth as lazy needs (milli-points with the instant each describes, as hunger), the
--                     shelter a warm place or an inn gave until some instant, and whether the next collapse may admit them
--                     (a stay of cause exposure or dehydration disarms it until the need is below severe again)
--   player_garments   the garments a player dresses in: moved out of the bag when put on (item reason garment_worn), kept with
--                     their wear left; at most one worn per slot; a garment that wears out stays as a row of status worn_out
--   player_illness    waterborne and chill sickness: a course with an end, shortened by a medicine of the settlement stock
--   hospital_stays.cause   two more causes: exposure and dehydration
--
-- No foreign key to players or cities (as the other village tables). Conventions as 0137.
BEGIN;

CREATE TABLE player_survival (
    player_id     uuid        PRIMARY KEY,
    thirst_milli  integer     NOT NULL DEFAULT 0,
    thirst_at     timestamptz NOT NULL,
    warmth_milli  integer     NOT NULL DEFAULT 0,
    warmth_at     timestamptz NOT NULL,
    shelter_until timestamptz NULL,
    shelter_bonus integer     NOT NULL DEFAULT 0,
    cold_armed    boolean     NOT NULL DEFAULT true,
    thirst_armed  boolean     NOT NULL DEFAULT true,
    notified_at   timestamptz NULL,
    CONSTRAINT player_survival_check CHECK (thirst_milli BETWEEN 0 AND 100000 AND warmth_milli BETWEEN 0 AND 100000 AND shelter_bonus >= 0)
);

CREATE TABLE player_garments (
    id         uuid        PRIMARY KEY,
    player_id  uuid        NOT NULL,
    item       text        NOT NULL,
    slot       text        NOT NULL,
    uses_left  integer     NOT NULL,
    worn       boolean     NOT NULL DEFAULT false,
    status     text        NOT NULL DEFAULT 'kept',
    counted_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT player_garments_check CHECK (uses_left >= 0 AND status IN ('kept', 'worn_out') AND (status = 'kept' OR NOT worn))
);
CREATE UNIQUE INDEX player_garments_slot_idx ON player_garments (player_id, slot) WHERE worn AND status = 'kept';
CREATE INDEX player_garments_player_idx ON player_garments (player_id) WHERE status = 'kept';

CREATE TABLE player_illness (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL,
    kind           text        NOT NULL,
    onset_at       timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    status         text        NOT NULL DEFAULT 'active',
    settlement_id  uuid        NULL,
    building_id    uuid        NULL,
    medicine_item  text        NULL,
    medicine_units integer     NOT NULL DEFAULT 0,
    treated_at     timestamptz NULL,
    CONSTRAINT player_illness_check CHECK (kind IN ('waterborne', 'chill') AND status IN ('active', 'ended') AND ends_at >= onset_at
        AND medicine_units >= 0 AND (medicine_units = 0) = (medicine_item IS NULL))
);
CREATE UNIQUE INDEX player_illness_one_active_idx ON player_illness (player_id, kind) WHERE status = 'active';

ALTER TABLE hospital_stays DROP CONSTRAINT hospital_stays_cause_check;
ALTER TABLE hospital_stays ADD CONSTRAINT hospital_stays_cause_check
    CHECK (cause IN ('crime', 'war', 'work', 'faction_crime', 'exposure', 'dehydration'));

COMMIT;
