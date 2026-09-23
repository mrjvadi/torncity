package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EconomyAdmin holds the operator-side ledger queries behind `admin economy`.
//
// These are whole-table reads that no player command ever needs, so they are
// kept off application.LedgerRepository: a port a handler can reach should
// not offer a scan of the entire ledger.
type EconomyAdmin struct {
	q querier
}

// NewEconomyAdmin returns the operator queries over the pool.
func NewEconomyAdmin(p *Pool) *EconomyAdmin { return &EconomyAdmin{q: p.Raw()} }

// PlayersWithoutStartingGrant lists, oldest first, every player who has no
// starting grant yet. It is only a work list: the grant itself is still
// guarded by the unique index, so a player granted between this read and the
// grant is skipped, not paid twice.
func (a *EconomyAdmin) PlayersWithoutStartingGrant(ctx context.Context) ([]string, error) {
	rows, err := a.q.Query(ctx, `
		SELECT p.id::text FROM players p
		 WHERE NOT EXISTS (SELECT 1 FROM reward_grants r
		                    WHERE r.player_id = p.id AND r.source = 'starting_grant')
		 ORDER BY p.created_at, p.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing players without a starting grant: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UnbalancedTransaction is a ledger transaction whose legs do not sum to zero.
// Sum is text because SUM(bigint) is numeric and a broken ledger is exactly
// where it might not fit an int64.
type UnbalancedTransaction struct {
	TransactionID string
	Sum           string
}

// DriftedAccount is an account whose cached balance disagrees with its entries.
type DriftedAccount struct {
	AccountID string
	Kind      string
	Cached    int64
	Derived   string
}

// LedgerVerification is the result of the three ADR 0009 invariant checks.
type LedgerVerification struct {
	// LedgerSum is SUM(amount) over the whole ledger; it must be "0".
	LedgerSum string
	// Unbalanced lists up to limit transactions that do not sum to zero.
	Unbalanced []UnbalancedTransaction
	// Drifted lists up to limit accounts whose cache disagrees with entries.
	Drifted []DriftedAccount

	// Context for the operator, not invariants.
	Entries      int64
	Transactions int64
	Accounts     int64
	// MoneySupply is the sum of every non-system balance: the money that
	// exists in players' and organisations' hands.
	MoneySupply string
}

// OK reports whether every invariant holds.
func (v LedgerVerification) OK() bool {
	return v.LedgerSum == "0" && len(v.Unbalanced) == 0 && len(v.Drifted) == 0
}

// VerifyLedger runs the three invariants of docs/adr/0009-economic-control.md
// section 4 — the same queries tests/economy_invariants_test.go runs — and
// reports every violation it finds, up to limit per check.
func (a *EconomyAdmin) VerifyLedger(ctx context.Context, limit int) (LedgerVerification, error) {
	var v LedgerVerification

	if err := a.q.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)::text, count(*), count(DISTINCT transaction_id)
		  FROM ledger_entries`).Scan(&v.LedgerSum, &v.Entries, &v.Transactions); err != nil {
		return v, fmt.Errorf("postgres: summing the ledger: %w", err)
	}

	rows, err := a.q.Query(ctx, `
		SELECT transaction_id::text, SUM(amount)::text
		  FROM ledger_entries
		 GROUP BY transaction_id
		HAVING SUM(amount) <> 0
		 ORDER BY transaction_id
		 LIMIT $1`, limit)
	if err != nil {
		return v, fmt.Errorf("postgres: checking transactions: %w", err)
	}
	for rows.Next() {
		var u UnbalancedTransaction
		if err := rows.Scan(&u.TransactionID, &u.Sum); err != nil {
			rows.Close()
			return v, err
		}
		v.Unbalanced = append(v.Unbalanced, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return v, err
	}

	rows, err = a.q.Query(ctx, `
		SELECT a.id::text, a.kind, a.balance, COALESCE(SUM(e.amount), 0)::text
		  FROM accounts a
		  LEFT JOIN ledger_entries e ON e.account_id = a.id
		 GROUP BY a.id, a.kind, a.balance
		HAVING a.balance <> COALESCE(SUM(e.amount), 0)
		 ORDER BY a.id
		 LIMIT $1`, limit)
	if err != nil {
		return v, fmt.Errorf("postgres: checking balances: %w", err)
	}
	for rows.Next() {
		var d DriftedAccount
		if err := rows.Scan(&d.AccountID, &d.Kind, &d.Cached, &d.Derived); err != nil {
			rows.Close()
			return v, err
		}
		v.Drifted = append(v.Drifted, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return v, err
	}

	if err := a.q.QueryRow(ctx, `
		SELECT count(*),
		       COALESCE(SUM(balance) FILTER (WHERE kind NOT IN ('system_source', 'system_sink')), 0)::text
		  FROM accounts`).Scan(&v.Accounts, &v.MoneySupply); err != nil {
		return v, fmt.Errorf("postgres: reading money supply: %w", err)
	}
	return v, nil
}

// AuditEntry is one audit_logs row.
type AuditEntry struct {
	Actor      string
	Action     string
	TargetType string
	NewValue   map[string]any
	Reason     string
	At         time.Time
}

// AppendAudit records an operator action (ADR 0009 section 7: every change
// leaves a row with actor, value, time and reason).
func (a *EconomyAdmin) AppendAudit(ctx context.Context, e AuditEntry) error {
	newValue, err := json.Marshal(e.NewValue)
	if err != nil {
		return fmt.Errorf("postgres: encoding audit value: %w", err)
	}
	if _, err := a.q.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, $2, $3, NULL, NULL, $4, $5, $6)`,
		e.Actor, e.Action, e.TargetType, newValue, e.Reason, e.At.UTC()); err != nil {
		return fmt.Errorf("postgres: writing audit row: %w", err)
	}
	return nil
}
