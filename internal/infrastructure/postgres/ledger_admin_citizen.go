package postgres

import (
	"context"
	"fmt"
)

// CitizenInvariants are the checks of `admin economy verify` for the citizen
// loop (migration 0058_citizen_loop): a lot's price, a building's permit,
// cost and materials, and the property tax each appear in the ledger exactly
// as their journal rows record, and none of these reasons ever credits a
// player (no residency faucet, docs/adr/0033 section 4.4).
type CitizenInvariants struct {
	// LotSaleLedger is the settlement_lot_sale credited to treasuries in the
	// ledger; LotSaleRows the price the settlement_lots rows record;
	// LotSaleMismatched the lots whose ledger transaction does not move
	// exactly that price from the buyer's cash to the village treasury.
	LotSaleLedger, LotSaleRows, LotSaleMismatched int64
	// PermitLedger, PermitRows: the same for permit fees.
	PermitLedger, PermitRows int64
	// ConstructionLedger and ConstructionRows are what citizens paid for the
	// building itself (sink side); MaterialsLedger and MaterialsRows what
	// they paid for bought materials.
	ConstructionLedger, ConstructionRows int64
	MaterialsLedger, MaterialsRows       int64
	// BuildingMismatched counts private buildings whose permit, construction
	// or materials in the ledger (by reference) differ from their row.
	BuildingMismatched int64
	// TaxLedger is the settlement_property_tax credited to treasuries; TaxRows
	// the tax the paid rows record; TaxMismatched the paid rows whose ledger
	// transaction does not move exactly their due from the owner's cash.
	TaxLedger, TaxRows, TaxMismatched int64
	// CitizenPaidToPlayer counts ledger entries of these five reasons that
	// credit a player's cash: it must be zero, the loop is never a faucet.
	CitizenPaidToPlayer int64
}

func (v CitizenInvariants) ok() bool {
	return v.LotSaleLedger == v.LotSaleRows && v.LotSaleMismatched == 0 &&
		v.PermitLedger == v.PermitRows && v.ConstructionLedger == v.ConstructionRows &&
		v.MaterialsLedger == v.MaterialsRows && v.BuildingMismatched == 0 &&
		v.TaxLedger == v.TaxRows && v.TaxMismatched == 0 && v.CitizenPaidToPlayer == 0
}

// verifyCitizen runs the citizen loop's invariants.
func (a *EconomyAdmin) verifyCitizen(ctx context.Context, v *LedgerVerification) error {
	s := &v.CitizenInvariants
	credited := `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = $1 AND amount > 0`
	type check struct {
		into *int64
		what string
		sql  string
		args []any
	}
	checks := []check{
		{&s.LotSaleLedger, "lot sales", credited, []any{"settlement_lot_sale"}},
		{&s.LotSaleRows, "lot rows", `SELECT COALESCE(SUM(price), 0)::bigint FROM settlement_lots`, nil},
		{&s.LotSaleMismatched, "lot transactions", `
			SELECT count(*) FROM settlement_lots l
			 WHERE (SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = l.ledger_transaction_id AND e.reason = 'settlement_lot_sale'
			           AND e.reference_type = 'settlement_lots' AND e.reference_id = l.id) <> 2
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = l.ledger_transaction_id AND e.amount > 0) <> l.price`, nil},
		{&s.PermitLedger, "permit fees", credited, []any{"settlement_permit_fee"}},
		{&s.PermitRows, "permit rows", `SELECT COALESCE(SUM(permit_fee), 0)::bigint FROM settlement_private_buildings`, nil},
		{&s.ConstructionLedger, "private construction", credited, []any{"citizen_construction"}},
		{&s.ConstructionRows, "private construction rows", `SELECT COALESCE(SUM(construction_paid), 0)::bigint FROM settlement_private_buildings`, nil},
		{&s.MaterialsLedger, "bought materials", credited, []any{"citizen_materials"}},
		{&s.MaterialsRows, "bought materials rows", `SELECT COALESCE(SUM(materials_paid), 0)::bigint FROM settlement_private_buildings`, nil},
		{&s.BuildingMismatched, "private building transactions", `
			SELECT count(*) FROM settlement_private_buildings b
			 WHERE (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.reason = 'settlement_permit_fee'
			         AND e.reference_type = 'settlement_private_buildings' AND e.reference_id = b.building_id AND e.amount > 0) <> b.permit_fee
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.reason = 'citizen_construction'
			         AND e.reference_type = 'settlement_private_buildings' AND e.reference_id = b.building_id AND e.amount > 0) <> b.construction_paid
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.reason = 'citizen_materials'
			         AND e.reference_type = 'settlement_private_buildings' AND e.reference_id = b.building_id AND e.amount > 0) <> b.materials_paid`, nil},
		{&s.TaxLedger, "property tax", credited, []any{"settlement_property_tax"}},
		{&s.TaxRows, "property tax rows", `SELECT COALESCE(SUM(due), 0)::bigint FROM settlement_property_tax WHERE paid_at IS NOT NULL`, nil},
		{&s.TaxMismatched, "property tax transactions", `
			SELECT count(*) FROM settlement_property_tax t
			 WHERE t.paid_at IS NOT NULL
			   AND ((SELECT count(*) FROM ledger_entries e
			          WHERE e.transaction_id = t.ledger_transaction_id AND e.reason = 'settlement_property_tax'
			            AND e.reference_type = 'settlement_property_tax' AND e.reference_id = t.id) <> 2
			     OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			          WHERE e.transaction_id = t.ledger_transaction_id AND e.amount > 0) <> t.due)`, nil},
		{&s.CitizenPaidToPlayer, "citizen reasons paying a player", `
			SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason IN ('settlement_lot_sale', 'settlement_permit_fee', 'citizen_construction',
			                    'citizen_materials', 'settlement_property_tax')
			   AND e.amount > 0 AND a.kind = 'player_cash'`, nil},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
