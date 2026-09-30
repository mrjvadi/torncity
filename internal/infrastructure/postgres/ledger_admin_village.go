package postgres

import (
	"context"
	"fmt"
)

// VillageInvariants are the checks of `admin economy verify` for a village
// treasury's two faucets (migration 0052_village_treasury): the founding
// grant and residents' donations. Each ledger credit must be exactly what
// its row records, every grant must be the ledger transaction its row names,
// and every founded settlement must have received its one grant.
type VillageInvariants struct {
	// GrantLedger is the settlement_grant credited to treasuries in the
	// ledger; GrantRows the amount the settlement_grants rows record.
	GrantLedger, GrantRows int64
	// GrantMismatched counts grant rows whose ledger transaction does not
	// credit that settlement's treasury exactly the recorded amount, once.
	GrantMismatched int64
	// Ungranted counts founded settlements with no grant row: run
	// `admin settlement backfill-grants`.
	Ungranted int64
	// DonationLedger is the settlement_donation credited to treasuries in
	// the ledger; DonationRows the amount the settlement_donations rows
	// record; DonationMismatched the rows whose ledger transaction does not
	// move exactly that amount from the donor's cash to the village treasury.
	DonationLedger, DonationRows int64
	DonationMismatched           int64
	// TopupLedger, TopupRows, TopupMismatched: the same for the operator's
	// top-ups (settlement_topups).
	TopupLedger, TopupRows int64
	TopupMismatched        int64
}

func (v VillageInvariants) ok() bool {
	return v.GrantLedger == v.GrantRows && v.GrantMismatched == 0 && v.Ungranted == 0 &&
		v.DonationLedger == v.DonationRows && v.DonationMismatched == 0 &&
		v.TopupLedger == v.TopupRows && v.TopupMismatched == 0
}

// verifyVillage runs the village treasury's invariants.
func (a *EconomyAdmin) verifyVillage(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	credited := `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = $1 AND amount > 0`
	type check struct {
		into *int64
		what string
		sql  string
		args []any
	}
	checks := []check{
		{&s.GrantLedger, "settlement grants", credited, []any{"settlement_grant"}},
		{&s.GrantRows, "settlement grant rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM settlement_grants`, nil},
		{&s.GrantMismatched, "settlement grant transactions", `
			SELECT count(*) FROM settlement_grants g
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = g.ledger_transaction_id AND e.reason = 'settlement_grant'
			           AND e.reference_type = 'settlement_grants' AND e.reference_id = g.settlement_id
			           AND e.amount > 0) <> 1
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = g.ledger_transaction_id AND e.amount > 0) <> g.amount`, nil},
		{&s.Ungranted, "founded settlements without a grant", `
			SELECT count(*) FROM cities c
			 WHERE c.origin = 'founded'
			   AND NOT EXISTS (SELECT 1 FROM settlement_grants g WHERE g.settlement_id = c.id)`, nil},
		{&s.DonationLedger, "settlement donations", credited, []any{"settlement_donation"}},
		{&s.DonationRows, "settlement donation rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM settlement_donations`, nil},
		{&s.DonationMismatched, "settlement donation transactions", `
			SELECT count(*) FROM settlement_donations d
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = d.ledger_transaction_id AND e.reason = 'settlement_donation'
			           AND e.reference_type = 'settlement_donations' AND e.reference_id = d.id) <> 2
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = d.ledger_transaction_id AND e.amount > 0) <> d.amount`, nil},
		{&s.TopupLedger, "settlement top-ups", credited, []any{"settlement_topup"}},
		{&s.TopupRows, "settlement top-up rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM settlement_topups`, nil},
		{&s.TopupMismatched, "settlement top-up transactions", `
			SELECT count(*) FROM settlement_topups t
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = t.ledger_transaction_id AND e.reason = 'settlement_topup'
			           AND e.reference_type = 'settlement_topups' AND e.reference_id = t.id
			           AND e.amount > 0) <> 1
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = t.ledger_transaction_id AND e.amount > 0) <> t.amount`, nil},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
