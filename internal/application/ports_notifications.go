package application

import (
	"context"
	"time"
)

// This file is the /inbox screen's read side (migrations/0037_notification_
// inbox): a player's stored notifications, grouped by category, and marking
// them read. What WRITES a notification and keeps the one badge message
// edited in place is cmd/notifier (internal/workers/notification), which is
// not a game command and shares none of this Tx's transaction — see
// postgres.PlayerInboxRepository, which backs both this interface and that
// package's own.

// NotificationItem is one row of a player's notification history: something
// they were, or will be, told. It carries its text ready-rendered in both
// shipped languages (TextFA, TextEN) because it was rendered once, in the
// player's language at the time, for each — never re-rendered from raw event
// data — so a player who changes language later still reads every past item
// correctly instead of in whichever language happened to be current.
type NotificationItem struct {
	ID       string
	PlayerID string
	// Category groups the inbox screen (companies, finance, jobs, market,
	// government, war, social, health, crime, ...).
	Category string
	// Kind is the event behind it, "<domain>.<event>" (routeKind in
	// internal/workers/notification), kept for tracing.
	Kind           string
	TextFA, TextEN string
	// LinkAddr is a callback address (keyboards.Data) the item's "open"
	// button replays, for a notice with a screen of its own to point at.
	// Empty means the item has none.
	LinkAddr  string
	CreatedAt time.Time
	// ReadAt is nil until the player opens the item's category, or "mark
	// all read".
	ReadAt *time.Time
}

// Text picks the item's rendered text for lang, falling back to Persian
// (the one language every locale falls back to; see configs/locales/fa.yml).
func (n NotificationItem) Text(lang string) string {
	if lang == "en" && n.TextEN != "" {
		return n.TextEN
	}
	return n.TextFA
}

// CategoryCount is one category's tally, for the inbox hub and the badge's
// breakdown.
type CategoryCount struct {
	Category string
	Count    int
}

// InboxSummary is what the inbox hub screen and the badge both read: the
// unread total, unread per category, and the most recent items overall (the
// badge's teaser line).
type InboxSummary struct {
	Unread     int
	Categories []CategoryCount
	Recent     []NotificationItem
}

// NotificationInboxRepository is a player's stored notifications and their
// inbox badge.
type NotificationInboxRepository interface {
	// Summary is what the inbox hub screen and the badge both need: the
	// unread total, the unread count per category, and the `recent` most
	// recent items overall (any read state), newest first.
	Summary(ctx context.Context, playerID string, recent int) (InboxSummary, error)
	// List returns one category's items, newest first, page 1-based
	// (page < 1 is page 1). It reports the total number of pages too.
	List(ctx context.Context, playerID, category string, page, pageSize int) ([]NotificationItem, int, error)
	// MarkAllRead marks every unread item read and reports how many changed.
	MarkAllRead(ctx context.Context, playerID string) (int, error)
}

// The types below are the write side cmd/notifier uses
// (internal/workers/notification), spelled here rather than in that package
// so postgres.PlayerInboxRepository — which backs both this interface and
// notification.PlayerInbox — needs to import neither package to satisfy the
// other, exactly as every other notifier port (Links, Players, Inbox) is
// already spelled in application types.

// InboxRecord is one notification to store, read already when the caller
// says so (an instant notice, told already) or unread otherwise.
type InboxRecord struct {
	PlayerID       string
	Category       string
	Kind           string
	TextFA, TextEN string
	LinkAddr       string
	// SourceMessageID is the outbox event's Metadata.MessageID(), kept for
	// tracing a notification back to what produced it. Not relied on for
	// idempotency: the caller's own redelivery guard (inbox_messages) has
	// already passed by the time this is written.
	SourceMessageID string
}

// InboxBadge is the one Telegram message a player's inbox count is edited
// onto (migrations/0037, player_inbox_badges). TelegramMessageID zero means
// no message exists yet.
type InboxBadge struct {
	PlayerID          string
	BotID             string
	ChatID            int64
	TelegramMessageID int64
	UnreadCount       int
	// LastNotificationAt is when the most recent item was added: the 24h
	// reminder measures the badge's age from this, not any one item's own
	// timestamp.
	LastNotificationAt *time.Time
	// LastEditedAt is the edit throttle's bookkeeping: an item inside the
	// throttle window updates the count here but does not call Telegram.
	LastEditedAt *time.Time
	// RemindedAt is when a 24h-unread reminder last went out, so it fires
	// once per pile of unread items, not on every poll.
	RemindedAt *time.Time
}

// InboxReminder is one player whose badge is due the 24h-unread nudge.
type InboxReminder struct {
	PlayerID          string
	BotID             string
	ChatID            int64
	TelegramMessageID int64
	UnreadCount       int
}
