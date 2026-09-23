package notification

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// --- fakes -------------------------------------------------------------

const (
	playerID = "4d0e4d7c-5d3a-4c55-9a53-2b8c0c3f1a11"
	cityID   = "9b0c1c5e-0f5e-4a8e-8f49-6f2f3f3c2b22"
	botA     = "a1a1a1a1-0000-4000-8000-000000000001"
	botB     = "b2b2b2b2-0000-4000-8000-000000000002"
)

type fakePlayers struct{ player *application.Player }

func (f fakePlayers) GetByID(_ context.Context, id string) (*application.Player, error) {
	if f.player == nil || f.player.ID != id {
		return nil, application.ErrPlayerNotFound
	}
	return f.player, nil
}

type fakeCities struct{}

func (fakeCities) ByID(_ context.Context, id string) (*application.City, error) {
	if id != cityID {
		return nil, application.ErrCityNotFound
	}
	return &application.City{ID: cityID, Code: "calderis", Name: "Calderis"}, nil
}

type fakeLinks struct {
	links       []application.BotLink
	unreachable []string
	err         error
}

func (f *fakeLinks) ReachableBotLinks(_ context.Context, id string) ([]application.BotLink, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []application.BotLink
	for _, l := range f.links {
		if l.PlayerID == id && l.IsReachable {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeLinks) MarkBotUnreachable(_ context.Context, pid, bid string) error {
	f.unreachable = append(f.unreachable, bid)
	for i := range f.links {
		if f.links[i].PlayerID == pid && f.links[i].BotID == bid {
			f.links[i].IsReachable = false
		}
	}
	return nil
}

type fakeInbox struct{ done map[string]bool }

func (f *fakeInbox) Processed(_ context.Context, id, consumer string) (bool, error) {
	return f.done[id+"|"+consumer], nil
}

func (f *fakeInbox) MarkProcessed(_ context.Context, id, consumer string) (bool, error) {
	if f.done == nil {
		f.done = map[string]bool{}
	}
	fresh := !f.done[id+"|"+consumer]
	f.done[id+"|"+consumer] = true
	return fresh, nil
}

type sent struct {
	subject string
	meta    envelope.Metadata
	notice  Notice
}

// fakeSender answers with the scripted outcome for the bot a notice names,
// delivered by default, or with err when set.
type fakeSender struct {
	outcomes  map[string]Outcome
	err       error
	sent      []sent
	delivered int
}

func (f *fakeSender) Send(_ context.Context, subject string, env *envelope.Envelope) (Receipt, error) {
	var n Notice
	if err := env.Decode(&n); err != nil {
		return Receipt{}, err
	}
	f.sent = append(f.sent, sent{subject: subject, meta: env.Metadata, notice: n})
	if f.err != nil {
		return Receipt{}, f.err
	}
	if o, ok := f.outcomes[env.Metadata.BotID]; ok {
		return Receipt{Outcome: o}, nil
	}
	f.delivered++
	return Receipt{Outcome: OutcomeDelivered}, nil
}

type rig struct {
	w      *Worker
	links  *fakeLinks
	inbox  *fakeInbox
	sender *fakeSender
	msgs   *i18n.Catalog
	now    time.Time
}

func newRig(t *testing.T, player *application.Player, links ...application.BotLink) *rig {
	t.Helper()
	msgs, err := i18n.Load("../../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{
		links:  &fakeLinks{links: links},
		inbox:  &fakeInbox{},
		sender: &fakeSender{outcomes: map[string]Outcome{}},
		msgs:   msgs,
		now:    time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
	}
	r.w, err = New(Config{
		Msgs:          msgs,
		Players:       fakePlayers{player: player},
		Links:         r.links,
		Inbox:         r.inbox,
		Sender:        r.sender,
		Deps:          Deps{Cities: fakeCities{}},
		SendBudget:    10 * time.Second,
		ReceiptMargin: 2 * time.Second,
		MaxAge:        24 * time.Hour,
		Now:           func() time.Time { return r.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func travelRoute(t *testing.T) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == "travel" && r.Event == "completed" {
			return r
		}
	}
	t.Fatal("no travel.completed route")
	return Route{}
}

// arrivalEvent is the envelope the outbox worker publishes for a landed
// journey: the scheduler's metadata (no bot, no chat, the default language)
// around TravelHandler.Complete's payload.
func arrivalEvent(t *testing.T, requestID string, receivedAt time.Time, payload map[string]any) *envelope.Envelope {
	t.Helper()
	if payload == nil {
		payload = map[string]any{
			"travel_id":  "5c5c5c5c-0000-4000-8000-000000000005",
			"player_id":  playerID,
			"to_city_id": cityID,
			"xp":         25,
			"levels":     []int{3},
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &envelope.Envelope{
		Metadata: envelope.Metadata{
			RequestID:     requestID,
			TraceID:       "trace-" + requestID,
			Command:       "travel.arrive",
			Language:      "fa",
			ReceivedAt:    receivedAt,
			SchemaVersion: envelope.SchemaVersion,
		},
		Payload: raw,
	}
}

func link(bot string, chat int64) application.BotLink {
	return application.BotLink{PlayerID: playerID, BotID: bot, TelegramChatID: chat, IsReachable: true}
}

func englishPlayer() *application.Player {
	return &application.Player{ID: playerID, TelegramUserID: 777, Language: "en"}
}

// --- tests -------------------------------------------------------------

// The event reaches the right player, through the most recently seen bot, in
// the language the player chose rather than the scheduler's default.
func TestArrivalIsSentToThePlayerThroughTheFirstLinkInTheirLanguage(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botB, 2002), link(botA, 1001))
	route := travelRoute(t)

	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-1", r.now.Add(-time.Minute), nil)); err != nil {
		t.Fatal(err)
	}

	if len(r.sender.sent) != 1 {
		t.Fatalf("sent %d notices, want 1", len(r.sender.sent))
	}
	s := r.sender.sent[0]
	if want := "game.notify." + playerID + ".v1"; s.subject != want {
		t.Errorf("subject %q, want %q", s.subject, want)
	}
	if s.meta.BotID != botB || s.meta.TelegramChatID != 2002 {
		t.Errorf("sent through bot %s chat %d, want %s chat 2002", s.meta.BotID, s.meta.TelegramChatID, botB)
	}
	if s.meta.PlayerID != playerID || s.meta.TelegramUserID != 777 || s.meta.Language != "en" {
		t.Errorf("notice metadata %+v does not name the English-speaking player", s.meta)
	}
	if s.meta.TraceID != "trace-req-1" {
		t.Errorf("trace id %q, want the event's", s.meta.TraceID)
	}
	if s.meta.CallbackQueryID != nil || s.notice.Response.MessageID != 0 {
		t.Error("a notice must send, never edit or answer")
	}
	arrived := r.msgs.T("en", "travel.arrived", map[string]any{"city": r.msgs.T("en", "city.calderis", nil)})
	if !strings.Contains(s.notice.Response.Text, arrived) {
		t.Errorf("notice is not the English arrival in Calderis:\n%s", s.notice.Response.Text)
	}
	// The send budget less the receipt margin: the gateway may start a send
	// until then, and the rest of the budget is the receipt's way back.
	if want := r.now.Add(10*time.Second - 2*time.Second); !s.notice.DeliverBy.Equal(want) {
		t.Errorf("deliver_by %v, want %v (budget less the receipt margin)", s.notice.DeliverBy, want)
	}
	if !r.inbox.done["req-1|"+route.Durable()] {
		t.Error("the delivered event was not recorded in the inbox")
	}
}

// With no stored language, the event's language is used.
func TestArrivalFallsBackToTheEventLanguage(t *testing.T) {
	r := newRig(t, &application.Player{ID: playerID, TelegramUserID: 777}, link(botA, 1001))
	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.sent) != 1 || r.sender.sent[0].meta.Language != "fa" {
		t.Fatalf("notice was not written in the event's language: %+v", r.sender.sent)
	}
}

// JetStream delivers at least once: a redelivered event is not sent again.
func TestRedeliveredEventIsSentOnce(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	route := travelRoute(t)
	ev := arrivalEvent(t, "req-1", r.now, nil)

	for i := 0; i < 3; i++ {
		if err := r.w.Handle(context.Background(), route, ev); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if len(r.sender.sent) != 1 {
		t.Errorf("sent %d notices for one event, want 1", len(r.sender.sent))
	}
}

// A send nobody confirmed is retried, and the inbox is written only once it
// is confirmed: recording first would lose the notification.
func TestUnconfirmedSendIsRetriedAndRecordedOnlyAfterDelivery(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	route := travelRoute(t)
	ev := arrivalEvent(t, "req-1", r.now, nil)

	r.sender.err = errors.New("nats: no responders available for request")
	if err := r.w.Handle(context.Background(), route, ev); err == nil {
		t.Fatal("an unconfirmed send was acknowledged")
	}
	if r.inbox.done["req-1|"+route.Durable()] {
		t.Fatal("the inbox was written before delivery")
	}

	r.sender.err = nil
	if err := r.w.Handle(context.Background(), route, ev); err != nil {
		t.Fatal(err)
	}
	if err := r.w.Handle(context.Background(), route, ev); err != nil {
		t.Fatal(err)
	}
	if got := r.sender.delivered; got != 1 {
		t.Errorf("delivered %d times, want 1", got)
	}
}

// A failure the gateway reports (flood wait past the deadline, Bot API down)
// is retried, not dropped, and no second bot is tried meanwhile.
func TestGatewayFailureIsRetried(t *testing.T) {
	for _, o := range []Outcome{OutcomeFailed, OutcomeExpired} {
		t.Run(string(o), func(t *testing.T) {
			r := newRig(t, englishPlayer(), link(botA, 1001), link(botB, 2002))
			r.sender.outcomes[botA] = o
			if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err == nil {
				t.Fatal("a failed send was acknowledged")
			}
			if len(r.sender.sent) != 1 {
				t.Errorf("tried %d bots, want only the first", len(r.sender.sent))
			}
			if len(r.links.unreachable) != 0 {
				t.Errorf("a transient failure marked %v unreachable", r.links.unreachable)
			}
		})
	}
}

// A blocked bot is marked unreachable and the next link carries the notice.
func TestBlockedBotIsMarkedAndTheNextLinkIsUsed(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001), link(botB, 2002))
	r.sender.outcomes[botA] = OutcomeUnreachable

	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.links.unreachable) != 1 || r.links.unreachable[0] != botA {
		t.Errorf("marked %v unreachable, want [%s]", r.links.unreachable, botA)
	}
	if len(r.sender.sent) != 2 || r.sender.sent[1].meta.BotID != botB || r.sender.sent[1].meta.TelegramChatID != 2002 {
		t.Fatalf("the second link was not used: %+v", r.sender.sent)
	}
}

