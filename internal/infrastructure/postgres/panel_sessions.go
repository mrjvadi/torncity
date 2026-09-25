package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Sign-in state of the web panel: failed attempts and locks, sessions (only
// the SHA-256 of their token is stored) and the remembered answers of
// mutating requests.

// LoginFailure records one wrong password against the account and, from the
// lockAfter-th failure in a row on, locks it for base doubled per further
// failure, capped at max. It returns the lock, if any.
func (r *PanelAccounts) LoginFailure(ctx context.Context, id string, at time.Time, lockAfter int, base, max time.Duration,
) (*time.Time, error) {
	var locked *time.Time
	err := r.pool.QueryRow(ctx, `UPDATE panel_accounts SET failed_logins = failed_logins + 1,
		locked_until = CASE WHEN failed_logins + 1 >= $3 THEN
		    $2::timestamptz + LEAST($4::bigint * power(2, LEAST(failed_logins + 1 - $3, 30))::bigint, $5::bigint) * interval '1 millisecond'
		  ELSE locked_until END,
		updated_at = $2
		WHERE id = $1::uuid RETURNING locked_until`, id, at.UTC(), lockAfter, base.Milliseconds(), max.Milliseconds()).Scan(&locked)
	if err != nil {
		return nil, fmt.Errorf("postgres: recording a failed sign-in: %w", err)
	}
	return locked, nil
}

// LoginSuccess clears the failures and records the sign-in.
func (r *PanelAccounts) LoginSuccess(ctx context.Context, id string, at time.Time) error {
	if _, err := r.pool.Exec(ctx, `UPDATE panel_accounts SET failed_logins = 0, locked_until = NULL,
		last_login_at = $2, updated_at = $2 WHERE id = $1::uuid`, id, at.UTC()); err != nil {
		return fmt.Errorf("postgres: recording a sign-in: %w", err)
	}
	return nil
}

// AuditPanel writes one audit row for the panel's own events (sign-in,
// sign-out) outside any other change.
func (r *PanelAccounts) AuditPanel(ctx context.Context, actor, action, reason string, value map[string]any, at time.Time) error {
	return (&EconomyAdmin{q: r.pool}).AppendAudit(ctx, AuditEntry{Actor: actor, Action: action,
		TargetType: "panel_accounts", NewValue: value, Reason: reason, At: at})
}

// PanelSession is one signed-in browser.
type PanelSession struct {
	TokenHash, AccountID, CSRFToken string
	CreatedAt, LastSeenAt, ExpiresAt time.Time
	ClientIP, UserAgent             string
	// Username is the account's, as read with it.
	Username string
}

// ErrNoPanelSession means the token names no live session.
var ErrNoPanelSession = errors.New("postgres: no live panel session")

// CreateSession stores a new session and, in the same transaction, ends the
// one the browser held before (replaced, may be empty) — the rotation at
// sign-in.
func (r *PanelAccounts) CreateSession(ctx context.Context, s PanelSession, replaced string) error {
	return r.inPanelTx(ctx, func(tx pgx.Tx) error {
		if replaced != "" {
			if _, err := tx.Exec(ctx, `UPDATE panel_sessions SET revoked_at = $2
				WHERE token_hash = $1 AND revoked_at IS NULL`, replaced, s.CreatedAt.UTC()); err != nil {
				return fmt.Errorf("postgres: ending the replaced session: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO panel_sessions (token_hash, account_id, csrf_token, created_at,
			last_seen_at, expires_at, client_ip, user_agent) VALUES ($1, $2::uuid, $3, $4, $4, $5, $6, $7)`,
			s.TokenHash, s.AccountID, s.CSRFToken, s.CreatedAt.UTC(), s.ExpiresAt.UTC(), s.ClientIP, s.UserAgent); err != nil {
			return fmt.Errorf("postgres: creating a panel session: %w", err)
		}
		return nil
	})
}

// Session reads the live session behind tokenHash at now: not ended, not
// past its absolute expiry, seen within idle, its account active. A session
// read is touched, at most once a touch interval, so idle expiry slides.
func (r *PanelAccounts) Session(ctx context.Context, tokenHash string, now time.Time, idle, touch time.Duration,
) (*PanelSession, error) {
	var s PanelSession
	err := r.pool.QueryRow(ctx, `SELECT s.token_hash, s.account_id::text, s.csrf_token, s.created_at, s.last_seen_at,
		s.expires_at, s.client_ip, s.user_agent, a.username
		FROM panel_sessions s JOIN panel_accounts a ON a.id = s.account_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2
		  AND s.last_seen_at > $2::timestamptz - $3::bigint * interval '1 millisecond' AND a.status = 'active'`,
		tokenHash, now.UTC(), idle.Milliseconds()).Scan(&s.TokenHash, &s.AccountID, &s.CSRFToken, &s.CreatedAt,
		&s.LastSeenAt, &s.ExpiresAt, &s.ClientIP, &s.UserAgent, &s.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoPanelSession
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a panel session: %w", err)
	}
	if now.Sub(s.LastSeenAt) >= touch {
		if _, err := r.pool.Exec(ctx, `UPDATE panel_sessions SET last_seen_at = $2 WHERE token_hash = $1`,
			tokenHash, now.UTC()); err != nil {
			return nil, fmt.Errorf("postgres: touching a panel session: %w", err)
		}
		s.LastSeenAt = now
	}
	return &s, nil
}

// EndSession ends one session (sign-out).
func (r *PanelAccounts) EndSession(ctx context.Context, tokenHash string, at time.Time) error {
	if _, err := r.pool.Exec(ctx, `UPDATE panel_sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash, at.UTC()); err != nil {
		return fmt.Errorf("postgres: ending a panel session: %w", err)
	}
	return nil
}

// Purge deletes sessions ended or expired before cutoff and remembered
// answers older than it.
func (r *PanelAccounts) Purge(ctx context.Context, sessionsBefore, requestsBefore time.Time) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM panel_sessions WHERE expires_at < $1 OR revoked_at < $1`,
		sessionsBefore.UTC()); err != nil {
		return fmt.Errorf("postgres: purging panel sessions: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM panel_requests WHERE created_at < $1`, requestsBefore.UTC()); err != nil {
		return fmt.Errorf("postgres: purging panel requests: %w", err)
	}
	return nil
}

// PanelAnswer is a remembered answer to a mutating request.
type PanelAnswer struct {
	Route    string
	Status   int // 0 while the request is still being carried out
	Response json.RawMessage
}

// BeginRequest claims (account, request id) for route. It returns nil when
// the claim is fresh, or the answer remembered for it.
func (r *PanelAccounts) BeginRequest(ctx context.Context, accountID, requestID, route string, at time.Time) (*PanelAnswer, error) {
	tag, err := r.pool.Exec(ctx, `INSERT INTO panel_requests (account_id, request_id, route, status, created_at)
		VALUES ($1::uuid, $2, $3, 0, $4) ON CONFLICT (account_id, request_id) DO NOTHING`,
		accountID, requestID, route, at.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: claiming a panel request: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil, nil
	}
	var a PanelAnswer
	var body []byte
	if err := r.pool.QueryRow(ctx, `SELECT route, status, response FROM panel_requests
		WHERE account_id = $1::uuid AND request_id = $2`, accountID, requestID).Scan(&a.Route, &a.Status, &body); err != nil {
		return nil, fmt.Errorf("postgres: reading a panel request: %w", err)
	}
	a.Response = body
	return &a, nil
}

// FinishRequest remembers the answer given to a claimed request.
func (r *PanelAccounts) FinishRequest(ctx context.Context, accountID, requestID string, status int, response []byte) error {
	if _, err := r.pool.Exec(ctx, `UPDATE panel_requests SET status = $3, response = $4
		WHERE account_id = $1::uuid AND request_id = $2`, accountID, requestID, status, response); err != nil {
		return fmt.Errorf("postgres: remembering a panel answer: %w", err)
	}
	return nil
}

// ForgetRequest releases a claim whose request failed on the server's side,
// so the same request id may be tried again.
func (r *PanelAccounts) ForgetRequest(ctx context.Context, accountID, requestID string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM panel_requests WHERE account_id = $1::uuid AND request_id = $2`,
		accountID, requestID); err != nil {
		return fmt.Errorf("postgres: releasing a panel request: %w", err)
	}
	return nil
}
