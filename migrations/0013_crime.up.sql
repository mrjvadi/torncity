-- 0013_crime — the crime engine: crimes, jail, reports and investigations.
-- Rules: internal/domain/crime. Content: configs/content/crimes.yml
-- (docs/adr/0004-content-system.md). Policy: the police chief's levers in
-- configs/content/governance.yml, read through the policy resolver
-- (docs/adr/0015-player-held-offices.md). Money: every take, fine, fee,
-- restitution and bail moves through the ledger of 0006 under its own reason
-- (docs/adr/0009-economic-control.md); nothing here holds a balance.
-- Decisions: docs/adr/0019-crime-engine.md. Schema: docs/database.md,
-- section 10.
--
-- What is content and what is state:
--   * crime_content — content, one set of rows per content version, like
--     career_definitions: the tiers, venues, categories and crimes, each as
--     the authored document.
--   * criminal_profiles, crimes, jail_sentences, crime_reports — state. They
--     name a crime by its CODE, the identity every version shares.
--   * crime_npc_proceeds — the running total of the one faucet crime opens,
--     per UTC day, so the global daily cap holds under concurrency.
--   * players.last_active_at — when a player last did anything, so a crime
--     lands only on someone actually playing.
--
-- One crime at a time, one sentence at a time, one report per theft: each is
-- a partial unique index, so a double press, a redelivery or two devices
-- cannot break it whatever races.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- crime_content — the crime content of one content version.
-- ---------------------------------------------------------------------------
CREATE TABLE crime_content (
    id                 uuid  PRIMARY KEY,
    content_version_id uuid  NOT NULL REFERENCES content_versions (id),
    -- tier | venue | category | crime: which list of crimes.yml.
    kind               text  NOT NULL,
    code               text  NOT NULL,
    -- The entry's place in its list: tiers are a ladder and venues are
    -- matched in order, so the order is content too.
    position           int   NOT NULL,
    definition         jsonb NOT NULL,

    CONSTRAINT crime_content_kind_check CHECK (kind IN ('tier', 'venue', 'category', 'crime')),
    CONSTRAINT crime_content_version_kind_code_key UNIQUE (content_version_id, kind, code)
);

CREATE INDEX crime_content_content_version_id_idx ON crime_content (content_version_id);

COMMENT ON COLUMN crime_content.definition IS
    'The authored entry (content.CrimeTierDef, VenueDef, CrimeCategoryDef or CrimeDef) as a document. Validated by the loader before it is written.';

-- ---------------------------------------------------------------------------
-- players.last_active_at — the last time the player did anything in the game.
-- NULL until their first command after this migration: nobody is "nearby"
-- until they play.
-- ---------------------------------------------------------------------------
ALTER TABLE players ADD COLUMN last_active_at timestamptz NULL;

CREATE INDEX players_city_active_idx ON players (city_id, last_active_at)
    WHERE status = 'active';

-- ---------------------------------------------------------------------------
-- criminal_profiles — one row per player who has ever touched crime: nerve,
-- heat, criminal experience and the public counts of their record. The row
-- is also the lock every crime command of the player takes first.
-- ---------------------------------------------------------------------------
CREATE TABLE criminal_profiles (
    player_id          uuid        PRIMARY KEY REFERENCES players (id),
    nerve              int         NOT NULL,
    nerve_updated_at   timestamptz NOT NULL,
    heat               int         NOT NULL DEFAULT 0,
    heat_updated_at    timestamptz NOT NULL,
    criminal_xp        bigint      NOT NULL DEFAULT 0,
    attempts           int         NOT NULL DEFAULT 0,
    successes          int         NOT NULL DEFAULT 0,
    arrests            int         NOT NULL DEFAULT 0,
    convictions        int         NOT NULL DEFAULT 0,
    -- What a conviction ordered and the offender could not pay, kept for the
    -- debt system of ADR 0006; nothing collects it yet.
    unpaid_restitution bigint      NOT NULL DEFAULT 0,
    unpaid_fines       bigint      NOT NULL DEFAULT 0,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,

    CONSTRAINT criminal_profiles_nerve_check CHECK (nerve >= 0),
    CONSTRAINT criminal_profiles_heat_check CHECK (heat >= 0),
    CONSTRAINT criminal_profiles_counts_check
        CHECK (criminal_xp >= 0 AND attempts >= 0 AND successes >= 0 AND arrests >= 0 AND convictions >= 0),
    CONSTRAINT criminal_profiles_unpaid_check CHECK (unpaid_restitution >= 0 AND unpaid_fines >= 0)
);

