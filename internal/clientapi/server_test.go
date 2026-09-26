package clientapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func loadPolicy(t *testing.T) *groups.Policy {
	t.Helper()
	p, err := groups.LoadPolicy("../../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func loadActionMeta(t *testing.T) *ActionMetadata {
	t.Helper()
	m, err := LoadActionMetadata("../../configs/actions.yml")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestActionsTranslateTheKeyboard(t *testing.T) {
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "Map", CallbackData: "map:list"}, {Text: "Bazaar", CallbackData: "place:go:bazaar"}},
		{{Text: "Deposit…", CallbackData: "ask:bank.deposit"}, {Text: "Pay…", CallbackData: "ask:bank.pay:K7Q2M9A:card"}},
		{{Text: "Mine", CallbackData: "-2s:bank:show"}, {Text: "Site", URL: "https://example.com"}},
		{{Text: "Stale", CallbackData: "nosuch:thing"}, {Text: "Evil", CallbackData: "bank:show:<script>"}},
	}}
	got := Actions(kb, loadPolicy(t), loadActionMeta(t))
	want := []Action{
		{Label: "Map", Command: "map.list", Row: 0, Kind: KindNavigation, Icon: "action:map"},
		{Label: "Bazaar", Command: "place.go", Args: map[string]any{"place": "bazaar"}, Row: 0, Kind: KindPrimary, Icon: "action:travel"},
		{Label: "Deposit…", Command: "bank.deposit", Args: map[string]any{}, Input: &ActionInput{Field: "amount"}, Row: 1, Kind: KindPrimary, Icon: "action:deposit"},
		{Label: "Pay…", Command: "bank.pay", Args: map[string]any{"to": "K7Q2M9A", "method": "card"}, Input: &ActionInput{Field: "amount"}, Row: 1, Kind: KindPrimary, Icon: "action:pay", Group: "bank_pay"},
		{Label: "Mine", Command: "bank.show", Row: 2, Kind: KindNavigation, Icon: "action:bank"},
		{Label: "Site", URL: "https://example.com", Row: 2},
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("actions\n got %s\nwant %s", gb, wb)
	}
}

