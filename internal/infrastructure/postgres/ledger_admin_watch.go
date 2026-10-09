package postgres

import (
	"context"
	"fmt"
)

// verifyWatch runs the checks of the night watch when migration 0137 is applied (docs/adr/0052): the wages in the
// ledger are what the days paid, the firewood in the item journal is what the fires burnt, and each day is the sum of
// its posts (guards, wage, fuel, held), with its one ledger transaction for exactly the wage.
func (a *EconomyAdmin) verifyWatch(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.watch_days') IS NOT NULL`).Scan(&s.Watch); err != nil {
		return fmt.Errorf("postgres: looking for the watch days: %w", err)
	}
	if !s.Watch {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.WatchLedger, "watch wages in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'watch_wage' AND amount > 0`},
		{&s.WatchRows, "watch wages in the days", `SELECT COALESCE(SUM(wage), 0)::bigint FROM watch_days`},
		{&s.WatchItems, "watch fuel in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'watch_fuel'`},
		{&s.WatchFuelRows, "watch fuel in the days", `SELECT COALESCE(SUM(fuel), 0)::bigint FROM watch_days`},
		{&s.WatchDayBroken, "watch days against their posts", `
			SELECT count(*) FROM watch_days d
			 WHERE d.wage <> COALESCE((SELECT SUM(p.wage) FROM watch_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day), 0)
			    OR d.fuel <> COALESCE((SELECT SUM(p.fuel) FROM watch_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day), 0)
			    OR d.guards <> COALESCE((SELECT SUM(p.guards) FROM watch_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day), 0)
			    OR d.held <> (SELECT count(*) FROM watch_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day AND p.held)
			    OR (d.wage > 0 AND (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.wage_tx AND e.amount > 0) <> d.wage)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
