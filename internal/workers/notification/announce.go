package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Announcements: public lines in a city's Telegram groups.
//
// A notice is private, to one player. An announcement is the opposite: one
// line posted in the group(s) a city is played in (city_group_links), for
// everyone there — a player arrived in the city, a player was jailed there —
// and, for a payment started in a group, one line in that group saying who
// paid whom. An announcement never carries an amount, a balance or anything
// else that is one player's own business; it names players by their display
// name, as every public screen does.
//
// It is written in the GROUP's language (the link's), not the player's, and
// sent through the bot that serves the group.
//
// # Exactly once, per group
//
// Each group a line goes to is recorded in the inbox on its own, under this
// route's consumer and the group's chat id, after the gateway confirms the
// send — so a redelivery after a partial failure sends only to the groups
// that did not get it, and a group never reads the same line twice (bar the
// crash window a notice has too; see the package doc).
//
// # A busy city
//
// A group reads at most announce.max_per_window lines in any announce.window.
// A line over the limit is not sent and not retried; it is counted, and the
// next line that goes out says how many were held back. The count is kept in
// this process, so two notifier instances each keep their own; the bound is
// then per instance, which is still a bound.

// Announcer turns one event into an Announcement, or nil when the event is
// nothing to announce.
type Announcer func(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error)

// Announcement is a public line before it knows its groups and language.
type Announcement struct {
	// CityID names the city whose groups read the line. ChatID, instead,
	// names one group directly (the group a payment started in), and BotID
	// the bot to send through when that group is linked to no city.
	CityID string
	ChatID int64
	BotID  string
	// PlayerID is the player the line is about, whose display name it
	// carries when Name is empty.
	PlayerID string
	Name     string
	// Line writes the line in a group's language. name is the player's
	// display name, empty for one with none worth showing.
	Line func(c screens.Context, name string) string
}

// CityGroups is the read an announcement needs: which groups a city is
// played in, and whether a group is linked to a city at all.
type CityGroups interface {
	ForCity(ctx context.Context, cityID, cityCode string) ([]application.CityGroup, error)
	ByChat(ctx context.Context, chatID int64) (*application.CityGroup, error)
}

// throttle bounds the lines one group receives; see the file comment.
type throttle struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	groups map[int64]*groupWindow
}

type groupWindow struct {
	start time.Time
	sent  int
	held  int
}

