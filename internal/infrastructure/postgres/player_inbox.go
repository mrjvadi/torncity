package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// This file persists the player-facing inbox (migrations/0037_notification_
// inbox): the notifications stored for a player and the one badge message
// their count is edited onto. It backs two ports with one implementation:
//
//   - application.NotificationInboxRepository, read (and marked read) by the
//     /inbox screen, a game command, inside the caller's uow.Do transaction;
//   - notification.PlayerInbox, written by cmd/notifier as events arrive,
//     outside any transaction — a Telegram send cannot roll back with one,
//     the same reason inbox.go's consumer-dedup table stands apart.
//
// Both are satisfied structurally: this type imports neither port package,
// and both ports are spelled in application types, exactly like every other
// notifier port (Links, Players, Inbox). See tests/notification_store_
// integration_test.go for the compile-time proof.
type PlayerInboxRepository struct {
	q querier
}

// NewPlayerInboxRepository returns a repository over the pool, for
// cmd/notifier, which holds no unit of work.
func NewPlayerInboxRepository(p *Pool) *PlayerInboxRepository { return &PlayerInboxRepository{q: p.shared()} }

var _ application.NotificationInboxRepository = (*PlayerInboxRepository)(nil)

// --- written by cmd/notifier ------------------------------------------------

// Record inserts one stored notification.
func (r *PlayerInboxRepository) Record(ctx context.Context, item application.InboxRecord, read bool, now time.Time) error {
	if !validUUID(item.PlayerID) {
		return application.ErrPlayerNotFound
	}
	var readAt any
	if read {
		readAt = now.UTC()
	}
	if _, err := r.q.Exec(ctx, `
INSERT INTO player_notifications
	(id, player_id, category, kind, text_fa, text_en, link_addr, source_message_id, created_at, read_at)
VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4, $5, $6, $7, $8, $9)`,
		item.PlayerID, item.Category, item.Kind, item.TextFA, item.TextEN, item.LinkAddr, item.SourceMessageID,
		now.UTC(), readAt); err != nil {
		return fmt.Errorf("postgres: recording a notification for player %s: %w", item.PlayerID, err)
	}
	return nil
}

// Badge reads the player's badge row, nil when none exists yet.
func (r *PlayerInboxRepository) Badge(ctx context.Context, playerID string) (*application.InboxBadge, error) {
	if !validUUID(playerID) {
		return nil, nil
	}
	var b application.InboxBadge
	err := r.q.QueryRow(ctx, `
SELECT player_id::text, bot_id::text, chat_id, telegram_message_id, unread_count,
       last_notification_at, last_edited_at, reminded_at
  FROM player_inbox_badges WHERE player_id = $1::uuid`, playerID).
		Scan(&b.PlayerID, &b.BotID, &b.ChatID, &b.TelegramMessageID, &b.UnreadCount,
			&b.LastNotificationAt, &b.LastEditedAt, &b.RemindedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading the inbox badge of player %s: %w", playerID, err)
	}
	return &b, nil
}

// SaveBadge upserts the badge row.
func (r *PlayerInboxRepository) SaveBadge(ctx context.Context, b application.InboxBadge, now time.Time) error {
	if !validUUID(b.PlayerID) || !validUUID(b.BotID) {
		return fmt.Errorf("postgres: saving an inbox badge: player and bot must both be real")
	}
	if _, err := r.q.Exec(ctx, `
INSERT INTO player_inbox_badges
	(player_id, bot_id, chat_id, telegram_message_id, unread_count,
	 last_notification_at, last_edited_at, reminded_at, created_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $9)
ON CONFLICT (player_id) DO UPDATE SET
	bot_id = EXCLUDED.bot_id, chat_id = EXCLUDED.chat_id,
	telegram_message_id = EXCLUDED.telegram_message_id, unread_count = EXCLUDED.unread_count,
	last_notification_at = EXCLUDED.last_notification_at, last_edited_at = EXCLUDED.last_edited_at,
	reminded_at = EXCLUDED.reminded_at, updated_at = EXCLUDED.updated_at`,
		b.PlayerID, b.BotID, b.ChatID, b.TelegramMessageID, b.UnreadCount,
		b.LastNotificationAt, b.LastEditedAt, b.RemindedAt, now.UTC()); err != nil {
		return fmt.Errorf("postgres: saving the inbox badge of player %s: %w", b.PlayerID, err)
	}
	return nil
}