// A bot the gateway does not serve is skipped but not marked: the player did
// not block it.
func TestUnservedBotIsSkippedWithoutMarking(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001), link(botB, 2002))
	r.sender.outcomes[botA] = OutcomeUnknownBot

	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.links.unreachable) != 0 {
		t.Errorf("marked %v unreachable", r.links.unreachable)
	}
	if len(r.sender.sent) != 2 {
		t.Errorf("tried %d bots, want 2", len(r.sender.sent))
	}
}

// Nobody to tell is not an error: no link, every link blocked, no player, a
// payload that cannot be read or an event from long ago are all acknowledged
// without a crash and without a send.
func TestUndeliverableEventsAreAcknowledged(t *testing.T) {
	tests := map[string]struct {
		player *application.Player
		links  []application.BotLink
		block  []string
		ev     func(r *rig) *envelope.Envelope
		sends  int
	}{
		"no link": {player: englishPlayer()},
		"every link blocked": {
			player: englishPlayer(), links: []application.BotLink{link(botA, 1), link(botB, 2)},
			block: []string{botA, botB}, sends: 2,
		},
		"no player": {links: []application.BotLink{link(botA, 1)}},
		"unreadable payload": {
			player: englishPlayer(), links: []application.BotLink{link(botA, 1)},
			ev: func(r *rig) *envelope.Envelope {
				e := arrivalEvent(t, "req-1", r.now, nil)
				e.Payload = json.RawMessage(`"not an object"`)
				return e
			},
		},
		"unknown city": {
			player: englishPlayer(), links: []application.BotLink{link(botA, 1)},
			ev: func(r *rig) *envelope.Envelope {
				return arrivalEvent(t, "req-1", r.now, map[string]any{"player_id": playerID, "to_city_id": "gone"})
			},
		},
		"too old": {
			player: englishPlayer(), links: []application.BotLink{link(botA, 1)},
			ev: func(r *rig) *envelope.Envelope { return arrivalEvent(t, "req-1", r.now.Add(-25*time.Hour), nil) },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, tt.player, tt.links...)
			for _, b := range tt.block {
				r.sender.outcomes[b] = OutcomeUnreachable
			}
			ev := arrivalEvent(t, "req-1", r.now, nil)
			if tt.ev != nil {
				ev = tt.ev(r)
			}
			if err := r.w.Handle(context.Background(), travelRoute(t), ev); err != nil {
				t.Fatalf("an undeliverable event was sent back for redelivery: %v", err)
			}
			if len(r.sender.sent) != tt.sends {
				t.Errorf("sent %d notices, want %d", len(r.sender.sent), tt.sends)
			}
		})
	}
}

