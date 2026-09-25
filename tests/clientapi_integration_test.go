//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The game client API end to end: a player asks the bot for a link code
// (device.link, the real handler), a client signs in with it, plays a
// command (device.list) through the command pipeline and gets the real
// screen back, renews its tokens, and is signed out from the bot.
//
// PostgreSQL is required. With INTEGRATION_REDIS_URL the link codes are the
// Redis store, else an in-memory one; with INTEGRATION_NATS_URL the command
// goes over JetStream to a responder that runs the game's handler and
// answers on the response subject, as cmd/game does, else a direct bus
// stands in for the broker.

// ensureClientTables applies migrations/0033 when the database has not had
// it yet (the tables are new; applying twice is refused by CREATE TABLE).
func ensureClientTables(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var present bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.client_devices') IS NOT NULL`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present {
		return
	}
	sql, err := os.ReadFile("../migrations/0033_client_devices.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(testCtx(t), string(sql)); err != nil {
		t.Fatalf("applying 0033_client_devices: %v", err)
	}
	// Recorded as `admin migrate` records it, so a later migrate does not
	// apply it a second time.
	if _, err := pool.Raw().Exec(testCtx(t), `INSERT INTO schema_migrations (version, applied_at) VALUES ('0033_client_devices', now())
		ON CONFLICT (version) DO NOTHING`); err != nil {
		t.Fatalf("recording 0033_client_devices: %v", err)
	}
}

// memLinkCodes is the in-memory stand-in for the Redis code store.
type memLinkCodes struct {
	mu    sync.Mutex
	codes map[string]application.ClientLinkClaim
	byReq map[string]application.ClientLinkCode
}

func (m *memLinkCodes) Issue(_ context.Context, c application.ClientLinkClaim, req string, ttl time.Duration, _ int) (application.ClientLinkCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.byReq[req]; ok {
		return prev, nil
	}
	code, err := infraredis.NewLinkCode()
	if err != nil {
		return application.ClientLinkCode{}, err
	}
	m.codes[code] = c
	out := application.ClientLinkCode{Code: code, ExpiresAt: time.Now().Add(ttl)}
	m.byReq[req] = out
	return out, nil
}

func (m *memLinkCodes) Redeem(_ context.Context, code string) (application.ClientLinkClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.codes[infraredis.NormalizeLinkCode(code)]
	delete(m.codes, infraredis.NormalizeLinkCode(code))
	if !ok {
		return application.ClientLinkClaim{}, application.ErrClientLinkCodeInvalid
	}
	return c, nil
}

// directBus runs the command's handler in-process, as the broker and the
// game would.
type directBus struct {
	run func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error)
}

func (b directBus) Request(ctx context.Context, _ string, env *envelope.Envelope) (*envelope.Envelope, error) {
	resp, err := b.run(ctx, env)
	if err != nil {
		return nil, err
	}
	return envelope.New(env.Metadata, resp)
}

type memLimiter struct{}

