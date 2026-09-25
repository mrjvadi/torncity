-- 0024_legislature_and_budget, reversed. Proposals and their votes, the city
-- clocks and the budget periods go; the money the budget spent stays where
-- the ledger put it (budget_spending, defence_contribution stay in the closed
-- set's history). Allocation values already written stay in policy_values,
-- whose public record is append-only; 0008's check is restored as it was.

BEGIN;

DROP TABLE city_budget_periods;
DROP TABLE city_clocks;
DROP TABLE proposal_votes;
DROP TABLE proposals;

CREATE OR REPLACE FUNCTION policy_value_must_respect_lever() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lever record;
    place text;
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


ALTER TABLE lever_definitions DROP CONSTRAINT lever_definitions_confirmation_check;
ALTER TABLE lever_definitions
    DROP COLUMN confirm_above,
    DROP COLUMN confirmation_quorum,
    DROP COLUMN confirmation_threshold,
    DROP COLUMN confirmation_rule,
    DROP COLUMN requires_confirmation_by;

COMMIT;
