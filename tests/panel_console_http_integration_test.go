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

// TestPanelConsoleOverHTTP signs in to a panel over the real database and
// reads every console endpoint that is not a list (those are covered one by
// one in TestPanelViewsRun): the dossiers, the figures, the series, the
// system page, the search, the content diff and the operator's own account.
func TestPanelConsoleOverHTTP(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	accounts := postgres.NewPanelAccounts(pool)
	user := fmt.Sprintf("con-%d", time.Now().UnixNano()%1_000_000_000)
	const password = "integration console password"
	hash, err := credential.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Create(ctx, postgres.PanelAccountChange{Username: user, Actor: "integration", Reason: "test", At: time.Now()}, hash); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, q := range []string{
			`DELETE FROM panel_requests WHERE account_id = (SELECT id FROM panel_accounts WHERE username = $1)`,
			`DELETE FROM panel_sessions WHERE account_id = (SELECT id FROM panel_accounts WHERE username = $1)`,
			`DELETE FROM panel_accounts WHERE username = $1`,
		} {
			_, _ = pool.Raw().Exec(bg, q, user)
		}
	})
	p := insertPlayer(t, pool)
	var city, country string
	_ = pool.Raw().QueryRow(ctx, `SELECT c.code, k.code FROM cities c JOIN jurisdictions j ON j.id = c.jurisdiction_id
		JOIN jurisdictions k ON k.id = j.parent_id ORDER BY c.code LIMIT 1`).Scan(&city, &country)

	cfg := config.Defaults()
	pg := &panel.PG{Pool: pool, Ops: operator.Ops{Pool: pool, Language: "fa"}, ContentDir: "../configs/content", Config: cfg}
	srv, err := panel.New(panel.Options{Config: cfg.Panel, Accounts: accounts, Backend: pg, Console: pg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"username": user, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	resp, err := ts.Client().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %v %v", resp, err)
	}
	resp.Body.Close()
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == "__Host-tc_panel" {
			cookie = c.Value
		}
	}

	paths := []string{
		"/api/dossier/players/" + p.PublicCode,
		"/api/kpis",
		"/api/series/economy?days=30",
		"/api/series/players.new?days=7",
		"/api/system",
		"/api/search?q=a",
		"/api/content/diff",
		"/api/content/sections/cities",
		"/api/me",
		"/api/me/sessions",
		"/api/views",
		"/api/views/ledger?player=" + p.PublicCode + "&format=csv",
	}
	if city != "" {
		paths = append(paths, "/api/dossier/cities/"+city, "/api/dossier/countries/"+country)
	}
	for _, path := range paths {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: "__Host-tc_panel", Value: cookie})
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	for path, want := range map[string]int{
		"/api/dossier/players/ZZZZZZZ":  http.StatusNotFound,
		"/api/dossier/companies/ZZZZZZ": http.StatusNotFound,
		"/api/dossier/wars/999999":      http.StatusNotFound,
		"/api/series/nope":              http.StatusBadRequest,
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: "__Host-tc_panel", Value: cookie})
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
}
