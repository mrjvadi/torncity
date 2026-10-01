BEGIN;

ALTER TABLE player_notifications
    ALTER COLUMN text_fa DROP DEFAULT,
    ALTER COLUMN text_en DROP DEFAULT,
    DROP COLUMN view,
    DROP COLUMN screen;

COMMIT;
