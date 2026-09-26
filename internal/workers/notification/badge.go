package notification

import (
	"context"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file is the inbox badge: the ONE Telegram message a player's unread
// count is shown on, edited in place as more inbox-mode notices arrive
// (Worker.classify, ModeInbox), instead of the flood of separate messages
// every notice used to be. An instant notice (ModeInstant) is unaffected —
// see notification.go — and is also archived here, already read, so /inbox
// is a player's whole history rather than only what piled up.
//
// PlayerInbox stores a player's notifications and this one badge row
// (migrations/0037_notification_inbox); postgres.PlayerInboxRepository backs
// it, the same table the /inbox screen reads through
// application.NotificationInboxRepository.
type PlayerInbox interface {
	// Record stores one item, already read when read is true (an instant
	// notice, told already) or unread otherwise. Called only once the
	// caller's own redelivery guard (Config.Inbox) has passed, so it asks no
	// further idempotency of its own.
	Record(ctx context.Context, item application.InboxRecord, read bool, now time.Time) error
	// Summary is the player's unread total, per category, and the `recent`
	// most recent items overall, for the badge's teaser line.
	Summary(ctx context.Context, playerID string, recent int) (application.InboxSummary, error)
	// Badge reads the player's badge row, nil when none exists yet.
	Badge(ctx context.Context, playerID string) (*application.InboxBadge, error)
	// SaveBadge upserts it.
	SaveBadge(ctx context.Context, b application.InboxBadge, now time.Time) error
	// DueReminders returns up to limit players whose badge has sat unread
	// since before `since`, with no reminder sent since the last item
	// arrived, and marks reminded_at on every one it returns.
	DueReminders(ctx context.Context, since, now time.Time, limit int) ([]application.InboxReminder, error)
	// Prune deletes read items older than before, for retention.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// badgeTeaserItems is how many of the most recent items the badge quotes.
const badgeTeaserItems = 2

// storeInboxItem records one inbox-mode notice and keeps the badge current.
//
// Only Record's failure is returned (and so retried): the item itself must
// not be lost. Everything after — the summary, the badge send or edit — is
// best effort and logged, because the item is already safely stored and
// /inbox always shows the truth regardless of what the badge currently
// says; the next item, or the player opening /inbox, catches it up.
func (w *Worker) storeInboxItem(ctx context.Context, route Route, meta envelope.Metadata, draft *Draft,
	player *application.Player, lang, category string, log *slog.Logger,
) error {
	now := w.cfg.Now()
	item := application.InboxRecord{
		PlayerID: player.ID, Category: category, Kind: routeKind(route),
		TextFA: renderText(draft, w.cfg.Msgs, "fa"), TextEN: renderText(draft, w.cfg.Msgs, "en"),
		LinkAddr: draft.Link, SourceMessageID: meta.MessageID(),
	}
	if err := w.cfg.PlayerInbox.Record(ctx, item, false, now); err != nil {
		log.Error("cannot record an inbox notification", slog.String("error", err.Error()))
		return err
	}

	summary, err := w.cfg.PlayerInbox.Summary(ctx, player.ID, badgeTeaserItems)
	if err != nil {
		log.Warn("cannot read the inbox summary", slog.String("error", err.Error()))
		return nil
	}
	w.publishInboxUpdate(ctx, meta, player.ID, summary.Unread, log)
	w.updateBadge(ctx, meta, player, lang, summary, now, log)
	return nil
}

// archiveInstant records an instant notice in the player's history, already
// read: it was just told at once, the way it always has been.
func (w *Worker) archiveInstant(ctx context.Context, route Route, meta envelope.Metadata, draft *Draft,
	player *application.Player, category string, log *slog.Logger,
) {
	item := application.InboxRecord{
		PlayerID: player.ID, Category: category, Kind: routeKind(route),
		TextFA: renderText(draft, w.cfg.Msgs, "fa"), TextEN: renderText(draft, w.cfg.Msgs, "en"),
		LinkAddr: draft.Link, SourceMessageID: meta.MessageID(),
	}
	if err := w.cfg.PlayerInbox.Record(ctx, item, true, w.cfg.Now()); err != nil {
		log.Warn("cannot archive an instant notification", slog.String("error", err.Error()))
	}
}

// renderText is a draft's text in one language, for storage. It ignores the
// keyboard: the inbox screen builds its own buttons from the stored item.
func renderText(draft *Draft, msgs screens.Translator, lang string) string {
	return draft.Screen(screens.Context{Msgs: msgs, Lang: lang}).Text
}

// updateBadge sends the badge the first time and edits it after, throttled;
// an edit that cannot land — the message was deleted, or Telegram otherwise
// refuses it — falls back to sending a fresh one, exactly as the design
// asks, without this package assuming which of the Bot API's reasons applies
// (see the editMessageText documentation for what it does and does not
// promise about how long a message stays editable).
func (w *Worker) updateBadge(ctx context.Context, meta envelope.Metadata, player *application.Player, lang string,
	summary application.InboxSummary, now time.Time, log *slog.Logger,
) {
	view := badgeView(summary, lang)

	badge, err := w.cfg.PlayerInbox.Badge(ctx, player.ID)
	if err != nil {
		log.Warn("cannot read the inbox badge", slog.String("error", err.Error()))
		return
	}
	if badge == nil || badge.TelegramMessageID == 0 {
		w.sendFreshBadge(ctx, meta, player, lang, view, summary.Unread, now, log)
		return
	}

	updated := *badge
	updated.UnreadCount = summary.Unread
	updated.LastNotificationAt = &now

	if badge.LastEditedAt != nil && w.cfg.EditThrottle > 0 && now.Sub(*badge.LastEditedAt) < w.cfg.EditThrottle {
		// Coalesced: the count is saved without calling Telegram again. The
		// next item — or the player opening /inbox before one arrives —
		// catches the message up.
		if err := w.cfg.PlayerInbox.SaveBadge(ctx, updated, now); err != nil {
			log.Warn("cannot save the inbox badge", slog.String("error", err.Error()))
		}
		return
	}

	link := application.BotLink{BotID: badge.BotID, TelegramChatID: badge.ChatID}
	resp := screens.InboxBadge(screens.Context{Msgs: w.cfg.Msgs, Lang: lang, MessageID: badge.TelegramMessageID}, view)
	env, err := envelope.New(noticeMetadata(meta, player, link, lang),
		Notice{DeliverBy: now.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin), Response: *resp, Edit: true})
	if err != nil {
		log.Warn("cannot build the inbox badge edit", slog.String("error", err.Error()))
		return
	}
	receipt, sendErr := w.cfg.Sender.Send(ctx, subjects.Notify(player.ID), env)
	if sendErr == nil && receipt.Outcome == OutcomeDelivered {
		updated.LastEditedAt = &now
		if err := w.cfg.PlayerInbox.SaveBadge(ctx, updated, now); err != nil {
			log.Warn("cannot save the inbox badge", slog.String("error", err.Error()))
		}
		return
	}
	log.Info("cannot edit the inbox badge; sending it fresh instead",
		slog.String("outcome", string(receipt.Outcome)), slog.String("detail", receipt.Detail))
	w.sendFreshBadge(ctx, meta, player, lang, view, summary.Unread, now, log)
}

// sendFreshBadge sends a new badge message through the player's most
// recently seen bot and remembers its id. No reachable link is not an
// error: nobody to tell right now, and the next item tries again.
func (w *Worker) sendFreshBadge(ctx context.Context, meta envelope.Metadata, player *application.Player, lang string,
	view screens.InboxBadgeView, unread int, now time.Time, log *slog.Logger,
) {
	links, err := w.cfg.Links.ReachableBotLinks(ctx, player.ID)
	if err != nil {
		log.Warn("cannot read the player's bot links for the inbox badge", slog.String("error", err.Error()))
		return
	}
	if len(links) == 0 {
		return
	}
	link := links[0]
	resp := screens.InboxBadge(screens.Context{Msgs: w.cfg.Msgs, Lang: lang}, view)
	env, err := envelope.New(noticeMetadata(meta, player, link, lang),
		Notice{DeliverBy: now.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin), Response: *resp})
	if err != nil {
		log.Warn("cannot build the inbox badge", slog.String("error", err.Error()))
		return
	}
	receipt, sendErr := w.cfg.Sender.Send(ctx, subjects.Notify(player.ID), env)
	if sendErr != nil || receipt.Outcome != OutcomeDelivered {
		log.Info("cannot send the inbox badge", slog.String("outcome", string(receipt.Outcome)))
		return
	}
	badge := application.InboxBadge{
		PlayerID: player.ID, BotID: link.BotID, ChatID: link.TelegramChatID,
		TelegramMessageID: receipt.MessageID, UnreadCount: unread, LastNotificationAt: &now, LastEditedAt: &now,
	}
	if err := w.cfg.PlayerInbox.SaveBadge(ctx, badge, now); err != nil {
		log.Warn("cannot save the inbox badge", slog.String("error", err.Error()))
	}
}

