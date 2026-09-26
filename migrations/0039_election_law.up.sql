-- 0039_election_law — election law as a real, player-legislated policy
-- lever, and what it takes to stand: endorsements, candidate vetting by an
-- election commission, and the frozen law an open election ran under.
--
-- Content: configs/content/governance.yml (one election_law lever per
-- elected office, "<jurisdiction>.election_law.<office>", held by the body
-- that legislates for that jurisdiction). Rules:
-- internal/domain/election.Fields (the document's shape),
-- application.CheckElectionLaw / CheckElectionLawExclusion (the safety
-- bounds). Decision: docs/adr/0015-player-held-offices.md section 2 — a
-- change takes effect only from the next election's opening, never inside
-- one already under way, which is why elections.law_json below freezes the
-- law an election ran under instead of the application re-reading the lever
-- live every time it is asked.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are
-- named.

BEGIN;

-- ---------------------------------------------------------------------------
-- lever_definitions — election_law joins allocation as a structured lever
-- with real behaviour: its document is the same generic value_json an
-- allocation stores (policy_value_must_respect_lever below tells the two
-- apart by value_type), and it carries two extra columns of its own: the
-- operator's per-field bounds, and the certificates (education.yml course
-- codes) an office's law may require.
-- ---------------------------------------------------------------------------
ALTER TABLE lever_definitions
    ADD COLUMN bounds_json       jsonb NULL,
    ADD COLUMN education_options text[] NOT NULL DEFAULT '{}';

ALTER TABLE lever_definitions DROP CONSTRAINT lever_definitions_type_check;
ALTER TABLE lever_definitions
    ADD CONSTRAINT lever_definitions_type_check CHECK (
        (value_kind = 'scalar'     AND value_type IN ('bps', 'money', 'int')) OR
        (value_kind = 'structured' AND value_type IN ('bool', 'enum', 'map', 'allocation', 'election_law')));

ALTER TABLE lever_definitions
    ADD CONSTRAINT lever_definitions_election_law_check CHECK (
        (value_type = 'election_law') = (bounds_json IS NOT NULL)
        AND (value_type = 'election_law' OR cardinality(education_options) = 0));

-- ---------------------------------------------------------------------------
-- policy_values — an election law's fields are checked like an allocation's
-- shares: known keys only, whole numbers, each inside its own bound. Unlike
-- an allocation there is no "at most the whole"; instead every one of the 14
-- fields election.Fields names must be present (checked by counting: with
-- every key one of the 14 known ones, 14 distinct keys can only be all of
-- them), and each must lie within lever.bounds_json's bound for it.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION policy_value_must_respect_lever() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lever record;
    place text;
    share record;
    total bigint := 0;
    key_count int;
