-- 0013_crime, reversed. Scheduled 'crime', 'crime_investigation' and
-- 'jail_release' rows in game_actions are left where they are: game_actions
-- is an open vocabulary, and a row nobody routes is reported by the
-- scheduler rather than lost. Ledger rows posted under the crime reasons stay
-- too: the ledger is append-only and balanced on its own.

BEGIN;

DROP TABLE crime_npc_proceeds;
DROP TABLE crime_reports;
ALTER TABLE crimes DROP CONSTRAINT crimes_jail_sentence_id_fkey;
DROP TABLE jail_sentences;
DROP TABLE crimes;
DROP TABLE criminal_profiles;
DROP INDEX players_city_active_idx;
ALTER TABLE players DROP COLUMN last_active_at;
DROP TABLE crime_content;

COMMIT;