// badgeView turns a summary into the badge's view.
func badgeView(summary application.InboxSummary, lang string) screens.InboxBadgeView {
	v := screens.InboxBadgeView{Unread: summary.Unread}
	for _, c := range summary.Categories {
		v.Categories = append(v.Categories, screens.InboxCategoryCount{Category: c.Category, Count: c.Count})
	}
	for i, item := range summary.Recent {
		if i >= badgeTeaserItems {
			break
		}
		v.Teaser = append(v.Teaser, item.Text(lang))
	}
	return v
}

// SendDueReminders nudges players whose badge has sat unread for at least
// since, once per pile of unread items (PlayerInbox.DueReminders marks the
// batch as reminded). cmd/notifier calls this on a timer
// (notifications.reminder_check); it is not event-driven, because nothing
// arriving is what a reminder is about.
func (w *Worker) SendDueReminders(ctx context.Context, since time.Duration, limit int) {
	if w.cfg.PlayerInbox == nil {
		return
	}
	now := w.cfg.Now()
	log := w.cfg.Logger
	due, err := w.cfg.PlayerInbox.DueReminders(ctx, now.Add(-since), now, limit)
	if err != nil {
		log.Warn("cannot read due inbox reminders", slog.String("error", err.Error()))
		return
	}
	for _, rem := range due {
		w.sendReminder(ctx, rem, now, log)
	}
}

