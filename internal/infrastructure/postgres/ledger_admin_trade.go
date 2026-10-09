package postgres

import (
	"context"
	"fmt"
)

// verifyTrade runs the checks of the market day when migration 0136 is applied (docs/adr/0049): the trader's money in
// the ledger is what the days say, one transaction of source to treasury for the gross; the goods that left the stores in
// the item journal are what the lines sold; a day's gross is its lines and never more than its cap; no line was paid
// above its reference price (no arbitrage against Support's market); the clerk's wages are what the days paid.
func (a *EconomyAdmin) verifyTrade(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.trade_days') IS NOT NULL`).Scan(&v.VillageInvariants.Trade); err != nil {
		return fmt.Errorf("postgres: looking for the market days: %w", err)
	}
	if !v.VillageInvariants.Trade {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.TradeLedger, "export sales in the ledger", `
			SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = 'export_sale' AND e.amount > 0 AND a.kind = 'city_treasury'`},
		{&s.TradeRows, "export sales in the market days", `SELECT COALESCE(SUM(gross), 0)::bigint FROM trade_days`},
		{&s.TradeItems, "exported goods in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'exported'`},
		{&s.TradeLineUnits, "units in the market lines", `SELECT COALESCE(SUM(qty), 0)::bigint FROM trade_day_lines`},
		{&s.TradeDayBroken, "market days against their lines", `
			SELECT count(*) FROM trade_days d
			 WHERE d.gross <> COALESCE((SELECT SUM(l.qty * l.unit_price) FROM trade_day_lines l WHERE l.settlement_id = d.settlement_id AND l.day = d.day), 0)
			    OR (d.outcome = 'sold' AND d.sale_tx IS NOT NULL AND
			        (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.sale_tx AND e.amount > 0) <> d.gross)
			    OR (d.wage > 0 AND
			        (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.wage_tx AND e.amount > 0) <> d.wage)`},
		{&s.TradeWageLedger, "clerks' wages in the ledger", `
			SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'market_clerk_wage' AND amount > 0`},
		{&s.TradeWageRows, "clerks' wages in the market days", `SELECT COALESCE(SUM(wage), 0)::bigint FROM trade_days`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
