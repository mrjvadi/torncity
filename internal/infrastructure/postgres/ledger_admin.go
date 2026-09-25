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
func NewEconomyAdmin(p *Pool) *EconomyAdmin { return &EconomyAdmin{q: p.shared()} }

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

	// Production, the invariants of the production economy (migrations/
	// 0020): Production is false before that migration.
	Production bool
	ProductionInvariants

	// Military is whether the armed forces' tables exist (migration 0021);
	// MilitaryInvariants their checks.
	Military bool
	MilitaryInvariants

	// War is whether war's tables exist (migration 0022); WarInvariants
	// their checks (ledger_admin_war.go).
	War bool
	WarInvariants

	// StageE is whether stage E's tables exist (migration 0023);
	// StageEInvariants their checks (ledger_admin_stage_e.go).
	StageE bool
	StageEInvariants

	// StageF is whether stage F's tables exist (migrations 0024, 0025);
	// StageFInvariants their checks (ledger_admin_stage_f.go).
	StageF bool
	StageFInvariants

	// Life is whether a life's tables exist (migration 0028); LifeInvariants
	// their checks (ledger_admin_life.go).
	Life bool
	LifeInvariants

	// Finance is whether finance's tables exist (migration 0029);
	// FinanceInvariants their checks (ledger_admin_finance.go).
	Finance bool
	FinanceInvariants

	// DefenceInvariants are the armed forces' wages
	// (ledger_admin_defence.go); they need nothing but the ledger.
	DefenceInvariants
}

// MilitaryInvariants are the armed forces' checks
// (docs/adr/0022-military-and-diplomacy.md §2.12).
type MilitaryInvariants struct {
	// OrphanStateAccounts are national treasuries and defence funds whose
	// owner is not a country.
	OrphanStateAccounts int64
	// OrphanStateHoldings are goods a state holds that name no country, or
	// that have no military asset row; OrphanAssets asset rows whose piece
	// the state does not hold.
	OrphanStateHoldings int64
	OrphanAssets        int64
	// The ledger, reason by reason, against the rows: the cities' levy
	// credited to national treasuries, the appropriation credited to
	// defence funds, the upkeep taken from them, and arms paid for.
	LevyLedger, LevyRows                   int64
	AppropriationLedger, AppropriationRows int64
	UpkeepLedger, UpkeepRows               int64
	ProcurementLedger, ProcurementRows     int64
	// Procured is pieces moved to states by procurement; ProcuredRows the
	// quantity the procurements say.
	Procured, ProcuredRows int64
}

func (m MilitaryInvariants) ok() bool {
	return m.OrphanStateAccounts == 0 && m.OrphanStateHoldings == 0 && m.OrphanAssets == 0 &&
		m.LevyLedger == m.LevyRows && m.AppropriationLedger == m.AppropriationRows && m.UpkeepLedger == m.UpkeepRows &&
		m.ProcurementLedger == m.ProcurementRows && m.Procured == m.ProcuredRows
}

// ProductionInvariants are the production economy's checks of `admin
// economy verify`.
type ProductionInvariants struct {
	// OrphanHolders counts organisation holdings whose company does not
	// exist.
	OrphanHolders int64
	// Each pair is what the ledger moved under a reason and what the rows
	// of the production economy say it should have: they must agree.
	LicenseLedger, LicenseRows   int64
	SaleLedger, SaleRows         int64
	SupplyLedger, SupplyRows     int64
	ResearchLedger, ResearchRows int64
	// UnpaidLicenses counts licenses whose ledger transaction is not a
	// technology_license payment of their price to the licensor.
	UnpaidLicenses int64
	// NPCStockJournal and NPCStockPeriods are the units stocked companies
	// sold the population, in the item journal and in the settled periods.
	NPCStockJournal, NPCStockPeriods int64
	// Orders lists orders whose journal disagrees with them: inputs taken
	// other than the order consumed, or output other than it made.
	Orders []string
}

