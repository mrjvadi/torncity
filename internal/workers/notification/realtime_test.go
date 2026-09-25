package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
)

// realtimeServer is a fake Centrifugo server API that records publications,
// or fails every one when down.
type realtimeServer struct {
	mu   sync.Mutex
	pubs []map[string]any
	down bool
	srv  *httptest.Server
}

func newRealtimeServer(t *testing.T) *realtimeServer {
	t.Helper()
	rs := &realtimeServer{}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if rs.down || r.Header.Get("X-API-Key") != "api-key" || r.URL.Path != "/api/publish" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(b, &p)
		rs.pubs = append(rs.pubs, p)
		_, _ = w.Write([]byte(`{"result":{}}`))
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *realtimeServer) published() []map[string]any {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]map[string]any(nil), rs.pubs...)
}

func withRealtime(r *rig, rs *realtimeServer) {
	r.w.cfg.Realtime = centrifugo.NewPublisher(rs.srv.URL+"/api", "api-key", time.Second)
	r.w.cfg.RealtimeLanguages = []string{"fa", "en"}
}

// A notice goes to Telegram and, the same, to the player's channel.
func TestNoticeIsAlsoPublishedToThePlayersChannel(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	rs := newRealtimeServer(t)
	withRealtime(r, rs)

	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-rt", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if r.sender.delivered != 1 {
		t.Fatalf("telegram deliveries = %d", r.sender.delivered)
	}
	pubs := rs.published()
	if len(pubs) != 1 {
		t.Fatalf("published %d, want 1", len(pubs))
	}
	p := pubs[0]
	data, _ := p["data"].(map[string]any)
	if p["channel"] != "player:"+playerID || data["type"] != "notice" || data["kind"] != "travel.completed" ||
		data["text"] != r.sender.sent[0].notice.Response.Text || !strings.HasPrefix(p["idempotency_key"].(string), "req-rt:") {
		t.Errorf("publication = %v", p)
	}
}

// The realtime server being down changes nothing for Telegram.
func TestRealtimeFailureNeverBreaksTelegram(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	rs := newRealtimeServer(t)
	rs.down = true
	withRealtime(r, rs)
	route := travelRoute(t)

	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-down", r.now, nil)); err != nil {
		t.Fatalf("a realtime failure failed the notice: %v", err)
	}
	if r.sender.delivered != 1 || !r.inbox.done["req-down|"+route.Durable()] {
		t.Error("the notice must be delivered and recorded as usual")
	}

	// And a Telegram failure is still retried, realtime or not.
	r2 := newRig(t, englishPlayer(), link(botA, 1001))
	withRealtime(r2, newRealtimeServer(t))
	r2.sender.err = errors.New("gateway away")
	if err := r2.w.Handle(context.Background(), route, arrivalEvent(t, "req-retry", r2.now, nil)); err == nil {
		t.Error("a failed Telegram send must still be retried")
	}
}

// A city announcement goes to the city's channel, in every language.
func TestAnnouncementIsPublishedToTheCitysChannel(t *testing.T) {
	r := announceRig(t, 0)
	rs := newRealtimeServer(t)
	withRealtime(r, rs)

	if err := r.w.Handle(context.Background(), announceRoute(t, "travel", "completed"), arrivalEvent(t, "req-ann", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.sent) != 1 {
		t.Fatalf("group posts = %d", len(r.sender.sent))
	}
	pubs := rs.published()
	if len(pubs) != 1 {
		t.Fatalf("published %d, want 1", len(pubs))
	}
	data, _ := pubs[0]["data"].(map[string]any)
	texts, _ := data["texts"].(map[string]any)
	if pubs[0]["channel"] != "city:calderis" || data["type"] != "announce" || data["kind"] != "travel.completed" ||
		data["text"] != texts["fa"] || !strings.Contains(texts["en"].(string), "Ada") || texts["fa"] == texts["en"] {
		t.Errorf("publication = %v", pubs[0])
	}
}
