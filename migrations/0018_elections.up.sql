-- 0018_elections — elections for the offices acquired by election.
--
-- Elections for offices acquired by election (configs/content/governance.yml,
-- internal/domain/election): a candidacy period, then a vote, then one count
-- that fills the seats. Three scheduled game_actions drive one election: the
-- vote opening (election_voting, at candidacy_ends_at), the count
-- (election_count, at voting_ends_at) and, after the count, the next
-- election opening (election_open). Each settles once: the vote opening
-- moves voting_opened_at from NULL, the count moves status from 'open', and
-- the next opening happens only while the election it follows is the latest.
--
-- THE SECRET BALLOT. Who voted and whom they voted for are two tables that
-- share nothing but the election: election_voters records that a resident
-- voted (one row each, the unique key is what stops a second vote);
-- election_ballots records a choice with no voter, no timestamp and a random
-- id, so no join and no ordering ties a ballot to a person. Both rows are
-- written in one transaction.

CREATE TABLE elections (
    id                  uuid        PRIMARY KEY,
    -- A short public number for buttons and announcements.
    no                  bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    office_code         text        NOT NULL,
    jurisdiction_id     uuid        NOT NULL REFERENCES jurisdictions (id),
    seats               int         NOT NULL,
    status              text        NOT NULL,
    opens_at            timestamptz NOT NULL,
    candidacy_ends_at   timestamptz NOT NULL,
    voting_ends_at      timestamptz NOT NULL,
    -- The scheduled count (game_actions), the one path to 'counted'.
    count_action_id     uuid        NULL,
    -- Set once, when the vote opened and was announced.
    voting_opened_at    timestamptz NULL,
    content_version     int         NOT NULL,
    counted_at          timestamptz NULL,
    votes_cast          bigint      NULL,

    CONSTRAINT elections_status_check CHECK (status IN ('open', 'counted')),
    CONSTRAINT elections_seats_check CHECK (seats >= 1),
    CONSTRAINT elections_calendar_check CHECK (opens_at < candidacy_ends_at AND candidacy_ends_at < voting_ends_at),
    CONSTRAINT elections_counted_check CHECK ((status = 'counted') = (counted_at IS NOT NULL))
);

-- At most one election under way per office and jurisdiction.
CREATE UNIQUE INDEX elections_one_open_idx ON elections (office_code, jurisdiction_id) WHERE status = 'open';
CREATE INDEX elections_jurisdiction_idx ON elections (jurisdiction_id, opens_at DESC);

CREATE TABLE election_candidates (
    election_id           uuid        NOT NULL REFERENCES elections (id),
    player_id             uuid        NOT NULL REFERENCES players (id),
    stood_at              timestamptz NOT NULL,
    deposit               bigint      NOT NULL,
    deposit_method        text        NULL,
    deposit_transaction_id uuid       NULL,
    -- Set by the count.
    votes                 bigint      NULL,
    elected               boolean     NULL,
    seat                  int         NULL,
    deposit_returned      boolean     NULL,

    CONSTRAINT election_candidates_pkey PRIMARY KEY (election_id, player_id),
    CONSTRAINT election_candidates_deposit_check CHECK (deposit >= 0)
);

CREATE TABLE election_voters (
    election_id uuid NOT NULL REFERENCES elections (id),
    player_id   uuid NOT NULL REFERENCES players (id),

    CONSTRAINT election_voters_pkey PRIMARY KEY (election_id, player_id)
);

CREATE TABLE election_ballots (
    id           uuid NOT NULL PRIMARY KEY,
    election_id  uuid NOT NULL REFERENCES elections (id),
    candidate_id uuid NOT NULL REFERENCES players (id)
);

CREATE INDEX election_ballots_election_idx ON election_ballots (election_id, candidate_id);

-- A ballot is never changed or taken back.
CREATE FUNCTION election_ballots_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'election_ballots is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER election_ballots_append_only
    BEFORE UPDATE OR DELETE ON election_ballots
    FOR EACH ROW EXECUTE FUNCTION election_ballots_append_only();