// ok reports whether every production invariant holds.
func (p ProductionInvariants) ok() bool {
	return p.OrphanHolders == 0 && p.LicenseLedger == p.LicenseRows && p.SaleLedger == p.SaleRows &&
		p.SupplyLedger == p.SupplyRows && p.ResearchLedger == p.ResearchRows && p.UnpaidLicenses == 0 &&
		p.NPCStockJournal == p.NPCStockPeriods && len(p.Orders) == 0
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
		len(v.DriftedStacks) == 0 && v.OrphanPieces == 0 && v.CompanyInvariants.ok() && v.ProductionInvariants.ok() &&
		v.MilitaryInvariants.ok() && v.WarInvariants.ok() && v.StageEInvariants.ok() && v.StageFInvariants.ok() &&
		v.DefenceInvariants.ok() && v.LifeInvariants.ok() && v.FinanceInvariants.ok()
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
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.org_stacks') IS NOT NULL`).Scan(&v.Production); err != nil {
		return v, fmt.Errorf("postgres: looking for the production economy: %w", err)
	}
	if v.Goods {
		if err := a.verifyGoods(ctx, &v, limit); err != nil {
			return v, err
		}
	}
	if v.Production {
		if err := a.verifyProduction(ctx, &v, limit); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.military_assets') IS NOT NULL`).Scan(&v.Military); err != nil {
		return v, fmt.Errorf("postgres: looking for the armed forces: %w", err)
	}
	if v.Military {
		if err := a.verifyMilitary(ctx, &v); err != nil {
			return v, err
		}
		if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.war_operations') IS NOT NULL`).Scan(&v.War); err != nil {
			return v, fmt.Errorf("postgres: looking for war: %w", err)
		}
	}
	if v.War {
		if err := a.verifyWar(ctx, &v); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.payment_holds') IS NOT NULL`).Scan(&v.StageE); err != nil {
		return v, fmt.Errorf("postgres: looking for stage E: %w", err)
	}
	if v.StageE {
		if err := a.verifyStageE(ctx, &v); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.city_budget_periods') IS NOT NULL
	   AND to_regclass('public.properties') IS NOT NULL`).Scan(&v.StageF); err != nil {
		return v, fmt.Errorf("postgres: looking for stage F: %w", err)
	}
	if v.StageF {
		if err := a.verifyStageF(ctx, &v); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.life_sleeps') IS NOT NULL`).Scan(&v.Life); err != nil {
		return v, fmt.Errorf("postgres: looking for lives: %w", err)
	}
	if v.Life {
		if err := a.verifyLife(ctx, &v); err != nil {
			return v, err
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.loans') IS NOT NULL`).Scan(&v.Finance); err != nil {
		return v, fmt.Errorf("postgres: looking for finance: %w", err)
	}
	if v.Finance {
		if err := a.verifyFinance(ctx, &v); err != nil {
			return v, err
		}
	}
	if err := a.verifyDefence(ctx, &v); err != nil {
		return v, err
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
	orphans := `
		SELECT count(*) FROM item_pieces p
		 WHERE NOT EXISTS (SELECT 1 FROM item_movements m
		                    WHERE m.piece_id = p.id AND m.from_player IS NULL
		                      AND m.reason IN ('shop_purchase', 'crime_loot', 'grant'))`
	if v.Production {
		// A produced piece's first row comes from its order, into an
		// organisation.
		orphans = `
		SELECT count(*) FROM item_pieces p
		 WHERE NOT EXISTS (SELECT 1 FROM item_movements m
		                    WHERE m.piece_id = p.id AND m.from_player IS NULL AND m.from_org IS NULL
		                      AND m.reason IN ('shop_purchase', 'crime_loot', 'grant', 'produced'))`
	}
	if err := a.q.QueryRow(ctx, orphans).Scan(&v.OrphanPieces); err != nil {
		return fmt.Errorf("postgres: checking pieces' origins: %w", err)
	}
	if !v.Production {
		return nil
	}
	// An organisation's stacks are its journal's units in less its units
	// out, as a player's are.
	rows, err = a.q.Query(ctx, `
		WITH flows AS (
		    SELECT to_org_kind AS kind, to_org AS org, item_code, to_holding AS holding, quantity AS delta
		      FROM item_movements WHERE piece_id IS NULL AND to_org IS NOT NULL
		    UNION ALL
		    SELECT from_org_kind, from_org, item_code, from_holding, -quantity
		      FROM item_movements WHERE piece_id IS NULL AND from_org IS NOT NULL
		), journal AS (
		    SELECT kind, org, item_code, holding, SUM(delta) AS qty FROM flows GROUP BY 1, 2, 3, 4
		)
		SELECT COALESCE(s.org_kind, j.kind) || ':' || COALESCE(s.org_id, j.org)::text, COALESCE(s.item_code, j.item_code),
		       COALESCE(s.holding, j.holding), COALESCE(s.quantity, 0), COALESCE(j.qty, 0)
		  FROM org_stacks s
		  FULL JOIN journal j ON j.kind = s.org_kind AND j.org = s.org_id AND j.item_code = s.item_code AND j.holding = s.holding
		 WHERE COALESCE(s.quantity, 0) <> COALESCE(j.qty, 0)
		 ORDER BY 1, 2, 3
		 LIMIT $1`, limit)
	if err != nil {
		return fmt.Errorf("postgres: checking organisations' stacks against the item journal: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DriftedStack
		if err := rows.Scan(&d.PlayerID, &d.Item, &d.Holding, &d.Held, &d.Journal); err != nil {
			return err
		}
		v.DriftedStacks = append(v.DriftedStacks, d)
	}
	return rows.Err()
}

// verifyProduction runs the production economy's invariants: every
// organisation holding names a company that exists; the ledger moved
// exactly what the licenses, the company sales, the supplier purchases and
// the research say, reason by reason; every license was paid to its
// licensor at its price; and every order's journal is the order — its
// inputs taken as it consumed them, its output as it made it.
func (a *EconomyAdmin) verifyProduction(ctx context.Context, v *LedgerVerification, limit int) error {
	p := &v.ProductionInvariants
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM org_stacks s WHERE s.org_kind = 'company'
		          AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = s.org_id))
		     + (SELECT count(*) FROM item_pieces i WHERE i.org_kind = 'company'
		          AND NOT EXISTS (SELECT 1 FROM companies c WHERE c.id = i.org_id))`).Scan(&p.OrphanHolders); err != nil {
		return fmt.Errorf("postgres: checking organisations' holdings: %w", err)
	}
	sums := []struct {
		ledger, rows *int64
		reason, kind string
		sql          string
	}{
		{&p.LicenseLedger, &p.LicenseRows, "technology_license", "company_treasury", `SELECT COALESCE(SUM(price), 0)::bigint FROM technology_licenses`},
		{&p.SaleLedger, &p.SaleRows, "company_sale", "company_treasury", `SELECT COALESCE(SUM(total), 0)::bigint FROM company_sales`},
		{&p.SupplyLedger, &p.SupplyRows, "supplier_purchase", "system_sink", `SELECT COALESCE(SUM(total), 0)::bigint FROM supply_purchases`},
		{&p.ResearchLedger, &p.ResearchRows, "research", "system_sink", `SELECT COALESCE(SUM(cost), 0)::bigint FROM company_research`},
	}
	for _, s := range sums {
		if err := a.q.QueryRow(ctx, `
			SELECT COALESCE(SUM(e.amount), 0)::bigint
			  FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = $1 AND a.kind = $2 AND e.amount > 0`, s.reason, s.kind).Scan(s.ledger); err != nil {
			return fmt.Errorf("postgres: summing %s in the ledger: %w", s.reason, err)
		}
		if err := a.q.QueryRow(ctx, s.sql).Scan(s.rows); err != nil {
			return fmt.Errorf("postgres: summing %s rows: %w", s.reason, err)
		}
	}
	if err := a.q.QueryRow(ctx, `
		SELECT count(*) FROM technology_licenses l
		 WHERE NOT EXISTS (
		       SELECT 1 FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		        WHERE e.transaction_id = l.ledger_transaction_id AND e.reason = 'technology_license'
		          AND a.kind = 'company_treasury' AND a.owner_id = l.licensor_company_id AND e.amount = l.price)`).Scan(&p.UnpaidLicenses); err != nil {
		return fmt.Errorf("postgres: checking licenses' payments: %w", err)
	}
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'npc_sale'),
		       (SELECT COALESCE(SUM(stock_units), 0)::bigint FROM company_periods)`).Scan(&p.NPCStockJournal, &p.NPCStockPeriods); err != nil {
		return fmt.Errorf("postgres: checking stocked sales: %w", err)
	}
	rows, err := a.q.Query(ctx, `
		WITH inputs AS (
		    SELECT reference_id AS id, SUM(quantity) AS qty FROM item_movements
		     WHERE reason = 'production_input' AND reference_type = 'production_orders' GROUP BY 1
		), outputs AS (
		    SELECT reference_id AS id, SUM(quantity) AS qty FROM item_movements
		     WHERE reason = 'produced' AND reference_type = 'production_orders' GROUP BY 1
		), consumed AS (
		    SELECT o.id, COALESCE(SUM(c.value::bigint), 0) AS qty
		      FROM production_orders o LEFT JOIN LATERAL jsonb_each_text(o.consumed) c ON true GROUP BY o.id
		)
		SELECT o.no::text || ': consumed ' || c.qty || ', journal took ' || COALESCE(i.qty, 0)
		       || '; made ' || CASE WHEN o.status = 'done' THEN o.output_qty ELSE 0 END || ', journal says ' || COALESCE(t.qty, 0)
		  FROM production_orders o
		  JOIN consumed c ON c.id = o.id
		  LEFT JOIN inputs i ON i.id = o.id
		  LEFT JOIN outputs t ON t.id = o.id
		 WHERE c.qty <> COALESCE(i.qty, 0)
		    OR (CASE WHEN o.status = 'done' THEN o.output_qty ELSE 0 END) <> COALESCE(t.qty, 0)
		 ORDER BY o.no LIMIT $1`, limit)
	if err != nil {
		return fmt.Errorf("postgres: checking production orders against the journal: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		p.Orders = append(p.Orders, s)
	}
	return rows.Err()
}

