package postgres

import (
	"context"
	"fmt"
)

// verifyLots runs the checks of the function-and-content model when migration 0133 is applied (docs/adr/0045
// phase B1): modules and looks belong to a function row, a function row to a building; an order's work is what
// its fitout shifts did, no more; the materials the item journal says left the stores are what the orders cost;
// the fee of a change of use in the ledger is what the orders paid in SUP, and every conversion row has its fee;
// the history of conversions is append-only.
func (a *EconomyAdmin) verifyLots(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.building_functions') IS NOT NULL AND to_regclass('public.building_works') IS NOT NULL`).Scan(&v.VillageInvariants.Lots); err != nil {
		return fmt.Errorf("postgres: looking for the lot model: %w", err)
	}
	if !v.VillageInvariants.Lots {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.LotOrphans, "modules and looks without a function", `
			SELECT (SELECT count(*) FROM building_modules m WHERE NOT EXISTS (SELECT 1 FROM building_functions f
			         WHERE f.building_ref_kind = m.building_ref_kind AND f.building_ref_id = m.building_ref_id))
			     + (SELECT count(*) FROM building_looks l WHERE NOT EXISTS (SELECT 1 FROM building_functions f
			         WHERE f.building_ref_kind = l.building_ref_kind AND f.building_ref_id = l.building_ref_id))
			     + (SELECT count(*) FROM building_functions f WHERE f.building_ref_kind = 'settlement_building'
			         AND NOT EXISTS (SELECT 1 FROM settlement_buildings b WHERE b.id = f.building_ref_id))`},
		{&s.LotWorkMismatched, "orders and their shifts", `
			SELECT count(*) FROM building_works w
			 WHERE EXISTS (SELECT 1 FROM labor_jobs j WHERE j.building_id = w.building_id AND j.kind = 'fitout' AND j.created_at >= w.ordered_at)
			   AND ((w.status = 'done' AND w.work_done < w.work_required)
			     OR w.work_done > COALESCE((SELECT SUM(sh.work_points) FROM settlement_shifts sh
			         WHERE sh.building_id = w.building_id AND sh.kind = 'fitout' AND sh.status = 'done'
			           AND sh.finish_at >= w.ordered_at), 0))`},
		{&s.LotShiftsWithoutJob, "fitout shifts without a job", `
			SELECT count(*) FROM settlement_shifts s
			 WHERE s.kind = 'fitout' AND (s.work_points <= 0 OR NOT EXISTS
			       (SELECT 1 FROM labor_jobs j WHERE j.id = s.job_id AND j.kind = 'fitout'))`},
		{&s.LotMaterialsMismatched, "materials of the orders in the item journal", `
			SELECT ABS((SELECT COALESCE(SUM(quantity), 0) FROM item_movements WHERE reason = 'fitout_materials')
			         - (SELECT COALESCE(SUM(v.value::bigint), 0) FROM building_works w, jsonb_each_text(w.cost_materials) v))`},
		{&s.LotFeeMismatched, "fees of changes of use", `
			SELECT ABS((SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE reason = 'use_change_fee' AND amount > 0)
			         - (SELECT COALESCE(SUM(w.fee_paid), 0) FROM building_works w
			             WHERE NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = w.id)))
			     + (SELECT count(*) FROM function_conversions c
			         WHERE NOT EXISTS (SELECT 1 FROM building_works w WHERE w.id = c.work_id AND w.status = 'done' AND w.fee_paid = c.fee))`},
		{&s.LotHistoryGuards, "conversion history guard", `
			SELECT count(*) FROM pg_trigger WHERE tgrelid = 'function_conversions'::regclass AND NOT tgisinternal
			   AND tgname IN ('function_conversions_append_only', 'function_conversions_no_truncate')`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
