package postgres

import (
	"context"
	"fmt"
)

// verifyService runs the checks of the daily services when migration 0137 is applied (docs/adr/0052): the wages in the
// ledger are what the days paid, the supplies in the item journal are what the open posts used, and each day is the sum
// of its posts (staff, wage, held), with its one ledger transaction for exactly the wage.
func (a *EconomyAdmin) verifyService(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.service_days') IS NOT NULL`).Scan(&s.Service); err != nil {
		return fmt.Errorf("postgres: looking for the service days: %w", err)
	}
	if !s.Service {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.ServiceLedger, "service wages in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'service_wage' AND amount > 0`},
		{&s.ServiceRows, "service wages in the days", `SELECT COALESCE(SUM(wage), 0)::bigint FROM service_days`},
		{&s.ServiceItems, "service upkeep in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'service_upkeep'`},
		{&s.ServiceUsedRows, "service upkeep in the posts", `
			SELECT COALESCE(SUM(u.value::bigint), 0)::bigint FROM service_day_posts p, jsonb_each_text(p.used) u`},
		{&s.ServiceDayBroken, "service days against their posts", `
			SELECT count(*) FROM service_days d
			 WHERE d.wage <> COALESCE((SELECT SUM(p.wage) FROM service_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day), 0)
			    OR d.staff <> COALESCE((SELECT SUM(p.staff) FROM service_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day), 0)
			    OR d.held <> (SELECT count(*) FROM service_day_posts p WHERE p.settlement_id = d.settlement_id AND p.day = d.day AND p.held)
			    OR (d.wage > 0 AND (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.wage_tx AND e.amount > 0) <> d.wage)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
