package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

const cityGroup int64 = -1001234567890

type fakeCityGroups struct{ groups []application.CityGroup }

func (f fakeCityGroups) ForCity(_ context.Context, id, _ string) ([]application.CityGroup, error) {
	var out []application.CityGroup
	for _, g := range f.groups {
		if g.CityID == id {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f fakeCityGroups) ByChat(_ context.Context, chat int64) (*application.CityGroup, error) {
	for _, g := range f.groups {
		if g.ChatID == chat {
			return &g, nil
		}
	}
	return nil, nil
}

func announceRig(t *testing.T, max int) *rig {
	t.Helper()
	r := newRig(t, &application.Player{ID: playerID, TelegramUserID: 77, DisplayName: "Ada", Language: "en"})
	r.w.cfg.Groups = fakeCityGroups{groups: []application.CityGroup{
		{CityID: cityID, CityCode: "calderis", ChatID: cityGroup, BotID: botA, Language: "fa"},
	}}
	r.w.throttle = &throttle{window: time.Minute, max: max, groups: map[int64]*groupWindow{}}
	return r
}

func announceRoute(t *testing.T, domain, event string) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == domain && r.Event == event && r.Announce != nil {
			return r
		}
	}
	t.Fatalf("no announcement for %s.%s", domain, event)
	return Route{}
}

// Every route has its own consumer, so an announcement and the private
// notice of one event are recorded apart.
func TestEveryRouteHasItsOwnConsumer(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Routes() {
		if seen[r.Durable()] {
			t.Errorf("two routes share the consumer %s", r.Durable())
		}
		seen[r.Durable()] = true
	}
}

// An arrival is posted in the city's group, in the group's language, through
// the group's bot, once however often it is delivered.
func TestArrivalIsAnnouncedInTheCityGroup(t *testing.T) {
	r := announceRig(t, 0)
	route := announceRoute(t, "travel", "completed")
	env := arrivalEvent(t, "req-arrive", r.now, nil)
	for i := 0; i < 2; i++ {
		if err := r.w.Handle(context.Background(), route, env); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sender.sent) != 1 {
		t.Fatalf("posted %d times, want once", len(r.sender.sent))
	}
	got := r.sender.sent[0]
	if !got.notice.Announcement || got.meta.TelegramChatID != cityGroup || got.meta.BotID != botA || got.meta.Language != "fa" {
		t.Errorf("the line went %+v", got.meta)
	}
	text := got.notice.Response.Text
	if !strings.Contains(text, "Ada") || !strings.Contains(text, "کالدریس") || got.notice.Response.Keyboard != nil {
		t.Errorf("the line is %q", text)
	}
}

// A busy group gets a few lines at a time; what is held back is counted on
// the next line that goes out.
func TestABusyGroupIsNotFlooded(t *testing.T) {
	r := announceRig(t, 2)
	route := announceRoute(t, "travel", "completed")
	for i, id := range []string{"r1", "r2", "r3", "r4"} {
		if err := r.w.Handle(context.Background(), route, arrivalEvent(t, id, r.now.Add(time.Duration(i)*time.Second), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sender.sent) != 2 {
		t.Fatalf("posted %d lines in a window of 2", len(r.sender.sent))
	}
	r.now = r.now.Add(2 * time.Minute)
	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "r5", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	last := r.sender.sent[len(r.sender.sent)-1].notice.Response.Text
	if !strings.Contains(last, "۲") {
		t.Errorf("the next line does not count the two held back: %q", last)
	}
}

// A payment started in a group is announced there, without its amount; one
// started in a private chat is announced nowhere.
func TestPaymentIsAnnouncedWhereItStarted(t *testing.T) {
	r := announceRig(t, 0)
	route := announceRoute(t, "bank", "payment_received")
	event := func(id string, origin any) *envelope.Envelope {
		payload := map[string]any{"payer_name": "Ada", "payee_name": "Bob", "amount": 987654, "payee_id": playerID}
		if origin != nil {
			payload["origin_chat_id"] = origin
		}
		raw, _ := json.Marshal(payload)
		env := arrivalEvent(t, id, r.now, nil)
		env.Payload = raw
		env.Metadata.BotID = botB
		return env
	}
	if err := r.w.Handle(context.Background(), route, event("p1", nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.sent) != 0 {
		t.Fatalf("a private payment was announced: %+v", r.sender.sent)
	}
	if err := r.w.Handle(context.Background(), route, event("p2", "-1009999")); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.sent) != 1 || r.sender.sent[0].meta.TelegramChatID != -1009999 || r.sender.sent[0].meta.BotID != botB {
		t.Fatalf("the payment line went %+v", r.sender.sent)
	}
	text := r.sender.sent[0].notice.Response.Text
	if strings.Contains(text, "987") || strings.Contains(text, "۹۸۷") || !strings.Contains(text, "Bob") {
		t.Errorf("the payment line is %q", text)
	}
}