BEGIN
    SELECT ld.* INTO lever
      FROM lever_definitions ld
      JOIN content_versions cv ON cv.id = ld.content_version_id AND cv.status = 'active'
     WHERE ld.code = NEW.lever_code;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'policy value for unknown lever %', NEW.lever_code
            USING ERRCODE = 'foreign_key_violation', CONSTRAINT = 'policy_values_lever_exists';
    END IF;

    SELECT kind INTO place FROM jurisdictions WHERE id = NEW.jurisdiction_id;
    IF place IS DISTINCT FROM lever.jurisdiction_kind THEN
        RAISE EXCEPTION 'lever % is a % lever, not a % one', NEW.lever_code, lever.jurisdiction_kind, place
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_jurisdiction_kind';
    END IF;

    IF NEW.value_kind IS DISTINCT FROM lever.value_kind THEN
        RAISE EXCEPTION 'lever % holds % values, not %', NEW.lever_code, lever.value_kind, NEW.value_kind
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_lever_kind';
    END IF;

    IF lever.value_kind = 'scalar' AND (NEW.value < lever.min_value OR NEW.value > lever.max_value) THEN
        RAISE EXCEPTION 'policy value % for % is outside [%, %]', NEW.value, NEW.lever_code,
                lever.min_value, lever.max_value
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_bounds';
    END IF;

    IF lever.value_type = 'allocation' THEN
        IF jsonb_typeof(NEW.value_json) IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'allocation for % is not a mapping', NEW.lever_code
                USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_allocation';
        END IF;
        FOR share IN SELECT key, value FROM jsonb_each(NEW.value_json) LOOP
            IF NOT (share.key = ANY (lever.categories))
               OR jsonb_typeof(share.value) IS DISTINCT FROM 'number'
               OR share.value::text !~ '^[0-9]+$' THEN
                RAISE EXCEPTION 'allocation for % gives % the share %', NEW.lever_code, share.key, share.value
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_allocation';
            END IF;
            total := total + share.value::text::bigint;
        END LOOP;
        IF total > 10000 THEN
            RAISE EXCEPTION 'allocation for % divides % bps, more than the whole', NEW.lever_code, total
                USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_allocation';
        END IF;
    ELSIF lever.value_type = 'election_law' THEN
        IF jsonb_typeof(NEW.value_json) IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'election law for % is not a mapping', NEW.lever_code
                USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_election_law';
        END IF;
        SELECT count(*) INTO key_count FROM jsonb_object_keys(NEW.value_json);
        IF key_count IS DISTINCT FROM 14 THEN
            RAISE EXCEPTION 'election law for % has % fields, want 14', NEW.lever_code, key_count
                USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_election_law';
        END IF;
        FOR share IN SELECT key, value FROM jsonb_each(NEW.value_json) LOOP
            IF share.key NOT IN ('candidacy_hours', 'voting_hours', 'min_level', 'min_residency_hours',
                                  'clean_record', 'voter_min_residency_hours', 'deposit', 'refund_share_bps',
                                  'reopen_after_hours', 'endorsements_required', 'term_limit_consecutive',
                                  'term_limit_total', 'education_rank', 'min_age')
               OR jsonb_typeof(share.value) IS DISTINCT FROM 'number'
               OR share.value::text !~ '^-?[0-9]+$' THEN
                RAISE EXCEPTION 'election law for % has field % = %', NEW.lever_code, share.key, share.value
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_election_law';
            END IF;
            IF lever.bounds_json ? share.key THEN
                IF share.value::text::bigint < (lever.bounds_json -> share.key ->> 'min')::bigint
                   OR share.value::text::bigint > (lever.bounds_json -> share.key ->> 'max')::bigint THEN
                    RAISE EXCEPTION 'election law for % field % = % is outside its bound', NEW.lever_code, share.key, share.value
                        USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_within_election_law';
                END IF;
            END IF;
        END LOOP;
    END IF;

    IF NEW.effective_at < NEW.set_at + make_interval(secs => lever.notice_seconds) THEN
        RAISE EXCEPTION 'policy value for % takes effect before its notice of % seconds',
                NEW.lever_code, lever.notice_seconds
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_notice';
    END IF;

    IF TG_OP = 'INSERT' AND EXISTS (
        SELECT 1 FROM policy_values pv
         WHERE pv.jurisdiction_id = NEW.jurisdiction_id
           AND pv.lever_code = NEW.lever_code
           AND pv.set_at > NEW.set_at - make_interval(secs => lever.change_cooldown_seconds)
    ) THEN
        RAISE EXCEPTION 'policy value for % is within the cooldown of % seconds since the last change',
                NEW.lever_code, lever.change_cooldown_seconds
            USING ERRCODE = 'check_violation', CONSTRAINT = 'policy_values_cooldown';
    END IF;

    RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- elections — the resolved election law an election ran under, frozen at its
-- opening (OpenElection), so a change to the law reaches only the NEXT
-- election, never one already candidating or voting. NULL for an election
-- opened before this migration.
-- ---------------------------------------------------------------------------
ALTER TABLE elections
    ADD COLUMN law_json jsonb NULL;

