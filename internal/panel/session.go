package panel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

// cookieName carries the session. The __Host- prefix makes the browser
// refuse it unless it is Secure, host-only and for the whole site.
const cookieName = "__Host-tc_panel"

// csrfHeader carries the session's CSRF token on every change.
const csrfHeader = "X-CSRF-Token"

// touchEvery bounds how often a session's last-seen time is written.
const touchEvery = time.Minute

// randomToken is 32 random bytes, base64url.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("panel: no randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenHash is what the database keeps of a session token.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// sessionKey is the request context's session.
type sessionKey struct{}

func sessionOf(r *http.Request) *postgres.PanelSession {
	s, _ := r.Context().Value(sessionKey{}).(*postgres.PanelSession)
	return s
}

// loginRequest is what the sign-in form posts.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// sessionReply is what a signed-in browser learns about its session.
type sessionReply struct {
	Username  string    `json:"username"`
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
	IdleFor   string    `json:"idle_timeout"`
}

// errWrong is the one answer to every wrong sign-in, whatever was wrong.
const errWrong = "bad_credentials"

// login checks a username, password and (when enrolled) one-time code.
//
// Every wrong answer is the same 401, costs the same argon2id check, and
// counts against both the address and the name; a name without an account
// is locked in memory exactly as a real account is locked in the database.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	ip := s.clientIP(r)
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "forbidden_origin", "")
		return
	}
	if ok, wait := s.loginIP.allow(ip, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "")
		return
	}
	var req loginRequest
	if err := decodeJSON(w, r, s.cfg.MaxBodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Username))
	if name == "" || len(name) > 64 || req.Password == "" || len(req.Password) > 256 {
		writeError(w, http.StatusUnauthorized, errWrong, "")
		return
	}
	select {
	case s.verifying <- struct{}{}:
		defer func() { <-s.verifying }()
	case <-r.Context().Done():
		return
	}

	acct, err := s.accounts.ByUsername(r.Context(), name)
	if err != nil && !errors.Is(err, postgres.ErrNoPanelAccount) {
		s.log.Error("panel: reading an account", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if acct == nil {
		credential.BurnTime(req.Password)
		if until := s.unknown.lockedUntil(name, now); !until.IsZero() {
			s.lockedReply(w, until, now)
			return
		}
		s.unknown.fail(name, now)
		s.auditLogin(r.Context(), "panel:?", "panel.login_failed", name, ip, now)
		writeError(w, http.StatusUnauthorized, errWrong, "")
		return
	}
	if acct.LockedUntil != nil && acct.LockedUntil.After(now) {
		credential.BurnTime(req.Password)
		s.lockedReply(w, *acct.LockedUntil, now)
		return
	}
	ok, err := credential.VerifyPassword(acct.PasswordHash, req.Password)
	if err != nil {
		s.log.Error("panel: a stored password hash is malformed", slog.String("username", acct.Username))
	}
	if ok && acct.TOTPEnabled {
		ok = credential.VerifyTOTP(acct.TOTPSecret, req.Code, now)
	}
	if !ok || !acct.Active() {
		if _, err := s.accounts.LoginFailure(r.Context(), acct.ID, now, s.cfg.LockoutAfter, s.cfg.LockoutBase, s.cfg.LockoutMax); err != nil {
			s.log.Error("panel: recording a failed sign-in", slog.String("error", err.Error()))
		}
		s.auditLogin(r.Context(), "panel:?", "panel.login_failed", name, ip, now)
		writeError(w, http.StatusUnauthorized, errWrong, "")
		return
	}
	s.startSession(w, r, acct, ip, now)
}

func (s *Server) lockedReply(w http.ResponseWriter, until, now time.Time) {
	w.Header().Set("Retry-After", fmt.Sprint(int(until.Sub(now).Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "locked", "")
}

// startSession issues a fresh session (rotating out any the browser held)
// and its cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, acct *postgres.PanelAccount, ip string, now time.Time) {
	token, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	csrf, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	var replaced string
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		replaced = tokenHash(c.Value)
	}
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = strings.ToValidUTF8(ua[:200], "")
	}
	sess := postgres.PanelSession{TokenHash: tokenHash(token), AccountID: acct.ID, CSRFToken: csrf, CreatedAt: now,
		LastSeenAt: now, ExpiresAt: now.Add(s.cfg.SessionAbsolute), ClientIP: ip, UserAgent: ua, Username: acct.Username}
	if err := s.accounts.CreateSession(r.Context(), sess, replaced); err != nil {
		s.log.Error("panel: creating a session", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if err := s.accounts.LoginSuccess(r.Context(), acct.ID, now); err != nil {
		s.log.Error("panel: recording a sign-in", slog.String("error", err.Error()))
	}
	s.auditLogin(r.Context(), "panel:"+acct.Username, "panel.login", acct.Username, ip, now)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Expires: sess.ExpiresAt,
		MaxAge: int(s.cfg.SessionAbsolute.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, s.reply(&sess))
}

func (s *Server) reply(sess *postgres.PanelSession) sessionReply {
	return sessionReply{Username: sess.Username, CSRF: sess.CSRFToken, ExpiresAt: sess.ExpiresAt, IdleFor: s.cfg.SessionIdle.String()}
}

// auditLogin records a sign-in or a failed one: the name as typed (cut to
// 64 characters), the address, never the password.
func (s *Server) auditLogin(ctx context.Context, actor, action, name, ip string, now time.Time) {
	if utf8.RuneCountInString(name) > 64 {
		name = string([]rune(name)[:64])
	}
	if err := s.accounts.AuditPanel(ctx, actor, action, "sign-in", map[string]any{"username": name, "ip": ip}, now); err != nil {
		s.log.Error("panel: writing a sign-in audit row", slog.String("error", err.Error()))
	}
}

// logout ends the session and clears the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	now := s.now()
	if err := s.accounts.EndSession(r.Context(), sess.TokenHash, now); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if err := s.accounts.AuditPanel(r.Context(), "panel:"+sess.Username, "panel.logout", "sign-out",
		map[string]any{"username": sess.Username}, now); err != nil {
		s.log.Error("panel: writing a sign-out audit row", slog.String("error", err.Error()))
	}
	clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode})
}

// authed requires a live session; for any method but GET it also requires
// the panel's own origin and the session's CSRF token.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" || len(c.Value) > 100 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		sess, err := s.accounts.Session(r.Context(), tokenHash(c.Value), s.now(), s.cfg.SessionIdle, touchEvery)
		if errors.Is(err, postgres.ErrNoPanelSession) {
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		if err != nil {
			s.log.Error("panel: reading a session", slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "internal", "")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.sameOrigin(r) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "")
				return
			}
			got := r.Header.Get(csrfHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
				writeError(w, http.StatusForbidden, "csrf", "")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	}
}

// session answers who is signed in, and the CSRF token to change things with.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.reply(sessionOf(r)))
}

// decodeJSON reads one JSON object of at most limit bytes, refusing unknown
// fields and trailing data.
func decodeJSON(w http.ResponseWriter, r *http.Request, limit int, into any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errors.New("not json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(limit)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}
