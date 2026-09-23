-- Reverses 0011_jobs_and_education.up.sql, in reverse dependency order.
--
-- This destroys every job, shift record, enrolment and certificate. It exists
-- for a development database; anywhere with real players, roll forward with a
-- new migration instead. The ledger transactions that paid wages and fees
-- stay: they belong to 0006, and money never disappears with a feature.
--
-- DROP TABLE takes the append-only triggers with it.

BEGIN;

DROP TABLE IF EXISTS certifications;
DROP TABLE IF EXISTS enrollments;
DROP TABLE IF EXISTS work_shifts;
DROP TABLE IF EXISTS employments;
DROP TABLE IF EXISTS course_definitions;
DROP TABLE IF EXISTS career_definitions;

COMMIT;
