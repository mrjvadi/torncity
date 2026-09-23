-- Reverses 0004_game_action_claims.up.sql.
-- The index goes first because it is defined on a column this file drops;
-- dropping the column would take the index with it, but naming it keeps the
-- reversal explicit rather than relying on that side effect.
--
-- Rolling this back does not change any row's status. A row that was running
-- stays running; it simply loses the claim time the reaper needs, which is the
-- state 0002_phase1 left the table in.

BEGIN;

DROP INDEX IF EXISTS game_actions_claimed_idx;

ALTER TABLE game_actions
    DROP COLUMN IF EXISTS claimed_by,
    DROP COLUMN IF EXISTS claimed_at;

COMMIT;
