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

	// The village economy (migration 0055). MaterialLedger is what the
	// ledger says villages paid Support for materials, MaterialRows the sum of
	// the purchase rows' totals; MaterialMismatched the rows whose ledger
	// transaction is not exactly treasury -> sink for that total.
	// MaterialItems and MaterialItemRows: the units the item journal says
	// entered village stocks as bought, and the units the rows say.
	MaterialLedger, MaterialRows    int64
	MaterialMismatched              int64
	MaterialItems, MaterialItemRows int64
	// WageLedger is what the ledger says villages paid residents for shifts,
	// WageRows what the finished shifts record; WageMismatched the shifts whose
	// ledger transaction is not exactly treasury -> the worker's cash for the
	// wage paid. ShiftItems and ShiftItemRows: the units the item journal says
	// shifts produced, and the units the finished shifts record.
	WageLedger, WageRows      int64
	WageMismatched            int64
	ShiftItems, ShiftItemRows int64
}

func (v VillageInvariants) ok() bool {
	return v.GrantLedger == v.GrantRows && v.GrantMismatched == 0 && v.Ungranted == 0 &&
		v.DonationLedger == v.DonationRows && v.DonationMismatched == 0 &&
		v.TopupLedger == v.TopupRows && v.TopupMismatched == 0 &&
		v.MaterialLedger == v.MaterialRows && v.MaterialMismatched == 0 && v.MaterialItems == v.MaterialItemRows &&
		v.WageLedger == v.WageRows && v.WageMismatched == 0 && v.ShiftItems == v.ShiftItemRows
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
		{&s.MaterialLedger, "village material purchases", credited, []any{"settlement_material_purchase"}},
		{&s.MaterialRows, "village material purchase rows", `SELECT COALESCE(SUM(total), 0)::bigint FROM settlement_material_purchases`, nil},
		{&s.MaterialMismatched, "village material purchase transactions", `
			SELECT count(*) FROM settlement_material_purchases p
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = p.ledger_transaction_id AND e.reason = 'settlement_material_purchase'
			           AND e.reference_type = 'settlement_material_purchases' AND e.reference_id = p.id) <> 2
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = p.ledger_transaction_id AND e.amount > 0) <> p.total`, nil},
		{&s.MaterialItems, "village material units in the item journal", `
			SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements
			 WHERE reason = 'supplied' AND reference_type = 'settlement_material_purchase'`, nil},
		{&s.MaterialItemRows, "village material units in the purchase rows", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM settlement_material_purchases`, nil},
		{&s.WageLedger, "village shift wages", credited, []any{"settlement_wage"}},
		{&s.WageRows, "village shift wage rows", `SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts WHERE status = 'done'`, nil},
		{&s.WageMismatched, "village shift wage transactions", `
			SELECT count(*) FROM settlement_shifts s
			 WHERE s.status = 'done'
			   AND ((s.wage_paid > 0
			         AND ((SELECT count(*) FROM ledger_entries e
			                WHERE e.transaction_id = s.ledger_transaction_id AND e.reason = 'settlement_wage'
			                  AND e.reference_type = 'settlement_shifts' AND e.reference_id = s.id) <> 2
			           OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			                WHERE e.transaction_id = s.ledger_transaction_id AND e.amount > 0) <> s.wage_paid))
			     OR (s.wage_paid = 0 AND s.ledger_transaction_id IS NOT NULL))`, nil},
		{&s.ShiftItems, "village goods produced in the item journal", `
			SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements
			 WHERE reason = 'produced' AND reference_type = 'settlement_shift'`, nil},
		{&s.ShiftItemRows, "village goods produced in the shift rows", `
			SELECT COALESCE(SUM(v.value::bigint), 0)::bigint
			  FROM settlement_shifts s, jsonb_each_text(s.produced) v WHERE s.status = 'done'`, nil},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
