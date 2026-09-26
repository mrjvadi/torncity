//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// The inbox badge end to end (migrations/0037_notification_inbox), against a
// real Postgres schema: an inbox-mode notice is stored and counted on one
// edited-in-place message, a redelivery never double-counts, an instant kind
// still goes out on its own, opening /inbox marks everything read and clears
// the badge, and the 24h-unread reminder fires exactly once per pile of
// unread items. The gateway itself is not involved: a fake Sender stands in
// for it, exactly the port cmd/notifier hands the real one through.

// fakeTelegramMessage is one message the fake sender is holding, keyed by
// the id it invented for it.
type fakeTelegramMessage struct {
	text string
}

// fakeNoticeSender is notification.Sender, standing in for the gateway: it
// interprets the same Notice contract (Edit, Response.Type, Response.
// MessageID) the real gateway does, without a network or a Bot API in
// between.
type fakeNoticeSender struct {
	messages map[int64]*fakeTelegramMessage
	nextID   int64
	sends    []string // text of every NEW message sent, in order
	edits    []string // text of every edit applied, in order
}

func newFakeNoticeSender() *fakeNoticeSender {
	return &fakeNoticeSender{messages: map[int64]*fakeTelegramMessage{}}
}

func (f *fakeNoticeSender) Send(_ context.Context, _ string, env *envelope.Envelope) (notification.Receipt, error) {
	var notice notification.Notice
	if err := env.Decode(&notice); err != nil {
		return notification.Receipt{}, fmt.Errorf("fake sender: undecodable notice: %w", err)
	}
	resp := notice.Response

	if notice.Edit && resp.Type == presenter.ActionEditMessage && resp.MessageID != 0 {
		msg, ok := f.messages[resp.MessageID]
		if !ok {
			// The gateway would report this as a failure to edit; badge.go
			// is expected to fall back to a fresh send on the caller's next
			// attempt, exactly as it does for a message Telegram lost.
			return notification.Receipt{Outcome: notification.OutcomeFailed, Detail: "message to edit not found"}, nil
		}
		msg.text = resp.Text
		f.edits = append(f.edits, resp.Text)
		return notification.Receipt{Outcome: notification.OutcomeDelivered}, nil
	}

	f.nextID++
	id := f.nextID
	f.messages[id] = &fakeTelegramMessage{text: resp.Text}
	f.sends = append(f.sends, resp.Text)
	return notification.Receipt{Outcome: notification.OutcomeDelivered, MessageID: id}, nil
}

// route finds a Route by domain and event; the render functions themselves
// are package-private to internal/workers/notification, so a test reaches
// them only through the public table, exactly as cmd/notifier does.
func routeFor(t *testing.T, domain, event string) notification.Route {
	t.Helper()
	for _, r := range notification.Routes() {
		if r.Domain == domain && r.Event == event && r.Render != nil {
			return r
		}
	}
	t.Fatalf("no private route for %s.%s", domain, event)
	return notification.Route{}
}

// inboxEventMeta is metadata.Validate's minimum for an event Handle
// consumes, one EventID per call so each is a distinct message the
// consumer-dedup table (inbox_messages) has never seen.
func inboxEventMeta(t *testing.T, domain, event string) envelope.Metadata {
	t.Helper()
	id := randomToken(t, 16)
	return envelope.Metadata{
		RequestID: "req_" + id, TraceID: "trc_" + id, EventID: "evt_" + id,
		Command: domain + "." + event, SchemaVersion: envelope.SchemaVersion,
	}
}

