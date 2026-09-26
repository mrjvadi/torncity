-- 0037_notification_inbox, reversed. Every stored notification and the
-- badges go; players go back to a flood of Telegram messages.

BEGIN;

ALTER TABLE player_life DROP COLUMN hunger_alert_at;

DROP INDEX player_inbox_badges_reminder_idx;
DROP TABLE player_inbox_badges;

DROP INDEX player_notifications_read_at_idx;
DROP INDEX player_notifications_player_created_idx;
DROP INDEX player_notifications_unread_idx;
DROP TABLE player_notifications;

COMMIT;
