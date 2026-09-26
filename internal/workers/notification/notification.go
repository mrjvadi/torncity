// Package notification tells players about things that happened to them
// while they were not looking.
//
// A domain event — a journey landed — reaches the event stream through the
// outbox. This worker consumes it on a durable, explicitly acknowledged
// consumer, renders the screen for it in the player's language, picks a bot
// the player can actually be reached through, and asks the gateway to send
// it. The gateway owns the Telegram API, the per-bot rate limits and the
// flood waits; this worker owns the decision of what to say, to whom, and
// through which bot.
//
//	outbox ─▶ game.event.<domain>.<event>.v1 ─▶ notifier (durable, per event)
//	                                               │ request
//	                                               ▼
//	                                  game.notify.<player>.v1 ─▶ gateway ─▶ Telegram
//	                                               ▲ receipt
//	                                               └──────────────────────┘
//
// # Which bot
//
// Telegram lets a bot message only the users who started it. The player's
// reachable links (player_bot_links) are tried most recently seen first: that
// is the conversation the player is most likely to be looking at. A link the
// gateway reports unreachable — the player blocked that bot — is marked so
// and the next one is tried. A player with no reachable link is not an error:
// there is nobody to tell, and the event is done.
//
// # Exactly once, nearly
//
// Sending a Telegram message is not idempotent, so this worker cannot lean on
// a replay being harmless the way cmd/game does. It checks the inbox before
// sending and records the event there after the gateway confirms delivery —
// never before, because a record written first and a send that then fails
// loses the notification with no trace. The window that remains is a crash, a
// lost acknowledgement or a failed inbox write between the delivery and the
// record: the redelivery then sends once more. That is the accepted trade. A
// second "you have arrived" is harmless; a lost one is not.
package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Players reads the player a notification is for: their stored language and
// their Telegram id.
type Players interface {
	// GetByID returns the player, or an error classified NOT_FOUND
	// (application.ErrPlayerNotFound).
	GetByID(ctx context.Context, id string) (*application.Player, error)
}

// Links reads and maintains the player's chats with the bot fleet.
type Links interface {
	// ReachableBotLinks returns the player's links that are still reachable,
	// most recently seen first. No links is an empty slice, not an error.
	ReachableBotLinks(ctx context.Context, playerID string) ([]application.BotLink, error)
	// MarkBotUnreachable records that the player can no longer be reached
	// through this bot. The next message the player sends it makes the link
	// reachable again (PlayerRepository.LinkBot).
	MarkBotUnreachable(ctx context.Context, playerID, botID string) error
}

// Inbox is the consumer-side record of which events were already notified.
type Inbox interface {
	// Processed reports whether this consumer already finished this message.
	Processed(ctx context.Context, messageID, consumer string) (bool, error)
	// MarkProcessed records that it has.
	MarkProcessed(ctx context.Context, messageID, consumer string) (bool, error)
}

// Sender hands a notice to the gateway and waits for its receipt.
//
// An error means no receipt came back — nobody listening, a timeout, a broken
// connection — so whether the message was sent is unknown and the event is
// retried.
type Sender interface {
	Send(ctx context.Context, subject string, env *envelope.Envelope) (Receipt, error)
}

// Config is the worker's wiring.
type Config struct {
	Logger  *slog.Logger
	Msgs    screens.Translator
	Players Players
	Links   Links
	Inbox   Inbox
	Sender  Sender
	Deps    Deps

	// SendBudget bounds one event's whole delivery, every bot tried
	// included. It must be shorter than the consumer's ack wait, or the
	// broker redelivers an event that is still being sent.
	SendBudget time.Duration

	// ReceiptMargin is the end of the send budget the gateway may not spend:
	// the notice's DeliverBy is SendBudget minus this, and the rest is left
	// for the receipt to travel back. A send the gateway starts at the very
	// end of the budget would finish after this worker stopped listening,
	// and the redelivery would be a duplicate. It must be positive and
	// shorter than SendBudget.
	ReceiptMargin time.Duration

	// MaxAge is how old an event may be and still be announced. A consumer
	// created for the first time reads the stream from the start, and a
	// month of "you have arrived" arriving at once is worse than none. Zero
	// disables the check.
	MaxAge time.Duration

	// Now is the clock. Nil means time.Now.
	Now func() time.Time

	// Groups are the cities' Telegram groups, for announcements. Nil posts
	// no announcement.
	Groups CityGroups
	// AnnounceWindow and AnnounceMax bound the lines one group receives:
	// at most AnnounceMax in any AnnounceWindow (announce.window and
	// announce.max_per_window). Zero leaves them unbounded.
	AnnounceWindow time.Duration
	AnnounceMax    int

	// Realtime, when set, also publishes every notice and every city
	// announcement to the realtime server (realtime.go); a failure there
	// never touches the Telegram delivery. RealtimeLanguages are the
	// languages an announcement is written in there, the default first.
	Realtime          Realtime
	RealtimeLanguages []string

	// PlayerInbox, DeliveryModes and EditThrottle are the notification
	// inbox (badge.go): what stores a notice and counts it on the badge,
	// which kind sends at once instead, and how often the badge message may
	// be edited. PlayerInbox nil — the zero Config, every existing test —
	// keeps every notice's delivery exactly as it was before this feature:
	// sent at once, nothing stored, no badge. See Worker.classify.
	PlayerInbox   PlayerInbox
	DeliveryModes *DeliveryModes
	EditThrottle  time.Duration
}