// A database that cannot be read is retried rather than taken for "no links".
func TestLinkReadFailureIsRetried(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1))
	r.links.err = errors.New("connection refused")
	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err == nil {
		t.Fatal("a failed read was acknowledged")
	}
}

var (
	uuidPattern   = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	internalWords = regexp.MustCompile(`\b(active|pending|in_transit|fa|en|nil|null|NULL|calderis)\b`)
)

// What the player reads carries no identifier, no stored value and no
// unfilled placeholder, in either language, and its buttons lead back into
// the game.
func TestArrivalNoticeShowsNothingInternal(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		t.Run(lang, func(t *testing.T) {
			p := englishPlayer()
			p.Language = lang
			r := newRig(t, p, link(botA, 1001))
			if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-1", r.now, nil)); err != nil {
				t.Fatal(err)
			}
			resp := r.sender.sent[0].notice.Response
			visible := []string{resp.Text}
			if resp.Keyboard == nil || len(resp.Keyboard.Rows) == 0 {
				t.Fatal("the notice has no buttons")
			}
			for _, row := range resp.Keyboard.Rows {
				for _, b := range row {
					visible = append(visible, b.Text)
				}
			}
			for _, text := range visible {
				if m := uuidPattern.FindString(text); m != "" {
					t.Errorf("an identifier %q reached the player: %q", m, text)
				}
				if m := internalWords.FindString(text); m != "" {
					t.Errorf("an internal value %q reached the player: %q", m, text)
				}
				if strings.ContainsAny(text, "{}") {
					t.Errorf("a placeholder reached the player: %q", text)
				}
			}
			t.Logf("\n%s", resp.Text)
		})
	}
}