// DueReminders returns up to limit players whose badge has unread items that
// have sat since before `since`, with no reminder sent after the most recent
// item arrived, and marks reminded_at on every one it returns so the same
// pile of unread items is never nudged twice.
//
// The select-then-update runs inside one statement (UPDATE ... RETURNING) so
// two notifier instances polling at once cannot both claim the same player.
func (r *PlayerInboxRepository) DueReminders(ctx context.Context, since, now time.Time, limit int) ([]application.InboxReminder, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.q.Query(ctx, `
UPDATE player_inbox_badges b SET reminded_at = $3
WHERE b.player_id IN (
	SELECT player_id FROM player_inbox_badges
	 WHERE unread_count > 0
	   AND telegram_message_id <> 0
	   AND last_notification_at IS NOT NULL AND last_notification_at <= $1
	   AND (reminded_at IS NULL OR reminded_at < last_notification_at)
	 ORDER BY last_notification_at
	 LIMIT $2
)
RETURNING b.player_id::text, b.bot_id::text, b.chat_id, b.telegram_message_id, b.unread_count`,
		since.UTC(), limit, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: reading due inbox reminders: %w", err)
	}
	defer rows.Close()

	var out []application.InboxReminder
	for rows.Next() {
		var rem application.InboxReminder
		if err := rows.Scan(&rem.PlayerID, &rem.BotID, &rem.ChatID, &rem.TelegramMessageID, &rem.UnreadCount); err != nil {
			return nil, fmt.Errorf("postgres: reading a due inbox reminder: %w", err)
		}
		out = append(out, rem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading due inbox reminders: %w", err)
	}
	return out, nil
}

// Prune deletes read items older than before, for retention
// (notifications.retention). It reports how many rows were removed.
func (r *PlayerInboxRepository) Prune(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.q.Exec(ctx, `DELETE FROM player_notifications WHERE read_at IS NOT NULL AND read_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: pruning read notifications: %w", err)
	}
	return tag.RowsAffected(), nil
}

// --- read by the /inbox screen (application.NotificationInboxRepository) ---

// Summary reads the unread total, the unread count per category, and the
// `recent` most recent items overall (any read state), newest first.
func (r *PlayerInboxRepository) Summary(ctx context.Context, playerID string, recent int) (application.InboxSummary, error) {
	var sum application.InboxSummary
	if !validUUID(playerID) {
		return sum, nil
	}
	if err := r.q.QueryRow(ctx, `
SELECT count(*) FROM player_notifications WHERE player_id = $1::uuid AND read_at IS NULL`, playerID).
		Scan(&sum.Unread); err != nil {
		return sum, fmt.Errorf("postgres: counting unread notifications of player %s: %w", playerID, err)
	}

	catRows, err := r.q.Query(ctx, `
SELECT category, count(*) FROM player_notifications
 WHERE player_id = $1::uuid AND read_at IS NULL
 GROUP BY category ORDER BY category`, playerID)
	if err != nil {
		return sum, fmt.Errorf("postgres: grouping unread notifications of player %s: %w", playerID, err)
	}
	defer catRows.Close()
	for catRows.Next() {
		var c application.CategoryCount
		if err := catRows.Scan(&c.Category, &c.Count); err != nil {
			return sum, fmt.Errorf("postgres: reading an unread category of player %s: %w", playerID, err)
		}
		sum.Categories = append(sum.Categories, c)
	}
	if err := catRows.Err(); err != nil {
		return sum, fmt.Errorf("postgres: grouping unread notifications of player %s: %w", playerID, err)
	}

	if recent <= 0 {
		return sum, nil
	}
	items, err := r.scanItems(ctx, `
SELECT id::text, player_id::text, category, kind, text_fa, text_en, link_addr, created_at, read_at
  FROM player_notifications WHERE player_id = $1::uuid
 ORDER BY created_at DESC LIMIT $2`, playerID, recent)
	if err != nil {
		return sum, fmt.Errorf("postgres: reading recent notifications of player %s: %w", playerID, err)
	}
	sum.Recent = items
	return sum, nil
}

// List returns one category's items, newest first, and the total number of
// pages of pageSize.
func (r *PlayerInboxRepository) List(ctx context.Context, playerID, category string, page, pageSize int) ([]application.NotificationItem, int, error) {
	if !validUUID(playerID) {
		return nil, 0, nil
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 1
	}
	var total int
	if err := r.q.QueryRow(ctx, `
SELECT count(*) FROM player_notifications WHERE player_id = $1::uuid AND category = $2`, playerID, category).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: counting %s notifications of player %s: %w", category, playerID, err)
	}
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}

	items, err := r.scanItems(ctx, `
SELECT id::text, player_id::text, category, kind, text_fa, text_en, link_addr, created_at, read_at
  FROM player_notifications WHERE player_id = $1::uuid AND category = $2
 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, playerID, category, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: reading %s notifications of player %s: %w", category, playerID, err)
	}
	return items, pages, nil
}

// MarkAllRead marks every unread item read and reports how many changed.
func (r *PlayerInboxRepository) MarkAllRead(ctx context.Context, playerID string) (int, error) {
	if !validUUID(playerID) {
		return 0, nil
	}
	tag, err := r.q.Exec(ctx, `
UPDATE player_notifications SET read_at = $2 WHERE player_id = $1::uuid AND read_at IS NULL`,
		playerID, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: marking notifications read for player %s: %w", playerID, err)
	}
	return int(tag.RowsAffected()), nil
}

// scanItems runs a query returning the notification item columns in the
// fixed order every caller above uses, and scans every row.
func (r *PlayerInboxRepository) scanItems(ctx context.Context, sql string, args ...any) ([]application.NotificationItem, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []application.NotificationItem
	for rows.Next() {
		var it application.NotificationItem
		if err := rows.Scan(&it.ID, &it.PlayerID, &it.Category, &it.Kind, &it.TextFA, &it.TextEN, &it.LinkAddr,
			&it.CreatedAt, &it.ReadAt); err != nil {
			return nil, err
		}
		it.CreatedAt = it.CreatedAt.UTC()
		if it.ReadAt != nil {
			v := it.ReadAt.UTC()
			it.ReadAt = &v
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
