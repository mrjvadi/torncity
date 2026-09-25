-- 0024_legislature_and_budget — stage F, politics: votes of a body
-- (a council confirming a mayor's budget, a national assembly approving a
-- declaration of war, a council deciding a lever it holds), allocation
-- levers with behaviour, and a city's budget spent every city period by its
-- allocation. Rules: internal/domain/legislature and internal/domain/budget.
-- Content: configs/content/governance.yml (requires_confirmation_by and its
-- rule on levers and actions; city.budget) and configs/content/budget.yml.
-- Decision: docs/adr/0024-property-and-politics.md. Money:
-- docs/adr/0009-economic-control.md (budget_spending, defence_contribution).
--
-- EXACTLY ONCE. A proposal is decided once: its row moves from 'open' once,
-- under its lock, whether by the vote that settles it or by its scheduled
-- close. One seat votes once on one proposal (primary key). One lever or
-- action has at most one open proposal in one place (partial unique index).
-- A city period is settled once: the clock row is locked and must name the
-- action and period, and the period's record has the city and the period as
-- its primary key.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- money is bigint minor units; no DEFAULT now(); CHECK constraints are named.

BEGIN;

-- ---------------------------------------------------------------------------
-- lever_definitions — who confirms a change, and how. NULL: nobody; the
-- holder decides alone.
-- ---------------------------------------------------------------------------
ALTER TABLE lever_definitions
    ADD COLUMN requires_confirmation_by text   NULL,
    ADD COLUMN confirmation_rule        text   NULL,
    ADD COLUMN confirmation_threshold   text   NULL,
    ADD COLUMN confirmation_quorum      text   NULL,
    ADD COLUMN confirm_above            bigint NULL;

ALTER TABLE lever_definitions
    ADD CONSTRAINT lever_definitions_confirmation_check CHECK (
        (requires_confirmation_by IS NULL) = (confirmation_rule IS NULL)
        AND (requires_confirmation_by IS NULL OR decision_rule = 'single')
        AND (confirmation_rule IS NULL OR confirmation_rule IN ('majority', 'supermajority', 'unanimous'))
        AND ((confirmation_rule IS NOT DISTINCT FROM 'supermajority') = (confirmation_threshold IS NOT NULL))
        AND (confirmation_quorum IS NULL OR requires_confirmation_by IS NOT NULL)
        AND (confirm_above IS NULL OR (requires_confirmation_by IS NOT NULL AND value_kind = 'scalar'
                                       AND confirm_above >= 0)));

-- ---------------------------------------------------------------------------
-- policy_values — an allocation's shares are checked like a scalar's bounds:
-- only the lever's categories, whole non-negative bps, at most the whole.
-- The rest of the function is 0008's, unchanged.
-- ---------------------------------------------------------------------------
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

-- ---------------------------------------------------------------------------
-- proposals — a change of a lever, or an action, put to a body's vote. The
-- value is the scalar proposed, or the document: an allocation's shares, an
-- action's arguments. The rule, threshold, quorum and seats are copied from
-- the content at the opening, so a vote is decided by the rules it opened
-- under.
-- ---------------------------------------------------------------------------
CREATE TABLE proposals (
    id                 uuid        PRIMARY KEY,
    no                 bigint      GENERATED ALWAYS AS IDENTITY UNIQUE,
    kind               text        NOT NULL,
    jurisdiction_id    uuid        NOT NULL REFERENCES jurisdictions (id),
    subject            text        NOT NULL,
    value              bigint      NULL,
    value_json         jsonb       NULL,
    proposed_by        uuid        NOT NULL REFERENCES players (id),
    proposer_office_id uuid        NOT NULL REFERENCES offices (id),
    proposer_office    text        NOT NULL,
    body               text        NOT NULL,
    rule               text        NOT NULL,
    threshold          text        NULL,
    quorum             text        NULL,
    seats              int         NOT NULL,
    status             text        NOT NULL,
    opened_at          timestamptz NOT NULL,
    closes_at          timestamptz NOT NULL,
    close_action_id    uuid        NOT NULL REFERENCES game_actions (id),
    decided_at         timestamptz NULL,
    yes_votes          int         NULL,
    no_votes           int         NULL,
    -- Why a passed proposal could not be applied, for a lapsed one.
    lapse_reason       text        NULL,
    -- The change a passed lever proposal made.
    policy_value_id    uuid        NULL REFERENCES policy_values (id),
    content_version    int         NOT NULL,

    CONSTRAINT proposals_kind_check CHECK (kind IN ('lever', 'action')),
    CONSTRAINT proposals_value_check CHECK (value IS NOT NULL OR value_json IS NOT NULL),
    CONSTRAINT proposals_rule_check CHECK (rule IN ('majority', 'supermajority', 'unanimous')),
    CONSTRAINT proposals_threshold_check CHECK ((rule = 'supermajority') = (threshold IS NOT NULL)),
    CONSTRAINT proposals_seats_check CHECK (seats >= 1),
    CONSTRAINT proposals_status_check CHECK (status IN ('open', 'passed', 'failed', 'lapsed')),
    CONSTRAINT proposals_window_check CHECK (closes_at > opened_at),
    CONSTRAINT proposals_decided_check CHECK (
        (status = 'open') = (decided_at IS NULL)
        AND (status = 'open') = (yes_votes IS NULL)
        AND (status = 'open') = (no_votes IS NULL)
        AND (status = 'lapsed') = (lapse_reason IS NOT NULL)),
    CONSTRAINT proposals_votes_check CHECK (yes_votes IS NULL OR (yes_votes >= 0 AND no_votes >= 0))
);
CREATE UNIQUE INDEX proposals_one_open_idx ON proposals (jurisdiction_id, kind, subject) WHERE status = 'open';
CREATE INDEX proposals_jurisdiction_idx ON proposals (jurisdiction_id, opened_at DESC);

