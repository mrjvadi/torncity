package centrifugo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
)

func TestPublish(t *testing.T) {
	var got map[string]any
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/publish" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		key = r.Header.Get("X-API-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		if got["channel"] == "bad:x" {
			_, _ = w.Write([]byte(`{"error":{"code":102,"message":"unknown channel"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{}}`))
	}))
	defer srv.Close()

	p := NewPublisher(srv.URL+"/api/", "k1", time.Second)
	if err := p.Publish(context.Background(), PlayerChannel("p1"), map[string]any{"type": "notice"}, "e1"); err != nil {
		t.Fatal(err)
	}
	if key != "k1" || got["channel"] != "player:p1" || got["idempotency_key"] != "e1" {
		t.Errorf("sent %v with key %q", got, key)
	}
	if err := p.Publish(context.Background(), "bad:x", 1, ""); err == nil {
		t.Error("an error answer must be an error")
	}
	if err := NewPublisher(srv.URL, "", time.Second).Publish(context.Background(), "c", 1, ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("no key: %v", err)
	}
}

func TestTokens(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tk := NewTokens("secret", 15*time.Minute)

	conn, exp, err := tk.Connection("p1", []string{PlayerChannel("p1")}, now)
	if err != nil {
		t.Fatal(err)
	}
	var cc ConnectionClaims
	if err := hsjwt.Verify([]byte("secret"), conn, &cc); err != nil {
		t.Fatal(err)
	}
	if cc.Subject != "p1" || cc.ExpiresAt != now.Add(15*time.Minute).Unix() || cc.IssuedAt != now.Unix() ||
		len(cc.Channels) != 1 || cc.Channels[0] != "player:p1" || !exp.Equal(now.Add(15*time.Minute)) {
		t.Errorf("connection claims %+v", cc)
	}

	sub, _, err := tk.Subscription("p1", CityChannel("tehran"), now)
	if err != nil {
		t.Fatal(err)
	}
	var sc SubscriptionClaims
	if err := hsjwt.Verify([]byte("secret"), sub, &sc); err != nil {
		t.Fatal(err)
	}
	if sc.Subject != "p1" || sc.Channel != "city:tehran" || sc.ExpiresAt == 0 {
		t.Errorf("subscription claims %+v", sc)
	}
}