func TestNormalizeArgs(t *testing.T) {
	got, err := NormalizeArgs(map[string]json.RawMessage{
		"amount": json.RawMessage(`5000`), "to": json.RawMessage(`"@ali"`), "yes": json.RawMessage(`true`),
		"args": json.RawMessage(`["a", 2]`), "skip": json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"amount":"5000","args":["a","2"],"to":"@ali","yes":"true"}` {
		t.Errorf("got %s", b)
	}
	for _, bad := range []string{`{"x":1}`, `1.5`, `[{"a":1}]`} {
		if _, err := NormalizeArgs(map[string]json.RawMessage{"a": json.RawMessage(bad)}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

// fakeBus answers every command with a fixed screen, as the game would.
type fakeBus struct {
	mu   sync.Mutex
	sent []*envelope.Envelope
	subj []string
	resp *presenter.Response
	hang bool
}

func (b *fakeBus) Request(ctx context.Context, subject string, env *envelope.Envelope) (*envelope.Envelope, error) {
	b.mu.Lock()
	b.sent = append(b.sent, env)
	b.subj = append(b.subj, subject)
	b.mu.Unlock()
	if b.hang {
		<-ctx.Done()
		return nil, ErrTimeout
	}
	return envelope.New(env.Metadata, b.resp)
}

type fakeWorld struct{ city string }

func (w fakeWorld) Bootstrap(_ context.Context, pr Principal) (Bootstrap, error) {
	return Bootstrap{Player: BootstrapPlayer{ID: pr.PlayerID, CityCode: w.city}}, nil
}
func (w fakeWorld) CityCode(context.Context, string) (string, error) { return w.city, nil }

type apiFixture struct {
	*authFixture
	bus *fakeBus
	srv *httptest.Server
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	af := newAuthFixture(t)
	gen := &ids{n: 100}
	bus := &fakeBus{resp: presenter.WithView(presenter.Edit(9, "Your bank", &presenter.Keyboard{Rows: [][]presenter.Button{
		{{Text: "Deposit…", CallbackData: "ask:bank.deposit"}},
	}}).MarkPrivate(), "bank", struct{ Cash int }{5})}
	s := NewServer(ServerConfig{
		Auth: af.auth,
		Bridge: &Bridge{Bus: bus, Policy: loadPolicy(t), ActionMeta: loadActionMeta(t), Timeout: 200 * time.Millisecond, InstanceID: "clientapi-test",
			NewID: gen.next, Now: af.clock.now},
		World:  fakeWorld{city: "tehran"},
		Limits: &memLimits{counts: map[string]int{}}, Realtime: centrifugo.NewTokens("rt-secret", 15*time.Minute),
		SignInsPerMinute: 5, CommandsPerMinute: 100, MaxBodyBytes: 16384, Now: af.clock.now,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &apiFixture{authFixture: af, bus: bus, srv: srv}
}

func (f *apiFixture) call(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestLinkThenCommandRoundTrip(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)

	status, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345", "device_name": "Pixel"})
	if status != http.StatusOK {
		t.Fatalf("link: %d %v", status, session)
	}
	token := session["access_token"].(string)

	status, screen := f.call(t, "POST", "/api/v1/command", token, map[string]any{
		"command": "bank.deposit", "args": map[string]any{"amount": 5000}, "idempotency_key": "tap-1"})
	if status != http.StatusOK {
		t.Fatalf("command: %d %v", status, screen)
	}
	if screen["ok"] != true || screen["screen"] != "bank" || screen["text"] != "Your bank" {
		t.Errorf("screen = %v", screen)
	}
	if v, _ := screen["view"].(map[string]any); v["cash"] != 5.0 {
		t.Errorf("view = %v", screen["view"])
	}
	actions, _ := screen["actions"].([]any)
	a0, _ := actions[0].(map[string]any)
	if len(actions) != 1 || a0["command"] != "bank.deposit" || a0["kind"] != "primary" || a0["icon"] != "action:deposit" {
		t.Errorf("actions = %v", screen["actions"])
	}

	env := f.bus.sent[0]
	m := env.Metadata
	if f.bus.subj[0] != "game.command.bank.deposit.v1" || m.ChatType != envelope.ChatTypeClient || m.BotID != "bot-a" ||
		m.TelegramUserID != 77 || m.TelegramChatID != 77 || m.PlayerID != "p1" || m.Command != "bank.deposit" ||
		m.Action != "deposit" || m.Language != "fa" || m.InGroup() || m.GatewayInstanceID != "clientapi-test" {
		t.Errorf("metadata = %+v", m)
	}
	if m.IdempotencyKey == "" || m.IdempotencyKey[:7] != "client:" || m.Validate() != nil {
		t.Errorf("idempotency key %q, valid %v", m.IdempotencyKey, m.Validate())
	}
	if string(env.Payload) != `{"amount":"5000"}` {
		t.Errorf("payload = %s", env.Payload)
	}
}

func TestCommandRefusals(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	token := session["access_token"].(string)

	cases := []struct {
		body   any
		token  string
		status int
		code   string
	}{
		{map[string]any{"command": "crime.list"}, token, http.StatusForbidden, "group_only"},
		{map[string]any{"command": "travel.arrive"}, token, http.StatusBadRequest, "unknown_command"},
		{map[string]any{"command": "nope.nope"}, token, http.StatusBadRequest, "unknown_command"},
		{map[string]any{"command": "bank.show", "args": map[string]any{"x": map[string]int{"a": 1}}}, token, http.StatusBadRequest, "bad_request"},
		{map[string]any{"command": "bank.show"}, "", http.StatusUnauthorized, "unauthorized"},
		{map[string]any{"command": "bank.show"}, "garbage", http.StatusUnauthorized, "unauthorized"},
	}
	for i, c := range cases {
		status, out := f.call(t, "POST", "/api/v1/command", c.token, c.body)
		if status != c.status || errCode(out) != c.code || out["ok"] != false {
			t.Errorf("case %d: %d %v, want %d %s", i, status, out, c.status, c.code)
		}
	}
	if len(f.bus.sent) != 0 {
		t.Errorf("a refused command was published: %d", len(f.bus.sent))
	}

	f.bus.hang = true
	status, out := f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": "bank.show"})
	if status != http.StatusGatewayTimeout || errCode(out) != "timeout" {
		t.Errorf("timeout: %d %v", status, out)
	}
}

func TestSignInIsRateLimitedAndCodesAreChecked(t *testing.T) {
	f := newAPIFixture(t)
	for i := 0; i < 5; i++ {
		status, out := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ZZZZ9999"})
		if status != http.StatusUnauthorized || errCode(out) != "invalid_code" {
			t.Fatalf("attempt %d: %d %v", i, status, out)
		}
	}
	status, out := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ZZZZ9999"})
	if status != http.StatusTooManyRequests || errCode(out) != "rate_limited" {
		t.Errorf("sixth attempt: %d %v", status, out)
	}
}

func TestRefreshAndLogoutEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, first := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})

	status, second := f.call(t, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first["refresh_token"].(string)})
	if status != http.StatusOK || second["refresh_token"] == first["refresh_token"] {
		t.Fatalf("refresh: %d %v", status, second)
	}
	status, out := f.call(t, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first["refresh_token"].(string)})
	if status != http.StatusUnauthorized || errCode(out) != "refresh_token_reused" {
		t.Errorf("reuse: %d %v", status, out)
	}

	f.codes.put("QRST2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, s := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "QRST2345"})
	tok := s["access_token"].(string)
	if status, _ := f.call(t, "POST", "/api/v1/auth/logout", tok, nil); status != http.StatusOK {
		t.Errorf("logout: %d", status)
	}
	if status, _ := f.call(t, "GET", "/api/v1/bootstrap", tok, nil); status != http.StatusUnauthorized {
		t.Errorf("after logout: %d", status)
	}
}

func TestRealtimeTokens(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, s := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	tok := s["access_token"].(string)

	status, out := f.call(t, "GET", "/api/v1/realtime/token", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, out)
	}
	var cc centrifugo.ConnectionClaims
	if err := hsjwt.Verify([]byte("rt-secret"), out["token"].(string), &cc); err != nil {
		t.Fatal(err)
	}
	if cc.Subject != "p1" || len(cc.Channels) != 1 || cc.Channels[0] != "player:p1" || cc.ExpiresAt-cc.IssuedAt != 900 {
		t.Errorf("connection claims = %+v", cc)
	}

	status, out = f.call(t, "GET", "/api/v1/realtime/subscribe?channel=city:tehran", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("subscribe: %d %v", status, out)
	}
	var sc centrifugo.SubscriptionClaims
	if err := hsjwt.Verify([]byte("rt-secret"), out["token"].(string), &sc); err != nil || sc.Channel != "city:tehran" || sc.Subject != "p1" {
		t.Errorf("subscription claims = %+v, %v", sc, err)
	}
	for _, ch := range []string{"city:berlin", "player:p2", "panel:ops", ""} {
		if status, out := f.call(t, "GET", "/api/v1/realtime/subscribe?channel="+ch, tok, nil); status != http.StatusForbidden {
			t.Errorf("%q: %d %v", ch, status, out)
		}
	}
}
