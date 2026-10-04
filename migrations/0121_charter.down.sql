BEGIN;
DROP TRIGGER IF EXISTS charter_audit_no_change ON charter_audit;
DROP FUNCTION IF EXISTS charter_audit_append_only();
DROP TABLE IF EXISTS charter_audit;
DROP TABLE IF EXISTS charter_seats;
DROP TABLE IF EXISTS charter_offices;
COMMIT;
