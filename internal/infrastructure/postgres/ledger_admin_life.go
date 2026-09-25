package postgres

import (
	"context"
	"fmt"
)

// LifeInvariants are the checks of `admin economy verify` for a character's
// life (docs/adr/0025-life-and-legacy.md): a hostel's lodging fees in the
// ledger match the paid nights, each paid night has one ledger transaction
// of its price, and no free night moved money.
type LifeInvariants struct {
	LodgingLedger, LodgingRows int64
	// UnpaidNights are paid nights with no lodging fee of their own in the
	// ledger.
	UnpaidNights int64
}

func (s LifeInvariants) ok() bool {
	return s.LodgingLedger == s.LodgingRows && s.UnpaidNights == 0
}

// verifyLife runs the life's invariants.
func (a *EconomyAdmin) verifyLife(ctx context.Context, v *LedgerVerification) error {
	s := &v.LifeInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.LodgingLedger, "lodging fees", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries
			WHERE reason = 'lodging_fee' AND amount > 0`},
		{&s.LodgingRows, "nights paid", `SELECT COALESCE(SUM(price), 0)::bigint FROM life_sleeps`},
		{&s.UnpaidNights, "nights without their fee", `
			SELECT count(*) FROM life_sleeps s
			 WHERE s.price > 0 AND NOT EXISTS (
			     SELECT 1 FROM ledger_entries e
			      WHERE e.transaction_id = s.ledger_transaction_id AND e.reason = 'lodging_fee'
			        AND e.reference_type = 'life_sleeps' AND e.reference_id = s.id AND e.amount = s.price)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