// sendReminder tells a player their inbox has sat unread, with a button to
// open it. It goes out through the badge's own bot and chat: the badge
// message is what "open" leads to, so the reminder must come from a
// conversation the player can actually see it in.
func (w *Worker) sendReminder(ctx context.Context, rem application.InboxReminder, now time.Time, log *slog.Logger) {
	player, err := w.cfg.Players.GetByID(ctx, rem.PlayerID)
	if err != nil {
		log.Warn("cannot read the player for an inbox reminder", slog.String("error", err.Error()))
		return
	}
	lang := handlers.RenderLanguage(envelope.Metadata{}, player)
	link := application.BotLink{BotID: rem.BotID, TelegramChatID: rem.ChatID}
	resp := screens.InboxReminder(screens.Context{Msgs: w.cfg.Msgs, Lang: lang}, screens.InboxReminderView{Unread: rem.UnreadCount})
	// No source event started this: it is a timer, not something that
	// happened. Metadata.Validate still asks for a request id, a trace id
	// and a command, so a synthetic one is given, exactly the shape
	// noticeMetadata then addresses to the player below.
	meta := noticeMetadata(envelope.Metadata{
		RequestID: "inbox-reminder-" + rem.PlayerID, TraceID: "inbox-reminder-" + rem.PlayerID,
		Command: "inbox.reminder", SchemaVersion: envelope.SchemaVersion,
	}, player, link, lang)
	env, err := envelope.New(meta, Notice{DeliverBy: now.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin), Response: *resp})
	if err != nil {
		log.Warn("cannot build an inbox reminder", slog.String("error", err.Error()))
		return
	}
	if _, err := w.cfg.Sender.Send(ctx, subjects.Notify(player.ID), env); err != nil {
		log.Warn("cannot send an inbox reminder", slog.String("error", err.Error()))
	}
}