-- ---------------------------------------------------------------------------
-- crimes — one row per attempt. An instant attempt is written resolved; a
-- timed one is written 'in_progress' with its end on the schedule, and moves
-- once, under a row lock, to its outcome. The row is the criminal record.
-- ---------------------------------------------------------------------------
CREATE TABLE crimes (
    id                    uuid        PRIMARY KEY,
    player_id             uuid        NOT NULL REFERENCES players (id),
    crime_code            text        NOT NULL,
    category              text        NOT NULL,
    -- Where it was committed: the city, and the venue inside it.
    city_id               uuid        NOT NULL REFERENCES cities (id),
    venue_code            text        NOT NULL,
    -- Who it landed on, decided by chance at the start: never chosen.
    victim_kind           text        NOT NULL,
    victim_player_id      uuid        NULL REFERENCES players (id),
    status                text        NOT NULL,
    -- The odds, fixed at the start: the number shown is the number rolled.
    chance_bps            int         NOT NULL,
    nerve_cost            int         NOT NULL,
    -- What the thief gained (succeeded) — minor units.
    reward_amount         bigint      NOT NULL DEFAULT 0,
    -- A success against a player that was seen: the victim knows who.
    witnessed             boolean     NOT NULL DEFAULT false,
    -- On an arrest: the fine ordered and what was actually paid.
    fine_amount           bigint      NOT NULL DEFAULT 0,
    fine_paid             bigint      NOT NULL DEFAULT 0,
    jail_sentence_id      uuid        NULL,
    -- The take (theft or crime_proceeds); NULL when nothing moved.
    ledger_transaction_id uuid        NULL,
    -- The scheduled end of a timed attempt (action_type 'crime').
    game_action_id        uuid        NULL REFERENCES game_actions (id),
    content_version       int         NOT NULL,
    started_at            timestamptz NOT NULL,
    resolves_at           timestamptz NOT NULL,
    resolved_at           timestamptz NULL,

    CONSTRAINT crimes_status_check CHECK (status IN ('in_progress', 'succeeded', 'escaped', 'caught')),
    CONSTRAINT crimes_victim_kind_check CHECK (victim_kind IN ('npc', 'player', 'business', 'property')),
    CONSTRAINT crimes_victim_player_check CHECK ((victim_kind = 'player') = (victim_player_id IS NOT NULL)),
    CONSTRAINT crimes_resolved_check CHECK ((status = 'in_progress') = (resolved_at IS NULL)),
    CONSTRAINT crimes_period_check CHECK (resolves_at >= started_at),
    CONSTRAINT crimes_amounts_check
        CHECK (reward_amount >= 0 AND fine_amount >= 0 AND fine_paid >= 0 AND fine_paid <= fine_amount),
    CONSTRAINT crimes_chance_check CHECK (chance_bps BETWEEN 0 AND 10000),
    CONSTRAINT crimes_nerve_check CHECK (nerve_cost > 0)
);

-- One crime at a time.
CREATE UNIQUE INDEX crimes_one_in_progress_idx ON crimes (player_id) WHERE status = 'in_progress';

-- The criminal record, most recent first.
CREATE INDEX crimes_player_idx ON crimes (player_id, started_at DESC);

-- "When was this player last robbed" — the victim cooldowns.
CREATE INDEX crimes_victim_idx ON crimes (victim_player_id, started_at DESC)
    WHERE victim_player_id IS NOT NULL AND status = 'succeeded';

-- ---------------------------------------------------------------------------
-- jail_sentences — time served. One 'serving' sentence per player; a second
-- conviction extends it rather than stacking a second row.
-- ---------------------------------------------------------------------------
CREATE TABLE jail_sentences (
    id                  uuid        PRIMARY KEY,
    player_id           uuid        NOT NULL REFERENCES players (id),
    -- The city whose police jailed them, whose treasury bail is paid into.
    city_id             uuid        NOT NULL REFERENCES cities (id),
    crime_id            uuid        NULL REFERENCES crimes (id),
    -- arrest (caught in the act) | conviction (a report was solved).
    reason              text        NOT NULL,
    -- The sentence in GAME seconds, as handed down.
    term_seconds        bigint      NOT NULL,
    -- The scheduled release (action_type 'jail_release'); replaced when a
    -- conviction extends the sentence.
    game_action_id      uuid        NOT NULL REFERENCES game_actions (id),
    status              text        NOT NULL,
    bail_paid           bigint      NOT NULL DEFAULT 0,
    bail_transaction_id uuid        NULL,
    starts_at           timestamptz NOT NULL,
    -- starts_at plus the term on the game clock. A later change of the clock
    -- never moves it.
    ends_at             timestamptz NOT NULL,
    released_at         timestamptz NULL,

    CONSTRAINT jail_sentences_reason_check CHECK (reason IN ('arrest', 'conviction')),
    CONSTRAINT jail_sentences_status_check CHECK (status IN ('serving', 'released', 'bailed')),
    CONSTRAINT jail_sentences_term_check CHECK (term_seconds > 0),
    CONSTRAINT jail_sentences_period_check CHECK (ends_at > starts_at),
    CONSTRAINT jail_sentences_bail_check CHECK (bail_paid >= 0),
    CONSTRAINT jail_sentences_released_check CHECK ((status = 'serving') = (released_at IS NULL))
);

