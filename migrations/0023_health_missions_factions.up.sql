-- 0023_health_missions_factions — stage E: a player's health and the
-- hospitals that treat it, the missions a city's boards offer, factions
-- (their members, requests, bank and organised crimes), and the watch that
-- flags alt-account abuse and farming. Rules: internal/domain/health,
-- mission, faction and watch. Content: configs/content/health.yml,
-- missions.yml, factions.yml, crimes.yml (injuries, organised crimes) and
-- companies.yml (a clinic's care). Decision:
-- docs/adr/0023-health-missions-factions.md. Money:
-- docs/adr/0009-economic-control.md (hospital_fee, treatment_fee,
-- faction_registration, faction_deposit, faction_withdrawal, faction_cut,
-- payment_hold, payment_release, payment_return; mission_reward was listed
-- from the start).
--
-- EXACTLY ONCE. A stay is admitted once per player at a time (partial unique
-- index) and discharged by one scheduled game_action it names; a treatment is
-- one per stay (unique). A mission is active once per player and mission at a
-- time; an event moves a player's missions once (mission_progress_events is
-- the consumer's inbox, written in the same transaction as the progress). A
-- faction's organised crime is resolved by one scheduled game_action, its row
-- moving from 'running' once, under its lock. A held payment is settled once:
-- its row moves from 'held' once, under its lock.
--
-- NOBODY DIES. Health never falls below the content's floor (at least 1);
-- the CHECK below is the last word on it.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- player_health — when a player's health last recovered at rest. Health
-- itself stays in player_stats; this row only says from when the game clock
-- has been giving it back. No row: nothing to catch up (a player never hurt).
-- ---------------------------------------------------------------------------
CREATE TABLE player_health (
    player_id   uuid        PRIMARY KEY REFERENCES players (id),
    rest_since  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

ALTER TABLE player_stats ADD CONSTRAINT player_stats_alive_check CHECK (health >= 1) NOT VALID;

-- ---------------------------------------------------------------------------
-- hospital_stays — a player hospitalised: why, where, until when, and the
-- health they arrived and leave with.
-- ---------------------------------------------------------------------------
CREATE TABLE hospital_stays (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id),
    city_id        uuid        NOT NULL REFERENCES cities (id),
    cause          text        NOT NULL,
    cause_ref      uuid        NULL,
    status         text        NOT NULL,
    health_in      int         NOT NULL,
    health_out     int         NOT NULL,
    admitted_at    timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    game_action_id uuid        NOT NULL REFERENCES game_actions (id),
    discharged_at  timestamptz NULL,

    CONSTRAINT hospital_stays_cause_check CHECK (cause IN ('crime', 'war', 'work', 'faction_crime')),
    CONSTRAINT hospital_stays_status_check CHECK (status IN ('admitted', 'discharged')),
    CONSTRAINT hospital_stays_health_check CHECK (health_in >= 1 AND health_out >= health_in),
    CONSTRAINT hospital_stays_time_check CHECK (ends_at >= admitted_at),
    CONSTRAINT hospital_stays_discharged_check CHECK ((status = 'discharged') = (discharged_at IS NOT NULL))
);
CREATE UNIQUE INDEX hospital_stays_one_admitted_idx ON hospital_stays (player_id) WHERE status = 'admitted';
CREATE INDEX hospital_stays_player_idx ON hospital_stays (player_id, admitted_at DESC);

-- ---------------------------------------------------------------------------
-- clinic_services — what a player clinic charges for a treatment, and
-- whether it takes patients. A company of a kind with care (companies.yml).
-- ---------------------------------------------------------------------------
CREATE TABLE clinic_services (
    company_id uuid        PRIMARY KEY REFERENCES companies (id),
    price      bigint      NOT NULL,
    open       boolean     NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT clinic_services_price_check CHECK (price >= 0)
);

-- ---------------------------------------------------------------------------
-- hospital_treatments — one treatment of one stay (append-only): who
-- treated, what it cost and who was paid, what medicine it used, and the
-- time it took off the stay.
-- ---------------------------------------------------------------------------
CREATE TABLE hospital_treatments (
    id                    uuid        PRIMARY KEY,
    stay_id               uuid        NOT NULL REFERENCES hospital_stays (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    city_id               uuid        NOT NULL REFERENCES cities (id),
    provider              text        NOT NULL,
    company_id            uuid        NULL REFERENCES companies (id),
    price                 bigint      NOT NULL,
    method                text        NOT NULL,
    ledger_transaction_id uuid        NULL,
    medicine_item         text        NULL,
    medicine_units        int         NOT NULL,
    doctor_level          int         NOT NULL,
    reduction_bps         int         NOT NULL,
    saved_seconds         bigint      NOT NULL,
    created_at            timestamptz NOT NULL,

    CONSTRAINT hospital_treatments_stay_key UNIQUE (stay_id),
    CONSTRAINT hospital_treatments_provider_check CHECK (provider IN ('city', 'clinic')),
    CONSTRAINT hospital_treatments_company_check CHECK ((provider = 'clinic') = (company_id IS NOT NULL)),
    CONSTRAINT hospital_treatments_price_check CHECK (price >= 0),
    CONSTRAINT hospital_treatments_paid_check CHECK ((price > 0) = (ledger_transaction_id IS NOT NULL)),
    CONSTRAINT hospital_treatments_method_check CHECK (method IN ('cash', 'card', 'free')),
    CONSTRAINT hospital_treatments_medicine_check CHECK (medicine_units >= 0
        AND (medicine_units = 0) = (medicine_item IS NULL)),
    CONSTRAINT hospital_treatments_reduction_check CHECK (reduction_bps BETWEEN 0 AND 10000 AND saved_seconds >= 0)
);
CREATE INDEX hospital_treatments_company_idx ON hospital_treatments (company_id, created_at DESC) WHERE company_id IS NOT NULL;

CREATE TRIGGER hospital_treatments_append_only
    BEFORE UPDATE OR DELETE ON hospital_treatments
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- mission_assignments — a mission a player took from a board: its progress,
-- what it paid, and how it ended.
-- ---------------------------------------------------------------------------
CREATE TABLE mission_assignments (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    player_id       uuid        NOT NULL REFERENCES players (id),
    mission_code    text        NOT NULL,
    city_id         uuid        NOT NULL REFERENCES cities (id),
    board           text        NOT NULL,
    status          text        NOT NULL,
    -- One count per objective, in the content's order.
    progress        bigint[]    NOT NULL,
    accepted_at     timestamptz NOT NULL,
    expires_at      timestamptz NULL,
    ended_at        timestamptz NULL,
    -- What the completion paid, and what the caps kept back.
    reward_cash     bigint      NOT NULL,
    reward_withheld bigint      NOT NULL,
    reward_xp       bigint      NOT NULL,
    -- The reward_grants row of the cash, when there was any. Not a foreign
    -- key: reward_grants is append-only, and a reference into it would
    -- change what TRUNCATE on it reports (admin economy verify checks the
    -- two against each other instead).
    reward_grant_id uuid        NULL,
    content_version int         NOT NULL,

    CONSTRAINT mission_assignments_status_check CHECK (status IN ('active', 'completed', 'abandoned', 'expired')),
    CONSTRAINT mission_assignments_ended_check CHECK ((status = 'active') = (ended_at IS NULL)),
    CONSTRAINT mission_assignments_reward_check CHECK (reward_cash >= 0 AND reward_withheld >= 0 AND reward_xp >= 0
        AND (status = 'completed' OR (reward_cash = 0 AND reward_xp = 0 AND reward_grant_id IS NULL)))
);
CREATE UNIQUE INDEX mission_assignments_one_active_idx ON mission_assignments (player_id, mission_code) WHERE status = 'active';
CREATE INDEX mission_assignments_player_idx ON mission_assignments (player_id, accepted_at DESC);
CREATE INDEX mission_assignments_completed_idx ON mission_assignments (player_id, ended_at) WHERE status = 'completed';

-- ---------------------------------------------------------------------------
-- mission_progress_events — the missions' inbox: an event that moved a
-- player's missions, once.
-- ---------------------------------------------------------------------------
CREATE TABLE mission_progress_events (
    event_id     text        NOT NULL,
    player_id    uuid        NOT NULL REFERENCES players (id),
    subject      text        NOT NULL,
    processed_at timestamptz NOT NULL,

    CONSTRAINT mission_progress_events_pkey PRIMARY KEY (event_id, player_id)
);

-- ---------------------------------------------------------------------------
-- factions — a gang or an organisation: its name, its home city, its
-- leader, its linked Telegram group, and whether it stands.
-- ---------------------------------------------------------------------------
CREATE TABLE factions (
    id                         uuid        PRIMARY KEY,
    code                       text        NOT NULL UNIQUE,
    name                       text        NOT NULL,
    name_key                   text        NOT NULL,
    city_id                    uuid        NOT NULL REFERENCES cities (id),
    leader_id                  uuid        NOT NULL REFERENCES players (id),
    status                     text        NOT NULL,
    founding_fee               bigint      NOT NULL,
    registration_transaction_id uuid       NULL,
    chat_id                    bigint      NULL,
    chat_bot_id                text        NULL,
    chat_language              text        NULL,
    content_version            int         NOT NULL,
    founded_at                 timestamptz NOT NULL,
    disbanded_at               timestamptz NULL,
    updated_at                 timestamptz NOT NULL,

    CONSTRAINT factions_status_check CHECK (status IN ('active', 'disbanded')),
    CONSTRAINT factions_disbanded_check CHECK ((status = 'disbanded') = (disbanded_at IS NOT NULL)),
    CONSTRAINT factions_fee_check CHECK (founding_fee >= 0),
    CONSTRAINT factions_chat_check CHECK ((chat_id IS NULL) = (chat_bot_id IS NULL) AND (chat_id IS NULL OR chat_id < 0))
);
CREATE UNIQUE INDEX factions_name_idx ON factions (name_key) WHERE status = 'active';
CREATE UNIQUE INDEX factions_chat_idx ON factions (chat_id) WHERE status = 'active' AND chat_id IS NOT NULL;
CREATE INDEX factions_city_idx ON factions (city_id) WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- faction_members — who belongs to a faction, and at what rank. A player
-- belongs to one faction at most.
-- ---------------------------------------------------------------------------
CREATE TABLE faction_members (
    player_id  uuid        PRIMARY KEY REFERENCES players (id),
    faction_id uuid        NOT NULL REFERENCES factions (id),
    rank       text        NOT NULL,
    joined_at  timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT faction_members_rank_check CHECK (rank IN ('leader', 'officer', 'member'))
);
CREATE INDEX faction_members_faction_idx ON faction_members (faction_id);
CREATE UNIQUE INDEX faction_members_one_leader_idx ON faction_members (faction_id) WHERE rank = 'leader';

-- ---------------------------------------------------------------------------
-- faction_requests — an invitation a faction sent, or an application a
-- player made, and how it was answered.
-- ---------------------------------------------------------------------------
CREATE TABLE faction_requests (
    id         uuid        PRIMARY KEY,
    no         bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    faction_id uuid        NOT NULL REFERENCES factions (id),
    player_id  uuid        NOT NULL REFERENCES players (id),
    kind       text        NOT NULL,
    status     text        NOT NULL,
    by_player  uuid        NOT NULL REFERENCES players (id),
    created_at timestamptz NOT NULL,
    decided_by uuid        NULL REFERENCES players (id),
    decided_at timestamptz NULL,

    CONSTRAINT faction_requests_kind_check CHECK (kind IN ('invite', 'apply')),
    CONSTRAINT faction_requests_status_check CHECK (status IN ('pending', 'accepted', 'declined', 'withdrawn')),
    CONSTRAINT faction_requests_decided_check CHECK ((status = 'pending') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX faction_requests_one_pending_idx ON faction_requests (faction_id, player_id) WHERE status = 'pending';
CREATE INDEX faction_requests_player_idx ON faction_requests (player_id) WHERE status = 'pending';

-- ---------------------------------------------------------------------------
-- faction_operations — an organised crime: planned at a place, a crew
-- gathered there, run on the game clock and settled once.
-- ---------------------------------------------------------------------------
CREATE TABLE faction_operations (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    faction_id      uuid        NOT NULL REFERENCES factions (id),
    crime_code      text        NOT NULL,
    city_id         uuid        NOT NULL REFERENCES cities (id),
    place_code      text        NOT NULL,
    planned_by      uuid        NOT NULL REFERENCES players (id),
    status          text        NOT NULL,
    chance_bps      int         NOT NULL,
    take            bigint      NOT NULL,
    faction_cut     bigint      NOT NULL,
    game_action_id  uuid        NULL REFERENCES game_actions (id),
    gather_until    timestamptz NOT NULL,
    launched_at     timestamptz NULL,
    resolves_at     timestamptz NULL,
    resolved_at     timestamptz NULL,
    content_version int         NOT NULL,
    created_at      timestamptz NOT NULL,

    CONSTRAINT faction_operations_status_check CHECK (status IN ('gathering', 'running', 'succeeded', 'escaped', 'caught', 'called_off')),
    CONSTRAINT faction_operations_run_check CHECK ((status = 'gathering' OR status = 'called_off') = (launched_at IS NULL)
        AND (launched_at IS NULL) = (game_action_id IS NULL) AND (launched_at IS NULL) = (resolves_at IS NULL)),
    CONSTRAINT faction_operations_resolved_check CHECK ((status IN ('succeeded', 'escaped', 'caught')) = (resolved_at IS NOT NULL)),
    CONSTRAINT faction_operations_money_check CHECK (take >= 0 AND faction_cut >= 0 AND faction_cut <= take
        AND chance_bps BETWEEN 0 AND 10000)
);
CREATE UNIQUE INDEX faction_operations_one_open_idx ON faction_operations (faction_id) WHERE status IN ('gathering', 'running');

-- faction_operation_crew — who took part, at what rank, and their share.
CREATE TABLE faction_operation_crew (
    operation_id uuid        NOT NULL REFERENCES faction_operations (id),
    player_id    uuid        NOT NULL REFERENCES players (id),
    rank         text        NOT NULL,
    share        bigint      NOT NULL,
    joined_at    timestamptz NOT NULL,

    CONSTRAINT faction_operation_crew_pkey PRIMARY KEY (operation_id, player_id),
    CONSTRAINT faction_operation_crew_rank_check CHECK (rank IN ('leader', 'officer', 'member')),
    CONSTRAINT faction_operation_crew_share_check CHECK (share >= 0)
);
CREATE INDEX faction_operation_crew_player_idx ON faction_operation_crew (player_id);

-- ---------------------------------------------------------------------------
-- watch_flags — what the watch noticed: a rule, the player (and the other
-- account) it is about, how strongly, and the evidence. An operator clears
-- a flag; nothing is ever banned automatically.
-- ---------------------------------------------------------------------------
CREATE TABLE watch_flags (
    id              uuid        PRIMARY KEY,
    no              bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    rule            text        NOT NULL,
    player_id       uuid        NOT NULL REFERENCES players (id),
    other_player_id uuid        NULL REFERENCES players (id),
    score           int         NOT NULL,
    hits            int         NOT NULL,
    evidence        jsonb       NOT NULL,
    status          text        NOT NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    cleared_at      timestamptz NULL,
    cleared_by      text        NULL,
    note            text        NULL,

    CONSTRAINT watch_flags_rule_check CHECK (rule IN ('one_way_transfers', 'off_market_trade', 'single_partner', 'command_rate')),
    CONSTRAINT watch_flags_status_check CHECK (status IN ('open', 'cleared')),
    CONSTRAINT watch_flags_cleared_check CHECK ((status = 'cleared') = (cleared_at IS NOT NULL)),
    CONSTRAINT watch_flags_hits_check CHECK (hits >= 1 AND score >= 0),
    CONSTRAINT watch_flags_pair_check CHECK (other_player_id IS NULL OR other_player_id <> player_id)
);
CREATE UNIQUE INDEX watch_flags_one_open_idx ON watch_flags
    (rule, player_id, COALESCE(other_player_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE status = 'open';
CREATE INDEX watch_flags_open_idx ON watch_flags (created_at DESC) WHERE status = 'open';

-- ---------------------------------------------------------------------------
-- payment_holds — a payment the watch held for review: the money sits in
-- the payer's escrow until an operator releases it to the payee or returns
-- it to the payer.
-- ---------------------------------------------------------------------------
CREATE TABLE payment_holds (
    id                    uuid        PRIMARY KEY,
    no                    bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    payer_id              uuid        NOT NULL REFERENCES players (id),
    payee_id              uuid        NOT NULL REFERENCES players (id),
    method                text        NOT NULL,
    amount                bigint      NOT NULL,
    flag_id               uuid        NULL REFERENCES watch_flags (id),
    status                text        NOT NULL,
    hold_transaction_id   uuid        NOT NULL,
    settle_transaction_id uuid        NULL,
    created_at            timestamptz NOT NULL,
    settled_at            timestamptz NULL,
    settled_by            text        NULL,
    note                  text        NULL,

    CONSTRAINT payment_holds_method_check CHECK (method IN ('cash', 'card')),
    CONSTRAINT payment_holds_amount_check CHECK (amount > 0),
    CONSTRAINT payment_holds_pair_check CHECK (payer_id <> payee_id),
    CONSTRAINT payment_holds_status_check CHECK (status IN ('held', 'released', 'returned')),
    CONSTRAINT payment_holds_settled_check CHECK ((status = 'held') = (settled_at IS NULL)
        AND (settled_at IS NULL) = (settle_transaction_id IS NULL))
);
CREATE INDEX payment_holds_held_idx ON payment_holds (created_at) WHERE status = 'held';

COMMENT ON TABLE player_health IS 'When a player''s health last recovered at rest, on the game clock.';
COMMENT ON TABLE hospital_stays IS 'A player hospitalised; one admitted stay per player at a time.';
COMMENT ON TABLE clinic_services IS 'A player clinic''s treatment price and whether it takes patients.';
COMMENT ON TABLE hospital_treatments IS 'One treatment per stay: provider, price, medicine used, time saved. Append-only.';
COMMENT ON TABLE mission_assignments IS 'A mission a player took from a board, its progress and reward.';
COMMENT ON TABLE mission_progress_events IS 'The missions'' inbox: each event moves a player''s missions once.';
COMMENT ON TABLE factions IS 'A player faction: name, home city, leader, linked group.';
COMMENT ON TABLE faction_members IS 'Faction membership and rank; one faction per player.';
COMMENT ON TABLE faction_requests IS 'Faction invitations and applications.';
COMMENT ON TABLE faction_operations IS 'A faction''s organised crime, resolved once on the game clock.';
COMMENT ON TABLE faction_operation_crew IS 'The crew of an organised crime and each one''s share.';
COMMENT ON TABLE watch_flags IS 'Behavioural anti-cheat flags with evidence; cleared by an operator, never an automatic ban.';
COMMENT ON TABLE payment_holds IS 'Payments held for review in the payer''s escrow, released or returned once.';

COMMIT;
