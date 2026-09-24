-- 0016_city_groups_and_paused_courses, reversed. A course paused at the time
-- of the rollback keeps its completes_at; its old completion no longer
-- matches and a new one would have to be scheduled by hand.

BEGIN;

ALTER TABLE enrollments DROP COLUMN paused_at;
DROP TABLE city_group_links;

COMMIT;