// allow reports whether a line may go to chat now, and how many lines were
// held back since the last one that went out (to fold into this one).
func (t *throttle) allow(chat int64, now time.Time) (bool, int) {
	if t == nil || t.max <= 0 || t.window <= 0 {
		return true, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.groups[chat]
	if w == nil || now.Sub(w.start) >= t.window {
		held := 0
		if w != nil {
			held = w.held
		}
		t.groups[chat] = &groupWindow{start: now, sent: 1}
		return true, held
	}
	if w.sent >= t.max {
		w.held++
		return false, 0
	}
	w.sent++
	held := w.held
	w.held = 0
	return true, held
}

// announce posts one event's line in its groups. See the file comment.
func (w *Worker) announce(ctx context.Context, route Route, env *envelope.Envelope, now time.Time, log *slog.Logger) error {
	if w.cfg.Groups == nil {
		return nil
	}
	a, err := route.Announce(ctx, w.cfg.Deps, env)
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeInternal {
			log.Error("cannot render the announcement", slog.String("error", err.Error()))
			return err
		}
		log.Warn("event cannot be announced", slog.String("error", err.Error()))
		return nil
	}
	if a == nil {
		return nil
	}

	targets, err := w.targets(ctx, a, env.Metadata)
	if err != nil {
		log.Error("cannot read the groups to announce in", slog.String("error", err.Error()))
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	name := a.Name
	if name == "" && a.PlayerID != "" {
		p, err := w.cfg.Players.GetByID(ctx, a.PlayerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			return err
		}
	}

	consumer := route.Durable()
	for _, g := range targets {
		key := consumer + ":" + strconv.FormatInt(g.ChatID, 10)
		done, err := w.cfg.Inbox.Processed(ctx, env.Metadata.RequestID, key)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		ok, held := w.throttle.allow(g.ChatID, now)
		if !ok {
			log.Info("announcement held back: the group had its fill for now", slog.Int64("chat_id", g.ChatID))
			if _, err := w.cfg.Inbox.MarkProcessed(ctx, env.Metadata.RequestID, key); err != nil {
				log.Warn("cannot record the announcement in the inbox", slog.String("error", err.Error()))
			}
			continue
		}
		c := screens.Context{Msgs: w.cfg.Msgs, Lang: g.Language}
		text := screens.Announcement(c, a.Line(c, name), held)
		if err := w.sendAnnouncement(ctx, now, env.Metadata, g, text, log); err != nil {
			return err
		}
		if _, err := w.cfg.Inbox.MarkProcessed(ctx, env.Metadata.RequestID, key); err != nil {
			log.Warn("cannot record the announcement in the inbox", slog.String("error", err.Error()))
		}
	}
	return nil
}

// targets are the groups a line goes to: the city's, or the one group named.
func (w *Worker) targets(ctx context.Context, a *Announcement, meta envelope.Metadata) ([]application.CityGroup, error) {
	if a.ChatID != 0 {
		g, err := w.cfg.Groups.ByChat(ctx, a.ChatID)
		if err != nil {
			return nil, err
		}
		if g != nil {
			return []application.CityGroup{*g}, nil
		}
		// A group no city is linked to: through the bot the event came from,
		// in the language of the player who acted there.
		bot := a.BotID
		if bot == "" {
			bot = meta.BotID
		}
		if bot == "" {
			return nil, nil
		}
		return []application.CityGroup{{ChatID: a.ChatID, BotID: bot, Language: meta.Language}}, nil
	}
	if a.CityID == "" {
		return nil, nil
	}
	return w.cfg.Groups.ForCity(ctx, a.CityID, "")
}

// errAnnouncementFailed is a send whose receipt says it may succeed later.
var errAnnouncementFailed = errors.New("notification: announcement not delivered")

// sendAnnouncement hands one line to the gateway for one group and waits for
// its receipt. A group the bot cannot post in any more is logged and skipped:
// retrying will not change that.
func (w *Worker) sendAnnouncement(ctx context.Context, start time.Time, event envelope.Metadata, g application.CityGroup, text string, log *slog.Logger) error {
	ctx, cancel := context.WithDeadline(ctx, start.Add(w.cfg.SendBudget))
	defer cancel()
	meta := event
	meta.BotID = g.BotID
	meta.TelegramChatID = g.ChatID
	meta.ChatType = "supergroup"
	meta.Language = g.Language
	meta.TelegramUserID = 0
	meta.TelegramMessageID = 0
	meta.CallbackQueryID = nil
	meta.ReplyToMessageID = nil
	meta.TelegramThreadID = nil

	env, err := envelope.New(meta, Notice{
		DeliverBy:    start.Add(w.cfg.SendBudget - w.cfg.ReceiptMargin),
		Response:     *presenter.Message(text, nil),
		Announcement: true,
	})
	if err != nil {
		return err
	}
	receipt, err := w.cfg.Sender.Send(ctx, subjects.Notify("group"+strconv.FormatInt(-g.ChatID, 10)), env)
	if err != nil {
		return err
	}
	switch receipt.Outcome {
	case OutcomeDelivered:
		log.Info("announcement posted", slog.Int64("chat_id", g.ChatID), slog.String("bot_id", g.BotID))
		return nil
	case OutcomeUnreachable, OutcomeUnknownBot:
		log.Warn("the group cannot be posted in; the announcement is dropped there",
			slog.Int64("chat_id", g.ChatID), slog.String("outcome", string(receipt.Outcome)), slog.String("detail", receipt.Detail))
		return nil
	}
	return fmt.Errorf("%w: group %d: %s: %s", errAnnouncementFailed, g.ChatID, receipt.Outcome, receipt.Detail)
}

// shownName is a player's display name as a public line shows it: empty for
// the placeholder a record gets when no real name was on hand, so the line
// says "a player" instead of an identifier.
func shownName(p *application.Player) string {
	if p == nil || p.DisplayName == "player-"+strconv.FormatInt(p.TelegramUserID, 10) {
		return ""
	}
	return p.DisplayName
}

// The announcements.

// arrivalAnnouncement: a player arrived in a city (travel.completed).
func arrivalAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	var ev travelCompleted
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("travel.completed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.ToCityID == "" {
		return nil, apperrors.InvalidInput("travel.completed names no player or no city")
	}
	city, err := deps.Cities.ByID(ctx, ev.ToCityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{
		CityID: city.ID, PlayerID: ev.PlayerID,
		Line: func(c screens.Context, name string) string {
			return screens.ArrivalAnnouncement(c, name, city.Code, city.Name)
		},
	}, nil
}

// crimeJailed is the payload CrimeHandler writes when it jails a player.
type crimeJailed struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	CityID     string `json:"city_id"`
}

// jailAnnouncement: a player was jailed in a city (crime.jailed). Why and
// for how long are not said.
func jailAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	var ev crimeJailed
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("crime.jailed payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.CityID == "" {
		return nil, apperrors.InvalidInput("crime.jailed names no player or no city")
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{
		CityID: city.ID, PlayerID: ev.PlayerID, Name: ev.PlayerName,
		Line: func(c screens.Context, name string) string {
			return screens.JailAnnouncement(c, name, city.Code, city.Name)
		},
	}, nil
}

// paymentMade is what a payment's group line reads from bank.payment_received.
type paymentMade struct {
	PayerName string `json:"payer_name"`
	PayeeName string `json:"payee_name"`
	Origin    string `json:"origin_chat_id"`
}

// paymentAnnouncement: in the group a payment was started in, who paid whom.
// Never the amount. A payment started in a private chat announces nothing.
func paymentAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	var ev paymentMade
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("bank.payment_received payload is unreadable").WithCause(err)
	}
	chat, err := strconv.ParseInt(ev.Origin, 10, 64)
	if err != nil || chat >= 0 {
		return nil, nil
	}
	return &Announcement{
		ChatID: chat, Name: ev.PayerName,
		Line: func(c screens.Context, name string) string {
			return screens.PaymentMade(c, screens.PaymentMadeView{PayerName: name, PayeeName: ev.PayeeName})
		},
	}, nil
}
