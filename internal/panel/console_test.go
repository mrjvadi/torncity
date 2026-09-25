package panel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// fakeConsole answers reads from a script and records the changes.
type fakeConsole struct {
	mu        sync.Mutex
	reads     []string
	answer    func(sql string, args []any) postgres.PanelTable
	moderated []string
}

func (f *fakeConsole) Read(_ context.Context, sql string, args ...any) (postgres.PanelTable, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, sql)
	if f.answer != nil {
		return f.answer(sql, args), nil
	}
	if strings.HasPrefix(sql, "SELECT id::text FROM ") {
		return postgres.PanelTable{Columns: []string{"id"}, Rows: [][]any{{"00000000-0000-4000-8000-00000000abcd"}}}, nil
	}
	return postgres.PanelTable{Columns: []string{"x"}}, nil
}

func (f *fakeConsole) Moderate(_ context.Context, player, kind string, _ time.Duration, a operator.Actor) (postgres.Moderated, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moderated = append(f.moderated, kind+" "+player+" "+a.Name+" "+a.Reason)
	return postgres.Moderated{No: 1, Player: player, Kind: kind}, nil
}
func (f *fakeConsole) LiftModeration(_ context.Context, player, kind string, _ operator.Actor) (postgres.Moderated, error) {
	return postgres.Moderated{No: 1, Player: player, Kind: kind}, nil
}
func (f *fakeConsole) Release(context.Context, string, operator.Actor) (postgres.Ended, error) {
	return postgres.Ended{}, postgres.ErrPanelConflict
}
func (f *fakeConsole) Discharge(context.Context, string, operator.Actor) (postgres.Ended, error) {
	return postgres.Ended{ID: "s"}, nil
}
func (f *fakeConsole) Requeue(_ context.Context, id string, _ operator.Actor) (postgres.Requeued, error) {
	return postgres.Requeued{ID: id}, nil
}
func (f *fakeConsole) Dissolve(_ context.Context, code string, _ operator.Actor) (handlers.OperatorClosing, error) {
	return handlers.OperatorClosing{Code: code}, nil
}
func (f *fakeConsole) EconomySeries(context.Context, int, time.Time) (EconomySeries, error) {
	return EconomySeries{}, nil
}
func (f *fakeConsole) ContentDiff(context.Context) (ContentDiff, error) { return ContentDiff{}, nil }
func (f *fakeConsole) ContentSection(context.Context, string) (any, error) {
	return nil, postgres.ErrNotFound
}
func (f *fakeConsole) NATS(context.Context) (NATSStatus, error) { return NATSStatus{}, nil }
func (f *fakeConsole) Age(context.Context, time.Time, time.Time) (int, string, bool) {
	return 0, "", false
}

// publisher records publications.
type publisher struct {
	mu  sync.Mutex
	got []Event
}

func (p *publisher) Publish(_ context.Context, channel string, data any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if channel == OpsChannel {
		if ev, ok := data.(Event); ok {
			p.got = append(p.got, ev)
		}
	}
	return nil
}

func (p *publisher) events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.got...)
}

