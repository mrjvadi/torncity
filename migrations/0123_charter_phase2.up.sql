-- Charter phase 2 (docs/adr/0044 6.5, 6.6): elected offices, recall by petition and
-- vote, amendments by the residents' vote, and the acting head. Ballots are secret:
-- the votes table is read only to count and to stop a second vote.
BEGIN;

ALTER TABLE charter_offices ADD COLUMN deputy boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX charter_offices_deputy_idx ON charter_offices (settlement_id) WHERE deputy AND closed_at IS NULL;

ALTER TABLE charter_seats ADD COLUMN term_ends timestamptz NULL;

CREATE TABLE charter_ballots (
    id               uuid        PRIMARY KEY,
    settlement_id    uuid        NOT NULL REFERENCES cities (id),
    kind             text        NOT NULL CHECK (kind IN ('election', 'recall', 'amendment')),
    -- the office an election fills, a recall is about, an amendment changes
    office_id        uuid        NULL REFERENCES charter_offices (id),
    -- recall: the holder put to the vote
    target_player_id uuid        NULL REFERENCES players (id),
    -- amendment: the change, applied when the vote carries
    proposal         jsonb       NULL,
    opened_by        uuid        NULL,
    opens_at         timestamptz NOT NULL,
    closes_at        timestamptz NOT NULL,
    status           text        NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'passed', 'failed', 'no_result', 'cancelled', 'void')),
    -- how many residents could vote when it opened
    eligible         integer     NOT NULL,
    result           jsonb       NULL,
    game_action_id   uuid        NULL,
    settled_at       timestamptz NULL
);
CREATE UNIQUE INDEX charter_ballots_one_election_idx ON charter_ballots (office_id) WHERE kind = 'election' AND status = 'open';
CREATE UNIQUE INDEX charter_ballots_one_recall_idx ON charter_ballots (office_id, target_player_id) WHERE kind = 'recall' AND status = 'open';
CREATE UNIQUE INDEX charter_ballots_one_amendment_idx ON charter_ballots (settlement_id) WHERE kind = 'amendment' AND status = 'open';
CREATE INDEX charter_ballots_settlement_idx ON charter_ballots (settlement_id, opens_at DESC);

CREATE TABLE charter_ballot_candidates (
    ballot_id uuid        NOT NULL REFERENCES charter_ballots (id),
    player_id uuid        NOT NULL REFERENCES players (id),
    stood_at  timestamptz NOT NULL,
    PRIMARY KEY (ballot_id, player_id)
);

CREATE TABLE charter_ballot_votes (
    ballot_id uuid        NOT NULL REFERENCES charter_ballots (id),
    voter_id  uuid        NOT NULL REFERENCES players (id),
    -- an election: the candidate's player id; a recall or amendment: yes or no
    choice    text        NOT NULL,
    at        timestamptz NOT NULL,
    PRIMARY KEY (ballot_id, voter_id)
);

CREATE TABLE charter_petitions (
    id               uuid        PRIMARY KEY,
    settlement_id    uuid        NOT NULL REFERENCES cities (id),
    office_id        uuid        NOT NULL REFERENCES charter_offices (id),
    target_player_id uuid        NOT NULL REFERENCES players (id),
    started_by       uuid        NOT NULL REFERENCES players (id),
    created_at       timestamptz NOT NULL,
    status           text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'voted', 'cancelled')),
    ballot_id        uuid        NULL REFERENCES charter_ballots (id)
);
CREATE UNIQUE INDEX charter_petitions_one_open_idx ON charter_petitions (office_id, target_player_id) WHERE status = 'open';

CREATE TABLE charter_petition_signatures (
    petition_id uuid        NOT NULL REFERENCES charter_petitions (id),
    signer_id   uuid        NOT NULL REFERENCES players (id),
    at          timestamptz NOT NULL,
    PRIMARY KEY (petition_id, signer_id)
);

COMMIT;
