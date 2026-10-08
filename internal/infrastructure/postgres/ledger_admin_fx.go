package postgres

import (
	"context"
	"fmt"
)

// verifyFX runs the checks of the floating VC/SUP book when migration 0130 is applied (docs/adr/0033 6.8, 6.11):
// what the escrow accounts hold is exactly what the open orders still hold; the ledger's fills equal the fill
// rows and each fill is its two transactions; every order's filled quantity is what its fills add up to; and
// the reference rate's history is append-only, continuous, and ends at the rate the money carries.
func (a *EconomyAdmin) verifyFX(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.fx_orders') IS NOT NULL`).Scan(&v.VillageInvariants.FXBook); err != nil {
		return fmt.Errorf("postgres: looking for the book: %w", err)
	}
	if !v.VillageInvariants.FXBook {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.FXEscrowMismatched, "escrow accounts", `
			SELECT count(*) FROM (
			    SELECT a.owner_id, a.currency, a.balance AS held FROM accounts a WHERE a.kind = 'fx_escrow'
			) e FULL JOIN (
			    SELECT o.owner_id, CASE o.side WHEN 'buy' THEN 'SUP' ELSE st.currency_code END AS currency, SUM(o.escrow_left)::bigint AS owed
			      FROM fx_orders o JOIN village_currency_state st ON st.settlement_id = o.settlement_id
			     WHERE o.status = 'open' GROUP BY 1, 2
			) o ON o.owner_id = e.owner_id AND o.currency = e.currency
			WHERE COALESCE(e.held, 0) <> COALESCE(o.owed, 0)`},
		{&s.FXTradeLedgerSUP, "fills in the ledger (SUP)", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'fx_trade_sup' AND amount > 0`},
		{&s.FXTradeRowsSUP, "fill rows (SUP)", `SELECT COALESCE(SUM(sup_amount + sup_fee), 0)::bigint FROM fx_trades`},
		{&s.FXTradeLedgerVC, "fills in the ledger (units)", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'fx_trade_vc' AND amount > 0`},
		{&s.FXTradeRowsVC, "fill rows (units)", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM fx_trades`},
		{&s.FXTradeMismatched, "fill transactions", `
			SELECT count(*) FROM fx_trades t
			 WHERE ((t.sup_amount + t.sup_fee > 0) AND (
			          t.sup_transaction_id IS NULL
			       OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = t.sup_transaction_id AND e.amount > 0
			              AND e.reason = 'fx_trade_sup' AND e.reference_type = 'fx_trades' AND e.reference_id = t.id) <> t.sup_amount + t.sup_fee
			       OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			            WHERE e.transaction_id = t.sup_transaction_id AND e.amount > 0 AND a.kind = 'city_treasury') < t.sup_fee))
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = t.vc_transaction_id AND e.amount > 0
			           AND e.reason = 'fx_trade_vc' AND e.reference_type = 'fx_trades' AND e.reference_id = t.id) <> t.quantity
			    OR (SELECT COALESCE(SUM(-e.amount), 0) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			         WHERE e.transaction_id = t.vc_transaction_id AND e.amount < 0 AND a.kind = 'fx_escrow') <> t.quantity`},
		{&s.FXOrderMismatched, "order fills", `
			SELECT count(*) FROM fx_orders o
			 WHERE o.filled <> COALESCE((SELECT SUM(t.quantity) FROM fx_trades t WHERE t.buy_order_id = o.id OR t.sell_order_id = o.id), 0)
			    OR (o.status = 'filled' AND o.filled <> o.quantity)
			    OR (o.side = 'sell' AND o.status = 'open' AND o.escrow_left <> o.quantity - o.filled)`},
		{&s.FXRateMismatched, "rate history", `
			SELECT (SELECT count(*) FROM village_currency_state st
			         WHERE st.x_ref_ppm <> COALESCE((SELECT h.x_ref_after FROM fx_rate_history h WHERE h.settlement_id = st.settlement_id
			                                          ORDER BY h.period_no DESC LIMIT 1), 1000000))
			     + (SELECT count(*) FROM fx_rate_history h
			         WHERE EXISTS (SELECT 1 FROM fx_rate_history p WHERE p.settlement_id = h.settlement_id
			                          AND p.period_no = (SELECT max(q.period_no) FROM fx_rate_history q WHERE q.settlement_id = h.settlement_id AND q.period_no < h.period_no)
			                          AND p.x_ref_after <> h.x_ref_before))`},
		{&s.FXHistoryGuards, "rate history guard", `
			SELECT count(*) FROM pg_trigger WHERE tgrelid = 'fx_rate_history'::regclass AND NOT tgisinternal
			   AND tgname IN ('fx_rate_history_append_only', 'fx_rate_history_no_truncate')`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
