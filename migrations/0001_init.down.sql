-- Reverses 0001_init.up.sql.
-- Tables are dropped in reverse dependency order, so no DROP relies on CASCADE:
--   idempotency_keys -> players
--   player_bot_links -> players, telegram_bots
-- Everything else is free-standing.

BEGIN;

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS inbox_messages;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS player_bot_links;
DROP TABLE IF EXISTS players;
DROP TABLE IF EXISTS telegram_bots;

COMMIT;
