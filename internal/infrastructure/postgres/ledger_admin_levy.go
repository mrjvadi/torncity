package postgres

import (
	"context"
	"fmt"
)

// verifyLevyRefund runs the checks of the one-off refund of the national levy when migration 0135 is applied: a
// refund is exactly what national_levy took from that settlement, its transaction is the settlement's credit and the
// sources' debits for that amount, and the ledger's levy_refund credits are the rows' sum.
func (a *EconomyAdmin) verifyLevyRefund(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.levy_refunds') IS NOT NULL`).Scan(&v.VillageInvariants.LevyRefunds); err != nil {
		return fmt.Errorf("postgres: looking for the levy refunds: %w", err)
	}
	if !v.VillageInvariants.LevyRefunds {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.LevyRefundLedger, "levy refunds in the ledger", `
			SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = 'levy_refund' AND e.amount > 0 AND a.kind = 'city_treasury'`},
		{&s.LevyRefundRows, "levy refund rows", `SELECT COALESCE(SUM(levy), 0)::bigint FROM levy_refunds`},
		{&s.LevyRefundMismatched, "levy refunds against what was taken", `
			SELECT count(*) FROM levy_refunds r
			 WHERE r.levy <> COALESCE((SELECT SUM(-e.amount) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			                            WHERE a.kind = 'city_treasury' AND a.owner_id = r.settlement_id
			                              AND e.reason = 'national_levy' AND e.amount < 0), 0)
			    OR r.levy <> COALESCE((SELECT SUM(e.amount) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			                            WHERE e.transaction_id = r.ledger_transaction_id AND a.kind = 'city_treasury'
			                              AND a.owner_id = r.settlement_id AND e.reason = 'levy_refund' AND e.amount > 0), 0)
			    OR r.levy <> COALESCE((SELECT SUM(-e.amount) FROM ledger_entries e
			                            WHERE e.transaction_id = r.ledger_transaction_id AND e.amount < 0), 0)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
