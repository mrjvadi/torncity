package panel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

func grantBody(reason string) map[string]any {
	return map[string]any{"player": "AB12", "amount": 500, "reason": reason}
}

func TestReadsNeedASession(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodGet, "/api/overview", nil, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/overview", nil, "forged-token", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie: %d", rec.Code)
	}
}

func TestPlayerSearchAndDetail(t *testing.T) {
	h := newHarness(t)
	h.backend.players = []postgres.PlayerHit{{Code: "AB12", Name: "Sara", Username: "sara"}, {Code: "CD34", Name: "Ali"}}
	cookie, _, _ := h.login("javad", testPassword, "")

	rec := h.do(http.MethodGet, "/api/players?q=@sara", nil, cookie, nil)
	var hits []postgres.PlayerHit
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil || rec.Code != http.StatusOK || len(hits) != 1 || hits[0].Code != "AB12" {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodGet, "/api/players?q=nobody", nil, cookie, nil); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("an empty search is not []: %s", rec.Body)
	}
	if rec := h.do(http.MethodGet, "/api/players/ZZ99", nil, cookie, nil); rec.Code != http.StatusNotFound || errCode(rec) != "not_found" {
		t.Fatalf("unknown player: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodGet, "/api/overview?days=0", nil, cookie, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("days=0: %d", rec.Code)
	}
}

func TestChangesNeedCSRFAndTheOrigin(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	key := map[string]string{idempotencyHeader: "key-00000001"}

	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("test"), cookie, key); rec.Code != http.StatusForbidden || errCode(rec) != "csrf" {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	bad := map[string]string{idempotencyHeader: "key-00000001", csrfHeader: csrf + "x"}
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("test"), cookie, bad); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	foreign := map[string]string{idempotencyHeader: "key-00000001", csrfHeader: csrf, "Origin": "https://evil.example"}
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("test"), cookie, foreign); rec.Code != http.StatusForbidden || errCode(rec) != "forbidden_origin" {
		t.Fatalf("foreign origin: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "javad", "password": testPassword}, "",
		map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Fatalf("login from a foreign origin: %d", rec.Code)
	}
	if len(h.backend.grants) != 0 {
		t.Fatal("a refused request granted money")
	}
}

func TestChangesNeedAReasonAndAKey(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{idempotencyHeader: "key-00000002", csrfHeader: csrf}
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("   "), cookie, hdr); rec.Code != http.StatusBadRequest || errCode(rec) != "reason_required" {
		t.Fatalf("blank reason: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("ok"), cookie, map[string]string{csrfHeader: csrf}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no key: %d", rec.Code)
	}
	body := grantBody("ok")
	body["amount"] = -5
	if rec := h.do(http.MethodPost, "/api/economy/grant", body, cookie, hdr); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative amount: %d %s", rec.Code, rec.Body)
	}
	body = grantBody("ok")
	body["extra"] = true
	if rec := h.do(http.MethodPost, "/api/economy/grant", body, cookie, map[string]string{idempotencyHeader: "key-00000003", csrfHeader: csrf}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if len(h.backend.grants) != 0 {
		t.Fatal("a refused request granted money")
	}
}

func TestAGrantIsAttributedAndIdempotent(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{idempotencyHeader: "key-00000004", csrfHeader: csrf}
	first := h.do(http.MethodPost, "/api/economy/grant", grantBody("compensation for the outage"), cookie, hdr)
	if first.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", first.Code, first.Body)
	}
	again := h.do(http.MethodPost, "/api/economy/grant", grantBody("compensation for the outage"), cookie, hdr)
	var a1, a2 map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &a1)
	_ = json.Unmarshal(again.Body.Bytes(), &a2)
	if again.Code != http.StatusOK || again.Header().Get("Idempotent-Replay") != "true" || !reflect.DeepEqual(a1, a2) {
		t.Fatalf("replay: %d %q %s", again.Code, again.Header().Get("Idempotent-Replay"), again.Body)
	}
	if len(h.backend.grants) != 1 {
		t.Fatalf("the grant ran %d times", len(h.backend.grants))
	}
	a := h.backend.grants[0]
	if a.Name != "panel:javad" || a.Reason != "compensation for the outage" {
		t.Fatalf("actor: %+v", a)
	}
	other := h.do(http.MethodPost, "/api/watch/flags/7/clear", map[string]any{"reason": "looked"}, cookie, hdr)
	if other.Code != http.StatusUnprocessableEntity || errCode(other) != "key_reused" {
		t.Fatalf("a key reused on another route: %d %s", other.Code, other.Body)
	}
}

func TestAFailedChangeCanBeRetried(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{idempotencyHeader: "key-00000005", csrfHeader: csrf}
	h.backend.failing = errors.New("postgres: no player with that code")
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("x"), cookie, hdr); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failing grant: %d %s", rec.Code, rec.Body)
	} else if strings.Contains(rec.Body.String(), "postgres:") {
		t.Fatalf("the package prefix leaked: %s", rec.Body)
	}
	h.backend.failing = nil
	if rec := h.do(http.MethodPost, "/api/economy/grant", grantBody("x"), cookie, hdr); rec.Code != http.StatusOK || rec.Header().Get("Idempotent-Replay") != "" {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
}

func TestContentLoadNeedsTheReviewedChecksum(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{idempotencyHeader: "key-00000006", csrfHeader: csrf}
	if rec := h.do(http.MethodPost, "/api/content/load", map[string]any{"reason": "r", "confirm_checksum": "abc"}, cookie, hdr); rec.Code != http.StatusBadRequest {
		t.Fatalf("stale checksum: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodPost, "/api/content/load", map[string]any{"reason": "r", "confirm_checksum": "def"}, cookie, hdr); rec.Code != http.StatusOK {
		t.Fatalf("reviewed checksum: %d %s", rec.Code, rec.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/api/overview", nil, "", nil)
	for name, want := range map[string]string{
		"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"Cache-Control": "no-store", "Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP: %s", csp)
	}
}

func TestClientIPTrustsOnlyTheProxies(t *testing.T) {
	h := newHarness(t)
	s, _ := New(Options{Config: h.cfg, Backend: h.backend, Accounts: h.accounts})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:1"
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := s.clientIP(req); got != "192.0.2.10" {
		t.Fatalf("an untrusted peer's header was believed: %s", got)
	}
	req.RemoteAddr = "10.1.2.3:1"
	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("the proxy's CF-Connecting-IP: %s", got)
	}
	req.Header.Del("CF-Connecting-IP")
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.7, 10.0.0.5")
	if got := s.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("the rightmost untrusted hop: %s", got)
	}
}
