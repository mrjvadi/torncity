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

	// The labour market (migration 0059, ADR 0037). LaborWageLedger is what the
	// ledger says construction shifts were paid (labor_wage and labor_wage_npc,
	// the credits of each transaction), LaborWageRows what the finished shifts
	// record; LaborMismatched the finished construction shifts whose ledger
	// transaction does not move exactly the wage paid, its levy included.
	// LaborEscrowLedger is what citizens set aside for started shifts,
	// LaborEscrowRows what those shifts' wages add up to.
	// LaborBuiltWithoutWork counts buildings raised by work that are complete
	// with work missing (a timer finished them), LaborWorkUnbacked buildings
	// whose work done exceeds the points of their finished shifts.
	LaborWageLedger, LaborWageRows int64
	LaborMismatched                int64
	LaborEscrowLedger              int64
	LaborEscrowRows                int64
	LaborBuiltWithoutWork          int64
	LaborWorkUnbacked              int64

	// Teaching (migration 0120, docs/research/2026-10-03-activities-audit.md section 7).
	// Teaching is whether the tables exist. CourseFeeLedger is what the ledger says
	// students paid school treasuries, CourseFeeRows the fees of the enrolments seated
	// with a school teacher; TuitionLedger and TuitionRows the same for home teachers
	// (teacher and tax legs together); TeacherWageLedger what treasuries paid teachers
	// (player and NPC), TeacherWageRows the wages the seats record as paid.
	// TeachMismatched counts seats whose paid wage has no two-leg transaction, seats of a
	// completed class still unpaid, and seats paid more than they owed.
	Teaching                           bool
	CourseFeeLedger, CourseFeeRows     int64
	TuitionLedger, TuitionRows         int64
	TeacherWageLedger, TeacherWageRows int64
	TeachMismatched                    int64
	// Working nodes (migration 0125, roadmap 2.2 phase 3). Meals: the food points the
	// settlement_meals rows opened into kitchens (units x points) against the kitchens' own
	// opened_points; the points the shifts ate against the kitchens' eaten_points; the food
	// units the item journal says were eaten (reason meal_eaten) against the rows' units.
	// NPCShiftsWithoutJob counts NPC production shifts no job posted; NPCHungry NPC shifts
	// that started unfed (an NPC never does); CarryOutOfRange workplaces whose carried
	// fraction is not within [0, 10000).
	WorkNodes                                       bool
	MealOpenedRows, MealOpenedKitchen               int64
	MealEatenShifts, MealEatenKitchen               int64
	MealJournalUnits, MealRowUnits                  int64
	NPCShiftsWithoutJob, NPCHungry, CarryOutOfRange int64
	// Condition (migration 0126): Repairs is whether the columns exist; RepairWithoutJob counts
	// repair shifts no repair job posted or that restore nothing; DamageOutOfRange buildings
	// whose damage is outside 0..10000; RepairUnpaid finished repair shifts with a gain but
	// whose wage has no transaction while it was positive.
	Repairs                            bool
	RepairWithoutJob, DamageOutOfRange int64
	// Settlement currencies (migration 0127, ADR 0033 6.11). Currencies is whether the tables exist.
	// PotMismatched counts chartered settlements whose reserve pot does not hold what was deposited
	// less what was released; SupplyMismatched those whose units in existence (minted less burnt) are
	// not what the ledger holds outside the system accounts of that currency; IssuanceMismatched those
	// whose issuance log does not add up to the state (deposits, units) or whose basis exceeds the
	// deposits; StrayHoldings counts foreign holdings in a currency no settlement chartered.
	Currencies                        bool
	PotMismatched, SupplyMismatched   int64
	IssuanceMismatched, StrayHoldings int64
	// Local obligations and the desk (migration 0128). LocalLedgerPay and LocalRowsPay: the units the ledger
	// says treasuries paid players (local_wage) against the units of the local_payments rows; the same
	// for LocalLedgerCollect (local_payment) and the collect rows. LocalMismatched counts rows whose
	// ledger transaction is not exactly two legs of their units between holdings of their currency,
	// or whose units are not the SUP amount at the rate the row names (rounded up). DeskMismatched counts
	// desk trades whose two transactions are not the quote at their rate and fee.
	// Phase 2b (migration 0129): LocalLedgerTransfer and LocalRowsTransfer are the same match for payments
	// between players (local_transfer); BurnLedger and BurnRows the units destroyed (currency_burn) against
	// the burn rows of the issuance log; BurnMismatched counts burns whose transaction is not exactly the
	// payer's holding to the currency's system sink for their units, or whose settlement's burnt counter is
	// not the sum of its burn rows.
	LocalObligations                       bool
	LocalLedgerPay, LocalRowsPay           int64
	LocalLedgerCollect, LocalRowsCollect   int64
	LocalLedgerTransfer, LocalRowsTransfer int64
	BurnLedger, BurnRows, BurnMismatched   int64
	LocalMismatched, DeskMismatched        int64
	// The floating book (migration 0130): FXBook is whether the tables exist. FXEscrowMismatched counts
	// (owner, currency) pairs whose escrow account does not hold exactly what the owner's open orders still
	// hold; FXTradeLedger* the ledger's fills against the fill rows (SUP with the fee, and units);
	// FXTradeMismatched fills whose two transactions are not what the row says; FXOrderMismatched orders
	// whose filled quantity is not what their fills add up to; FXRateMismatched currencies whose x_ref is
	// not the last reading's or whose readings do not follow one another; FXHistoryGuards the triggers that
	// keep the history append-only (two expected).
	FXBook                               bool
	FXEscrowMismatched                   int64
	FXTradeLedgerSUP, FXTradeRowsSUP     int64
	FXTradeLedgerVC, FXTradeRowsVC       int64
	FXTradeMismatched, FXOrderMismatched int64
	FXRateMismatched, FXHistoryGuards    int64
	// The reserve tools (migration 0131): Reserve is whether the tables exist. ReserveFlowMismatched counts
	// moneys whose intervention_out/in counters differ from the ledger's pot legs; ReleasedMismatched those whose
	// released_sup differs from what left the pot for good (excess withdrawn, claims, retirement remainder);
	// ClaimMismatched claims that are not their burn and their payment; ClaimOverdrawn moneys whose claims took
	// more than the pot ever held; WithdrawalMismatched done withdrawals that are not their transaction;
	// RetiredPotMismatched retired moneys with a pot that is not empty; MacroGuards the triggers that keep the
	// macro history append-only (two expected).
	Reserve                                    bool
	ReserveFlowMismatched, ReleasedMismatched  int64
	ClaimMismatched, ClaimOverdrawn            int64
	WithdrawalMismatched, RetiredPotMismatched int64
	MacroGuards                                int64
	// ServiceMisrouted counts legs of training_fee (to a treasury, from a player),
	// trainer_wage (treasury to the sink) and bag_repair (to the sink) that go
	// anywhere else: those flows have no row table, so their routes are the check.
	ServiceMisrouted int64
}

