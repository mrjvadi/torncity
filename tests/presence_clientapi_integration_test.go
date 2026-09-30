//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

// The client API end to end for presence (docs/adr/0030, R1 and R2): a
// signed-in client's token subscribes it to the settlement it lives in, the
// status and player list answer with each viewer's own tier, and someone
// outside the settlement is refused its list.
func TestClientAPIPresenceEndpoints(t *testing.T) {
	pool := requirePostgres(t)
	rdb := requireRedis(t)
	ctx := testCtx(t)
	ensureClientTables(t, pool)

	home := cityIDByCode(t, pool, "calderis")
	away := cityIDByCode(t, pool, "brennhaven")
	support := cityIDByCode(t, pool, "support")
	// Only a founded settlement has a channel and a roster: stand in for one.
	asFoundedSettlement(t, pool, home)
	asFoundedSettlement(t, pool, away)
	botID := insertBot(t, pool)
	me, mate, outsider := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	placePlayer(t, pool, me.ID, home, "")
	placePlayer(t, pool, mate.ID, home, "city_centre")
	placePlayer(t, pool, outsider.ID, away, "")
	t.Cleanup(func() {
		for _, p := range []*application.Player{me, outsider} {
			_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM client_devices WHERE player_id = $1::uuid`, p.ID)
		}
	})

	catalog, err := i18n.Load("../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	players := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	devices := postgres.NewClientDevices(pool)
	codes := &memLinkCodes{codes: map[string]application.ClientLinkClaim{}, byReq: map[string]application.ClientLinkCode{}}
	auth, err := clientapi.NewAuth(clientapi.AuthConfig{
		Secret: []byte("integration-secret-integration-secret"), AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
		MaxDevices: 5, TelegramMaxAge: time.Hour, DefaultLanguage: testDefaultLanguage,
		Codes: codes, Devices: devices, Players: players, Once: memLimiter{}, NewID: clientapi.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := infraredis.NewPresence(rdb, time.Minute)
	svc := &application.PresenceService{Store: store, Repo: postgres.NewPresenceRepository(pool), RosterLimit: 50}
	srv := httptest.NewServer(clientapi.NewServer(clientapi.ServerConfig{
		Auth: auth, Limits: memLimiter{}, Realtime: centrifugo.NewTokens("x", time.Minute), Msgs: catalog,
		Presence: svc, Versions: infraredis.NewSettlementVersions(rdb, time.Hour), PresenceTTL: time.Minute,
		SignInsPerMinute: 100, CommandsPerMinute: 100, MaxBodyBytes: 16384,
	}).Handler())
	defer srv.Close()

	signIn := func(p *application.Player) string {
		codes.mu.Lock()
		codes.codes["ABCD2345"] = application.ClientLinkClaim{PlayerID: p.ID, BotID: botID}
		codes.mu.Unlock()
		b, _ := json.Marshal(map[string]string{"code": "ABCD2345", "device_name": "itest"})
		resp, err := http.Post(srv.URL+"/api/v1/auth/link", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		tok, _ := out["access_token"].(string)
		if tok == "" {
			t.Fatalf("sign-in: %v", out)
		}
		return tok
	}
	call := func(method, path, token string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	tok := signIn(me)
	if status, out := call("POST", "/api/v1/realtime/heartbeat", tok); status != http.StatusOK || out["ok"] != true {
		t.Errorf("heartbeat: %d %v", status, out)
	}
	// Calling the API made the player online.
	if got, _ := store.Online(ctx, []string{me.ID}); !got[me.ID] {
		t.Error("an authenticated call did not make the player online")
	}

	status, out := call("GET", "/api/v1/realtime/token", tok)
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, out)
	}
	channels, _ := out["channels"].([]any)
	if len(channels) != 2 || channels[0] != "player:"+me.ID || channels[1] != "settlement:"+home {
		t.Errorf("channels = %v", channels)
	}
	if status, out := call("GET", "/api/v1/realtime/subscribe?channel=settlement:"+away, tok); status != http.StatusForbidden {
		t.Errorf("subscribing to a settlement they are not in: %d %v", status, out)
	}
	if status, out := call("GET", "/api/v1/realtime/subscribe?channel=settlement:"+home, tok); status != http.StatusOK {
		t.Errorf("subscribing to their own settlement: %d %v", status, out)
	}

	// The mate is online in the same settlement: the full picture.
	_ = svc.Beat(ctx, mate.ID, time.Now())
	status, out = call("GET", "/api/v1/players/"+mate.ID+"/status", tok)
	if status != http.StatusOK || out["visible"] != true || out["online"] != true || out["place"] != "city_centre" || out["activity"] != "idle" {
		t.Errorf("status of a mate: %d %v", status, out)
	}
	status, out = call("GET", "/api/v1/settlements/"+home+"/players", tok)
	rows, _ := out["players"].([]any)
	if status != http.StatusOK || out["settlement_id"] != home || len(rows) != 2 || out["online"] != 2.0 {
		t.Errorf("players: %d %v", status, out)
	}
	if _, has := out["seq"]; !has {
		t.Errorf("no seq in the list: %v", out)
	}

	// A resident of Support (a content city) has no settlement channel, and
	// Support has no roster.
	dweller := insertPlayer(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(context.Background(), `DELETE FROM client_devices WHERE player_id = $1::uuid`, dweller.ID)
	})
	placePlayer(t, pool, dweller.ID, support, "")
	dTok := signIn(dweller)
	_, out = call("GET", "/api/v1/realtime/token", dTok)
	if chans, _ := out["channels"].([]any); len(chans) != 1 || chans[0] != "player:"+dweller.ID {
		t.Errorf("a Support resident's channels = %v, want only their own", chans)
	}
	if status, _ := call("GET", "/api/v1/realtime/subscribe?channel=settlement:"+support, dTok); status != http.StatusForbidden {
		t.Errorf("subscribing to Support as a settlement: %d", status)
	}
	if status, _ := call("GET", "/api/v1/settlements/"+support+"/players", dTok); status != http.StatusForbidden {
		t.Errorf("Support's roster: %d", status)
	}

	// Outside the settlement: no list.
	outTok := signIn(outsider)
	status, out = call("GET", "/api/v1/settlements/"+home+"/players", outTok)
	if e, _ := out["error"].(map[string]any); status != http.StatusForbidden || e["code"] != "not_in_settlement" {
		t.Errorf("an outsider's list: %d %v", status, out)
	}
	if status, _ := call("GET", "/api/v1/players/"+newUUID(t)+"/status", tok); status != http.StatusNotFound {
		t.Errorf("an unknown player: %d", status)
	}
}
