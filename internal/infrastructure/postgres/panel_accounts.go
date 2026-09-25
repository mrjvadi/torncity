package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The web panel's operators (migrations/0030): accounts created on the
// server only, with an audit row for every change, and the sign-in state the
// panel keeps for them. No password is ever passed in here, only its hash.

// PanelAccount is one operator of the web panel.
type PanelAccount struct {
	ID, Username, PasswordHash string
	TOTPSecret                 string
	TOTPEnabled                bool
	Status                     string
	FailedLogins               int
	LockedUntil, LastLoginAt   *time.Time
	CreatedBy                  string
	CreatedAt                  time.Time
}

// Active reports whether the account may sign in at all.
func (a PanelAccount) Active() bool { return a.Status == "active" }

// ErrNoPanelAccount means no account has that username.
var ErrNoPanelAccount = errors.New("postgres: no panel account with that username")

// ErrPanelAccountExists means the username is taken.
var ErrPanelAccountExists = errors.New("postgres: a panel account with that username exists")

// PanelAccounts reads and writes panel accounts and their sessions.
type PanelAccounts struct {
	pool querier
	p    *Pool
}

// NewPanelAccounts returns the repository over the pool.
func NewPanelAccounts(p *Pool) *PanelAccounts { return &PanelAccounts{pool: p.Raw(), p: p} }

const panelAccountColumns = `id::text, username, password_hash, COALESCE(totp_secret, ''), totp_enabled, status,
	failed_logins, locked_until, last_login_at, created_by, created_at`

func scanPanelAccount(row pgx.Row) (*PanelAccount, error) {
	var a PanelAccount
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.TOTPSecret, &a.TOTPEnabled, &a.Status,
		&a.FailedLogins, &a.LockedUntil, &a.LastLoginAt, &a.CreatedBy, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoPanelAccount
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a panel account: %w", err)
	}
	return &a, nil
}

// ByUsername reads one account.
func (r *PanelAccounts) ByUsername(ctx context.Context, username string) (*PanelAccount, error) {
	return scanPanelAccount(r.pool.QueryRow(ctx, `SELECT `+panelAccountColumns+` FROM panel_accounts WHERE username = $1`, username))
}

// List reads every account, by username.
func (r *PanelAccounts) List(ctx context.Context) ([]PanelAccount, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+panelAccountColumns+` FROM panel_accounts ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing panel accounts: %w", err)
	}
	defer rows.Close()
	var out []PanelAccount
	for rows.Next() {
		a, err := scanPanelAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// PanelAccountChange is who changes an account on the server, and why.
type PanelAccountChange struct {
	Username string
	Actor    string
	Reason   string
	At       time.Time
}

// inPanelTx runs fn in one transaction with an audit row written by it.
func (r *PanelAccounts) inPanelTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.p.Raw().Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: panel account: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: panel account: commit: %w", err)
	}
	return nil
}

func panelAudit(ctx context.Context, q querier, action string, ch PanelAccountChange, value map[string]any) error {
	if value == nil {
		value = map[string]any{}
	}
	value["username"] = ch.Username
	return (&EconomyAdmin{q: q}).AppendAudit(ctx, AuditEntry{Actor: ch.Actor, Action: action,
		TargetType: "panel_accounts", NewValue: value, Reason: ch.Reason, At: ch.At})
}

// Create adds an active account with the given password hash.
func (r *PanelAccounts) Create(ctx context.Context, ch PanelAccountChange, passwordHash string) error {
	if ch.Actor == "" || ch.Reason == "" || passwordHash == "" {
		return errors.New("postgres: a panel account needs who creates it, why, and a password")
	}
	return r.inPanelTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO panel_accounts (id, username, password_hash, totp_enabled, status,
			failed_logins, password_changed_at, created_by, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, false, 'active', 0, $3, $4, $3, $3)
			ON CONFLICT (username) DO NOTHING`, ch.Username, passwordHash, ch.At.UTC(), ch.Actor)
		if err != nil {
			return fmt.Errorf("postgres: creating a panel account: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrPanelAccountExists
		}
		return panelAudit(ctx, tx, "panel.account_create", ch, nil)
	})
}

// update changes one account with its audit row; revoke also ends every
// session it has.
func (r *PanelAccounts) update(ctx context.Context, ch PanelAccountChange, action string, value map[string]any,
	revoke bool, sql string, args ...any,
) error {
	if ch.Actor == "" || ch.Reason == "" {
		return errors.New("postgres: a panel account change needs who makes it and why")
	}
	return r.inPanelTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, sql, append([]any{ch.Username, ch.At.UTC()}, args...)...).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoPanelAccount
		}
		if err != nil {
			return fmt.Errorf("postgres: changing a panel account: %w", err)
		}
		if revoke {
			if _, err := tx.Exec(ctx, `UPDATE panel_sessions SET revoked_at = $2
				WHERE account_id = $1::uuid AND revoked_at IS NULL`, id, ch.At.UTC()); err != nil {
				return fmt.Errorf("postgres: ending panel sessions: %w", err)
			}
		}
		return panelAudit(ctx, tx, action, ch, value)
	})
}

// SetPassword replaces the password hash, clears any lock and ends every
// session the account had.
func (r *PanelAccounts) SetPassword(ctx context.Context, ch PanelAccountChange, passwordHash string) error {
	return r.update(ctx, ch, "panel.password_change", nil, true, `UPDATE panel_accounts
		SET password_hash = $3, failed_logins = 0, locked_until = NULL, password_changed_at = $2, updated_at = $2
		WHERE username = $1 RETURNING id::text`, passwordHash)
}

// SetStatus enables or disables an account; disabling ends its sessions.
func (r *PanelAccounts) SetStatus(ctx context.Context, ch PanelAccountChange, active bool) error {
	status, action := "disabled", "panel.account_disable"
	if active {
		status, action = "active", "panel.account_enable"
	}
	return r.update(ctx, ch, action, nil, !active, `UPDATE panel_accounts
		SET status = $3, failed_logins = 0, locked_until = NULL, updated_at = $2
		WHERE username = $1 RETURNING id::text`, status)
}

// SetTOTP enrols the account in two-factor sign-in with secret, or removes
// it when secret is empty. The secret never enters the audit row.
func (r *PanelAccounts) SetTOTP(ctx context.Context, ch PanelAccountChange, secret string) error {
	action := "panel.totp_enable"
	var value any = secret
	if secret == "" {
		action, value = "panel.totp_disable", nil
	}
	return r.update(ctx, ch, action, nil, true, `UPDATE panel_accounts
		SET totp_secret = $3, totp_enabled = ($3::text IS NOT NULL), updated_at = $2
		WHERE username = $1 RETURNING id::text`, value)
}
