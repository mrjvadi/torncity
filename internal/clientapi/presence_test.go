package clientapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/presence"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/shared/hsjwt"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

const (
	villageA = "11111111-0000-4000-8000-0000000000aa"
	villageB = "22222222-0000-4000-8000-0000000000bb"
	someone  = "33333333-0000-4000-8000-0000000000cc"
)

type fakePresence struct {
	mu      sync.Mutex
	beats   []string
	members map[string][]string
	status  application.PlayerStatus
	err     error
}

func (f *fakePresence) Beat(_ context.Context, id string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beats = append(f.beats, id)
	return nil
}
func (f *fakePresence) Status(_ context.Context, _, target string) (application.PlayerStatus, error) {
	if f.err != nil {
		return application.PlayerStatus{}, f.err
	}
	st := f.status
	st.PlayerID = target
	return st, nil
}
func (f *fakePresence) Players(_ context.Context, viewer, settlement string) (application.SettlementPlayers, error) {
	if f.err != nil {
		return application.SettlementPlayers{}, f.err
	}
	return application.SettlementPlayers{SettlementID: settlement, Online: 1, Players: []application.PlayerStatus{f.status}}, nil
}
func (f *fakePresence) Memberships(_ context.Context, id string) ([]string, error) {
	return f.members[id], nil
}
func (f *fakePresence) beatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.beats)
}

type fakeVersions struct{ v int64 }

func (f fakeVersions) Current(context.Context, string) (int64, error) { return f.v, nil }

func newPresenceFixture(t *testing.T, pr *fakePresence) (*apiFixture, string) {
	t.Helper()
	af := newAuthFixture(t)
	msgs, err := i18n.Load("../../configs/locales")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerConfig{
		Auth: af.auth, World: fakeWorld{city: "tehran"}, Msgs: msgs,
		Limits: &memLimits{counts: map[string]int{}}, Realtime: centrifugo.NewTokens("rt-secret", 15*time.Minute),
		Presence: pr, Versions: fakeVersions{v: 41}, PresenceTTL: 30 * time.Second,
		SignInsPerMinute: 5, CommandsPerMinute: 100, MaxBodyBytes: 16384, Now: af.clock.now,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	f := &apiFixture{authFixture: af, srv: srv}
	af.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, sess := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	return f, sess["access_token"].(string)
}

// Any signed-in call, and the explicit heartbeat, keep the player online.
func TestSignedInCallsAndHeartbeatBeatPresence(t *testing.T) {
	pr := &fakePresence{}
	f, tok := newPresenceFixture(t, pr)
	before := pr.beatCount()
	if status, out := f.call(t, "GET", "/api/v1/bootstrap", tok, nil); status != http.StatusOK {
		t.Fatalf("bootstrap: %d %v", status, out)
	}
	if pr.beatCount() != before+1 {
		t.Errorf("an authenticated call did not beat presence")
	}
	status, out := f.call(t, "POST", "/api/v1/realtime/heartbeat", tok, nil)
	if status != http.StatusOK || out["ok"] != true || out["ttl_seconds"] != 30.0 || pr.beatCount() != before+2 {
		t.Errorf("heartbeat: %d %v beats=%d", status, out, pr.beatCount())
	}
	if status, _ := f.call(t, "POST", "/api/v1/realtime/heartbeat", "", nil); status != http.StatusUnauthorized {
		t.Errorf("an unauthenticated heartbeat: %d", status)
	}
}

// The connection token puts the player on their own channel and on the
// channel of every settlement they live in or stand in, decided by the
// server; the subscription flow agrees.
func TestConnectionTokenCarriesTheSettlementChannels(t *testing.T) {
	pr := &fakePresence{members: map[string][]string{"p1": {villageA, villageB}}}
	f, tok := newPresenceFixture(t, pr)
	status, out := f.call(t, "GET", "/api/v1/realtime/token", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, out)
	}
	var cc centrifugo.ConnectionClaims
	if err := hsjwt.Verify([]byte("rt-secret"), out["token"].(string), &cc); err != nil {
		t.Fatal(err)
	}
	want := []string{"player:p1", "settlement:" + villageA, "settlement:" + villageB}
	if len(cc.Channels) != 3 || cc.Channels[0] != want[0] || cc.Channels[1] != want[1] || cc.Channels[2] != want[2] {
		t.Errorf("channels = %v, want %v", cc.Channels, want)
	}

	status, out = f.call(t, "GET", "/api/v1/realtime/subscribe?channel=settlement:"+villageB, tok, nil)
	if status != http.StatusOK {
		t.Fatalf("subscribe to a settlement of theirs: %d %v", status, out)
	}
	var sc centrifugo.SubscriptionClaims
	if err := hsjwt.Verify([]byte("rt-secret"), out["token"].(string), &sc); err != nil || sc.Channel != "settlement:"+villageB {
		t.Errorf("claims = %+v %v", sc, err)
	}
	for _, ch := range []string{"settlement:" + someone, "settlement:", "settlement:" + villageA + "x"} {
		if status, out := f.call(t, "GET", "/api/v1/realtime/subscribe?channel="+ch, tok, nil); status != http.StatusForbidden || errCode(out) != "forbidden_channel" {
			t.Errorf("%q: %d %v", ch, status, out)
		}
	}
}

