package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The operators' runtime switches (migrations/0041_runtime_switches): a
// small, generic key/value table so a setting can be flipped without a
// redeploy — telegram_play and telegram_notices today, whatever an operator
// needs tomorrow. Every change writes to audit_logs exactly as every other
// console change does (target_type "operator_switches"), so History reads
// the same trail `admin economy verify` and the console's other pages do.

// SwitchOps carries out switch changes and reads.
type SwitchOps struct{ q routed }

// NewSwitchOps returns them over the pool.
func NewSwitchOps(p *Pool) *SwitchOps { return &SwitchOps{q: p.shared()} }

// SwitchChange sets one switch's value.
type SwitchChange struct {
	Key, Value string
	Actor      PanelActor
}

// SwitchState is one switch's current row.
type SwitchState struct {
	Key        string    `json:"key"`
	Value      string    `json:"value"`
	ChangedBy  string    `json:"changed_by"`
	ChangedAt  time.Time `json:"changed_at"`
	Reason     string    `json:"reason"`
}

// Set stores key's new value, audited. It upserts: the first time a switch
// is touched creates its row, every time after that overwrites it — the
// table holds the current value only, never a history of its own, because
// audit_logs already is that history.
func (o *SwitchOps) Set(ctx context.Context, c SwitchChange) (SwitchState, error) {
	if err := c.Actor.valid(); err != nil {
		return SwitchState{}, err
	}
	key, value := strings.TrimSpace(c.Key), strings.TrimSpace(c.Value)
	if key == "" || value == "" {
		return SwitchState{}, errors.New("postgres: a switch needs a key and a value")
	}
	at := c.Actor.At.UTC()
	out := SwitchState{Key: key, Value: value, ChangedBy: c.Actor.Name, ChangedAt: at, Reason: c.Actor.Reason}
	err := o.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO operator_switches (key, value, changed_by, changed_at, reason)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, changed_by = EXCLUDED.changed_by,
				changed_at = EXCLUDED.changed_at, reason = EXCLUDED.reason`,
			key, value, c.Actor.Name, at, c.Actor.Reason); err != nil {
			return fmt.Errorf("postgres: setting a switch: %w", err)
		}
		return auditTx(ctx, tx, c.Actor, "switch.set", "operator_switches", "", map[string]any{"key": key, "value": value})
	})
	return out, err
}

// Get returns one switch's current value. found is false when it has never
// been set, which the caller treats as the switch's own default.
func (o *SwitchOps) Get(ctx context.Context, key string) (value string, found bool, err error) {
	err = o.q.QueryRow(ctx, `SELECT value FROM operator_switches WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres: reading a switch: %w", err)
	}
	return value, true, nil
}

// List returns every switch's current row, key ascending, for `admin switch
// list` and the panel's System > Switches page.
func (o *SwitchOps) List(ctx context.Context) ([]SwitchState, error) {
	rows, err := o.q.Query(ctx, `SELECT key, value, changed_by, changed_at, reason FROM operator_switches ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing switches: %w", err)
	}
	defer rows.Close()
	var out []SwitchState
	for rows.Next() {
		var s SwitchState
		if err := rows.Scan(&s.Key, &s.Value, &s.ChangedBy, &s.ChangedAt, &s.Reason); err != nil {
			return nil, fmt.Errorf("postgres: listing switches: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SwitchHistoryEntry is one past change to a switch, read from audit_logs.
type SwitchHistoryEntry struct {
	At     time.Time      `json:"at"`
	Actor  string         `json:"actor"`
	Reason string         `json:"reason"`
	Key    string         `json:"key"`
	Value  string         `json:"value"`
}

// History returns the most recent switch changes, newest first, at most
// limit of them (a non-positive or too-large limit is clamped to 200).
func (o *SwitchOps) History(ctx context.Context, limit int) ([]SwitchHistoryEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := o.q.Query(ctx, `SELECT created_at, actor, reason, new_value FROM audit_logs
		WHERE target_type = 'operator_switches' AND action = 'switch.set'
		ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading switch history: %w", err)
	}
	defer rows.Close()
	var out []SwitchHistoryEntry
	for rows.Next() {
		var e SwitchHistoryEntry
		var raw []byte
		if err := rows.Scan(&e.At, &e.Actor, &e.Reason, &raw); err != nil {
			return nil, fmt.Errorf("postgres: reading switch history: %w", err)
		}
		var v struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &v); err == nil {
			e.Key, e.Value = v.Key, v.Value
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// inTx runs fn in one transaction with the audit row it asks for. Mirrors
// PanelOps.inTx: never called from inside a unit of work (uow.Do), and
// refuses to run if it is.
func (o *SwitchOps) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if InTransaction(ctx) {
		return ErrPanelReadInCommand
	}
	tx, err := o.q.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: switch change: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: switch change: commit: %w", err)
	}
	return nil
}