// verifyMilitary runs the armed forces' invariants: every state account and
// every state holding belongs to a country; every piece a state holds is a
// military asset and every asset a piece the state holds; and the ledger
// moved exactly what the defence periods and the procurements say.
func (a *EconomyAdmin) verifyMilitary(ctx context.Context, v *LedgerVerification) error {
	m := &v.MilitaryInvariants
	if err := a.q.QueryRow(ctx, `
		SELECT count(*) FROM accounts a
		 WHERE a.kind IN ('state_treasury', 'defence_fund')
		   AND NOT EXISTS (SELECT 1 FROM jurisdictions j WHERE j.id = a.owner_id AND j.kind = 'country')`).Scan(&m.OrphanStateAccounts); err != nil {
		return fmt.Errorf("postgres: checking state accounts' owners: %w", err)
	}
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM org_stacks s WHERE s.org_kind = 'state')
		     + (SELECT count(*) FROM item_pieces i WHERE i.org_kind = 'state'
		          AND (NOT EXISTS (SELECT 1 FROM jurisdictions j WHERE j.id = i.org_id AND j.kind = 'country')
		               OR NOT EXISTS (SELECT 1 FROM military_assets x WHERE x.piece_id = i.id AND x.country_id = i.org_id
		                                AND x.status NOT IN ('destroyed', 'expended')))),
		       (SELECT count(*) FROM military_assets x
		         WHERE x.status NOT IN ('destroyed', 'expended')
		           AND NOT EXISTS (SELECT 1 FROM item_pieces i WHERE i.id = x.piece_id AND i.org_kind = 'state'
		                             AND i.org_id = x.country_id AND i.holding = 'warehouse'))`).Scan(
		&m.OrphanStateHoldings, &m.OrphanAssets); err != nil {
		return fmt.Errorf("postgres: checking states' holdings: %w", err)
	}
	sums := []struct {
		ledger, rows *int64
		reason, kind string
		sign         string
		sql          string
	}{
		{&m.LevyLedger, &m.LevyRows, "national_levy", "state_treasury", "> 0", `SELECT COALESCE(SUM(levy), 0)::bigint FROM military_periods`},
		{&m.AppropriationLedger, &m.AppropriationRows, "defence_appropriation", "defence_fund", "> 0",
			`SELECT COALESCE(SUM(appropriation), 0)::bigint FROM military_periods`},
		{&m.UpkeepLedger, &m.UpkeepRows, "military_upkeep", "defence_fund", "< 0",
			`SELECT COALESCE(SUM(upkeep_paid), 0)::bigint FROM military_periods`},
		{&m.ProcurementLedger, &m.ProcurementRows, "arms_procurement", "defence_fund", "< 0",
			`SELECT COALESCE(SUM(total), 0)::bigint FROM procurements`},
	}
	for _, s := range sums {
		// s.sign is one of two fixed strings of this file, never input.
		if err := a.q.QueryRow(ctx, `
			SELECT COALESCE(ABS(SUM(e.amount)), 0)::bigint
			  FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = $1 AND a.kind = $2 AND e.amount `+s.sign, s.reason, s.kind).Scan(s.ledger); err != nil {
			return fmt.Errorf("postgres: summing %s in the ledger: %w", s.reason, err)
		}
		if err := a.q.QueryRow(ctx, s.sql).Scan(s.rows); err != nil {
			return fmt.Errorf("postgres: summing %s rows: %w", s.reason, err)
		}
	}
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'procured'),
		       (SELECT COALESCE(SUM(quantity), 0)::bigint FROM procurements)`).Scan(&m.Procured, &m.ProcuredRows); err != nil {
		return fmt.Errorf("postgres: checking procured goods: %w", err)
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

// PlayerByCode resolves a public player code to an active player, for
// operator commands that name a player the way players do.
func (a *EconomyAdmin) PlayerByCode(ctx context.Context, code string) (id, label string, err error) {
	return activePlayerByCode(ctx, a.q, code)
}