// WorkNodesOK reports whether the working-node checks hold.
func (v VillageInvariants) WorkNodesOK() bool {
	return !v.WorkNodes || (v.MealOpenedRows == v.MealOpenedKitchen && v.MealEatenShifts == v.MealEatenKitchen &&
		v.MealJournalUnits == v.MealRowUnits && v.NPCShiftsWithoutJob == 0 && v.NPCHungry == 0 && v.CarryOutOfRange == 0 &&
		(!v.Repairs || (v.RepairWithoutJob == 0 && v.DamageOutOfRange == 0))) &&
		(!v.Currencies || (v.PotMismatched == 0 && v.SupplyMismatched == 0 && v.IssuanceMismatched == 0 && v.StrayHoldings == 0)) &&
		(!v.LocalObligations || (v.LocalLedgerPay == v.LocalRowsPay && v.LocalLedgerCollect == v.LocalRowsCollect && v.LocalLedgerTransfer == v.LocalRowsTransfer &&
			v.BurnLedger == v.BurnRows && v.BurnMismatched == 0 && v.LocalMismatched == 0 && v.DeskMismatched == 0)) &&
		(!v.FXBook || (v.FXEscrowMismatched == 0 && v.FXTradeLedgerSUP == v.FXTradeRowsSUP && v.FXTradeLedgerVC == v.FXTradeRowsVC &&
			v.FXTradeMismatched == 0 && v.FXOrderMismatched == 0 && v.FXRateMismatched == 0 && v.FXHistoryGuards == 2)) &&
		(!v.Reserve || (v.ReserveFlowMismatched == 0 && v.ReleasedMismatched == 0 && v.ClaimMismatched == 0 && v.ClaimOverdrawn == 0 &&
			v.WithdrawalMismatched == 0 && v.RetiredPotMismatched == 0 && v.MacroGuards == 2))
}