func achievementPayload(t *testing.T, playerID string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"player_id": playerID, "code": "first_million", "name": "First Million"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func paymentPayload(t *testing.T, playerID string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"payee_id": playerID, "payer_name": "A Friend", "method": "cash", "amount": 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// notificationInboxRules classifies achievement.awarded as inbox (its
// domain default in the shipped delivery.yml) and bank.payment_received as
// instant, exactly as configs/notifications/delivery.yml ships them; loaded
// for real so the test proves the shipped file, not a hand-rolled stand-in.
func notificationInboxRules(t *testing.T) *notification.DeliveryModes {
	t.Helper()
	d, err := notification.LoadDeliveryModes("../configs/notifications/delivery.yml")
	if err != nil {
		t.Fatalf("loading the shipped delivery modes: %v", err)
	}
	return d
}

func TestNotificationInboxBadge(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)

	botID := insertBot(t, pool)
	player := insertPlayer(t, pool)

	players := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	if err := players.LinkBot(ctx, application.BotLink{
		PlayerID: player.ID, BotID: botID, TelegramChatID: newTelegramUserID(t), IsReachable: true,
	}); err != nil {
		t.Fatalf("linking the player's bot: %v", err)
	}

	catalog, err := i18n.Load("../configs/locales")
	if err != nil {
		t.Fatalf("loading the message catalogue: %v", err)
	}

	sender := newFakeNoticeSender()
	playerInbox := postgres.NewPlayerInboxRepository(pool)
	clock := &testClock{now: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}

	worker, err := notification.New(notification.Config{
		Msgs:          i18n.NewStore(catalog),
		Players:       players,
		Links:         players,
		Inbox:         postgres.NewInboxStore(pool),
		Sender:        sender,
		Deps:          notification.Deps{Cities: postgres.NewCityRepository(pool)},
		SendBudget:    15 * time.Second,
		ReceiptMargin: 3 * time.Second,
		Now:           clock.Now,

		PlayerInbox:   playerInbox,
		DeliveryModes: notificationInboxRules(t),
		// No throttle: the three events below arrive back to back on the
		// same fake clock tick, and the test wants every one of them to
		// reach the badge so it can assert the final count, not the
		// throttle's own timing (badge.go's coalescing is exercised by the
		// notifier's unit tests instead).
		EditThrottle: 0,
	})
	if err != nil {
		t.Fatalf("building the worker: %v", err)
	}

	achievement := routeFor(t, "achievement", "awarded")

	handle := func(route notification.Route, meta envelope.Metadata, payload []byte) {
		t.Helper()
		env, err := envelope.New(meta, json.RawMessage(payload))
		if err != nil {
			t.Fatalf("building the envelope: %v", err)
		}
		if err := worker.Handle(ctx, route, env); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	badgeRow := func() application.InboxBadge {
		t.Helper()
		b, err := playerInbox.Badge(ctx, player.ID)
		if err != nil {
			t.Fatalf("reading the badge: %v", err)
		}
		if b == nil {
			t.Fatal("no badge row exists")
		}
		return *b
	}

	unreadCount := func() int {
		t.Helper()
		var n int
		if err := pool.Raw().QueryRow(ctx,
			`SELECT count(*) FROM player_notifications WHERE player_id = $1::uuid AND read_at IS NULL`, player.ID).
			Scan(&n); err != nil {
			t.Fatalf("counting unread notifications: %v", err)
		}
		return n
	}

	// --- three inbox events give one badge with count 3, no extra messages ---
	firstMeta := inboxEventMeta(t, "achievement", "awarded")
	handle(achievement, firstMeta, achievementPayload(t, player.ID))
	handle(achievement, inboxEventMeta(t, "achievement", "awarded"), achievementPayload(t, player.ID))
	handle(achievement, inboxEventMeta(t, "achievement", "awarded"), achievementPayload(t, player.ID))

	if got := unreadCount(); got != 3 {
		t.Fatalf("unread notifications = %d, want 3", got)
	}
	badge := badgeRow()
	if badge.UnreadCount != 3 {
		t.Fatalf("badge unread_count = %d, want 3", badge.UnreadCount)
	}
	if len(sender.sends) != 1 {
		t.Fatalf("badge messages SENT = %d, want exactly 1 (the rest must be edits of it): sends=%v edits=%v",
			len(sender.sends), sender.sends, sender.edits)
	}
	firstBadgeMessageID := badge.TelegramMessageID
	if firstBadgeMessageID == 0 {
		t.Fatal("badge has no telegram_message_id")
	}

	// --- redelivery of an already-processed event never double-counts ---
	handle(achievement, firstMeta, achievementPayload(t, player.ID))
	if got := unreadCount(); got != 3 {
		t.Fatalf("after a redelivery, unread notifications = %d, want still 3", got)
	}
	if len(sender.sends) != 1 {
		t.Fatalf("a redelivery sent a new badge message: sends=%v", sender.sends)
	}

	// --- an instant kind goes out immediately, as its own message ---
	payment := routeFor(t, "bank", "payment_received")
	sendsBefore := len(sender.sends)
	handle(payment, inboxEventMeta(t, "bank", "payment_received"), paymentPayload(t, player.ID))
	if len(sender.sends) != sendsBefore+1 {
		t.Fatalf("an instant notice did not send its own message: sends=%v", sender.sends)
	}
	var instantCategory string
	var instantReadAt *time.Time
	if err := pool.Raw().QueryRow(ctx,
		`SELECT category, read_at FROM player_notifications WHERE player_id = $1::uuid AND kind = 'bank.payment_received'`,
		player.ID).Scan(&instantCategory, &instantReadAt); err != nil {
		t.Fatalf("reading the archived instant notification: %v", err)
	}
	if instantReadAt == nil {
		t.Error("an instant notice's archived row must already be marked read")
	}
	if instantCategory != "finance" {
		t.Errorf("instant notice category = %q, want finance", instantCategory)
	}

	// --- opening /inbox marks everything read and clears the badge ---
	inboxHandler := handlers.NewInboxHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage),
		i18n.NewStore(catalog), handlers.InboxRules{PageSize: 5}, clock.Now)
	meta := envelope.Metadata{
		RequestID: "req_" + randomToken(t, 16), TraceID: "trc_" + randomToken(t, 16),
		Command: "inbox.show", SchemaVersion: envelope.SchemaVersion,
		TelegramUserID: player.TelegramUserID, TelegramChatID: player.TelegramUserID, ChatType: "private",
	}
	resp, err := inboxHandler.Show(ctx, meta)
	if err != nil {
		t.Fatalf("inbox.show: %v", err)
	}
	if resp == nil {
		t.Fatal("inbox.show returned no response")
	}
	if got := unreadCount(); got != 0 {
		t.Fatalf("after opening /inbox, unread notifications = %d, want 0", got)
	}
	cleared := badgeRow()
	if cleared.UnreadCount != 0 || cleared.TelegramMessageID != 0 {
		t.Fatalf("badge after opening /inbox = %+v, want cleared", cleared)
	}

	// --- the reminder fires once, 24h after the next item, on a fake clock ---
	handle(achievement, inboxEventMeta(t, "achievement", "awarded"), achievementPayload(t, player.ID))
	freshBadge := badgeRow()
	if freshBadge.TelegramMessageID == 0 || freshBadge.TelegramMessageID == firstBadgeMessageID {
		t.Fatalf("a fresh badge was not sent after the previous one was cleared: %+v", freshBadge)
	}
	sendsBeforeReminder := len(sender.sends)

	clock.Advance(24*time.Hour + time.Minute)
	worker.SendDueReminders(ctx, 24*time.Hour, 100)
	if len(sender.sends) != sendsBeforeReminder+1 {
		t.Fatalf("the 24h reminder did not send: sends=%v", sender.sends)
	}

	// A second poll, nothing new having arrived, must not remind again.
	worker.SendDueReminders(ctx, 24*time.Hour, 100)
	if len(sender.sends) != sendsBeforeReminder+1 {
		t.Fatalf("the reminder fired a second time for the same pile of unread items: sends=%v", sender.sends)
	}
}
