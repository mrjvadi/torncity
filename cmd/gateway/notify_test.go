package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/gateway/ratelimit"
	"github.com/mrjvadi/torncity/internal/gateway/registry"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// Placeholders shaped like tokens, as in internal/gateway/registry's tests.
const (
	noticeTokenBot01 = "000000001:PLACEHOLDER_NOT_A_REAL_TOKEN_BOT01"
	noticeTokenBot02 = "000000002:PLACEHOLDER_NOT_A_REAL_TOKEN_BOT02"
)

type fixedFleet struct{ bots []application.Bot }

func (f fixedFleet) ListEnabled(context.Context) ([]application.Bot, error) { return f.bots, nil }

type fixedSecrets map[string]string

func (s fixedSecrets) Resolve(ref string) (string, error) { return s[ref], nil }

// botAPICall is one request the fake Bot API received.
type botAPICall struct {
	token  string
	method string
	chatID int64
	text   string
}

// fakeBotAPI stands in for the Bot API server. answer decides each reply; nil
// answers ok.
type fakeBotAPI struct {
	mu     sync.Mutex
	calls  []botAPICall
	answer func(n int) (status int, body string)
}

func (f *fakeBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /bot<token>/<method>
	path := strings.TrimPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(path, "/")
	var body struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	f.calls = append(f.calls, botAPICall{token: token, method: method, chatID: body.ChatID, text: body.Text})
	n := len(f.calls)
	f.mu.Unlock()

	status, reply := http.StatusOK, `{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`
	if f.answer != nil {
		status, reply = f.answer(n)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, reply)
}

func (f *fakeBotAPI) recorded() []botAPICall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]botAPICall(nil), f.calls...)
}

