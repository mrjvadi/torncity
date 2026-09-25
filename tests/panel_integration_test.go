//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/panel"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

// TestPanelSignInReadAndAuditedChange drives the web panel against the real
// database: an account made the way `admin panel user add` makes it, a
// sign-in, one read (the audit trail, which shows the sign-in) and one
// audited change (an announcement), retried with the same key.
func TestPanelSignInReadAndAuditedChange(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	accounts := postgres.NewPanelAccounts(pool)
	user := fmt.Sprintf("it-%d", time.Now().UnixNano()%1_000_000_000)
	const password = "integration test password"
	hash, err := credential.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Create(ctx, postgres.PanelAccountChange{Username: user, Actor: "integration", Reason: "test",
		At: time.Now()}, hash); err != nil {
		t.Fatalf("creating the account: %v", err)
	}
	var eventID string
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, q := range []string{
			`DELETE FROM panel_requests WHERE account_id = (SELECT id FROM panel_accounts WHERE username = $1)`,
			`DELETE FROM panel_sessions WHERE account_id = (SELECT id FROM panel_accounts WHERE username = $1)`,
			`DELETE FROM panel_accounts WHERE username = $1`,
		} {
			if _, err := pool.Raw().Exec(ctx, q, user); err != nil {
				t.Errorf("cleaning up: %v", err)
			}
		}
		if eventID != "" {
			if _, err := pool.Raw().Exec(ctx, `DELETE FROM outbox WHERE event_id = $1::uuid`, eventID); err != nil {
				t.Errorf("cleaning up the event: %v", err)
			}
		}
	})

	cfg := config.Defaults().Panel
	srv, err := panel.New(panel.Options{Config: cfg, Accounts: accounts,
		Backend: &panel.PG{Pool: pool, Ops: operator.Ops{Pool: pool, Language: "fa"}, ContentDir: "../configs/content"},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	call := func(method, path, cookie, csrf, key string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(ctx, method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", ts.URL)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-tc_panel", Value: cookie})
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if out == nil {
			out = map[string]any{"raw": string(raw)}
		}
		return resp, out
	}

	if resp, _ := call(http.MethodPost, "/api/auth/login", "", "", "", map[string]string{"username": user, "password": "wrong password here"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", resp.StatusCode)
	}
	resp, reply := call(http.MethodPost, "/api/auth/login", "", "", "", map[string]string{"username": user, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %d %v", resp.StatusCode, reply)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-tc_panel" {
			cookie = c.Value
		}
	}
	csrf, _ := reply["csrf"].(string)

	// The read: the audit trail shows the failed and the good sign-in.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/audit?action=panel.login&limit=20", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-tc_panel", Value: cookie})
	got, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var lines []postgres.AuditLine
	if err := json.NewDecoder(got.Body).Decode(&lines); err != nil || got.StatusCode != http.StatusOK {
		t.Fatalf("audit read: %d %v", got.StatusCode, err)
	}
	got.Body.Close()
	seen := map[string]bool{}
	for _, l := range lines {
		seen[l.Actor+" "+l.Action] = true
	}
	if !seen["panel:"+user+" panel.login"] || !seen["panel:? panel.login_failed"] {
		t.Fatalf("the sign-ins are not on the audit trail: %v", seen)
	}

	// The change, twice with one key: one announcement, one audit row.
	reason := "integration " + user
	body := map[string]any{"text": "panel integration test", "reason": reason}
	resp, out := call(http.MethodPost, "/api/announce", cookie, csrf, "it-key-"+user, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("announce: %d %v", resp.StatusCode, out)
	}
	eventID, _ = out["event_id"].(string)
	resp, _ = call(http.MethodPost, "/api/announce", cookie, csrf, "it-key-"+user, body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("the retry was not a replay: %d", resp.StatusCode)
	}
	var rows int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'admin.announce'
		AND actor = $1 AND reason = $2`, "panel:"+user, reason).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("the announcement wrote %d audit rows, want 1", rows)
	}
}