// Worker turns events into notices.
type Worker struct {
	cfg      Config
	throttle *throttle
}

// New validates cfg and returns a worker.
func New(cfg Config) (*Worker, error) {
	switch {
	case cfg.Players == nil, cfg.Links == nil, cfg.Inbox == nil, cfg.Sender == nil, cfg.Deps.Cities == nil:
		return nil, errors.New("notification: players, links, inbox, sender and cities are all required")
	case cfg.SendBudget <= 0:
		return nil, errors.New("notification: send budget must be positive")
	case cfg.ReceiptMargin <= 0 || cfg.ReceiptMargin >= cfg.SendBudget:
		return nil, errors.New("notification: receipt margin must be positive and shorter than the send budget")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Worker{cfg: cfg, throttle: &throttle{
		window: cfg.AnnounceWindow, max: cfg.AnnounceMax, groups: map[int64]*groupWindow{},
	}}, nil
}

// Handle processes one delivery of one route's event.
//
// Returning nil is the acknowledgement; an error is a NAK with backoff. As in
// cmd/game, an error means "a later attempt may succeed", never "drop it".
func (w *Worker) Handle(ctx context.Context, route Route, env *envelope.Envelope) error {
	meta := env.Metadata
	if err := meta.Validate(); err != nil {
		w.cfg.Logger.Error("event metadata is invalid", slog.String("error", err.Error()))
		return nil
	}
	consumer := route.Durable()
	log := w.cfg.Logger.With(
		slog.String("trace_id", meta.TraceID),
		slog.String("request_id", meta.RequestID),
		slog.String("consumer", consumer))

	now := w.cfg.Now()
	if w.cfg.MaxAge > 0 && !meta.ReceivedAt.IsZero() && now.Sub(meta.ReceivedAt) > w.cfg.MaxAge {
		log.Info("event is too old to announce", slog.Time("received_at", meta.ReceivedAt))
		return nil
	}

	if route.Announce != nil {
		return w.announce(ctx, route, env, now, log)
	}

	// The message id is the outbox event's (Metadata.MessageID), so each of
	// several events of one kind a command wrote — a notice to every member
	// of a crew, to every player in a struck city — is told once; a message
	// from before the worker stamped it falls back to the request id.
	done, err := w.cfg.Inbox.Processed(ctx, meta.MessageID(), consumer)
	if err != nil {
		log.Error("cannot read the inbox", slog.String("error", err.Error()))
		return err
	}
	if done {
		log.Debug("event already notified")
		return nil
	}

	draft, err := route.Render(ctx, w.cfg.Deps, env)
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeInternal {
			log.Error("cannot render the notification", slog.String("error", err.Error()))
			return err
		}
		log.Warn("event cannot be announced", slog.String("error", err.Error()))
		return nil
	}
	if draft == nil {
		return nil
	}
	log = log.With(slog.String("player_id", draft.PlayerID))

	player, err := w.cfg.Players.GetByID(ctx, draft.PlayerID)
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeNotFound {
			log.Warn("the player the event names does not exist")
			return nil
		}
		log.Error("cannot read the player", slog.String("error", err.Error()))
		return err
	}

	lang := handlers.RenderLanguage(meta, player)
	resp := draft.Screen(screens.Context{Msgs: w.cfg.Msgs, Lang: lang})

	if w.cfg.PlayerInbox != nil {
		if mode, category := w.classify(route); mode == ModeInbox {
			if err := w.storeInboxItem(ctx, route, meta, draft, player, lang, category, log); err != nil {
				return err
			}
			if _, err := w.cfg.Inbox.MarkProcessed(ctx, meta.MessageID(), consumer); err != nil {
				log.Warn("cannot record the notification in the inbox", slog.String("error", err.Error()))
			}
			return nil
		}
	}

	delivered, err := w.deliver(ctx, now, meta, player, lang, resp, log)
	if err != nil {
		return err
	}
	if !delivered {
		log.Warn("no bot can reach the player; the notification is dropped")
	}
	w.publishNotice(ctx, route, meta, player.ID, resp, log)
	if w.cfg.PlayerInbox != nil {
		// Told at once; also kept in the player's history, already read
		// (see the package doc on badge.go). Best effort: the notice itself
		// already went out, and a lost archive row is not worth retrying
		// the whole event for.
		_, category := w.classify(route)
		w.archiveInstant(ctx, route, meta, draft, player, category, log)
	}

	// After success, never before; see the package doc.
	if _, err := w.cfg.Inbox.MarkProcessed(ctx, meta.MessageID(), consumer); err != nil {
		log.Warn("cannot record the notification in the inbox", slog.String("error", err.Error()))
	}
	return nil
}

