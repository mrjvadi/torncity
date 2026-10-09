package postgres

import (
	"context"
	"fmt"
)

// verifyToolWear runs the checks of the tool wear (docs/adr/0050): the tools that left the stores as shift inputs are the
// tools the shifts recorded as consumed, and no workplace carries more wear than the one tool that can be due.
func (a *EconomyAdmin) verifyToolWear(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	var have bool
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.settlement_shifts') IS NOT NULL`).Scan(&have); err != nil {
		return fmt.Errorf("postgres: looking for the shifts: %w", err)
	}
	if !have {
		return nil // a database from before the village
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.ToolInputs, "tools in the item journal", `
			SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements
			 WHERE item_code = 'tools' AND reason = 'production_input' AND reference_type = 'settlement_shift'`},
		{&s.ToolShiftUnits, "tools in the shifts", `
			SELECT COALESCE(SUM((consumed->>'tools')::bigint), 0)::bigint FROM settlement_shifts WHERE consumed ? 'tools'`},
		{&s.ToolWearBroken, "workplaces whose wear is out of range", `
			SELECT count(*) FROM settlement_buildings
			 WHERE carry ? '~tools' AND ((carry->>'~tools')::bigint < 0 OR (carry->>'~tools')::bigint > 10000)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
