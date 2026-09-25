-- 0028_life_and_legacy — stage G1: a character's life. Needs that drift on
-- the game clock (hunger, sleep, stress), the mood's clock, age, intelligence,
-- the headline rank by net worth, the bio and the avatar; the permanent life
-- history; the nights slept at a hostel or on a bench; the Telegram photos
-- kept per bot; and the leaderboards refreshed once a period. Rules:
-- internal/domain/life. Content: configs/content/life.yml. Decision:
-- docs/adr/0025-life-and-legacy.md. Money: docs/adr/0009-economic-control.md
-- (lodging_fee).
--
-- LAZY. Nothing ticks: a need is stored in milli-points with the instant it
-- describes (needs_at) and read forward on the game clock; happiness (in
-- player_stats) with happiness_at. What a hard-pressed body costs energy
-- regeneration is kept beside the energy it slows (player_stats.regen_bps),
-- so every reader of energy honours it.
--
-- EXACTLY ONCE. An event touches a life once (life_events is the consumer's
-- inbox, written in the same transaction as the change) and writes a history
-- entry once (life_history's source is unique per player). A leaderboard
-- period is refreshed once (leaderboard_periods' primary key, the clock row
-- locked first).
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- player_stats.regen_bps — how fast energy comes back, 10000 = as always. A
-- hungry, tired or stressed body regenerates slower (life.yml needs.*); the
-- value is set when the needs are looked at, and energy caught up to that
-- moment first.
-- ---------------------------------------------------------------------------
ALTER TABLE player_stats ADD COLUMN regen_bps int NOT NULL DEFAULT 10000;
ALTER TABLE player_stats ADD CONSTRAINT player_stats_regen_check CHECK (regen_bps BETWEEN 1 AND 10000);

-- ---------------------------------------------------------------------------
-- player_life — one row per character who has been looked at: needs in
-- milli-points (0..100000) as of needs_at, the instant the stored happiness
-- describes, intelligence, the rank held and the worth it was judged on, the
-- company equity the investors' board measures growth from, the bio and the
-- avatar (a content avatar's code, or 'photo' for the Telegram photo).
-- ---------------------------------------------------------------------------
CREATE TABLE player_life (
    player_id     uuid        PRIMARY KEY REFERENCES players (id),
    born_at       timestamptz NOT NULL,
    hunger        int         NOT NULL,
    sleep         int         NOT NULL,
    stress        int         NOT NULL,
    needs_at      timestamptz NOT NULL,
    happiness_at  timestamptz NOT NULL,
    intelligence  int         NOT NULL,
    rank          text        NULL,
    rank_since    timestamptz NULL,
    net_worth     bigint      NOT NULL DEFAULT 0,
    net_worth_at  timestamptz NULL,
    equity        bigint      NOT NULL DEFAULT 0,
    bio           text        NULL,
    avatar        text        NULL,
    last_sleep_at timestamptz NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,

    CONSTRAINT player_life_needs_check CHECK (hunger BETWEEN 0 AND 100000 AND sleep BETWEEN 0 AND 100000
        AND stress BETWEEN 0 AND 100000),
    CONSTRAINT player_life_intelligence_check CHECK (intelligence >= 0),
    CONSTRAINT player_life_rank_check CHECK ((rank IS NULL) = (rank_since IS NULL)),
    CONSTRAINT player_life_bio_check CHECK (bio IS NULL OR char_length(bio) BETWEEN 1 AND 500),
    CONSTRAINT player_life_avatar_check CHECK (avatar IS NULL OR avatar ~ '^[a-z][a-z0-9_]*$')
);
CREATE INDEX player_life_worth_idx ON player_life (net_worth DESC);

-- ---------------------------------------------------------------------------
-- life_history — a player's permanent timeline: what happened (a closed set
-- of kinds, worded by the screens), when, whether others may see it, whether
-- it was reconstructed from older records when the timeline began, what
-- wrote it (an event's id, a backfill's reference — once per player), and
-- what it is about. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE life_history (
    id          uuid        PRIMARY KEY,
    player_id   uuid        NOT NULL REFERENCES players (id),
    kind        text        NOT NULL,
    at          timestamptz NOT NULL,
    public      boolean     NOT NULL,
    backfilled  boolean     NOT NULL,
    source      text        NOT NULL,
    data        jsonb       NOT NULL,
    recorded_at timestamptz NOT NULL,

    CONSTRAINT life_history_source_key UNIQUE (player_id, source),
    CONSTRAINT life_history_kind_check CHECK (kind IN ('joined', 'first_job', 'hired', 'promoted', 'course',
        'certificate', 'company_founded', 'company_closed', 'property_bought', 'property_sold', 'election_won',
        'election_lost', 'office_taken', 'office_lost', 'jailed', 'convicted', 'hospitalised', 'war_command',
        'achievement', 'rank_up', 'rank_down', 'big_trade'))
);
CREATE INDEX life_history_player_idx ON life_history (player_id, at DESC, id);

CREATE TRIGGER life_history_append_only
    BEFORE UPDATE OR DELETE ON life_history
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER life_history_no_truncate
    BEFORE TRUNCATE ON life_history
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- life_events — the life consumer's inbox: an event touches a player's life
-- once.
-- ---------------------------------------------------------------------------
CREATE TABLE life_events (
    event_id     text        NOT NULL,
    player_id    uuid        NOT NULL REFERENCES players (id),
    subject      text        NOT NULL,
    processed_at timestamptz NOT NULL,

    CONSTRAINT life_events_pkey PRIMARY KEY (event_id, player_id)
);

-- ---------------------------------------------------------------------------
-- life_sleeps — a night at a spot anyone may sleep at (life.yml sleep.spots):
-- a hostel bed paid to the city (lodging_fee), a free park bench.
-- Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE life_sleeps (
    id                    uuid        PRIMARY KEY,
    player_id             uuid        NOT NULL REFERENCES players (id),
    spot                  text        NOT NULL,
    city_id               uuid        NOT NULL REFERENCES cities (id),
    price                 bigint      NOT NULL,
    method                text        NULL,
    ledger_transaction_id uuid        NULL,
    slept_at              timestamptz NOT NULL,

    CONSTRAINT life_sleeps_price_check CHECK (price >= 0),
    CONSTRAINT life_sleeps_paid_check CHECK ((price > 0) = (ledger_transaction_id IS NOT NULL)
        AND (price > 0) = (method IS NOT NULL)),
    CONSTRAINT life_sleeps_method_check CHECK (method IS NULL OR method IN ('cash', 'card'))
);
CREATE INDEX life_sleeps_player_idx ON life_sleeps (player_id, slept_at DESC);

CREATE TRIGGER life_sleeps_append_only
    BEFORE UPDATE OR DELETE ON life_sleeps
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER life_sleeps_no_truncate
    BEFORE TRUNCATE ON life_sleeps
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- player_photos — a player's Telegram profile photo as one bot knows it: a
-- file id is valid only for the bot that received it (Bot API, "Sending
-- files"), so each bot keeps its own, fetched with getUserProfilePhotos.
-- ---------------------------------------------------------------------------
CREATE TABLE player_photos (
    player_id  uuid        NOT NULL REFERENCES players (id),
    bot_id     uuid        NOT NULL,
    file_id    text        NOT NULL,
    fetched_at timestamptz NOT NULL,

    CONSTRAINT player_photos_pkey PRIMARY KEY (player_id, bot_id),
    CONSTRAINT player_photos_file_check CHECK (char_length(file_id) BETWEEN 1 AND 512)
);

-- ---------------------------------------------------------------------------
-- leaderboard_clock — the one clock the leaderboards refresh on (life.yml
-- leaderboards.period, GAME time).
-- ---------------------------------------------------------------------------
CREATE TABLE leaderboard_clock (
    id                int         PRIMARY KEY,
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL REFERENCES game_actions (id),
    updated_at        timestamptz NOT NULL,

    CONSTRAINT leaderboard_clock_one_check CHECK (id = 1),
    CONSTRAINT leaderboard_clock_period_check CHECK (period_no >= 1),
    CONSTRAINT leaderboard_clock_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- leaderboard_periods — each period refreshed, once. Append-only.
-- ---------------------------------------------------------------------------
CREATE TABLE leaderboard_periods (
    period_no    bigint      PRIMARY KEY,
    refreshed_at timestamptz NOT NULL,

    CONSTRAINT leaderboard_periods_no_check CHECK (period_no >= 1)
);

CREATE TRIGGER leaderboard_periods_append_only
    BEFORE UPDATE OR DELETE ON leaderboard_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER leaderboard_periods_no_truncate
    BEFORE TRUNCATE ON leaderboard_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- leaderboard_lines — one period's boards, materialised: who or what stands
-- where, by public code and name, a tag (a rank, a city, a kind of business)
-- and its values. The last `keep` periods are kept.
-- ---------------------------------------------------------------------------
CREATE TABLE leaderboard_lines (
    period_no bigint NOT NULL REFERENCES leaderboard_periods (period_no),
    board     text   NOT NULL,
    position  int    NOT NULL,
    code      text   NOT NULL,
    name      text   NOT NULL,
    tag       text   NOT NULL,
    tag_name  text   NOT NULL,
    value     bigint NOT NULL,
    extra     bigint NOT NULL,
    extra2    bigint NOT NULL,

    CONSTRAINT leaderboard_lines_pkey PRIMARY KEY (period_no, board, position),
    CONSTRAINT leaderboard_lines_board_check CHECK (board IN ('richest', 'companies', 'cities', 'workers', 'investors')),
    CONSTRAINT leaderboard_lines_position_check CHECK (position >= 1)
);

COMMIT;
