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

	// Goods, the item journal's invariants (migrations/0017): Goods is
	// false before that migration, and the rest are then empty.
	Goods bool
	// DriftedStacks lists up to limit stacks whose quantity disagrees with
	// the journal's units in less units out.
	DriftedStacks []DriftedStack
	// OrphanPieces counts pieces with no journal row bringing them into the
	// world from a recorded origin.
	OrphanPieces int64

	// Companies, the invariants of player companies (migrations/0019):
	// Companies is false before that migration, and the rest are empty.
	Companies bool
	CompanyInvariants
}

// CompanyInvariants are the company checks of `admin economy verify`.
type CompanyInvariants struct {
	// OrphanCompanyAccounts counts company treasuries owned by no company.
	OrphanCompanyAccounts int64
	// Underfunded lists companies whose treasury holds less than the
	// wages their running shifts reserved.
	Underfunded []string
	// DissolvedWithMoney lists closed companies that still hold money.
	DissolvedWithMoney []string
	// OverBudget lists settled city periods whose companies were paid more
	// than the population's budget, or whose company rows do not add up to
	// what the period says was paid.
	OverBudget []string
	// NPCRevenue and PeriodRevenue are the NPC money every company ever
	// received in the ledger and in the settled periods; they must agree.
	NPCRevenue, PeriodRevenue int64
}

// ok reports whether every company invariant holds.
func (c CompanyInvariants) ok() bool {
	return c.OrphanCompanyAccounts == 0 && len(c.Underfunded) == 0 && len(c.DissolvedWithMoney) == 0 &&
		len(c.OverBudget) == 0 && c.NPCRevenue == c.PeriodRevenue
}

// DriftedStack is a stack the journal does not account for.
type DriftedStack struct {
	PlayerID, Item, Holding string
	Held, Journal           int64
}

// OK reports whether every invariant holds.
func (v LedgerVerification) OK() bool {
	return v.LedgerSum == "0" && len(v.Unbalanced) == 0 && len(v.Drifted) == 0 &&
		len(v.DriftedStacks) == 0 && v.OrphanPieces == 0 && v.CompanyInvariants.ok()
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
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.item_movements') IS NOT NULL`).Scan(&v.Goods); err != nil {
		return v, fmt.Errorf("postgres: looking for the item journal: %w", err)
	}
	if v.Goods {
		if err := a.verifyGoods(ctx, &v, limit); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.companies') IS NOT NULL`).Scan(&v.Companies); err != nil {
		return v, fmt.Errorf("postgres: looking for companies: %w", err)
	}
	if !v.Companies {
		return v, nil
	}
	return v, a.verifyCompanies(ctx, &v, limit)
}

