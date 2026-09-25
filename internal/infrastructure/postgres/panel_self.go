package postgres

import (
	"context"
	"fmt"
	"time"
)

// An operator's own sessions, for the console's account page: the live ones
// listed (never their tokens, only the first characters of the tokens'
// hashes as a handle), and one of them ended.

// PanelSessionLine is one live session of an account.
type PanelSessionLine struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

// sessionHandle is how many characters of a token hash name a session.
const sessionHandle = 16

// Sessions lists an account's live sessions, most recent first; the one
// whose hash is current is marked.
func (r *PanelAccounts) Sessions(ctx context.Context, accountID, current string, now time.Time) ([]PanelSessionLine, error) {
	rows, err := r.pool.Query(ctx, `SELECT left(token_hash, $3), created_at, last_seen_at, expires_at, client_ip, user_agent,
		token_hash = $2 FROM panel_sessions WHERE account_id = $1::uuid AND revoked_at IS NULL AND expires_at > $4
		ORDER BY last_seen_at DESC LIMIT 50`, accountID, current, sessionHandle, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("postgres: listing panel sessions: %w", err)
	}
	defer rows.Close()
	var out []PanelSessionLine
	for rows.Next() {
		var l PanelSessionLine
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.LastSeenAt, &l.ExpiresAt, &l.ClientIP, &l.UserAgent, &l.Current); err != nil {
			return nil, fmt.Errorf("postgres: listing panel sessions: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RevokeSession ends one of an account's sessions by its handle, and
// reports whether one was ended.
func (r *PanelAccounts) RevokeSession(ctx context.Context, accountID, handle string, at time.Time) (bool, error) {
	if len(handle) != sessionHandle {
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `UPDATE panel_sessions SET revoked_at = $3
		WHERE account_id = $1::uuid AND left(token_hash, $4) = $2 AND revoked_at IS NULL`,
		accountID, handle, at.UTC(), sessionHandle)
	if err != nil {
		return false, fmt.Errorf("postgres: ending a panel session: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
