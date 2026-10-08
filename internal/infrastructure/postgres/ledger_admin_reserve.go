package postgres

import (
	"context"
	"fmt"
)

// verifyReserve runs the checks of the reserve tools when migration 0131 is applied (docs/adr/0033 6.11, 6.13, 7):
// what left the pot for the book and came back equals the ledger; what left it for good (an excess withdrawn,
// a claim, the remainder at retirement) equals the ledger; every claim is its burn and its payment and the
// claims never take more than the pot held; every done withdrawal is its transaction; a retired money's pot is
// empty; and the macro history is append-only.
func (a *EconomyAdmin) verifyReserve(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.currency_claims') IS NOT NULL`).Scan(&v.VillageInvariants.Reserve); err != nil {
		return fmt.Errorf("postgres: looking for the reserve tools: %w", err)
	}
	if !v.VillageInvariants.Reserve {
		return nil
	}
	s := &v.VillageInvariants
	const potOf = `(SELECT a.id FROM accounts a WHERE a.kind = 'reserve_pot' AND a.owner_id = st.settlement_id AND a.currency = 'SUP')`
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.ReserveFlowMismatched, "intervention flows", `
			SELECT count(*) FROM village_currency_state st
			 WHERE st.intervention_out <> COALESCE((SELECT SUM(e.amount) FROM ledger_entries e
			        WHERE e.reason = 'intervention_buy' AND e.amount > 0 AND e.reference_type = 'fx_orders'
			          AND e.reference_id IN (SELECT o.id FROM fx_orders o WHERE o.settlement_id = st.settlement_id)), 0)
			    OR st.intervention_in <> COALESCE((SELECT SUM(e.amount) FROM ledger_entries e
			        WHERE e.account_id = ` + potOf + ` AND e.amount > 0 AND e.reason IN ('fx_release', 'fx_trade_sup')), 0)`},
		{&s.ReleasedMismatched, "released SUP", `
			SELECT count(*) FROM village_currency_state st
			 WHERE st.released_sup <> COALESCE((SELECT SUM(-e.amount) FROM ledger_entries e
			        WHERE e.account_id = ` + potOf + ` AND e.amount < 0 AND e.reason IN ('reserve_release', 'wind_down_claim')), 0)`},
		{&s.ClaimMismatched, "claims", `
			SELECT count(*) FROM currency_claims c
			 WHERE (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			         WHERE e.transaction_id = c.burn_transaction_id AND e.reason = 'currency_burn' AND e.amount > 0 AND a.kind = 'system_sink') <> c.units
			    OR (c.sup > 0 AND (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = c.sup_transaction_id AND e.reason = 'wind_down_claim' AND e.amount > 0) <> c.sup)
			    OR (c.sup = 0 AND c.sup_transaction_id IS NOT NULL)
			    OR c.sup > c.pot_before`},
		{&s.ClaimOverdrawn, "claims against the pot", `
			SELECT count(*) FROM village_currency_state st
			 WHERE COALESCE((SELECT SUM(c.sup) FROM currency_claims c WHERE c.settlement_id = st.settlement_id), 0) > st.deposited_sup + st.intervention_in`},
		{&s.WithdrawalMismatched, "withdrawals", `
			SELECT count(*) FROM currency_withdrawals w
			 WHERE (w.status = 'done' AND (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			          WHERE e.transaction_id = w.ledger_transaction_id AND e.reason = 'reserve_release' AND e.amount > 0) <> w.sup)
			    OR (w.status <> 'done' AND w.ledger_transaction_id IS NOT NULL)`},
		{&s.RetiredPotMismatched, "retired pots", `
			SELECT count(*) FROM village_currency_state st
			 WHERE st.status = 'retired' AND COALESCE((SELECT a.balance FROM accounts a WHERE a.id = ` + potOf + `), 0) <> 0`},
		{&s.MacroGuards, "macro history guard", `
			SELECT count(*) FROM pg_trigger WHERE tgrelid = 'village_macro_periods'::regclass AND NOT tgisinternal
			   AND tgname IN ('village_macro_periods_append_only', 'village_macro_periods_no_truncate')`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
