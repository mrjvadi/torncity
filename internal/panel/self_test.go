package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

// The fake store's self-service, with the database's semantics: a
// password or two-factor change ends every session of the account.

func (f *fakeAccounts) revokeAll(username string) {
	a := f.accounts[username]
	for h, s := range f.sessions {
		if s.AccountID == a.ID {
			f.revoked[h] = true
		}
	}
}

func (f *fakeAccounts) SetPassword(_ context.Context, ch postgres.PanelAccountChange, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[ch.Username].PasswordHash = hash
	f.revokeAll(ch.Username)
	f.audits = append(f.audits, ch.Actor+" panel.password_change")
	return nil
}

func (f *fakeAccounts) SetTOTP(_ context.Context, ch postgres.PanelAccountChange, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.accounts[ch.Username]
	a.TOTPSecret, a.TOTPEnabled = secret, secret != ""
	f.revokeAll(ch.Username)
	return nil
}

func (f *fakeAccounts) Sessions(_ context.Context, accountID, current string, _ time.Time) ([]postgres.PanelSessionLine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.PanelSessionLine
	for h, s := range f.sessions {
		if s.AccountID == accountID && !f.revoked[h] {
			out = append(out, postgres.PanelSessionLine{ID: h[:16], Current: h == current})
		}
	}
	return out, nil
}

func (f *fakeAccounts) RevokeSession(_ context.Context, accountID, handle string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for h, s := range f.sessions {
		if s.AccountID == accountID && strings.HasPrefix(h, handle) && !f.revoked[h] {
			f.revoked[h] = true
			return true, nil
		}
	}
	return false, nil
}

func TestChangeOwnPassword(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{csrfHeader: csrf}
	rec := h.do(http.MethodPost, "/api/me/password", map[string]string{"current": "wrong password!", "new": "another long password"},
		cookie, hdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a wrong current password: %d", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/me/password", map[string]string{"current": testPassword, "new": "short"}, cookie, hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a short password: %d", rec.Code)
	}
	rec = h.do(http.MethodPost, "/api/me/password", map[string]string{"current": testPassword, "new": "another long password"},
		cookie, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	var fresh string
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			fresh = c.Value
		}
	}
	if fresh == "" || fresh == cookie {
		t.Fatal("no fresh session after the change")
	}
	if rec := h.do(http.MethodGet, "/api/me", nil, cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the old session survived: %d", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/me", nil, fresh, nil); rec.Code != http.StatusOK {
		t.Fatalf("the fresh session: %d", rec.Code)
	}
	if _, _, rec := h.login("javad", "another long password", ""); rec.Code != http.StatusOK {
		t.Fatalf("signing in with the new password: %d", rec.Code)
	}
}

func TestEnrolAndLeaveTwoFactor(t *testing.T) {
	h := newHarness(t)
	cookie, csrf, _ := h.login("javad", testPassword, "")
	hdr := map[string]string{csrfHeader: csrf}
	rec := h.do(http.MethodPost, "/api/me/totp/begin", map[string]string{"password": testPassword}, cookie, hdr)
	var begun struct{ Secret, URI string }
	if err := json.Unmarshal(rec.Body.Bytes(), &begun); err != nil || rec.Code != http.StatusOK || begun.Secret == "" ||
		!strings.HasPrefix(begun.URI, "otpauth://totp/") {
		t.Fatalf("begin: %d %s", rec.Code, rec.Body)
	}
	rec = h.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"password": testPassword, "secret": begun.Secret,
		"code": "000000"}, cookie, hdr)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a wrong code: %d", rec.Code)
	}
	code, _ := credential.TOTPCode(begun.Secret, h.clock.now())
	rec = h.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"password": testPassword, "secret": begun.Secret,
		"code": code}, cookie, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if !h.accounts.accounts["javad"].TOTPEnabled {
		t.Fatal("not enrolled")
	}
	fresh, csrf2, _ := h.login("javad", testPassword, code)
	hdr2 := map[string]string{csrfHeader: csrf2}
	if rec := h.do(http.MethodPost, "/api/me/totp/disable", map[string]string{"password": testPassword}, fresh, hdr2); rec.Code != http.StatusForbidden {
		t.Fatalf("leaving without a code: %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, "/api/me/totp/disable", map[string]string{"password": testPassword, "code": code}, fresh, hdr2); rec.Code != http.StatusOK {
		t.Fatalf("leaving: %d %s", rec.Code, rec.Body)
	}
}

func TestOwnSessions(t *testing.T) {
	h := newHarness(t)
	a, _, _ := h.login("javad", testPassword, "")
	b, csrfB, _ := h.login("javad", testPassword, "")
	rec := h.do(http.MethodGet, "/api/me/sessions", nil, b, nil)
	var list []postgres.PanelSessionLine
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("sessions: %s", rec.Body)
	}
	var other string
	for _, s := range list {
		if !s.Current {
			other = s.ID
		}
	}
	if rec := h.do(http.MethodPost, "/api/me/sessions/"+other+"/revoke", map[string]string{}, b,
		map[string]string{csrfHeader: csrfB}); rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do(http.MethodGet, "/api/me", nil, a, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the revoked session: %d", rec.Code)
	}
}
