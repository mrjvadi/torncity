-- 0004_game_action_claims — when, and by whom, a scheduled action was claimed.
-- Scope: two columns and one index on game_actions, so that a claim which was
-- never finished can be found and handed back to the schedule.
-- Schema authority: docs/database.md, game_actions, which lists both columns
-- and the index below.
--
-- Why this exists: GameActionRepository.Due claims a row by moving it from
-- 'scheduled' to 'running', and only 'scheduled' rows are ever claimed. A
-- scheduler that dies between the claim and Complete — or whose publish is
-- refused by the broker — therefore leaves the row in 'running' for ever, and
-- for a travel that is a player who never lands. Returning such a row requires
-- answering "how long has this been running?", and until now the table could
-- not: started_at is when the ACTION began (a journey's departure), not when a
-- scheduler picked it up, and nothing recorded the latter.
--
-- Conventions inherited from 0001_init:
--   * all instants are timestamptz, stored in UTC;
--   * no DEFAULT now() on a column — the application supplies every timestamp.
--     The one-off backfill below is a migration-time statement about rows that
--     already exist, not a default, and is explained where it happens.

BEGIN;

-- ---------------------------------------------------------------------------
-- The claim record.
--
-- Both columns are NULL-able because a row that has never been claimed has no
-- claim to describe, and a row returned to the schedule by the reaper has its
-- claim cleared so it looks exactly like one that was never picked up.
-- ---------------------------------------------------------------------------
ALTER TABLE game_actions
    ADD COLUMN claimed_at timestamptz NULL,
    ADD COLUMN claimed_by text        NULL;

COMMENT ON COLUMN game_actions.claimed_at IS
    'Instant a scheduler moved the row to running. The reaper returns a running row whose claim is older than the lease to scheduled. NULL when the row is not claimed.';
COMMENT ON COLUMN game_actions.claimed_by IS
    'Instance id of the scheduler that holds the claim, for an operator reading a stuck row. NULL when the claimant did not identify itself, or when the row is not claimed.';

-- Rows that are already running when this migration applies were claimed by a
-- build that recorded no claim time, and a NULL claimed_at never satisfies the
-- reaper's "claimed_at < cutoff". Left alone they would stay stranded, which is
-- the exact failure this migration closes. Stamping them with the migration's
-- own instant treats them as claimed just now: a scheduler that really is still
-- working on one keeps a full lease, and one that died is reaped a single lease
-- later. It touches only running rows, which is the in-flight batch and never
-- the history.
UPDATE game_actions
SET claimed_at = now()
WHERE status = 'running' AND claimed_at IS NULL;

-- The reaper's index. Its partial predicate is the same idea as
-- game_actions_due_idx: only rows currently claimed are indexed, so the index
-- stays roughly the size of one batch in flight no matter how many millions of
-- completed actions pile up behind it. The reaper's query (status = 'running'
-- AND claimed_at < cutoff ORDER BY claimed_at LIMIT n) is answered by walking
-- the front of this index and stopping — the project's rule that no tick may
-- scan the whole table holds for the recovery path as well as the happy one.
CREATE INDEX game_actions_claimed_idx
    ON game_actions (claimed_at)
    WHERE status = 'running';

COMMIT;