// TeachingOK reports whether the teaching checks hold.
func (v VillageInvariants) TeachingOK() bool {
	return !v.Teaching || (v.CourseFeeLedger == v.CourseFeeRows && v.TuitionLedger == v.TuitionRows &&
		v.TeacherWageLedger == v.TeacherWageRows && v.TeachMismatched == 0)
}

func (v VillageInvariants) ok() bool {
	return v.GrantLedger == v.GrantRows && v.GrantMismatched == 0 && v.Ungranted == 0 &&
		v.DonationLedger == v.DonationRows && v.DonationMismatched == 0 &&
		v.TopupLedger == v.TopupRows && v.TopupMismatched == 0 &&
		v.MaterialLedger == v.MaterialRows && v.MaterialMismatched == 0 && v.MaterialItems == v.MaterialItemRows &&
		v.WageLedger == v.WageRows && v.WageMismatched == 0 && v.ShiftItems == v.ShiftItemRows &&
		v.LaborWageLedger == v.LaborWageRows && v.LaborMismatched == 0 && v.LaborEscrowLedger == v.LaborEscrowRows &&
		v.LaborBuiltWithoutWork == 0 && v.LaborWorkUnbacked == 0 && v.ServiceMisrouted == 0 && v.TeachingOK() && v.WorkNodesOK()
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
		{&s.DonationRows, "settlement donation rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM settlement_donations d
			WHERE NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = d.id)`, nil},
		{&s.DonationMismatched, "settlement donation transactions", `
			SELECT count(*) FROM settlement_donations d
			 WHERE NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = d.id)
			   AND ((SELECT count(*) FROM ledger_entries e
			         WHERE e.transaction_id = d.ledger_transaction_id AND e.reason = 'settlement_donation'
			           AND e.reference_type = 'settlement_donations' AND e.reference_id = d.id) <> 2
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			         WHERE e.transaction_id = d.ledger_transaction_id AND e.amount > 0) <> d.amount)`, nil},
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
		{&s.WageRows, "village shift wage rows", `SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts s WHERE s.status = 'done' AND s.kind = 'production'
			AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = s.id)`, nil},
		{&s.WageMismatched, "village shift wage transactions", `
			SELECT count(*) FROM settlement_shifts s
			 WHERE s.status = 'done' AND s.kind = 'production'
			   AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = s.id)
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
		{&s.LaborWageLedger, "labour wages", `
			SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason IN ('labor_wage', 'labor_wage_npc') AND amount > 0`, nil},
		{&s.LaborWageRows, "labour wage rows", `
			SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts s WHERE s.status = 'done' AND s.kind IN ('construction', 'repair')
			   AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = s.id)`, nil},
		{&s.LaborMismatched, "labour wage transactions", `
			SELECT count(*) FROM settlement_shifts s
			 WHERE s.status = 'done' AND s.kind IN ('construction', 'repair')
			   AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_id = s.id)
			   AND ((s.wage_paid > 0
			         AND ((SELECT count(*) FROM ledger_entries e
			                WHERE e.transaction_id = s.ledger_transaction_id AND e.reason IN ('labor_wage', 'labor_wage_npc')
			                  AND e.reference_type = 'settlement_shifts' AND e.reference_id = s.id) <> 2 + (CASE WHEN s.fee > 0 THEN 1 ELSE 0 END)
			           OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e
			                WHERE e.transaction_id = s.ledger_transaction_id AND e.amount > 0) <> s.wage_paid))
			     OR (s.wage_paid = 0 AND s.ledger_transaction_id IS NOT NULL))`, nil},
		{&s.LaborEscrowLedger, "labour escrow", credited, []any{"labor_escrow"}},
		{&s.LaborEscrowRows, "labour escrow rows", `
			SELECT COALESCE(SUM(wage), 0)::bigint FROM settlement_shifts WHERE kind = 'construction' AND payer_kind = 'player'`, nil},
		{&s.ServiceMisrouted, "training and repair routes", `
			SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE (e.reason = 'training_fee' AND ((e.amount > 0 AND a.kind <> 'city_treasury') OR (e.amount < 0 AND a.kind <> 'player_cash')))
			    OR (e.reason = 'trainer_wage' AND ((e.amount > 0 AND a.kind <> 'system_sink') OR (e.amount < 0 AND a.kind <> 'city_treasury')))
			    OR (e.reason = 'bag_repair' AND ((e.amount > 0 AND a.kind <> 'system_sink') OR (e.amount < 0 AND a.kind NOT IN ('player_cash', 'player_bank'))))`, nil},
		{&s.LaborBuiltWithoutWork, "buildings finished without their work", `
			SELECT count(*) FROM settlement_buildings
			 WHERE work_required > 0 AND status IN ('complete', 'demolished') AND completed_at IS NOT NULL AND work_done < work_required`, nil},
		{&s.LaborWorkUnbacked, "buildings with work no shift did", `
			SELECT count(*) FROM settlement_buildings b
			 WHERE b.work_required > 0
			   AND b.work_done > (SELECT COALESCE(SUM(s.work_points), 0) FROM settlement_shifts s
			                       WHERE s.building_id = b.id AND s.status = 'done' AND s.kind = 'construction')`, nil},
	}
	for _, c := range checks {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}

