package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

// AN OPERATOR'S OWN ACCOUNT.
//
// Accounts are still made, disabled and re-keyed by someone else only on
// the server (`admin panel user`). What an operator may do for themself is
// change their own password and enrol in or leave two-factor sign-in — each
// only with their current password, and their current one-time code when
// enrolled — and see and end their own sessions. A password or two-factor
// change ends every session the account has, as on the command line; the
// browser that made it is signed in again at once.

// SelfAccounts is the account store's part that self-service needs;
// postgres.PanelAccounts has it.
type SelfAccounts interface {
	SetPassword(ctx context.Context, ch postgres.PanelAccountChange, passwordHash string) error
	SetTOTP(ctx context.Context, ch postgres.PanelAccountChange, secret string) error
	Sessions(ctx context.Context, accountID, current string, now time.Time) ([]postgres.PanelSessionLine, error)
	RevokeSession(ctx context.Context, accountID, handle string, at time.Time) (bool, error)
}

var _ SelfAccounts = (*postgres.PanelAccounts)(nil)

// selfReason is the reason an operator's own change is recorded with.
const selfReason = "self-service from the panel"

func (s *Server) self() (SelfAccounts, bool) {
	sa, ok := s.accounts.(SelfAccounts)
	return sa, ok
}

// me answers who is signed in: the account's standing, without secrets.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	acct, err := s.accounts.ByUsername(r.Context(), sess.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": acct.Username, "totp_enabled": acct.TOTPEnabled,
		"last_login_at": acct.LastLoginAt, "created_at": acct.CreatedAt, "created_by": acct.CreatedBy,
		"password_min_length": s.cfg.PasswordMinLength, "session_expires_at": sess.ExpiresAt})
}

func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.self()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	sess := sessionOf(r)
	list, err := sa.Sessions(r.Context(), sess.AccountID, sess.TokenHash, s.now())
	if err != nil {
		s.log.Error("panel: listing sessions", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if list == nil {
		list = []postgres.PanelSessionLine{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) revokeMySession(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.self()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	sess := sessionOf(r)
	now := s.now()
	if ok, wait := s.mutating.allow(sess.AccountID, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "")
		return
	}
	var body struct{}
	if err := decodeJSON(w, r, s.cfg.MaxBodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	id := r.PathValue("id")
	ended, err := sa.RevokeSession(r.Context(), sess.AccountID, id, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	if !ended {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := s.accounts.AuditPanel(r.Context(), "panel:"+sess.Username, "panel.session_revoke", selfReason,
		map[string]any{"username": sess.Username, "session": id}, now); err != nil {
		s.log.Error("panel: writing an audit row", "error", err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": id, "current": strings.HasPrefix(sess.TokenHash, id)})
}

// selfCheck is the common start of a self-service change: the store, the
// rate limit, the body, and the current password (and one-time code when
// enrolled) verified at the cost of a sign-in.
func (s *Server) selfCheck(w http.ResponseWriter, r *http.Request, into any, password func() string, code func() string,
) (SelfAccounts, *postgres.PanelAccount, bool) {
	sa, ok := s.self()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return nil, nil, false
	}
	sess := sessionOf(r)
	now := s.now()
	if ok, wait := s.mutating.allow(sess.AccountID, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "")
		return nil, nil, false
	}
	if err := decodeJSON(w, r, s.cfg.MaxBodyBytes, into); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return nil, nil, false
	}
	acct, err := s.accounts.ByUsername(r.Context(), sess.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return nil, nil, false
	}
	select {
	case s.verifying <- struct{}{}:
		defer func() { <-s.verifying }()
	case <-r.Context().Done():
		return nil, nil, false
	}
	pw := password()
	good := false
	if pw != "" && len(pw) <= 256 {
		good, _ = credential.VerifyPassword(acct.PasswordHash, pw)
	} else {
		credential.BurnTime(pw)
	}
	if good && acct.TOTPEnabled {
		good = credential.VerifyTOTP(acct.TOTPSecret, code(), now)
	}
	if !good {
		s.auditLogin(r.Context(), "panel:"+sess.Username, "panel.self_denied", sess.Username, s.clientIP(r), now)
		writeError(w, http.StatusForbidden, "bad_credentials", "")
		return nil, nil, false
	}
	return sa, acct, true
}

func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
		Code    string `json:"code"`
	}
	sa, acct, ok := s.selfCheck(w, r, &body, func() string { return body.Current }, func() string { return body.Code })
	if !ok {
		return
	}
	n := len([]rune(body.New))
	switch {
	case n < s.cfg.PasswordMinLength || len(body.New) > 256:
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("a password is %d to 256 characters", s.cfg.PasswordMinLength))
		return
	case body.New == body.Current:
		writeError(w, http.StatusBadRequest, "bad_request", "the new password is the current one")
		return
	}
	hash, err := credential.HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	now := s.now()
	if err := sa.SetPassword(r.Context(), postgres.PanelAccountChange{Username: acct.Username,
		Actor: "panel:" + acct.Username, Reason: selfReason, At: now}, hash); err != nil {
		s.log.Error("panel: changing a password", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	s.startSession(w, r, acct, s.clientIP(r), now)
}

func (s *Server) beginMyTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_, acct, ok := s.selfCheck(w, r, &body, func() string { return body.Password }, func() string { return "" })
	if !ok {
		return
	}
	if acct.TOTPEnabled {
		writeError(w, http.StatusConflict, "conflict", "two-factor sign-in is already on")
		return
	}
	secret, err := credential.NewTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret,
		"uri": credential.TOTPURI(s.cfg.TOTPIssuer, acct.Username, secret)})
}

func (s *Server) enableMyTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Secret   string `json:"secret"`
		Code     string `json:"code"`
	}
	sa, acct, ok := s.selfCheck(w, r, &body, func() string { return body.Password }, func() string { return "" })
	if !ok {
		return
	}
	now := s.now()
	secret := strings.ToUpper(strings.TrimSpace(body.Secret))
	switch {
	case acct.TOTPEnabled:
		writeError(w, http.StatusConflict, "conflict", "two-factor sign-in is already on")
		return
	case len(secret) < 16 || len(secret) > 64 || !credential.VerifyTOTP(secret, body.Code, now):
		writeError(w, http.StatusBadRequest, "bad_code", "")
		return
	}
	if err := sa.SetTOTP(r.Context(), postgres.PanelAccountChange{Username: acct.Username,
		Actor: "panel:" + acct.Username, Reason: selfReason, At: now}, secret); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	s.startSession(w, r, acct, s.clientIP(r), now)
}

func (s *Server) disableMyTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	sa, acct, ok := s.selfCheck(w, r, &body, func() string { return body.Password }, func() string { return body.Code })
	if !ok {
		return
	}
	if !acct.TOTPEnabled {
		writeError(w, http.StatusConflict, "conflict", "two-factor sign-in is not on")
		return
	}
	now := s.now()
	if err := sa.SetTOTP(r.Context(), postgres.PanelAccountChange{Username: acct.Username,
		Actor: "panel:" + acct.Username, Reason: selfReason, At: now}, ""); err != nil {
		if errors.Is(err, postgres.ErrNoPanelAccount) {
			writeError(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	s.startSession(w, r, acct, s.clientIP(r), now)
}
