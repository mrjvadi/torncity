package panel

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

const (
	testOrigin   = "https://panel.example.test"
	testPassword = "a long enough password"
)

// passwordHash is hashed once: argon2id is slow on purpose.
var passwordHash = sync.OnceValue(func() string {
	h, err := credential.HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	t        *testing.T
	srv      http.Handler
	accounts *fakeAccounts
	backend  *fakeBackend
	clock    *clock
	cfg      config.Panel
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := config.Defaults().Panel
	cfg.PublicURL = testOrigin
	cfg.TrustedProxies = []string{"10.0.0.0/8"}
	h := &harness{t: t, accounts: newFakeAccounts(), backend: &fakeBackend{}, clock: &clock{t: time.Unix(1_800_000_000, 0)}, cfg: cfg}
	h.accounts.add(postgres.PanelAccount{ID: "a1", Username: "javad", PasswordHash: passwordHash(), Status: "active"})
	s, err := New(Options{Config: cfg, Backend: h.backend, Accounts: h.accounts, Now: h.clock.now,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h.srv = s.Handler()
	return h
}

// do sends one request from ip with the given cookie and headers.
func (h *harness) do(method, path string, body any, cookie string, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.10:5555"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", testOrigin)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

// login signs in and returns the cookie value and CSRF token.
func (h *harness) login(user, password, code string) (string, string, *httptest.ResponseRecorder) {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": password, "code": code}, "", nil)
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			cookie = c.Value
		}
	}
	var reply sessionReply
	_ = json.Unmarshal(rec.Body.Bytes(), &reply)
	return cookie, reply.CSRF, rec
}

func errCode(rec *httptest.ResponseRecorder) string {
	var e apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

func TestLoginSetsAStrictSessionCookie(t *testing.T) {
	h := newHarness(t)
	_, csrf, rec := h.login("Javad", testPassword, "")
	if rec.Code != http.StatusOK || csrf == "" {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var c *http.Cookie
	for _, x := range rec.Result().Cookies() {
		if x.Name == cookieName {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie attributes: %+v", c)
	}
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Fatal("the session token is in the body")
	}
	if got := h.accounts.audits; len(got) != 1 || got[0] != "panel:javad panel.login" {
		t.Fatalf("audit rows: %v", got)
	}
	for hash := range h.accounts.sessions {
		if hash == c.Value {
			t.Fatal("the store holds the token itself, not its hash")
		}
	}
}

func TestWrongPasswordAndUnknownUserLookAlike(t *testing.T) {
	h := newHarness(t)
	_, _, wrong := h.login("javad", "not the password at all", "")
	_, _, unknown := h.login("nobody", testPassword, "")
	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("codes: %d %d", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("bodies differ: %q vs %q", wrong.Body, unknown.Body)
	}
	for _, a := range h.accounts.audits {
		if strings.Contains(a, testPassword) {
			t.Fatal("a password reached the audit trail")
		}
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < h.cfg.LockoutAfter; i++ {
		h.clock.advance(7 * time.Second) // under the per-address limit
		if _, _, rec := h.login("javad", "wrong password number", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, rec.Code)
		}
	}
	h.clock.advance(7 * time.Second)
	_, _, rec := h.login("javad", testPassword, "")
	if rec.Code != http.StatusTooManyRequests || errCode(rec) != "locked" {
		t.Fatalf("the right password during a lock: %d %s", rec.Code, rec.Body)
	}
	// A name without an account locks the same way.
	for i := 0; i < h.cfg.LockoutAfter; i++ {
		h.clock.advance(7 * time.Second)
		h.login("ghost", "whatever password", "")
	}
	h.clock.advance(7 * time.Second)
	if _, _, rec := h.login("ghost", "whatever password", ""); errCode(rec) != "locked" {
		t.Fatalf("an unknown name was not locked: %d %s", rec.Code, rec.Body)
	}
	// After the lock the right password works and clears the count.
	h.clock.advance(h.cfg.LockoutBase + time.Second)
	if _, _, rec := h.login("javad", testPassword, ""); rec.Code != http.StatusOK {
		t.Fatalf("after the lock: %d %s", rec.Code, rec.Body)
	}
}

func TestLockDoublesUpToTheCap(t *testing.T) {
	base, max := time.Minute, 10*time.Minute
	for n, want := range map[int]time.Duration{5: time.Minute, 6: 2 * time.Minute, 8: 8 * time.Minute, 9: max, 40: max} {
		if got := lockFor(n, 5, base, max); got != want {
			t.Errorf("failure %d: lock %s, want %s", n, got, want)
		}
	}
}

func TestLoginRateLimitPerAddress(t *testing.T) {
	h := newHarness(t)
	var last *httptest.ResponseRecorder
	for i := 0; i <= h.cfg.LoginPerMinute; i++ {
		_, _, last = h.login("user"+string(rune('a'+i)), "x-password-x", "")
	}
	if last.Code != http.StatusTooManyRequests || errCode(last) != "rate_limited" || last.Header().Get("Retry-After") == "" {
		t.Fatalf("attempt past the limit: %d %s", last.Code, last.Body)
	}
}

func TestDisabledAccountCannotSignInAndLosesItsSession(t *testing.T) {
	h := newHarness(t)
	cookie, _, _ := h.login("javad", testPassword, "")
	h.accounts.mu.Lock()
	h.accounts.accounts["javad"].Status = "disabled"
	h.accounts.mu.Unlock()
	if rec := h.do(http.MethodGet, "/api/auth/session", nil, cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account's session still works: %d", rec.Code)
	}
	if _, _, rec := h.login("javad", testPassword, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account signed in: %d", rec.Code)
	}
}

func TestTOTPIsEnforcedWhenEnrolled(t *testing.T) {
	h := newHarness(t)
	secret, _ := credential.NewTOTPSecret()
	h.accounts.mu.Lock()
	h.accounts.accounts["javad"].TOTPEnabled, h.accounts.accounts["javad"].TOTPSecret = true, secret
	h.accounts.mu.Unlock()
	if _, _, rec := h.login("javad", testPassword, ""); rec.Code != http.StatusUnauthorized || errCode(rec) != errWrong {
		t.Fatalf("no code: %d %s", rec.Code, rec.Body)
	}
	h.clock.advance(7 * time.Second)
	code, _ := credential.TOTPCode(secret, h.clock.now())
	if _, _, rec := h.login("javad", testPassword, code); rec.Code != http.StatusOK {
		t.Fatalf("with the code: %d %s", rec.Code, rec.Body)
	}
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	h := newHarness(t)
	cookie, _, _ := h.login("javad", testPassword, "")
	if rec := h.do(http.MethodGet, "/api/auth/session", nil, cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("fresh session: %d", rec.Code)
	}
	h.clock.advance(h.cfg.SessionIdle + time.Second)
	if rec := h.do(http.MethodGet, "/api/auth/session", nil, cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("idle session still works: %d", rec.Code)
	}

	cookie, _, _ = h.login("javad", testPassword, "")
	for step := time.Duration(0); step < h.cfg.SessionAbsolute; step += h.cfg.SessionIdle / 2 {
		h.clock.advance(h.cfg.SessionIdle / 2)
		h.do(http.MethodGet, "/api/auth/session", nil, cookie, nil)
	}
	if rec := h.do(http.MethodGet, "/api/auth/session", nil, cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a session past its absolute expiry still works: %d", rec.Code)
	}
}

func TestLogoutEndsTheSessionAndLoginRotatesIt(t *testing.T) {
	h := newHarness(t)
	first, _, _ := h.login("javad", testPassword, "")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"javad","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: first})
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second login: %d", rec.Code)
	}
	if got := h.do(http.MethodGet, "/api/auth/session", nil, first, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("the session held before signing in again survived")
	}
	var second, csrf string
	for _, c := range rec.Result().Cookies() {
		second = c.Value
	}
	var reply sessionReply
	_ = json.Unmarshal(rec.Body.Bytes(), &reply)
	csrf = reply.CSRF
	if out := h.do(http.MethodPost, "/api/auth/logout", map[string]any{}, second, map[string]string{csrfHeader: csrf}); out.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", out.Code, out.Body)
	}
	if got := h.do(http.MethodGet, "/api/auth/session", nil, second, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("the session survived sign-out")
	}
}
