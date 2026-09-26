-- 0041_runtime_switches, reversed.

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'torn_app') THEN
        GRANT UPDATE, DELETE, TRUNCATE ON audit_logs TO torn_app;
    END IF;
END $$;

DROP TABLE operator_switches;

COMMIT;