// verifyCompanies runs the company invariants: every company treasury
// belongs to a company; no treasury holds less than the wages its running
// shifts reserved; a closed company holds nothing; no settled period paid
// more than its budget or other than its companies' rows say; and the NPC
// money the ledger paid companies is exactly what the settled periods say.
func (a *EconomyAdmin) verifyCompanies(ctx context.Context, v *LedgerVerification, limit int) error {
	c := &v.CompanyInvariants
	if err := a.q.QueryRow(ctx, `
		SELECT count(*) FROM accounts a
		 WHERE a.kind = 'company_treasury'
		   AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = a.owner_id)`).Scan(&c.OrphanCompanyAccounts); err != nil {
		return fmt.Errorf("postgres: checking company accounts' owners: %w", err)
	}
	list := func(sql string, into *[]string) error {
		rows, err := a.q.Query(ctx, sql, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			*into = append(*into, s)
		}
		return rows.Err()
	}
	if err := list(`
		SELECT c.code || ': holds ' || COALESCE(a.balance, 0) || ', reserved ' || r.reserved
		  FROM companies c
		  JOIN (SELECT company_id, SUM(wage_reserved) AS reserved FROM shift_sessions
		         WHERE status = 'working' AND company_id IS NOT NULL GROUP BY company_id) r ON r.company_id = c.id
		  LEFT JOIN accounts a ON a.kind = 'company_treasury' AND a.owner_id = c.id
		 WHERE COALESCE(a.balance, 0) < r.reserved
		 ORDER BY c.code LIMIT $1`, &c.Underfunded); err != nil {
		return fmt.Errorf("postgres: checking companies' reserved wages: %w", err)
	}
	if err := list(`
		SELECT c.code || ': holds ' || a.balance
		  FROM companies c JOIN accounts a ON a.kind = 'company_treasury' AND a.owner_id = c.id
		 WHERE c.status = 'dissolved' AND a.balance <> 0
		 ORDER BY c.code LIMIT $1`, &c.DissolvedWithMoney); err != nil {
		return fmt.Errorf("postgres: checking closed companies: %w", err)
	}
	if err := list(`
		SELECT m.city_id::text || ' period ' || m.period_no || ': budget ' || m.budget || ', paid ' || m.paid
		       || ', companies say ' || COALESCE(SUM(p.revenue), 0)
		  FROM company_market_periods m
		  LEFT JOIN company_periods p ON p.city_id = m.city_id AND p.period_no = m.period_no
		 GROUP BY m.city_id, m.period_no, m.budget, m.paid
		HAVING m.paid > m.budget OR m.paid <> COALESCE(SUM(p.revenue), 0)
		 ORDER BY 1 LIMIT $1`, &c.OverBudget); err != nil {
		return fmt.Errorf("postgres: checking settled periods: %w", err)
	}
	if err := a.q.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.amount), 0)::bigint
		  FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		 WHERE e.reason = 'npc_purchase' AND a.kind = 'company_treasury'`).Scan(&c.NPCRevenue); err != nil {
		return fmt.Errorf("postgres: summing companies' NPC revenue: %w", err)
	}
	if err := a.q.QueryRow(ctx, `SELECT COALESCE(SUM(revenue), 0)::bigint FROM company_periods`).Scan(&c.PeriodRevenue); err != nil {
		return fmt.Errorf("postgres: summing settled revenue: %w", err)
	}
	return nil
}

// verifyGoods runs the item journal's two invariants: every stack is the
// journal's units in less its units out, and every piece came into the
// world from a recorded origin.
func (a *EconomyAdmin) verifyGoods(ctx context.Context, v *LedgerVerification, limit int) error {
	rows, err := a.q.Query(ctx, `
		WITH flows AS (
		    SELECT to_player AS player_id, item_code, to_holding AS holding, quantity AS delta
		      FROM item_movements WHERE piece_id IS NULL AND to_player IS NOT NULL
		    UNION ALL
		    SELECT from_player, item_code, from_holding, -quantity
		      FROM item_movements WHERE piece_id IS NULL AND from_player IS NOT NULL
		), journal AS (
		    SELECT player_id, item_code, holding, SUM(delta) AS qty FROM flows GROUP BY 1, 2, 3
		)
		SELECT COALESCE(s.player_id, j.player_id)::text, COALESCE(s.item_code, j.item_code),
		       COALESCE(s.holding, j.holding), COALESCE(s.quantity, 0), COALESCE(j.qty, 0)
		  FROM item_stacks s
		  FULL JOIN journal j ON j.player_id = s.player_id AND j.item_code = s.item_code AND j.holding = s.holding
		 WHERE COALESCE(s.quantity, 0) <> COALESCE(j.qty, 0)
		 ORDER BY 1, 2, 3
		 LIMIT $1`, limit)
	if err != nil {
		return fmt.Errorf("postgres: checking stacks against the item journal: %w", err)
	}
	for rows.Next() {
		var d DriftedStack
		if err := rows.Scan(&d.PlayerID, &d.Item, &d.Holding, &d.Held, &d.Journal); err != nil {
			rows.Close()
			return err
		}
		v.DriftedStacks = append(v.DriftedStacks, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := a.q.QueryRow(ctx, `
		SELECT count(*) FROM item_pieces p
		 WHERE NOT EXISTS (SELECT 1 FROM item_movements m
		                    WHERE m.piece_id = p.id AND m.from_player IS NULL
		                      AND m.reason IN ('shop_purchase', 'crime_loot', 'grant'))`).Scan(&v.OrphanPieces); err != nil {
		return fmt.Errorf("postgres: checking pieces' origins: %w", err)
	}
	return nil
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
