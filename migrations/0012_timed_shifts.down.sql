-- 0012_timed_shifts, reversed. Scheduled 'work_shift' rows in game_actions
-- are left where they are: game_actions is an open vocabulary, and a row
-- nobody routes is reported by the scheduler rather than lost.

BEGIN;

DROP TABLE shift_sessions;

COMMIT;
