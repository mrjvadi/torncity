package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// noticeQueue is the queue group every gateway instance joins for notices.
// Any instance can send through any bot in the fleet, exactly as for
// responses; the group is what stops all of them sending the same notice.
const noticeQueue = "gateway-notice-senders"

// onNotice sends one notification and answers the notifier with a receipt.
//
// A notice is a request from cmd/notifier (internal/workers/notification):
// the envelope names the bot and the chat, the payload says what to send and
// until when. The gateway decides nothing about the player; it sends through
// the bot it was told to, paced like every other message from that bot but
// behind any direct reply waiting for it, and reports what Telegram said.
func (g *gateway) onNotice(msg *natsgo.Msg) {
	g.inflight.Add(1)
	defer g.inflight.Done()

	receipt := g.deliverNotice(msg.Data)
	if msg.Reply == "" {
		// Nobody is waiting for the answer. The send has happened or not
		// either way; there is nothing to report to.
		return
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		g.logger.Error("cannot encode a notice receipt", slog.String("error", err.Error()))
		return
	}
	if err := msg.Respond(data); err != nil {
		g.logger.Warn("cannot answer the notifier", slog.String("error", err.Error()))
	}
}

// deliverNotice does the work of onNotice and returns what to answer.
func (g *gateway) deliverNotice(data []byte) notification.Receipt {
	var env envelope.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		g.logger.Error("notice is not decodable", slog.String("error", err.Error()))
		return notification.Receipt{Outcome: notification.OutcomeFailed, Detail: "notice is not decodable"}
	}
	meta := env.Metadata
	log := g.logger.With(metaAttrs(meta)...).With(slog.String("player_id", meta.PlayerID))

	var notice notification.Notice
	if err := env.Decode(&notice); err != nil || meta.TelegramChatID == 0 {
		log.Error("notice names no chat or carries no screen")
		return notification.Receipt{Outcome: notification.OutcomeFailed, Detail: "malformed notice"}
	}

	botKey, ok := g.botKeyByID[meta.BotID]
	if !ok {
		// Unlike a response, a notice is not dropped here: the notifier
		// knows the player's other bots and tries the next one.
		log.Warn("notice names a bot this gateway does not serve")
		return notification.Receipt{Outcome: notification.OutcomeUnknownBot}
	}

	// The notifier stops waiting shortly after DeliverBy and will retry the
	// event; a send started later would be a duplicate of that retry. So the
	// deadline bounds everything, the flood waits and the queueing behind
	// direct replies included. A notice without one gets the ceiling a
	// response gets.
	deadline := notice.DeliverBy
	if deadline.IsZero() {
		deadline = time.Now().Add(g.cfg.Gateway.ShutdownTimeout)
	}
	if !time.Now().Before(deadline) {
		log.Warn("notice arrived after its deadline; not sending")
		return notification.Receipt{Outcome: notification.OutcomeExpired, Detail: "deadline passed before sending"}
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	// A notice always sends a new message. The screen already says so; this
	// makes sure a malformed one cannot edit a message the player is reading.
	resp := notice.Response
	resp.Type = presenter.ActionSendMessage
	resp.MessageID = 0

	priority := laneNotice
	if notice.Announcement {
		// A public line for a group: posted in the room as it stands.
		priority = laneAnnounce
		resp.Keyboard = nil
	}
	err := g.sendNoticeViaFleet(ctx, botKey, meta, &resp, priority, log)
	receipt := noticeReceipt(ctx, err)

	switch receipt.Outcome {
	case notification.OutcomeDelivered:
		log.Info("notice delivered", slog.String("bot_key", botKey))
	case notification.OutcomeUnreachable:
		log.Info("the player cannot be reached through this bot",
			slog.String("bot_key", botKey), slog.String("detail", receipt.Detail))
	default:
		log.Warn("notice not delivered", slog.String("bot_key", botKey),
			slog.String("outcome", string(receipt.Outcome)), slog.String("error", receipt.Detail))
	}
	return receipt
}

// sendNoticeViaFleet sends a notice through the named bot at notice priority.
func (g *gateway) sendNoticeViaFleet(ctx context.Context, botKey string, meta envelope.Metadata, resp *presenter.Response, priority lane, log *slog.Logger) error {
	api, err := g.fleet.ClientFor(botKey)
	if err != nil {
		return err
	}
	return g.send(ctx, api, botKey, meta, resp, priority, log)
}

// noticeReceipt classifies the result of one send.
func noticeReceipt(ctx context.Context, err error) notification.Receipt {
	if err == nil {
		return notification.Receipt{Outcome: notification.OutcomeDelivered}
	}
	if isUnreachable(err) {
		return notification.Receipt{Outcome: notification.OutcomeUnreachable, Detail: err.Error()}
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return notification.Receipt{Outcome: notification.OutcomeExpired, Detail: err.Error()}
	}
	return notification.Receipt{Outcome: notification.OutcomeFailed, Detail: err.Error()}
}

// isUnreachable reports Telegram's permanent refusals to deliver to a chat:
// 403 is a bot blocked by the user or an account that is gone, and "chat not
// found" is a chat this bot never had or no longer has. Neither clears with a
// retry through the same bot.
func isUnreachable(err error) bool {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == 403 ||
		(apiErr.Code == 400 && strings.Contains(strings.ToLower(apiErr.Description), "chat not found"))
}

// lane is a send's priority (MASTER_PROMPT section 15). Only the first two
// levels exist so far.
type lane int

const (
	// laneDirect is P0: the answer to something the player just did.
	laneDirect lane = iota
	// laneNotice is P1: something the player did not ask for just now.
	laneNotice
	// laneAnnounce is P1 too: a public line for a group chat (a player
	// arrived in the city), posted in the group itself.
	laneAnnounce
)

// priorityLanes keeps notices behind direct replies, per bot.
//
// A direct send registers itself for as long as it waits and sends; a notice
// waits, before every attempt, until no direct send is registered for the
// same bot. The rate limiter stays one bucket per bot, so the two lanes share
// the bot's pace and a burst of notices can never push that bot over its
// limit — they only ever take the tokens direct replies leave. Other bots are
// unaffected, as with flood waits.
//
// It is not preemptive: a notice already past the check keeps its token, so a
// direct reply arriving that instant waits one token longer. That is a
// fraction of a second, and the alternative is cancelling a send mid-flight.
type priorityLanes struct {
	mu sync.Mutex
	// direct counts the direct sends in progress per bot key.
	direct map[string]int
	// clear is closed when a bot's count returns to zero.
	clear map[string]chan struct{}
}

func newPriorityLanes() *priorityLanes {
	return &priorityLanes{direct: make(map[string]int), clear: make(map[string]chan struct{})}
}

// enterDirect registers a direct send on botKey and returns its release.
// A nil receiver does nothing, so a gateway built without lanes still sends.
func (p *priorityLanes) enterDirect(botKey string) func() {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	if p.direct[botKey] == 0 {
		p.clear[botKey] = make(chan struct{})
	}
	p.direct[botKey]++
	p.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.direct[botKey]--
			if p.direct[botKey] == 0 {
				close(p.clear[botKey])
				delete(p.clear, botKey)
				delete(p.direct, botKey)
			}
		})
	}
}

// yield blocks until no direct send is registered on botKey, or ctx ends.
func (p *priorityLanes) yield(ctx context.Context, botKey string) error {
	if p == nil {
		return nil
	}
	for {
		p.mu.Lock()
		ch := p.clear[botKey]
		p.mu.Unlock()
		if ch == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}
