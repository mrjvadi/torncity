-- 0060_notification_neutral — the inbox keeps a notice as data, not as text.
--
-- A notice the notifier pushes is now a neutral screen and its view
-- (docs/adr/0039-presentation-split.md, section 8): each edge words it in the
-- reader's language when it is read, so a Telegram wording change reaches the
-- items already stored and the web words them with its own table. The row
-- keeps the screen's name and the view's JSON; text_fa and text_en stay for
-- the notices of screens that are not carried as data yet (written once, in
-- both shipped languages, as before) and are empty for the rest.

BEGIN;

ALTER TABLE player_notifications
    ADD COLUMN screen text  NOT NULL DEFAULT '',
    ADD COLUMN view   jsonb NULL,
    ALTER COLUMN text_fa SET DEFAULT '',
    ALTER COLUMN text_en SET DEFAULT '';

COMMIT;