// A lookup that fails leaves the player on their own channel; the next
// token is computed afresh.
func TestTokenWithoutPresenceKeepsOnlyThePlayerChannel(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, s := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	_, out := f.call(t, "GET", "/api/v1/realtime/token", s["access_token"].(string), nil)
	chans, _ := out["channels"].([]any)
	if len(chans) != 1 || chans[0] != "player:p1" {
		t.Errorf("channels = %v", chans)
	}
}

func TestPlayerStatusEndpoint(t *testing.T) {
	pr := &fakePresence{status: application.PlayerStatus{DisplayName: "Sara", PublicCode: "K7Q2M9A",
		Detail: presence.Detail{Visible: true, Online: true, Activity: presence.Working}}}
	f, tok := newPresenceFixture(t, pr)
	status, out := f.call(t, "GET", "/api/v1/players/"+someone+"/status", tok, nil)
	if status != http.StatusOK || out["id"] != someone || out["name"] != "Sara" || out["visible"] != true ||
		out["online"] != true || out["activity"] != "working" || out["activity_label"] == "" || out["activity_label"] == nil {
		t.Fatalf("status: %d %v", status, out)
	}
	if _, has := out["place"]; has {
		t.Errorf("a place the rules withheld is in the answer: %v", out)
	}

	// Withheld presence is not the same as offline.
	pr.status = application.PlayerStatus{DisplayName: "Sara"}
	_, out = f.call(t, "GET", "/api/v1/players/"+someone+"/status", tok, nil)
	if out["visible"] != false || out["online"] != nil {
		t.Errorf("withheld status = %v", out)
	}

	if status, out := f.call(t, "GET", "/api/v1/players/not-an-id/status", tok, nil); status != http.StatusNotFound || errCode(out) != "not_found" {
		t.Errorf("bad id: %d %v", status, out)
	}
	pr.err = application.ErrPlayerNotFound
	if status, _ := f.call(t, "GET", "/api/v1/players/"+someone+"/status", tok, nil); status != http.StatusNotFound {
		t.Errorf("unknown player: %d", status)
	}
	if status, _ := f.call(t, "GET", "/api/v1/players/"+someone+"/status", "", nil); status != http.StatusUnauthorized {
		t.Errorf("unauthenticated: %d", status)
	}
}

func TestSettlementPlayersEndpoint(t *testing.T) {
	pr := &fakePresence{status: application.PlayerStatus{PlayerID: someone, DisplayName: "Sara", PublicCode: "K7Q2M9A",
		Detail: presence.Detail{Visible: true, Online: true, Activity: presence.Idle, Place: "market"}}}
	f, tok := newPresenceFixture(t, pr)
	status, out := f.call(t, "GET", "/api/v1/settlements/"+villageA+"/players", tok, nil)
	if status != http.StatusOK || out["settlement_id"] != villageA || out["seq"] != 41.0 || out["online"] != 1.0 {
		t.Fatalf("players: %d %v", status, out)
	}
	rows, _ := out["players"].([]any)
	row, _ := rows[0].(map[string]any)
	if len(rows) != 1 || row["place"] != "market" || row["name"] != "Sara" {
		t.Errorf("rows = %v", rows)
	}

	pr.err = application.ErrNotInSettlement
	if status, out := f.call(t, "GET", "/api/v1/settlements/"+villageA+"/players", tok, nil); status != http.StatusForbidden || errCode(out) != "not_in_settlement" {
		t.Errorf("non-member: %d %v", status, out)
	}
	pr.err = errors.New("boom")
	if status, _ := f.call(t, "GET", "/api/v1/settlements/"+villageA+"/players", tok, nil); status != http.StatusInternalServerError {
		t.Errorf("internal: %d", status)
	}
	if status, _ := f.call(t, "GET", "/api/v1/settlements/nope/players", tok, nil); status != http.StatusNotFound {
		t.Errorf("bad id: %d", status)
	}
}
