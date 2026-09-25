package postgres

import (
	"context"
	"fmt"
)

// DefenceInvariants are the checks of `admin economy verify` for the armed
// forces as an employer (docs/adr/0022-military-and-diplomacy.md section
// 2.14): every military wage left a defence fund for a soldier's cash, and
// the ledger paid exactly the gross of the shifts it paid for.
type DefenceInvariants struct {
	// StrayWageLegs are military_wage entries on anything but a defence
	// fund (paying) or a player's cash (paid).
	StrayWageLegs int64
	// WageLedger is what the defence funds paid in military wages;
	// WageRows the gross of the shifts those payments name.
	WageLedger, WageRows int64
}

func (d DefenceInvariants) ok() bool {
	return d.StrayWageLegs == 0 && d.WageLedger == d.WageRows
}

// verifyDefence runs the defence invariants.
func (a *EconomyAdmin) verifyDefence(ctx context.Context, v *LedgerVerification) error {
	d := &v.DefenceInvariants
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		         WHERE e.reason = 'military_wage'
		           AND ((e.amount < 0 AND a.kind <> 'defence_fund') OR (e.amount > 0 AND a.kind <> 'player_cash'))),
		       (SELECT COALESCE(-SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		         WHERE e.reason = 'military_wage' AND a.kind = 'defence_fund'),
		       (SELECT COALESCE(SUM(w.gross), 0)::bigint FROM work_shifts w
		         WHERE w.ledger_transaction_id IN (SELECT transaction_id FROM ledger_entries WHERE reason = 'military_wage'))`).Scan(
		&d.StrayWageLegs, &d.WageLedger, &d.WageRows); err != nil {
		return fmt.Errorf("postgres: checking military wages: %w", err)
	}
	return nil
}
