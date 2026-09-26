-- 0039_election_law down.

BEGIN;

DROP TABLE IF EXISTS election_endorsements;
DROP FUNCTION IF EXISTS election_endorsements_append_only();

DROP TABLE IF EXISTS election_candidate_reviews;
DROP FUNCTION IF EXISTS election_candidate_reviews_append_only();

ALTER TABLE election_candidates
    DROP CONSTRAINT IF EXISTS election_candidates_review_reason_check,
    DROP CONSTRAINT IF EXISTS election_candidates_review_status_check,
    DROP COLUMN IF EXISTS review_reason,
    DROP COLUMN IF EXISTS review_status;

ALTER TABLE elections DROP COLUMN IF EXISTS law_json;

-- Restore 0024's policy_value_must_respect_lever, without the election_law
-- branch.
CREATE OR REPLACE FUNCTION policy_value_must_respect_lever() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lever record;
    place text;
    share record;
    total bigint := 0;
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

ALTER TABLE lever_definitions
    DROP CONSTRAINT IF EXISTS lever_definitions_election_law_check;
ALTER TABLE lever_definitions DROP CONSTRAINT IF EXISTS lever_definitions_type_check;
ALTER TABLE lever_definitions
    ADD CONSTRAINT lever_definitions_type_check CHECK (
        (value_kind = 'scalar'     AND value_type IN ('bps', 'money', 'int')) OR
        (value_kind = 'structured' AND value_type IN ('bool', 'enum', 'map', 'allocation')));

ALTER TABLE lever_definitions
    DROP COLUMN IF EXISTS education_options,
    DROP COLUMN IF EXISTS bounds_json;

COMMIT;
