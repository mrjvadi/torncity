-- 0050_presence — the player's "last seen" privacy setting
-- (docs/adr/0030-realtime-interest-and-presence.md sections 3.1, 3.2, R1).
--
-- Presence itself (is this player connected right now) is a Redis key with a
-- heartbeat TTL, never a Postgres row: it expires by itself, nothing is
-- written when a player goes away, and no replica owns it. What Postgres
-- keeps is the one thing that is a player's own decision and must survive a
-- Redis flush: who may see it.
--
--   everyone  online/offline is visible to fellow citizens and anyone the
--             detail rules (section 3.3) allow. The default.
--   contacts  only accepted friends, faction mates and the player's own
--             settlement.
--   nobody    nobody outside the player's own settlement sees it, and the
--             player sees nobody else's (Telegram's own asymmetric rule).
--
-- The setting gates presence and activity only, never the channel-level
-- facts of section 1 and 2 (a settlement's roster, arrivals and departures).
BEGIN;

ALTER TABLE players
    ADD COLUMN presence_visibility text NOT NULL DEFAULT 'everyone',
    ADD CONSTRAINT players_presence_visibility_check
        CHECK (presence_visibility IN ('everyone', 'contacts', 'nobody'));

COMMENT ON COLUMN players.presence_visibility IS
    'Who may see this player as online and what they are doing: everyone, contacts (friends and faction mates) or nobody. Presence itself lives in Redis (ADR 0030 section 3).';

COMMIT;