// Every route is consumable: a subject in the section 10 grammar, and a
// durable name no other route shares (it is also the inbox consumer).
func TestRoutesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Routes() {
		if !subjects.Grammar.MatchString(r.Subject()) {
			t.Errorf("%s does not match the subject grammar", r.Subject())
		}
		if seen[r.Durable()] {
			t.Errorf("durable %s is used twice", r.Durable())
		}
		seen[r.Durable()] = true
		if r.Render == nil {
			t.Errorf("%s has no renderer", r.Subject())
		}
	}
	if got := travelRoute(t).Subject(); got != "game.event.travel.completed.v1" {
		t.Errorf("travel route consumes %s", got)
	}
}

// A notice travels on the subject the subjects package addresses to the
// player, and that subject is inside the grammar the gateway subscribes by.
func TestNoticeSubjectIsTheSubjectsPackages(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-s", r.now.Add(-time.Minute), nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.sent) != 1 {
		t.Fatalf("sent %d notices, want 1", len(r.sender.sent))
	}
	got := r.sender.sent[0].subject
	if got != subjects.Notify(playerID) {
		t.Errorf("subject %q, want %q", got, subjects.Notify(playerID))
	}
	if !subjects.Grammar.MatchString(got) {
		t.Errorf("%q does not match the subject grammar", got)
	}
}

// The receipt margin must leave the gateway some of the budget and keep some
// for the receipt; anything else is refused when the worker is built.
func TestReceiptMarginMustFitInsideTheBudget(t *testing.T) {
	base := Config{
		Players:    fakePlayers{},
		Links:      &fakeLinks{},
		Inbox:      &fakeInbox{},
		Sender:     &fakeSender{},
		Deps:       Deps{Cities: fakeCities{}},
		SendBudget: 10 * time.Second,
	}
	for _, margin := range []time.Duration{0, -time.Second, 10 * time.Second, 11 * time.Second} {
		cfg := base
		cfg.ReceiptMargin = margin
		if _, err := New(cfg); err == nil {
			t.Errorf("a receipt margin of %s in a %s budget was accepted", margin, cfg.SendBudget)
		}
	}
	cfg := base
	cfg.ReceiptMargin = 2 * time.Second
	if _, err := New(cfg); err != nil {
		t.Errorf("a receipt margin of 2s in a 10s budget was refused: %v", err)
	}
}
