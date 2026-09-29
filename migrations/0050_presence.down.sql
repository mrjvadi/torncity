BEGIN;

ALTER TABLE players
    DROP CONSTRAINT players_presence_visibility_check,
    DROP COLUMN presence_visibility;

COMMIT;
