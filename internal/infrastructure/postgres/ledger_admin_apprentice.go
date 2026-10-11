package postgres

import (
	"context"
	"fmt"
)

// verifyApprentice runs the checks of the teaching network when migration 0148 is applied (docs/adr/0071): the fees and refunds in the
// ledger are what the contracts say, every certificate of the apprenticeship route has an apprenticeship that passed its exam, and no
// master has more running contracts than his bound allows (the bound is content; here, the schema's own hard ceiling of 10).
func (a *EconomyAdmin) verifyApprentice(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.apprenticeships') IS NOT NULL`).Scan(&s.Apprentice); err != nil {
		return fmt.Errorf("postgres: looking for the apprenticeships: %w", err)
	}
	if !s.Apprentice {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.ApprenticeFeeLedger, "apprentice fees in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'apprentice_fee' AND amount > 0`},
		{&s.ApprenticeFeeRows, "apprentice fees in the contracts", `SELECT COALESCE(SUM(fee_paid), 0)::bigint FROM apprenticeships`},
		{&s.ApprenticeRefundLedger, "apprentice refunds in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'apprentice_refund' AND amount > 0`},
		{&s.ApprenticeRefundRows, "apprentice refunds in the contracts", `SELECT COALESCE(SUM(refunded), 0)::bigint FROM apprenticeships`},
		{&s.ApprenticeCertBroken, "certificates of the apprenticeship route", `
			SELECT count(*) FROM certifications c WHERE c.route = 'apprenticeship' AND NOT EXISTS (
			  SELECT 1 FROM apprenticeships a JOIN apprentice_exams e ON e.apprenticeship_id = a.id AND e.passed
			   WHERE a.id = c.apprenticeship_id AND a.apprentice_id = c.player_id AND a.certified_at IS NOT NULL)`},
		{&s.ApprenticeOverSlots, "masters over the ceiling", `
			SELECT count(*) FROM (SELECT master_id FROM apprenticeships WHERE status = 'active' AND master_id IS NOT NULL GROUP BY master_id HAVING count(*) > 10) x`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