// classify is the delivery mode and inbox category of route, honouring
// Config.DeliveryModes when it is set and defaulting to ModeInstant — every
// notice sent at once, as before this feature — otherwise.
func (w *Worker) classify(route Route) (Mode, string) {
	if w.cfg.DeliveryModes == nil {
		return ModeInstant, route.Domain
	}
	return w.cfg.DeliveryModes.Classify(route.Domain, route.Event)
}

// deliver tries the player's reachable links in order until one delivers.
// It reports false with no error when no link could: nobody to tell.
//
// A failure that may clear — no receipt, or a receipt saying the send failed
// or ran out of time — stops here and is returned, so the event is retried
// from the top rather than sent through a second bot while the first may yet
// deliver it.
func (w *Worker) deliver(
	ctx context.Context,
	start time.Time,
	meta envelope.Metadata,
	player *application.Player,
	lang string,
	resp *presenter.Response,
	log *slog.Logger,
) (bool, error) {
	links, err := w.cfg.Links.ReachableBotLinks(ctx, player.ID)
	if err != nil {
		log.Error("cannot read the player's bot links", slog.String("error", err.Error()))
		return false, err
	}
	if len(links) == 0 {
		return false, nil
	}

	ctx, cancel := context.WithDeadline(ctx, start.Add(w.cfg.SendBudget))
	defer cancel()
	deliverBy := start.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin)

	for _, link := range links {
		env, err := envelope.New(noticeMetadata(meta, player, link, lang), Notice{DeliverBy: deliverBy, Response: *resp})
		if err != nil {
			log.Error("cannot build the notice", slog.String("error", err.Error()))
			return false, err
		}

		receipt, err := w.cfg.Sender.Send(ctx, subjects.Notify(player.ID), env)
		if err != nil {
			log.Warn("no receipt for the notice", slog.String("bot_id", link.BotID), slog.String("error", err.Error()))
			return false, err
		}

		switch receipt.Outcome {
		case OutcomeDelivered:
			log.Info("notification delivered", slog.String("bot_id", link.BotID))
			return true, nil

		case OutcomeSuppressed:
			log.Info("notification suppressed: telegram_notices is off", slog.String("bot_id", link.BotID))
			return true, nil

		case OutcomeUnreachable:
			log.Info("the player cannot be reached through this bot; trying the next",
				slog.String("bot_id", link.BotID), slog.String("detail", receipt.Detail))
			if err := w.cfg.Links.MarkBotUnreachable(ctx, player.ID, link.BotID); err != nil {
				// Not fatal: the next event tries this bot again and
				// learns the same thing.
				log.Warn("cannot mark the bot link unreachable",
					slog.String("bot_id", link.BotID), slog.String("error", err.Error()))
			}

		case OutcomeUnknownBot:
			log.Warn("the gateway does not serve this bot; trying the next", slog.String("bot_id", link.BotID))

		default:
			return false, fmt.Errorf("notification: notice through bot %s: %s: %s", link.BotID, receipt.Outcome, receipt.Detail)
		}
	}
	return false, nil
}

// noticeMetadata addresses a notice. The trace id is the event's, so the
// notification is found in the logs by the same id as the arrival that caused
// it; the chat and bot are the link's, and the language is the one the screen
// was written in.
func noticeMetadata(event envelope.Metadata, player *application.Player, link application.BotLink, lang string) envelope.Metadata {
	meta := event
	meta.PlayerID = player.ID
	meta.TelegramUserID = player.TelegramUserID
	meta.TelegramChatID = link.TelegramChatID
	meta.BotID = link.BotID
	meta.Language = lang
	meta.ChatType = "private"
	// A notice answers nothing and edits nothing.
	meta.TelegramMessageID = 0
	meta.CallbackQueryID = nil
	meta.ReplyToMessageID = nil
	meta.TelegramThreadID = nil
	return meta
}