// noticeGateway is a gateway with a real fleet, limiter and send path, whose
// Bot API is api.
func noticeGateway(t *testing.T, api *fakeBotAPI) *gateway {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	fleet, err := registry.New(registry.Config{
		Source: fixedFleet{bots: []application.Bot{
			{ID: "bot-id-1", BotKey: "bot01", TokenSecretRef: "telegram/bot01", Enabled: true, RateLimit: 30},
			{ID: "bot-id-2", BotKey: "bot02", TokenSecretRef: "telegram/bot02", Enabled: true, RateLimit: 30},
		}},
		Secrets:          fixedSecrets{"telegram/bot01": noticeTokenBot01, "telegram/bot02": noticeTokenBot02},
		BaseURL:          srv.URL,
		RequestTimeout:   2 * time.Second,
		MaxPollTimeout:   30 * time.Second,
		PollTimeoutGrace: 5 * time.Second,
		PollHTTPTimeout:  35 * time.Second,
		DefaultFloodWait: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fleet.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Gateway.SendAttempts = 3
	cfg.Gateway.ShutdownTimeout = 5 * time.Second

	return &gateway{
		cfg:        cfg,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		fleet:      fleet,
		limiter:    ratelimit.New(ratelimit.Config{Rate: 100, Burst: 100}),
		lanes:      newPriorityLanes(),
		botKeyByID: map[string]string{"bot-id-1": "bot01", "bot-id-2": "bot02"},
	}
}

// noticeData is what cmd/notifier sends: an envelope addressed to a bot and a
// chat, around a Notice.
func noticeData(t *testing.T, botID string, chatID int64, deliverBy time.Time, resp presenter.Response) []byte {
	t.Helper()
	env, err := envelope.New(envelope.Metadata{
		RequestID:      "req-1",
		TraceID:        "trace-1",
		Command:        "travel.arrive",
		PlayerID:       "player-1",
		BotID:          botID,
		TelegramChatID: chatID,
		Language:       "en",
		SchemaVersion:  envelope.SchemaVersion,
	}, notification.Notice{DeliverBy: deliverBy, Response: resp})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func arrival() presenter.Response {
	return *presenter.Message("You have arrived in Calderis.", nil)
}

// The notice goes out through the bot it names — bot02's token, not bot01's —
// to the chat it names, and the notifier is told it was delivered.
func TestNoticeIsSentThroughTheNamedBot(t *testing.T) {
	api := &fakeBotAPI{}
	g := noticeGateway(t, api)

	receipt := g.deliverNotice(noticeData(t, "bot-id-2", 5005, time.Now().Add(5*time.Second), arrival()))

	if receipt.Outcome != notification.OutcomeDelivered {
		t.Fatalf("receipt %+v, want delivered", receipt)
	}
	calls := api.recorded()
	if len(calls) != 1 {
		t.Fatalf("%d Bot API calls, want 1", len(calls))
	}
	c := calls[0]
	if c.token != noticeTokenBot02 || c.method != "sendMessage" || c.chatID != 5005 {
		t.Errorf("sent %s to chat %d through the wrong bot (token for bot01: %v)",
			c.method, c.chatID, c.token == noticeTokenBot01)
	}
	if c.text != "You have arrived in Calderis." {
		t.Errorf("sent %q", c.text)
	}
}

// A notice never edits, whatever its screen says.
func TestNoticeAlwaysSendsANewMessage(t *testing.T) {
	api := &fakeBotAPI{}
	g := noticeGateway(t, api)
	resp := arrival()
	resp.Type = presenter.ActionEditMessage
	resp.MessageID = 99

	if r := g.deliverNotice(noticeData(t, "bot-id-1", 1, time.Now().Add(5*time.Second), resp)); r.Outcome != notification.OutcomeDelivered {
		t.Fatalf("receipt %+v", r)
	}
	if calls := api.recorded(); len(calls) != 1 || calls[0].method != "sendMessage" {
		t.Errorf("calls %+v, want one sendMessage", calls)
	}
}

// A bot the player blocked is reported unreachable, so the notifier can mark
// the link and try another bot.
func TestBlockedBotIsReportedUnreachable(t *testing.T) {
	for name, answer := range map[string]string{
		"blocked":        `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`,
		"chat not found": `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`,
	} {
		t.Run(name, func(t *testing.T) {
			api := &fakeBotAPI{answer: func(int) (int, string) { return http.StatusOK, answer }}
			g := noticeGateway(t, api)

			r := g.deliverNotice(noticeData(t, "bot-id-1", 1, time.Now().Add(5*time.Second), arrival()))
			if r.Outcome != notification.OutcomeUnreachable {
				t.Errorf("receipt %+v, want unreachable", r)
			}
			if len(api.recorded()) != 1 {
				t.Errorf("a permanent refusal was retried %d times", len(api.recorded())-1)
			}
			if strings.Contains(r.Detail, noticeTokenBot01) {
				t.Error("the receipt carries a token")
			}
		})
	}
}

// A flood wait parks the bot and the notice goes out after it, still inside
// its deadline.
func TestNoticeWaitsOutAFloodWait(t *testing.T) {
	api := &fakeBotAPI{answer: func(n int) (int, string) {
		if n == 1 {
			return http.StatusTooManyRequests,
				`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`
		}
		return http.StatusOK, `{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`
	}}
	g := noticeGateway(t, api)

	r := g.deliverNotice(noticeData(t, "bot-id-1", 1, time.Now().Add(5*time.Second), arrival()))
	if r.Outcome != notification.OutcomeDelivered {
		t.Fatalf("receipt %+v, want delivered after the wait", r)
	}
	if n := len(api.recorded()); n != 2 {
		t.Errorf("%d calls, want 2", n)
	}
}

// A notice whose deadline passed is not sent: the notifier has stopped
// waiting and will retry the event, and sending now would be a duplicate.
func TestExpiredNoticeIsNotSent(t *testing.T) {
	api := &fakeBotAPI{}
	g := noticeGateway(t, api)

	r := g.deliverNotice(noticeData(t, "bot-id-1", 1, time.Now().Add(-time.Second), arrival()))
	if r.Outcome != notification.OutcomeExpired {
		t.Errorf("receipt %+v, want expired", r)
	}
	if len(api.recorded()) != 0 {
		t.Error("an expired notice was sent")
	}
}

// A bot this gateway does not serve is reported, not guessed at.
func TestNoticeForAnUnservedBot(t *testing.T) {
	api := &fakeBotAPI{}
	g := noticeGateway(t, api)

	r := g.deliverNotice(noticeData(t, "bot-id-9", 1, time.Now().Add(5*time.Second), arrival()))
	if r.Outcome != notification.OutcomeUnknownBot {
		t.Errorf("receipt %+v, want unknown_bot", r)
	}
	if len(api.recorded()) != 0 {
		t.Error("a notice for an unserved bot was sent through another bot")
	}
}

// Garbage on the subject is answered, not crashed on.
func TestMalformedNoticeIsAnsweredAsFailed(t *testing.T) {
	g := noticeGateway(t, &fakeBotAPI{})
	if r := g.deliverNotice([]byte("not json")); r.Outcome != notification.OutcomeFailed {
		t.Errorf("receipt %+v, want failed", r)
	}
	// Published without a reply subject: nothing to answer, nothing to crash.
	g.onNotice(&natsgo.Msg{Data: []byte("not json")})
}

// Section 15: a notice waits while a direct reply is in progress on the same
// bot, and only on that bot.
func TestNoticeYieldsToDirectRepliesOnTheSameBot(t *testing.T) {
	lanes := newPriorityLanes()
	release := lanes.enterDirect("bot01")

	// Another bot is not held up.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lanes.yield(ctx, "bot02"); err != nil {
		t.Fatalf("a notice on bot02 waited for bot01's reply: %v", err)
	}

	yielded := make(chan error, 1)
	go func() { yielded <- lanes.yield(ctx, "bot01") }()

	select {
	case err := <-yielded:
		t.Fatalf("the notice went ahead of the direct reply (err %v)", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()
	release() // releasing twice is harmless
	select {
	case err := <-yielded:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("the notice was never let through")
	}

	// A nil lane set, as in a gateway built without one, never blocks.
	var none *priorityLanes
	none.enterDirect("bot01")()
	if err := none.yield(ctx, "bot01"); err != nil {
		t.Fatal(err)
	}
}

// A notice stuck behind replies gives up at its deadline instead of waiting
// forever.
func TestNoticeBehindRepliesGivesUpAtItsDeadline(t *testing.T) {
	api := &fakeBotAPI{}
	g := noticeGateway(t, api)
	defer g.lanes.enterDirect("bot01")()

	r := g.deliverNotice(noticeData(t, "bot-id-1", 1, time.Now().Add(100*time.Millisecond), arrival()))
	if r.Outcome != notification.OutcomeExpired {
		t.Errorf("receipt %+v, want expired", r)
	}
	if len(api.recorded()) != 0 {
		t.Error("the notice was sent ahead of a direct reply")
	}
}