CREATE UNIQUE INDEX jail_sentences_one_serving_idx ON jail_sentences (player_id) WHERE status = 'serving';
CREATE INDEX jail_sentences_player_idx ON jail_sentences (player_id, starts_at DESC);

ALTER TABLE crimes
    ADD CONSTRAINT crimes_jail_sentence_id_fkey
    FOREIGN KEY (jail_sentence_id) REFERENCES jail_sentences (id);

-- ---------------------------------------------------------------------------
-- crime_reports — a theft its victim reported to the police, and the
-- investigation it opened. One report per theft.
-- ---------------------------------------------------------------------------
CREATE TABLE crime_reports (
    id                    uuid        PRIMARY KEY,
    crime_id              uuid        NOT NULL REFERENCES crimes (id),
    victim_player_id      uuid        NOT NULL REFERENCES players (id),
    -- The thief. Never shown to the victim unless the theft was seen or the
    -- case is solved.
    suspect_player_id     uuid        NOT NULL REFERENCES players (id),
    city_id               uuid        NOT NULL REFERENCES cities (id),
    status                text        NOT NULL,
    -- What was stolen, minor units, as the theft recorded it.
    stolen                bigint      NOT NULL,
    report_fee            bigint      NOT NULL,
    fee_transaction_id    uuid        NULL,
    -- Fixed when the report is filed.
    solve_chance_bps      int         NOT NULL,
    -- The scheduled end of the investigation (action_type
    -- 'crime_investigation').
    game_action_id        uuid        NOT NULL REFERENCES game_actions (id),
    restitution_paid      bigint      NOT NULL DEFAULT 0,
    restitution_shortfall bigint      NOT NULL DEFAULT 0,
    fine_amount           bigint      NOT NULL DEFAULT 0,
    fine_paid             bigint      NOT NULL DEFAULT 0,
    jail_sentence_id      uuid        NULL REFERENCES jail_sentences (id),
    filed_at              timestamptz NOT NULL,
    concludes_at          timestamptz NOT NULL,
    concluded_at          timestamptz NULL,

    CONSTRAINT crime_reports_crime_key UNIQUE (crime_id),
    CONSTRAINT crime_reports_status_check CHECK (status IN ('investigating', 'solved', 'unsolved')),
    CONSTRAINT crime_reports_concluded_check CHECK ((status = 'investigating') = (concluded_at IS NULL)),
    CONSTRAINT crime_reports_period_check CHECK (concludes_at > filed_at),
    CONSTRAINT crime_reports_amounts_check CHECK (
        stolen >= 0 AND report_fee >= 0 AND restitution_paid >= 0 AND restitution_shortfall >= 0
        AND restitution_paid + restitution_shortfall <= stolen
        AND fine_amount >= 0 AND fine_paid >= 0 AND fine_paid <= fine_amount),
    CONSTRAINT crime_reports_chance_check CHECK (solve_chance_bps BETWEEN 0 AND 10000)
);

CREATE INDEX crime_reports_victim_idx ON crime_reports (victim_player_id, filed_at DESC);
CREATE INDEX crime_reports_suspect_idx ON crime_reports (suspect_player_id, filed_at DESC);

-- ---------------------------------------------------------------------------
-- crime_npc_proceeds — what NPC crime has paid into the economy on each UTC
-- day. A take reserves its share of the cap here under the row lock, so two
-- thieves at the last unit of the cap cannot both be paid it.
-- ---------------------------------------------------------------------------
CREATE TABLE crime_npc_proceeds (
    day        date        PRIMARY KEY,
    paid       bigint      NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT crime_npc_proceeds_paid_check CHECK (paid >= 0)
);

COMMIT;