-- ---------------------------------------------------------------------------
-- proposal_votes — each seat's vote. Public: a legislator's vote is a matter
-- of record, unlike a ballot. Append-only; a vote is never changed.
-- ---------------------------------------------------------------------------
CREATE TABLE proposal_votes (
    proposal_id uuid        NOT NULL REFERENCES proposals (id),
    office_id   uuid        NOT NULL REFERENCES offices (id),
    player_id   uuid        NOT NULL REFERENCES players (id),
    vote        text        NOT NULL,
    cast_at     timestamptz NOT NULL,

    CONSTRAINT proposal_votes_pkey PRIMARY KEY (proposal_id, office_id),
    CONSTRAINT proposal_votes_player_key UNIQUE (proposal_id, player_id),
    CONSTRAINT proposal_votes_vote_check CHECK (vote IN ('yes', 'no'))
);

CREATE TRIGGER proposal_votes_append_only
    BEFORE UPDATE OR DELETE ON proposal_votes
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER proposal_votes_no_truncate
    BEFORE TRUNCATE ON proposal_votes
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

-- ---------------------------------------------------------------------------
-- city_clocks — each city's period (config city.period, GAME time): the
-- budget is spent, property pays its upkeep and tax, rent falls due.
-- ---------------------------------------------------------------------------
CREATE TABLE city_clocks (
    city_id           uuid        PRIMARY KEY REFERENCES cities (id),
    period_no         bigint      NOT NULL,
    period_started_at timestamptz NOT NULL,
    next_at           timestamptz NULL,
    action_id         uuid        NULL REFERENCES game_actions (id),
    updated_at        timestamptz NOT NULL,

    CONSTRAINT city_clocks_period_check CHECK (period_no >= 1),
    CONSTRAINT city_clocks_clock_check CHECK ((action_id IS NULL) = (next_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- city_budget_periods — one city period's budget: what the treasury held,
-- what the budget could spend of it, and each line's share, spending and the
-- effect it bought for the next period. Append-only; the primary key is what
-- makes a period's budget paid once.
-- ---------------------------------------------------------------------------
CREATE TABLE city_budget_periods (
    city_id    uuid        NOT NULL REFERENCES cities (id),
    period_no  bigint      NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at   timestamptz NOT NULL,
    treasury   bigint      NOT NULL,
    spendable  bigint      NOT NULL,
    spent      bigint      NOT NULL,
    -- What went to the country's defence fund, part of spent.
    defence    bigint      NOT NULL,
    -- [{"line": code, "share_bps": n, "spent": n, "effect": code, "effect_bps": n}]
    lines      jsonb       NOT NULL,

    CONSTRAINT city_budget_periods_pkey PRIMARY KEY (city_id, period_no),
    CONSTRAINT city_budget_periods_amounts_check CHECK (treasury >= 0 AND spendable >= 0 AND spendable <= treasury
        AND spent >= 0 AND spent <= spendable AND defence >= 0 AND defence <= spent),
    CONSTRAINT city_budget_periods_span_check CHECK (ended_at >= started_at)
);

CREATE TRIGGER city_budget_periods_append_only
    BEFORE UPDATE OR DELETE ON city_budget_periods
    FOR EACH ROW EXECUTE FUNCTION refuse_append_only_change();
CREATE TRIGGER city_budget_periods_no_truncate
    BEFORE TRUNCATE ON city_budget_periods
    FOR EACH STATEMENT EXECUTE FUNCTION refuse_append_only_change();

COMMIT;