// verifyTeaching runs the teaching checks when migration 0120 is applied.
func (a *EconomyAdmin) verifyTeaching(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.class_seats') IS NOT NULL`).Scan(&v.VillageInvariants.Teaching); err != nil {
		return fmt.Errorf("postgres: looking for teaching: %w", err)
	}
	if !v.VillageInvariants.Teaching {
		return nil
	}
	s := &v.VillageInvariants
	fees := `SELECT COALESCE(SUM(e.fee), 0)::bigint FROM enrollments e
	           JOIN class_seats c ON c.enrollment_id = e.id JOIN course_teachers t ON t.id = c.teacher_id WHERE t.employer = $1
	            AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_type = 'enrollments' AND lp.reference_id = e.id)`
	for _, c := range []struct {
		into *int64
		what string
		sql  string
		args []any
	}{
		{&s.CourseFeeLedger, "school course fees", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'course_fee' AND amount > 0`, nil},
		{&s.CourseFeeRows, "school course fee rows", fees, []any{"settlement"}},
		{&s.TuitionLedger, "home tuition", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'tuition' AND amount > 0`, nil},
		{&s.TuitionRows, "home tuition rows", fees, []any{"self"}},
		{&s.TeacherWageLedger, "teacher wages", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason IN ('teacher_wage', 'teacher_wage_npc') AND amount > 0`, nil},
		{&s.TeacherWageRows, "teacher wage rows", `SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM class_seats c WHERE NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_type = 'class_seats' AND lp.reference_id = c.enrollment_id)`, nil},
		{&s.TeachMismatched, "teaching seats", `
			SELECT count(*) FROM class_seats c JOIN enrollments e ON e.id = c.enrollment_id
			 WHERE c.wage_paid > c.wage
			    OR (c.wage_paid > 0 AND NOT EXISTS (SELECT 1 FROM local_payments lp WHERE lp.reference_type = 'class_seats' AND lp.reference_id = c.enrollment_id)
			        AND (SELECT count(*) FROM ledger_entries l
			         WHERE l.reference_type = 'class_seats' AND l.reference_id = c.enrollment_id
			           AND l.reason IN ('teacher_wage', 'teacher_wage_npc')) <> 2)
			    OR (e.status = 'completed' AND c.wage_paid_at IS NULL)`, nil},
	} {
		if err := a.q.QueryRow(ctx, c.sql, c.args...).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}

// verifyWorkNodes runs the working-node checks when migration 0125 is applied.
func (a *EconomyAdmin) verifyWorkNodes(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.settlement_kitchen') IS NOT NULL`).Scan(&v.VillageInvariants.WorkNodes); err != nil {
		return fmt.Errorf("postgres: looking for the kitchens: %w", err)
	}
	if !v.VillageInvariants.WorkNodes {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.MealOpenedRows, "meal openings", `SELECT COALESCE(SUM(units * points_each), 0)::bigint FROM settlement_meals`},
		{&s.MealOpenedKitchen, "kitchen opened points", `SELECT COALESCE(SUM(opened_points), 0)::bigint FROM settlement_kitchen`},
		{&s.MealEatenShifts, "points the shifts ate", `SELECT COALESCE(SUM(meal_points), 0)::bigint FROM settlement_shifts`},
		{&s.MealEatenKitchen, "kitchen eaten points", `SELECT COALESCE(SUM(eaten_points), 0)::bigint FROM settlement_kitchen`},
		{&s.MealJournalUnits, "meal units in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'meal_eaten'`},
		{&s.MealRowUnits, "meal units in the opening rows", `SELECT COALESCE(SUM(units), 0)::bigint FROM settlement_meals`},
		{&s.NPCShiftsWithoutJob, "NPC production shifts without a job", `
			SELECT count(*) FROM settlement_shifts WHERE kind = 'production' AND worker_kind = 'npc' AND job_id IS NULL`},
		{&s.NPCHungry, "NPC shifts that started unfed", `
			SELECT count(*) FROM settlement_shifts WHERE worker_kind = 'npc' AND kind = 'production' AND NOT fed`},
		{&s.CarryOutOfRange, "workplaces whose carry is out of range", `
			SELECT count(*) FROM settlement_buildings b, jsonb_each_text(b.carry) c
			 WHERE c.value::bigint < 0 OR c.value::bigint >= 10000`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	if err := a.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'settlement_shifts' AND column_name = 'condition_gain')`).Scan(&s.Repairs); err != nil {
		return fmt.Errorf("postgres: looking for repairs: %w", err)
	}
	if !s.Repairs {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.RepairWithoutJob, "repair shifts without a repair job", `
			SELECT count(*) FROM settlement_shifts s
			 WHERE s.kind = 'repair' AND (s.condition_gain <= 0 OR NOT EXISTS
			       (SELECT 1 FROM labor_jobs j WHERE j.id = s.job_id AND j.kind = 'repair'))`},
		{&s.DamageOutOfRange, "buildings with damage out of range", `SELECT count(*) FROM settlement_buildings WHERE damage_bps < 0 OR damage_bps > 10000`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}

// verifyCurrencies runs the settlement-currency checks when migration 0127 is applied: the reserve pot
// equals the deposits less the releases, the supply equals what was minted less what was burnt (and is
// what the ledger holds outside the system accounts of that currency), and every issuance is logged.
func (a *EconomyAdmin) verifyCurrencies(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.village_currency_state') IS NOT NULL`).Scan(&v.VillageInvariants.Currencies); err != nil {
		return fmt.Errorf("postgres: looking for settlement currencies: %w", err)
	}
	if !v.VillageInvariants.Currencies {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.PotMismatched, "reserve pots", `
			SELECT count(*) FROM village_currency_state st
			 WHERE COALESCE((SELECT a.balance FROM accounts a WHERE a.kind = 'reserve_pot' AND a.owner_id = st.settlement_id AND a.currency = 'SUP'), 0)
			       <> st.deposited_sup - st.released_sup - st.intervention_out + st.intervention_in`},
		{&s.SupplyMismatched, "currency supplies", `
			SELECT count(*) FROM village_currency_state st
			 WHERE st.minted_units - st.burnt_units
			       <> COALESCE((SELECT -SUM(a.balance) FROM accounts a WHERE a.currency = st.currency_code AND a.kind IN ('system_source', 'system_sink')), 0)
			    OR st.minted_units - st.burnt_units
			       <> COALESCE((SELECT SUM(a.balance) FROM accounts a WHERE a.currency = st.currency_code AND a.kind NOT IN ('system_source', 'system_sink')), 0)`},
		{&s.IssuanceMismatched, "currency issuance logs", `
			SELECT count(*) FROM village_currency_state st
			 WHERE st.minted_units <> COALESCE((SELECT SUM(l.units) FROM currency_issuance_log l WHERE l.settlement_id = st.settlement_id AND l.kind = 'mint'), 0)
			    OR st.deposited_sup <> COALESCE((SELECT SUM(l.deposit_sup) FROM currency_issuance_log l WHERE l.settlement_id = st.settlement_id AND l.kind = 'mint'), 0)
			    OR st.basis_sup > st.deposited_sup
			    OR EXISTS (SELECT 1 FROM currency_issuance_log l WHERE l.settlement_id = st.settlement_id AND l.kind = 'mint'
			                  AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.transaction_id = l.ledger_transaction_id
			                                    AND e.reason = 'currency_mint' AND e.amount = l.units))`},
		{&s.StrayHoldings, "foreign holdings in unchartered currencies", `
			SELECT count(*) FROM accounts a
			 WHERE a.kind = 'foreign_holding' AND a.currency NOT IN (SELECT currency_code FROM village_currency_state)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}

// verifyLocalObligations runs the checks of local obligations and the desk when migration 0128 is applied.
func (a *EconomyAdmin) verifyLocalObligations(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.local_payments') IS NOT NULL`).Scan(&v.VillageInvariants.LocalObligations); err != nil {
		return fmt.Errorf("postgres: looking for local payments: %w", err)
	}
	if !v.VillageInvariants.LocalObligations {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.LocalLedgerPay, "local wages in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'local_wage' AND amount > 0`},
		{&s.LocalRowsPay, "local wage rows", `SELECT COALESCE(SUM(units), 0)::bigint FROM local_payments WHERE direction = 'pay'`},
		{&s.LocalLedgerCollect, "local payments in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'local_payment' AND amount > 0`},
		{&s.LocalRowsCollect, "local payment rows", `SELECT COALESCE(SUM(units), 0)::bigint FROM local_payments WHERE direction = 'collect'`},
		{&s.LocalLedgerTransfer, "local transfers in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'local_transfer' AND amount > 0`},
		{&s.LocalRowsTransfer, "local transfer rows", `SELECT COALESCE(SUM(units), 0)::bigint FROM local_payments WHERE direction = 'transfer'`},
		{&s.BurnLedger, "burnt units in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = 'currency_burn' AND amount > 0`},
		{&s.BurnRows, "burn rows", `SELECT COALESCE(SUM(units), 0)::bigint FROM currency_issuance_log WHERE kind = 'burn'`},
		{&s.BurnMismatched, "burn transactions", `
			SELECT (SELECT count(*) FROM currency_issuance_log b
			         WHERE b.kind = 'burn'
			           AND ((SELECT count(*) FROM ledger_entries e WHERE e.transaction_id = b.ledger_transaction_id AND e.reason = 'currency_burn') <> 2
			             OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			                  WHERE e.transaction_id = b.ledger_transaction_id AND e.amount > 0 AND a.kind = 'system_sink'
			                    AND a.currency = (SELECT st.currency_code FROM village_currency_state st WHERE st.settlement_id = b.settlement_id)) <> b.units
			             OR b.reference_type IS NULL OR b.basis_sup < 0))
			     + (SELECT count(*) FROM village_currency_state st
			         WHERE st.burnt_units <> COALESCE((SELECT SUM(b.units) FROM currency_issuance_log b WHERE b.settlement_id = st.settlement_id AND b.kind = 'burn'), 0))`},
		{&s.LocalMismatched, "local payment transactions", `
			SELECT count(*) FROM local_payments p
			 WHERE (SELECT count(*) FROM ledger_entries e WHERE e.transaction_id = p.ledger_transaction_id
			          AND e.reason = CASE p.direction WHEN 'pay' THEN 'local_wage' WHEN 'transfer' THEN 'local_transfer' ELSE 'local_payment' END
			          AND e.reference_type = 'local_payments' AND e.reference_id = p.id) <> CASE WHEN p.cut_units > 0 THEN 3 ELSE 2 END
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = p.ledger_transaction_id AND e.amount > 0) <> p.units
			    OR (p.flow <> 'player_payment' AND p.units <> ceil(p.sup_amount::numeric * p.r0 * 1000000 / p.x_ref_ppm))`},
		{&s.DeskMismatched, "desk trades", `
			SELECT count(*) FROM currency_desk_trades t
			 WHERE (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = t.sup_transaction_id AND e.amount > 0
			           AND e.reason = 'fx_desk_sup' AND e.reference_type = 'currency_desk_trades' AND e.reference_id = t.id) <> t.sup_amount
			    OR (SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = t.local_transaction_id AND e.amount > 0
			           AND e.reason = 'fx_desk_local' AND e.reference_type = 'currency_desk_trades' AND e.reference_id = t.id) <> t.units
			    OR (t.side = 'buy' AND t.units <> floor((t.sup_amount - ceil(t.sup_amount::numeric * t.fee_bps / 10000)) * t.r0 * 1000000 / t.x_ref_ppm))
			    OR (t.side = 'sell' AND t.sup_amount <> floor((t.units - ceil(t.units::numeric * t.fee_bps / 10000)) * t.x_ref_ppm / (t.r0 * 1000000.0)))`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
