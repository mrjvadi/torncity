-- 0151_households: NPC households, the people behind the labour pool (ADR 0073, plan B9, slices H0 to H7).
--
-- The labour pool used to be a number: ceil((base_housing + homes - players) x participation). Nobody stood behind it, every NPC wage
-- went to the sink and nobody spent. A household is a small group that lives in one home of a settlement, owns a wallet (ledger account
-- kind household_wallet), eats, drinks, keeps warm, works a seat of the pool, pays for its needs at the stock and the shop, pays tax,
-- and stays or leaves by its satisfaction. A settlement takes part only when it has a household_settlements row (the flag npc.citizens
-- of ADR 0044 G4b, per settlement): 'dual' (households arrive, nothing else changes), 'live' (wages, needs and spending are real),
-- 'off' (the emergency stop).
--
--   household_settlements  the per-settlement flag, its phase, the old pool target and the head's two levers (admission, income tax)
--   households             one row per household; the wallet is the account (household_wallet, owner = households.id)
--   household_days         the fence of a settlement's local day (one row per day); done_at is set when every household was settled
--   household_ticks        the fence of one household's day (one row per household and day) with what it met and spent
--   household_events       append-only: arrived, left, born, grant, notice
--   household_rentals      a player's dwelling opened to tenants (H7): its rent and its slots
--
-- village_shop_sales gains household_id: a household buying at the shelf is a sale without a player.
-- Conventions as 0114: instants are timestamptz in UTC, no DEFAULT now(), named CHECK constraints, additive; never a wipe.
BEGIN;

CREATE TABLE household_settlements (
    settlement_id  uuid        PRIMARY KEY,
    state          text        NOT NULL,
    enrolled_at    timestamptz NOT NULL,
    flipped_at     timestamptz NULL,
    target_pool    bigint      NOT NULL DEFAULT 0,
    admission      text        NOT NULL DEFAULT 'open',
    income_tax_bps bigint      NOT NULL DEFAULT 0,
    updated_by     uuid        NULL,
    updated_at     timestamptz NOT NULL,
    CONSTRAINT household_settlements_state_check CHECK (state IN ('dual', 'live', 'off')),
    CONSTRAINT household_settlements_admission_check CHECK (admission IN ('open', 'closed')),
    CONSTRAINT household_settlements_tax_check CHECK (income_tax_bps >= 0 AND income_tax_bps <= 5000),
    CONSTRAINT household_settlements_target_check CHECK (target_pool >= 0)
);

CREATE TABLE households (
    id               uuid        PRIMARY KEY,
    settlement_id    uuid        NOT NULL,
    home_ref         text        NOT NULL,
    adults           int         NOT NULL,
    dependants       int         NOT NULL DEFAULT 0,
    earners          int         NOT NULL DEFAULT 0,
    name_seed        bigint      NOT NULL,
    state            text        NOT NULL DEFAULT 'living',
    satisfaction_bps int         NOT NULL,
    low_days         int         NOT NULL DEFAULT 0,
    worst_need       text        NOT NULL DEFAULT '',
    arrived_at       timestamptz NOT NULL,
    left_at          timestamptz NULL,
    last_day         bigint      NOT NULL DEFAULT -1,
    CONSTRAINT households_state_check CHECK (state IN ('living', 'leaving', 'gone')),
    CONSTRAINT households_size_check CHECK (adults >= 1 AND dependants >= 0 AND earners >= 0 AND earners <= adults),
    CONSTRAINT households_satisfaction_check CHECK (satisfaction_bps >= 0 AND satisfaction_bps <= 10000),
    CONSTRAINT households_gone_check CHECK ((state = 'gone') = (left_at IS NOT NULL))
);
CREATE INDEX households_by_settlement ON households (settlement_id, state);

CREATE TABLE household_days (
    settlement_id uuid        NOT NULL,
    day           bigint      NOT NULL,
    started_at    timestamptz NOT NULL,
    done_at       timestamptz NULL,
    households    int         NOT NULL DEFAULT 0,
    arrivals      int         NOT NULL DEFAULT 0,
    departures    int         NOT NULL DEFAULT 0,
    CONSTRAINT household_days_pk PRIMARY KEY (settlement_id, day)
);

CREATE TABLE household_ticks (
    household_id     uuid   NOT NULL,
    day              bigint NOT NULL,
    settlement_id    uuid   NOT NULL,
    food_bps         int    NOT NULL,
    water_bps        int    NOT NULL,
    warmth_bps       int    NOT NULL,
    shelter_bps      int    NOT NULL,
    work_bps         int    NOT NULL,
    care_bps         int    NOT NULL,
    spent            bigint NOT NULL DEFAULT 0,
    tax              bigint NOT NULL DEFAULT 0,
    rent             bigint NOT NULL DEFAULT 0,
    score_bps        int    NOT NULL,
    satisfaction_bps int    NOT NULL,
    CONSTRAINT household_ticks_pk PRIMARY KEY (household_id, day),
    CONSTRAINT household_ticks_amounts_check CHECK (spent >= 0 AND tax >= 0 AND rent >= 0)
);
CREATE INDEX household_ticks_by_day ON household_ticks (settlement_id, day);

CREATE TABLE household_events (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    settlement_id uuid        NOT NULL,
    household_id  uuid        NOT NULL,
    kind          text        NOT NULL,
    detail        text        NOT NULL DEFAULT '',
    at            timestamptz NOT NULL,
    CONSTRAINT household_events_kind_check CHECK (kind IN ('arrived', 'left', 'born', 'grant', 'notice', 'grown'))
);
CREATE INDEX household_events_by_settlement ON household_events (settlement_id, id DESC);

CREATE TABLE household_rentals (
    building_id uuid        PRIMARY KEY,
    settlement_id uuid      NOT NULL,
    owner_id    uuid        NOT NULL,
    rent        bigint      NOT NULL,
    slots       int         NOT NULL,
    listed_at   timestamptz NOT NULL,
    CONSTRAINT household_rentals_check CHECK (rent > 0 AND slots > 0)
);

ALTER TABLE accounts DROP CONSTRAINT accounts_kind_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_kind_check CHECK (kind IN (
    'player_cash', 'player_bank', 'company_treasury', 'faction_treasury',
    'city_treasury', 'system_sink', 'system_source', 'player_escrow',
    'state_treasury', 'defence_fund', 'national_bank', 'insurance_fund', 'player_savings',
    'foreign_holding', 'reserve_pot', 'fx_escrow', 'household_wallet'));

ALTER TABLE village_shop_sales ALTER COLUMN player_id DROP NOT NULL;
ALTER TABLE village_shop_sales ADD COLUMN household_id uuid NULL;
ALTER TABLE village_shop_sales ADD CONSTRAINT village_shop_sales_buyer_check CHECK ((player_id IS NULL) <> (household_id IS NULL));

-- The charter permission citizen.admit (open or close the settlement to arriving households) becomes active: the head office of a
-- stored charter that may put goods on sale gets it with the same hand.
UPDATE charter_offices
   SET grants = grants || '[{"p": "citizen.admit"}]'::jsonb
 WHERE acquisition = 'head' AND grants @> '[{"p": "trade.export"}]'::jsonb AND NOT grants @> '[{"p": "citizen.admit"}]'::jsonb;

COMMIT;