func newConsoleHarness(t *testing.T) (*harness, *fakeConsole, *publisher) {
	t.Helper()
	h := newHarness(t)
	fc := &fakeConsole{}
	pub := &publisher{}
	rt := &Realtime{Secret: []byte("test secret"), Publisher: pub, TokenTTL: 10 * time.Minute,
		WebSocketURL: "/connection/websocket"}
	s, err := New(Options{Config: h.cfg, Backend: h.backend, Accounts: h.accounts, Now: h.clock.now, Console: fc,
		Realtime: rt, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = s.Handler()
	return h, fc, pub
}

func TestViewStatementIsBoundNotSpliced(t *testing.T) {
	v := catalogue["players"]
	q := Query{Scopes: map[string]string{"city": "00000000-0000-4000-8000-000000000001"},
		Filters: map[string]string{"status": "active"}, Search: "x'; DROP TABLE players; --", Sort: "net_worth",
		Desc: true, Limit: 51, Offset: 50}
	sql, args, err := v.statement(q, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") || strings.Contains(sql, "active'") {
		t.Fatalf("operator input reached the statement:\n%s", sql)
	}
	if !strings.Contains(sql, "ORDER BY v.net_worth DESC NULLS LAST, v.code DESC") || !strings.HasSuffix(sql, "LIMIT $4 OFFSET $5") {
		t.Fatalf("order or paging:\n%s", sql)
	}
	if len(args) != 5 || args[2] != "%x'; DROP TABLE players; --%" {
		t.Fatalf("args %v", args)
	}
	if _, _, err := v.statement(Query{Sort: "no_such_column", Limit: 1}, time.Now()); !IsBadRequest(err) {
		t.Fatalf("an unknown sort: %v", err)
	}
	if _, _, err := v.statement(Query{Filters: map[string]string{"status": "hacked"}, Limit: 1}, time.Now()); !IsBadRequest(err) {
		t.Fatalf("a filter outside its options: %v", err)
	}
	if _, _, err := catalogue["inventory"].statement(Query{Limit: 1}, time.Now()); !IsBadRequest(err) {
		t.Fatalf("a list that needs a scope: %v", err)
	}
}

func TestTheCatalogueIsWellFormed(t *testing.T) {
	if len(catalogue) < 90 {
		t.Fatalf("only %d views", len(catalogue))
	}
	for _, n := range ViewNames() {
		if err := catalogue[n].check(); err != nil {
			t.Error(err)
		}
	}
	for n, s := range seriesCatalogue {
		if s.Scope != "" {
			if _, ok := scopeKinds[s.Scope]; !ok {
				t.Errorf("series %s: unknown scope %s", n, s.Scope)
			}
		}
	}
}

func TestAWindowGoesInsideTheAggregate(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	sql, args, err := catalogue["crime.stats"].statement(Query{Limit: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "WHERE true  AND c.started_at >= $1 GROUP BY") || args[0] != now.Add(-7*24*time.Hour) {
		t.Fatalf("window:\n%s\n%v", sql, args)
	}
}

func TestViewsEndpointPagesAndExports(t *testing.T) {
	h, fc, _ := newConsoleHarness(t)
	cookie, _, _ := h.login("javad", testPassword, "")
	rows := [][]any{}
	for i := range 3 {
		rows = append(rows, []any{"C" + string(rune('A'+i)), "name", "", int64(1), "active", "fa", "c", "c", "p", int64(1),
			"r", int64(1), int64(5), int64(6), false, time.Unix(0, 0), nil})
	}
	fc.answer = func(sql string, _ []any) postgres.PanelTable {
		return postgres.PanelTable{Columns: []string{"code"}, Rows: rows}
	}
	rec := h.do(http.MethodGet, "/api/views/players?limit=2&q=a", nil, cookie, nil)
	var page ViewPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK || !page.More || len(page.Rows) != 2 {
		t.Fatalf("page: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodGet, "/api/views/nope", nil, cookie, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown view: %d", rec.Code)
	}
	rec = h.do(http.MethodGet, "/api/views/players?format=csv", nil, cookie, nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rec.Body.String(), "code,name,username") {
		t.Fatalf("csv: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(strings.Join(h.accounts.audits, "\n"), "panel.export") {
		t.Fatal("the export was not audited")
	}
	if rec := h.do(http.MethodGet, "/api/views/players", nil, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

func TestCSVCellsCannotBeFormulas(t *testing.T) {
	for in, want := range map[any]string{"=HYPERLINK(1)": "'=HYPERLINK(1)", "-5": "-5", "+x": "'+x", "plain": "plain",
		int64(-3): "-3", nil: ""} {
		if got := csvCell(in); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
}

func decodeClaims(t *testing.T, tok string, secret []byte) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %s", tok)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("the signature does not verify")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	return c
}

func TestRealtimeTokensAreForSignedInOperatorsOnly(t *testing.T) {
	h, _, _ := newConsoleHarness(t)
	if rec := h.do(http.MethodGet, "/api/realtime/token", nil, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/realtime/subscribe?channel=panel:ops", nil, "forged", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged session: %d", rec.Code)
	}
	cookie, _, _ := h.login("javad", testPassword, "")
	rec := h.do(http.MethodGet, "/api/realtime/token", nil, cookie, nil)
	var conn struct {
		Token, URL, Channel string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conn); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	c := decodeClaims(t, conn.Token, []byte("test secret"))
	if c["sub"] != "panel-op:javad" || c["exp"].(float64) <= c["iat"].(float64) || c["channels"] != nil {
		t.Fatalf("connection claims: %v", c)
	}
	rec = h.do(http.MethodGet, "/api/realtime/subscribe?channel=panel:ops", nil, cookie, nil)
	var sub struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &sub)
	if s := decodeClaims(t, sub.Token, []byte("test secret")); s["channel"] != "panel:ops" || s["sub"] != c["sub"] {
		t.Fatalf("subscription claims: %v", s)
	}
	for _, ch := range []string{"player:1", "city:x", "panel:other", ""} {
		if rec := h.do(http.MethodGet, "/api/realtime/subscribe?channel="+url.QueryEscape(ch), nil, cookie, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("channel %q: %d", ch, rec.Code)
		}
	}
}

func TestRealtimeOffAnswers404(t *testing.T) {
	h := newHarness(t)
	cookie, _, _ := h.login("javad", testPassword, "")
	if rec := h.do(http.MethodGet, "/api/realtime/token", nil, cookie, nil); rec.Code != http.StatusNotFound || errCode(rec) != "realtime_off" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPublishPayloadShape(t *testing.T) {
	var got struct {
		Channel string
		Data    Event
	}
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-API-Key")
		if r.URL.Path != "/api/publish" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"result":{}}`))
	}))
	defer srv.Close()
	p := &CentrifugoPublisher{APIURL: srv.URL + "/api", APIKey: "k"}
	at := time.Unix(1_800_000_000, 0).UTC()
	if err := p.Publish(t.Context(), OpsChannel, Event{Type: "audit", At: at, Data: map[string]any{"action": "x"}}); err != nil {
		t.Fatal(err)
	}
	if key != "k" || got.Channel != "panel:ops" || got.Data.Type != "audit" || !got.Data.At.Equal(at) {
		t.Fatalf("published %+v with key %q", got, key)
	}
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"code":102,"message":"unknown channel"}}`))
	}))
	defer refusing.Close()
	if err := (&CentrifugoPublisher{APIURL: refusing.URL, APIKey: "k"}).Publish(t.Context(), OpsChannel, 1); err == nil {
		t.Fatal("a refusal in the body was taken for success")
	}
}

