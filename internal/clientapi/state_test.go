package clientapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// fakeSync answers the state sync endpoints from fixed values and records
// what it was asked.
type fakeSync struct {
	player string
	since  int64
	epoch  string
	limit  int
	kinds  statesync.KindSet
	cause  string
}

func (f *fakeSync) State(_ context.Context, playerID string, kinds statesync.KindSet) (statesync.Snapshot, error) {
	f.player, f.kinds = playerID, kinds
	return statesync.Snapshot{PTS: 7, Epoch: "1", Entities: map[string]map[string]statesync.SnapshotEntity{
		statesync.KindWallet: {"SUP": {V: 2, D: json.RawMessage(`{"cash":5,"bank":9}`)}},
	}}, nil
}

func (f *fakeSync) Updates(_ context.Context, playerID string, since int64, epoch string, limit int) (statesync.Difference, error) {
	f.player, f.since, f.epoch, f.limit = playerID, since, epoch, limit
	if since > 7 {
		return statesync.Difference{PTS: 7, Reset: true, Reason: statesync.ResetAhead, Epoch: "1", Updates: []statesync.Record{}}, nil
	}
	return statesync.Difference{PTS: 7, Epoch: "1", Updates: []statesync.Record{{PTS: 7, Type: "wallet.set", Entity: "wallet",
		ID: "SUP", V: 2, Op: "set", Data: json.RawMessage(`{"cash":5}`)}}}, nil
}

func (f *fakeSync) ForCommand(_ context.Context, playerID, requestID string) *statesync.CommandUpdates {
	f.player, f.cause = playerID, requestID
	return &statesync.CommandUpdates{PTS: 8, Records: []statesync.Record{{PTS: 8, Type: "wallet.set", Entity: "wallet", ID: "SUP",
		V: 3, Op: "set", Data: json.RawMessage(`{"cash":0}`), Cause: requestID}}}
}

func syncSignedIn(t *testing.T, f *apiFixture) string {
	t.Helper()
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	return session["access_token"].(string)
}

func TestStateEndpoints(t *testing.T) {
	sync := &fakeSync{}
	f := newAPIFixtureWith(t, func(c *ServerConfig) { c.Sync = sync; c.PullsPerMinute = 100 })
	token := syncSignedIn(t, f)

	status, snap := f.call(t, "GET", "/api/v1/state?kinds=wallet,vitals", token, nil)
	if status != http.StatusOK || snap["pts"] != 7.0 || sync.player != "p1" || !sync.kinds.Has("wallet") || sync.kinds.Has("skill") {
		t.Fatalf("state: %d %v (asked %+v)", status, snap, sync)
	}
	if _, ok := snap["channels"].(map[string]any); !ok {
		t.Errorf("the snapshot carries no channels map: %v", snap)
	}
	if status, out := f.call(t, "GET", "/api/v1/state?kinds=wallet,secrets", token, nil); status != http.StatusBadRequest {
		t.Errorf("an unknown kind: %d %v", status, out)
	}

	status, diff := f.call(t, "GET", "/api/v1/updates?since=6&limit=50&epoch=1", token, nil)
	ups, _ := diff["updates"].([]any)
	if status != http.StatusOK || len(ups) != 1 || sync.since != 6 || sync.limit != 50 || sync.epoch != "1" {
		t.Fatalf("updates: %d %v (asked %+v)", status, diff, sync)
	}
	if _, diff := f.call(t, "GET", "/api/v1/updates?since=9", token, nil); diff["reset"] != true || diff["reason"] != "ahead" {
		t.Errorf("a reset = %v", diff)
	}
	for _, q := range []string{"", "?since=x", "?since=-1", "?since=1&limit=0"} {
		if status, _ := f.call(t, "GET", "/api/v1/updates"+q, token, nil); status != http.StatusBadRequest {
			t.Errorf("updates%s = %d, want 400", q, status)
		}
	}

	status, screen := f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": "bank.show"})
	u, _ := screen["updates"].(map[string]any)
	recs, _ := u["records"].([]any)
	if status != http.StatusOK || len(recs) != 1 || sync.cause != screen["request_id"] {
		t.Fatalf("a command's updates: %v (asked for cause %q)", screen, sync.cause)
	}

	_, boot := f.call(t, "GET", "/api/v1/bootstrap", token, nil)
	if feats, _ := boot["features"].(map[string]any); feats["updates"] != true {
		t.Errorf("bootstrap features = %v", boot["features"])
	}
}

func TestStateEndpointsOff(t *testing.T) {
	f := newAPIFixture(t)
	token := syncSignedIn(t, f)
	for _, path := range []string{"/api/v1/state", "/api/v1/updates?since=0"} {
		if status, out := f.call(t, "GET", path, token, nil); status != http.StatusNotFound || errCode(out) != "state_sync_off" {
			t.Errorf("%s with sync off: %d %v", path, status, out)
		}
	}
	_, screen := f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": "bank.show"})
	if _, ok := screen["updates"]; ok {
		t.Errorf("a command answered updates with sync off: %v", screen)
	}
	_, boot := f.call(t, "GET", "/api/v1/bootstrap", token, nil)
	if feats, _ := boot["features"].(map[string]any); feats["updates"] != false {
		t.Errorf("bootstrap features = %v", boot["features"])
	}
}
