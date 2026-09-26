-- 0041_runtime_switches — a small, generic table of operator switches: a
-- runtime setting an operator can flip without a redeploy, with who changed
-- it, when, and why. The first switch it carries is telegram_play (on |
-- groups_off | off), which sends players to the web game instead of playing
-- through Telegram, and its companion telegram_notices (on | off), which
-- says whether Telegram notices still go out while play is off.
--
-- Numbered 0041 by assignment; it depends on none of the migrations between
-- it and 0035, the last one before it applied so far.
--
-- The table is generic on purpose: a future switch is one more row, not one
-- more table. operator_switches holds each switch's CURRENT value, with the
-- who/when/why of that value, so a reader (the gateway's cache, `admin
-- switch list`, the panel) never has to reconstruct "what does it say now"
-- from a log. The full history is the existing generic audit trail,
-- audit_logs (0003_content), which every operator action already writes to
-- (target_type 'operator_switches', target_id NULL — a switch has no uuid).
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE operator_switches (
    key         text        PRIMARY KEY,
    value       text        NOT NULL,
    changed_by  text        NOT NULL,
    changed_at  timestamptz NOT NULL,
    reason      text        NOT NULL,

    CONSTRAINT operator_switches_key_check CHECK (length(btrim(key)) > 0),
    CONSTRAINT operator_switches_value_check CHECK (length(btrim(value)) > 0),
    CONSTRAINT operator_switches_reason_check CHECK (length(btrim(reason)) > 0)
);

COMMENT ON TABLE operator_switches IS
    'One row per runtime switch: its current value and who set it, when and why. Reused by every future switch — nothing here names telegram_play specifically. History is in audit_logs (action ''switch.set'', target_type ''operator_switches'').';

-- audit_logs (0003_content) is now the history of every switch flip, on top
-- of everything it already recorded. It has stood without a database-level
-- write guard since it was created; a runtime switch is exactly the kind of
-- row an operator will want to trust completely under an incident, so the
-- guard is added here rather than deferred again. torn_app is the
-- application's runtime role in production; a database that has never heard
-- of it (a developer's own, this migration's own test database) skips the
-- grant instead of failing.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'torn_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM torn_app;
    END IF;
END $$;

COMMIT;