func (memLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
func (memLimiter) Once(context.Context, string, time.Duration) (bool, error)       { return true, nil }

func TestClientAPILinkCommandRoundTrip(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ensureClientTables(t, pool)

	botID := insertBot(t, pool)
	player := insertPlayer(t, pool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := pool.Raw().Exec(ctx, `DELETE FROM client_devices WHERE player_id = $1::uuid`, player.ID); err != nil {
			t.Errorf("cleaning up client devices: %v", err)
		}
	})

	catalog, err := i18n.Load("../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := groups.LoadPolicy("../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	players := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	devices := postgres.NewClientDevices(pool)

	var codes application.ClientLinkCodes = &memLinkCodes{codes: map[string]application.ClientLinkClaim{}, byReq: map[string]application.ClientLinkCode{}}
	if os.Getenv(envRedis) != "" {
		codes = infraredis.NewClientLinkCodes(requireRedis(t))
	}
	game, err := handlers.NewDevicesHandler(handlers.DevicesConfig{Msgs: catalog, Players: players, Codes: codes,
		Devices: devices, CodeTTL: 10 * time.Minute, PerHour: 50})
	if err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
		switch env.Metadata.Command {
		case "device.list":
			return game.List(ctx, env.Metadata)
		case "device.revoke":
			var req handlers.DeviceRevokeRequest
			if err := env.Decode(&req); err != nil {
				return nil, err
			}
			return game.Revoke(ctx, env.Metadata, req)
		}
		return nil, errors.New("no handler for " + env.Metadata.Command)
	}

	var bus clientapi.Bus = directBus{run: run}
	if os.Getenv(envNATS) != "" {
		conn := requireNATS(t)
		consumer := infranats.NewConsumer(conn, infranats.ConsumerOptions{AckWait: 5 * time.Second, MaxDeliver: 3,
			NakDelay: time.Second, Backoff: []time.Duration{time.Second}})
		for _, command := range []string{"device.list", "device.revoke"} {
			domain, action, _ := strings.Cut(command, ".")
			// The responder does what cmd/game does: run the handler and
			// publish the screen on the request's response subject.
			err := consumer.Subscribe(ctx, subjects.Command(domain, action), "itest-clientapi-"+strings.ReplaceAll(command, ".", "-"),
				func(ctx context.Context, env *envelope.Envelope) error {
					resp, err := run(ctx, env)
					if err != nil {
						return err
					}
					reply, err := envelope.New(env.Metadata, resp)
					if err != nil {
						return err
					}
					data, _ := json.Marshal(reply)
					return conn.Raw().Publish(subjects.Response(env.Metadata.RequestID), data)
				})
			if err != nil {
				t.Fatal(err)
			}
		}
		bus = clientapi.NewNATSBus(conn.Raw(), infranats.NewPublisher(conn))
	}

	auth, err := clientapi.NewAuth(clientapi.AuthConfig{
		Secret: []byte("integration-secret-integration-secret"), AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
		MaxDevices: 5, TelegramMaxAge: time.Hour, DefaultLanguage: testDefaultLanguage,
		Codes: codes, Devices: devices, Players: players, Once: memLimiter{}, NewID: clientapi.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(clientapi.NewServer(clientapi.ServerConfig{
		Auth: auth,
		Bridge: &clientapi.Bridge{Bus: bus, Policy: policy, Timeout: 10 * time.Second, InstanceID: "itest",
			NewID: clientapi.NewID, Now: time.Now},
		Limits: memLimiter{}, Realtime: centrifugo.NewTokens("x", time.Minute), Msgs: catalog,
		SignInsPerMinute: 100, CommandsPerMinute: 100, MaxBodyBytes: 16384,
	}).Handler())
	defer srv.Close()

	call := func(path, token string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
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

	// /link in the bot's private chat, through the real handler.
	linkScreen, err := game.Link(ctx, envelope.Metadata{RequestID: newUUID(t), BotID: botID,
		TelegramUserID: player.TelegramUserID, Language: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`[` + infraredis.LinkCodeAlphabet + `]{8}`).FindString(linkScreen.Text)
	if code == "" || !linkScreen.Private {
		t.Fatalf("no code on the private /link screen:\n%s", linkScreen.Text)
	}

	status, session := call("/api/v1/auth/link", "", map[string]string{"code": strings.ToLower(code), "device_name": "Integration phone"})
	if status != http.StatusOK {
		t.Fatalf("link: %d %v", status, session)
	}
	if p, _ := session["player"].(map[string]any); p["id"] != player.ID {
		t.Errorf("signed in as %v, want %s", session["player"], player.ID)
	}
	if status, out := call("/api/v1/auth/link", "", map[string]string{"code": code}); status != http.StatusUnauthorized {
		t.Errorf("a used code signed in again: %d %v", status, out)
	}
	access := session["access_token"].(string)

	// A command through the pipeline: the real device list comes back.
	status, screen := call("/api/v1/command", access, map[string]any{"command": "device.list", "idempotency_key": "list-1"})
	if status != http.StatusOK || screen["ok"] != true {
		t.Fatalf("device.list: %d %v", status, screen)
	}
	if !strings.Contains(screen["text"].(string), "Integration phone") {
		t.Errorf("the list does not show the linked client:\n%s", screen["text"])
	}
	var revoke map[string]any
	for _, a := range screen["actions"].([]any) {
		if a := a.(map[string]any); a["command"] == "device.revoke" {
			revoke = a
		}
	}
	if revoke == nil {
		t.Fatalf("no sign-out action in %v", screen["actions"])
	}

	// Renewal against the table: a new pair, and the old token is theft.
	status, renewed := call("/api/v1/auth/refresh", "", map[string]string{"refresh_token": session["refresh_token"].(string)})
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, renewed)
	}
	access = renewed["access_token"].(string)

	// Sign the device out by the action the screen offered, as the bot's
	// button would; the next request is refused.
	status, out := call("/api/v1/command", access, map[string]any{"command": revoke["command"], "args": revoke["args"]})
	if status != http.StatusOK || !strings.Contains(out["text"].(string), catalog.T("fa", "device.notice.revoked", nil)) {
		t.Fatalf("device.revoke: %d %v", status, out)
	}
	if status, out := call("/api/v1/command", access, map[string]any{"command": "device.list"}); status != http.StatusUnauthorized {
		t.Errorf("a signed-out client still plays: %d %v", status, out)
	}
	if status, out := call("/api/v1/auth/refresh", "", map[string]string{"refresh_token": renewed["refresh_token"].(string)}); status != http.StatusUnauthorized {
		t.Errorf("a signed-out client still renews: %d %v", status, out)
	}
}

// The refresh-token table's reuse rule, against PostgreSQL.
func TestClientDeviceTokenReuse(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	ensureClientTables(t, pool)
	botID := insertBot(t, pool)
	player := insertPlayer(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM client_devices WHERE player_id = $1::uuid`, player.ID)
	})
	repo := postgres.NewClientDevices(pool)
	now := time.Now().UTC()
	d := application.ClientDevice{ID: newUUID(t), PlayerID: player.ID, BotID: botID, Name: "x", Via: application.ClientViaLink,
		CreatedAt: now, LastSeenAt: now}
	if err := repo.Create(ctx, d, "h1", now.Add(time.Hour), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Rotate(ctx, "h1", "h2", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Rotate(ctx, "h1", "h3", now, now.Add(time.Hour)); !errors.Is(err, application.ErrClientTokenReused) {
		t.Fatalf("reuse: %v", err)
	}
	if got, _ := repo.Get(ctx, d.ID); got.RevokedAt == nil {
		t.Error("reuse must sign the device out")
	}
	if _, err := repo.Rotate(ctx, "h2", "h4", now, now.Add(time.Hour)); !errors.Is(err, application.ErrClientTokenInvalid) {
		t.Errorf("the newest token of a revoked device: %v", err)
	}
	if _, err := repo.Rotate(ctx, "nope", "h5", now, now.Add(time.Hour)); !errors.Is(err, application.ErrClientTokenInvalid) {
		t.Errorf("unknown: %v", err)
	}

	// The device limit signs the oldest out.
	for i := 0; i < 3; i++ {
		later := now.Add(time.Duration(i+1) * time.Second)
		if err := repo.Create(ctx, application.ClientDevice{ID: newUUID(t), PlayerID: player.ID, BotID: botID, Name: "n",
			Via: application.ClientViaLink, CreatedAt: later, LastSeenAt: later}, "l"+newUUID(t), later.Add(time.Hour), 2); err != nil {
			t.Fatal(err)
		}
	}
	active, err := repo.Active(ctx, player.ID)
	if err != nil || len(active) != 2 {
		t.Errorf("active = %d (%v), want 2", len(active), err)
	}
}

// The realtime publisher against a live Centrifugo, when one is configured
// (INTEGRATION_CENTRIFUGO_API_URL, INTEGRATION_CENTRIFUGO_API_KEY).
func TestCentrifugoPublish(t *testing.T) {
	url, key := os.Getenv("INTEGRATION_CENTRIFUGO_API_URL"), os.Getenv("INTEGRATION_CENTRIFUGO_API_KEY")
	if url == "" {
		t.Skip("INTEGRATION_CENTRIFUGO_API_URL is not set")
	}
	p := centrifugo.NewPublisher(url, key, 5*time.Second)
	if err := p.Publish(testCtx(t), centrifugo.PlayerChannel("itest"), map[string]string{"type": "notice"}, ""); err != nil {
		t.Fatalf("publish to a player channel: %v", err)
	}
	if err := p.Publish(testCtx(t), centrifugo.CityChannel("itest"), map[string]string{"type": "announce"}, "k1"); err != nil {
		t.Fatalf("publish to a city channel: %v", err)
	}
	if err := p.Publish(testCtx(t), "nosuchnamespace:x", 1, ""); err == nil {
		t.Error("a channel of no namespace was accepted")
	}
	if err := centrifugo.NewPublisher(url, "wrong", 5*time.Second).Publish(testCtx(t), centrifugo.PlayerChannel("x"), 1, ""); err == nil {
		t.Error("a wrong API key was accepted")
	}
}
