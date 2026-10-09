BEGIN;

ALTER TABLE service_day_posts DROP COLUMN grace;
DROP TABLE experience_days;

COMMIT;
