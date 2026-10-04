-- A settlement's own time zone (owner decision 2026-10-05: game time is real time on
-- UTC, and every settlement keeps its own local time for its daily rhythms: the
-- shop's morning delivery, the stores' day, the market day). NULL means the zone is
-- derived from the settlement's longitude on the generated world; the charter can
-- set it (permission settings.timezone).
BEGIN;
ALTER TABLE cities ADD COLUMN tz_offset_minutes smallint NULL
    CONSTRAINT cities_tz_offset_check CHECK (tz_offset_minutes BETWEEN -720 AND 840 AND tz_offset_minutes % 15 = 0);
ALTER TABLE cities ADD COLUMN tz_set_at timestamptz NULL;
COMMIT;
