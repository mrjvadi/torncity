package postgres

import (
	"context"
	"fmt"
)

// verifyCare runs the checks of the care of founded settlements when migration 0146 is applied (docs/adr/0069): the fees clinics
// charged in the ledger (hospital_fee) are what the village treatments say, the medicine that left the settlement stocks in the
// item journal (medicine_used) is what the treatments used, and every treatment of a settlement is its village row, of its own
// kind, and the other way round.
func (a *EconomyAdmin) verifyCare(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.village_treatments') IS NOT NULL`).Scan(&s.Care); err != nil {
		return fmt.Errorf("postgres: looking for the village treatments: %w", err)
	}
	if !s.Care {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.CareFeeLedger, "clinic fees in the ledger", `
			SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e
			  JOIN hospital_treatments t ON t.ledger_transaction_id = e.transaction_id
			 WHERE e.reason = 'hospital_fee' AND e.amount > 0 AND t.provider = 'village_clinic'`},
		{&s.CareFeeRows, "clinic fees in the village treatments", `SELECT COALESCE(SUM(fee), 0)::bigint FROM village_treatments`},
		{&s.CareMedicineJournal, "medicine used in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'medicine_used'`},
		{&s.CareMedicineRows, "medicine used in the village treatments", `SELECT COALESCE(SUM(medicine_units), 0)::bigint FROM village_treatments`},
		{&s.CareBroken, "treatments against their village rows", `
			SELECT (SELECT count(*) FROM hospital_treatments t
			         WHERE t.provider IN ('health_house', 'village_clinic')
			           AND NOT EXISTS (SELECT 1 FROM village_treatments v WHERE v.treatment_id = t.id AND v.kind = t.provider
			                                AND v.settlement_id = t.city_id AND v.fee = t.price AND v.medicine_item = t.medicine_item))
			     + (SELECT count(*) FROM village_treatments v
			         WHERE NOT EXISTS (SELECT 1 FROM hospital_treatments t WHERE t.id = v.treatment_id AND t.provider = v.kind))`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
