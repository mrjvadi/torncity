package postgres

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
)

// verifyPolity runs the checks of unions, countries, tenders and loans between polities when migration 0154 is applied (docs/adr/0076 15).
func (a *EconomyAdmin) verifyPolity(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.unions') IS NOT NULL`).Scan(&s.Polity); err != nil {
		return fmt.Errorf("postgres: looking for the unions: %w", err)
	}
	if !s.Polity {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.UnionDuesLedger, "dues in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'union_dues' AND a.kind = 'state_treasury' AND e.amount > 0`},
		{&s.UnionDuesRows, "dues in the rows", `SELECT COALESCE(SUM(paid), 0)::bigint FROM union_dues_periods`},
		{&s.UnionPayoutLedger, "union payouts in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason IN ('union_exit_settlement', 'union_dissolution_payout') AND a.kind = 'city_treasury' AND e.amount > 0`},
		{&s.UnionPayoutRows, "union payouts in the rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM union_settlements`},
		// an escrow holds, for an open tender, the bonds of its standing bids; for an awarded one, the price not yet paid out plus the bond of the winner;
		// for any other, nothing
		{&s.TenderEscrowBroken, "tender escrows", `SELECT count(*) FROM tenders t LEFT JOIN accounts a ON a.kind = 'works_escrow' AND a.owner_id = t.id WHERE COALESCE(a.balance, 0) <> (CASE t.status
			WHEN 'open' THEN COALESCE((SELECT sum(bond) FROM tender_bids b WHERE b.tender_id = t.id AND b.status = 'standing'), 0)
			WHEN 'awarded' THEN COALESCE(t.price, 0) - COALESCE((SELECT sum(paid) FROM tender_stages s WHERE s.tender_id = t.id), 0)
				+ COALESCE((SELECT sum(bond) FROM tender_bids b WHERE b.tender_id = t.id AND b.status = 'won'), 0)
			ELSE 0 END)`},
		{&s.DebtDisbursedLedger, "loans lent in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'sovereign_loan_disbursement' AND e.amount > 0`},
		{&s.DebtDisbursedRows, "loans lent in the rows", `SELECT COALESCE(SUM(principal), 0)::bigint FROM sovereign_loans WHERE accepted_at IS NOT NULL AND replaces IS NULL`},
		{&s.DebtRepaidLedger, "loans repaid in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason IN ('sovereign_loan_repayment', 'sovereign_loan_interest') AND e.amount > 0`},
		{&s.DebtRepaidRows, "loans repaid in the rows", `SELECT COALESCE(SUM(principal + interest), 0)::bigint FROM sovereign_loan_periods`},
		{&s.DebtPenaltyLedger, "late fees in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'sovereign_loan_penalty' AND e.amount > 0`},
		{&s.DebtFeesRows, "late fees paid in the log", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'sovereign_loan_penalty' AND e.amount > 0`},
		{&s.CountryParentBroken, "members of a country not under it", `SELECT count(*) FROM union_members m JOIN unions u ON u.id = m.union_id JOIN cities c ON c.id = m.settlement_id
			JOIN jurisdictions j ON j.id = c.jurisdiction_id WHERE u.kind = 'country' AND u.status = 'active' AND m.status IN ('active', 'notice') AND j.parent_id IS DISTINCT FROM u.jurisdiction_id`},
		{&s.SettlementTwoCountries, "settlements in two countries", `SELECT count(*) FROM (SELECT settlement_id FROM union_members WHERE union_kind = 'country' AND status IN ('active', 'notice') GROUP BY 1 HAVING count(*) > 1) x`},
		{&s.UnionNoManager, "active unions with no held manager", `SELECT count(*) FROM unions u WHERE u.status = 'active' AND NOT EXISTS (
			SELECT 1 FROM union_offices o JOIN union_seats s ON s.office_id = o.id AND s.until IS NULL
			WHERE o.union_id = u.id AND o.closed_at IS NULL AND o.grants @> '[{"p": "office.edit"}]'::jsonb AND o.grants @> '[{"p": "charter.amend"}]'::jsonb)`},
		{&s.UnionNoFounded, "unions with no founding line", `SELECT count(*) FROM unions u WHERE NOT EXISTS (SELECT 1 FROM union_audit a WHERE a.union_id = u.id AND a.action = 'founded')`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	// the late fees recorded in the log of the loans are those the ledger moved
	if err := a.q.QueryRow(ctx, `SELECT COALESCE(SUM((detail->>'fee')::bigint), 0)::bigint FROM sovereign_loan_events WHERE kind = 'fee_paid'`).Scan(&s.DebtFeesRows); err != nil {
		return fmt.Errorf("postgres: checking the late fees: %w", err)
	}
	// the delegation ceiling holds for every open office of an active union (rail D1)
	rows, err := a.q.Query(ctx, `SELECT u.delegations, o.grants FROM union_offices o JOIN unions u ON u.id = o.union_id WHERE o.closed_at IS NULL AND u.status = 'active'`)
	if err != nil {
		return fmt.Errorf("postgres: reading the union offices: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var del []string
		var raw []byte
		if err := rows.Scan(&del, &raw); err != nil {
			return fmt.Errorf("postgres: reading a union office: %w", err)
		}
		grants, err := grantsFromJSON(raw)
		if err != nil {
			return err
		}
		if polity.Ceiling(classesFromText(del), grants) != nil {
			s.UnionCeilingBroken++
		}
	}
	_ = charter.OfficeEdit
	return rows.Err()
}