func TestChangesArePublished(t *testing.T) {
	h, fc, pub := newConsoleHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := func(k string) map[string]string { return map[string]string{idempotencyHeader: k, csrfHeader: csrf} }
	rec := h.do(http.MethodPost, "/api/players/AB12CDE/moderation", map[string]any{"kind": "mute", "hours": 2, "reason": "spam"},
		cookie, hdr("key-mod-0001"))
	if rec.Code != http.StatusOK || len(fc.moderated) != 1 || fc.moderated[0] != "mute AB12CDE panel:javad spam" {
		t.Fatalf("mute: %d %s %v", rec.Code, rec.Body, fc.moderated)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(pub.events()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	evs := pub.events()
	if len(evs) != 1 || evs[0].Type != "change" {
		t.Fatalf("published %+v", evs)
	}
	// A ban needs the code typed again.
	rec = h.do(http.MethodPost, "/api/players/AB12CDE/moderation", map[string]any{"kind": "ban", "reason": "abuse"},
		cookie, hdr("key-mod-0002"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unconfirmed ban: %d %s", rec.Code, rec.Body)
	}
	rec = h.do(http.MethodPost, "/api/players/AB12CDE/moderation", map[string]any{"kind": "ban", "reason": "abuse",
		"confirm": "ab12cde"}, cookie, hdr("key-mod-0003"))
	if rec.Code != http.StatusOK {
		t.Fatalf("a confirmed ban: %d %s", rec.Code, rec.Body)
	}
	rec = h.do(http.MethodPost, "/api/companies/QWERTY/dissolve", map[string]any{"reason": "fraud", "confirm": "WRONG"},
		cookie, hdr("key-dis-0001"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unconfirmed dissolution: %d", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/players/AB12CDE/release", map[string]any{"reason": "bug"}, cookie, hdr("key-rel-0001"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a release of a free player: %d %s", rec.Code, rec.Body)
	}
	rec = h.do(http.MethodPost, "/api/actions/not-a-uuid/requeue", map[string]any{"reason": "x"}, cookie, hdr("key-req-0001"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed action id: %d", rec.Code)
	}
}

func TestFeedPublishesWhatIsNew(t *testing.T) {
	pub := &publisher{}
	now := time.Unix(1_800_000_000, 0).UTC()
	step := 0
	r := &fakeConsole{answer: func(sql string, _ []any) postgres.PanelTable {
		switch {
		case strings.Contains(sql, "max(id) FROM audit_logs"):
			return postgres.PanelTable{Columns: []string{"a", "b", "c"}, Rows: [][]any{{int64(10), now, now}}}
		case strings.Contains(sql, "FROM audit_logs WHERE id > $1"):
			step++
			if step == 1 {
				return postgres.PanelTable{Columns: []string{"id", "action"}, Rows: [][]any{{int64(11), "economy.grant"}, {int64(12), "player.ban"}}}
			}
		}
		return postgres.PanelTable{}
	}}
	f := &Feed{Reader: r, Publisher: pub, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := f.start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	evs := pub.events()
	if len(evs) != 2 || evs[0].Type != "audit" || f.lastAudit != 12 {
		t.Fatalf("events %+v, cursor %d", evs, f.lastAudit)
	}
}

func TestHealthOf(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if s := healthOf(map[string]any{}, now); s != "ok" {
		t.Fatal(s)
	}
	if s := healthOf(map[string]any{"actions_overdue": int64(3)}, now); s != "lagging" {
		t.Fatal(s)
	}
	if s := healthOf(map[string]any{"actions_stuck": int64(1)}, now); s != "degraded" {
		t.Fatal(s)
	}
	if s := healthOf(map[string]any{"outbox_oldest": now.Add(-2 * time.Minute)}, now); s != "lagging" {
		t.Fatal(s)
	}
}

func TestParseJSZ(t *testing.T) {
	raw := []byte(`{"account_details":[{"name":"$G","stream_detail":[{"name":"COMMANDS","state":{"messages":5,"bytes":900},
		"consumer_detail":[{"name":"game","num_pending":3,"num_ack_pending":1,"num_redelivered":2}]}]}]}`)
	s, err := parseJSZ(raw)
	if err != nil || len(s) != 1 || s[0].Messages != 5 || s[0].Consumers[0].Pending != 3 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestBuildSeriesFillsEveryDay(t *testing.T) {
	days := []string{"2026-09-24", "2026-09-25", "2026-09-26"}
	s := buildSeries("x", TInt, days, [][]any{{time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), "a", int64(4)},
		{"2026-09-26", "b", int64(9)}, {"2026-09-26", "a", int64(1)}})
	if len(s.Lines) != 2 || s.Lines[0].Key != "b" || s.Lines[1].Values[1] != 4 || s.Lines[1].Total != 5 {
		t.Fatalf("%+v", s)
	}
	if got := dayList(time.Date(2026, 9, 24, 21, 0, 0, 0, time.UTC), time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)); len(got) != 2 {
		t.Fatalf("days %v", got)
	}
}

func TestConnectSources(t *testing.T) {
	if got := connectSources("https://panel.example.test", "/connection/websocket", true); got != "'self' wss://panel.example.test" {
		t.Fatal(got)
	}
	if got := connectSources("https://panel.example.test", "wss://live.example.test/connection/websocket", true); got != "'self' wss://live.example.test" {
		t.Fatal(got)
	}
	if got := connectSources("https://panel.example.test", "", false); got != "'self'" {
		t.Fatal(got)
	}
}
