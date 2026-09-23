-- Reverses 0007_player_public_code.up.sql.
-- The index and the constraints go first because they are defined on columns
-- this file changes; the function goes last because the column's DEFAULT
-- depends on it.
--
-- Rolling this back loses every public code. A code a player has already
-- shared is then gone for good: re-applying the migration draws new ones.
-- Usernames that were '' and became NULL stay NULL; both meant "no username".

BEGIN;

DROP INDEX IF EXISTS players_username_lower_idx;

COMMENT ON COLUMN players.username IS NULL;

ALTER TABLE players
    DROP CONSTRAINT IF EXISTS players_public_code_check,
    DROP CONSTRAINT IF EXISTS players_public_code_key,
    DROP COLUMN IF EXISTS public_code;

DROP FUNCTION IF EXISTS players_draw_public_code();

COMMIT;