-- ---------------------------------------------------------------------------
-- election_candidates — vetting by an election commission (a player-held
-- office; auto-approved while nobody holds it, or once the candidacy window
-- ends with no decision — ADR 0015 section 2). Only an 'approved' candidate
-- appears on the ballot or is counted.
-- ---------------------------------------------------------------------------
ALTER TABLE election_candidates
    ADD COLUMN review_status text NOT NULL DEFAULT 'approved',
    ADD COLUMN review_reason text NULL;

ALTER TABLE election_candidates
    ADD CONSTRAINT election_candidates_review_status_check
        CHECK (review_status IN ('pending', 'approved', 'disqualified')),
    ADD CONSTRAINT election_candidates_review_reason_check
        CHECK ((review_status = 'disqualified') = (review_reason IS NOT NULL));

-- ---------------------------------------------------------------------------
-- election_candidate_reviews — the commission's decisions, public and
-- append-only: a disqualification without a published reason is not a
-- decision anyone can appeal (there is no court yet, ADR 0015 section 2, so
-- there is nothing further to record here).
-- ---------------------------------------------------------------------------
CREATE TABLE election_candidate_reviews (
    id                   uuid        PRIMARY KEY,
    election_id          uuid        NOT NULL REFERENCES elections (id),
    candidate_player_id  uuid        NOT NULL REFERENCES players (id),
    -- NULL: nobody held the commission, so the candidacy was auto-approved.
    reviewer_player_id   uuid        NULL REFERENCES players (id),
    decision             text        NOT NULL,
    reason_code          text        NULL,
    decided_at           timestamptz NOT NULL,

    CONSTRAINT election_candidate_reviews_decision_check CHECK (decision IN ('approved', 'disqualified')),
    CONSTRAINT election_candidate_reviews_reason_check CHECK ((decision = 'disqualified') = (reason_code IS NOT NULL))
);

CREATE INDEX election_candidate_reviews_election_idx
    ON election_candidate_reviews (election_id, candidate_player_id, decided_at);

CREATE FUNCTION election_candidate_reviews_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'election_candidate_reviews is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER election_candidate_reviews_append_only
    BEFORE UPDATE OR DELETE ON election_candidate_reviews
    FOR EACH ROW EXECUTE FUNCTION election_candidate_reviews_append_only();
CREATE TRIGGER election_candidate_reviews_no_truncate
    BEFORE TRUNCATE ON election_candidate_reviews
    FOR EACH STATEMENT EXECUTE FUNCTION election_candidate_reviews_append_only();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'torn_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON election_candidate_reviews FROM torn_app;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- election_endorsements — a resident endorsing a candidacy during the
-- candidacy window, one endorsement per resident per office per election
-- (the unique key, on election and endorser, not candidate: a resident
-- picks at most one candidacy of the same election to endorse). Public and
-- append-only, like a vote's record in the legislature, but unlike a
-- ballot: an endorsement names who gave it, because a candidate publishes
-- the endorsers behind a deep link, not a secret count.
-- ---------------------------------------------------------------------------
CREATE TABLE election_endorsements (
    id                   uuid        PRIMARY KEY,
    election_id          uuid        NOT NULL REFERENCES elections (id),
    candidate_player_id  uuid        NOT NULL REFERENCES players (id),
    endorser_player_id   uuid        NOT NULL REFERENCES players (id),
    endorsed_at          timestamptz NOT NULL,

    CONSTRAINT election_endorsements_one_per_voter_key UNIQUE (election_id, endorser_player_id)
);

CREATE INDEX election_endorsements_candidate_idx ON election_endorsements (election_id, candidate_player_id);

CREATE FUNCTION election_endorsements_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'election_endorsements is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER election_endorsements_append_only
    BEFORE UPDATE OR DELETE ON election_endorsements
    FOR EACH ROW EXECUTE FUNCTION election_endorsements_append_only();
CREATE TRIGGER election_endorsements_no_truncate
    BEFORE TRUNCATE ON election_endorsements
    FOR EACH STATEMENT EXECUTE FUNCTION election_endorsements_append_only();

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'torn_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON election_endorsements FROM torn_app;
    END IF;
END $$;

COMMIT;
