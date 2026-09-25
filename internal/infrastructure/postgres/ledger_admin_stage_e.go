package postgres

import (
	"context"
	"fmt"
)

// StageEInvariants are the checks of `admin economy verify` for stage E
// (docs/adr/0023-health-missions-factions.md): hospitals were paid exactly
// what their treatments say, and clinics used the medicine they say; faction
// banks belong to factions, hold nothing once disbanded, and move only by
// their own reasons and the organised crimes' cuts; mission cash matches the
// completed missions and their grants, within the day's caps; and every
// held payment is accounted for between hold, release and return.
type StageEInvariants struct {
	// Hospital fees against treatments: the city hospital's and clinics'.
	HospitalFeeLedger, HospitalFeeRows   int64
	TreatmentFeeLedger, TreatmentFeeRows int64
	// UnpaidTreatments are priced treatments with no ledger transaction.
	UnpaidTreatments int64
	// MedicineJournal and MedicineRows are the units clinics used, in the
	// item journal and in the treatments.
	MedicineJournal, MedicineRows int64

	// OrphanFactionAccounts are faction banks no faction owns;
	// DisbandedWithMoney disbanded factions still holding money;
	// StrayFactionMoves faction-bank ledger rows under any other reason.
	OrphanFactionAccounts, DisbandedWithMoney, StrayFactionMoves int64
	// The organised crimes' take and the faction banks' cut, in the ledger
	// and in the operations.
	HeistLedger, HeistRows int64
	CutLedger, CutRows     int64

	// Mission cash in the ledger, in the completed missions and in their
	// grants; and the most one player, and all together, were paid in one
	// UTC day (compared against the caps by the caller).
	MissionLedger, MissionRows, MissionGrants int64
	MissionPlayerDayMax, MissionEconomyDayMax int64

	// Held payments: what the ledger holds for them against the rows still
	// held, and settled holds with no settling transaction.
	HeldLedger, HeldRows int64
	UnsettledHolds       int64
}

func (s StageEInvariants) ok() bool {
	return s.HospitalFeeLedger == s.HospitalFeeRows && s.TreatmentFeeLedger == s.TreatmentFeeRows &&
		s.UnpaidTreatments == 0 && s.MedicineJournal == s.MedicineRows &&
		s.OrphanFactionAccounts == 0 && s.DisbandedWithMoney == 0 && s.StrayFactionMoves == 0 &&
		s.HeistLedger == s.HeistRows && s.CutLedger == s.CutRows &&
		s.MissionLedger == s.MissionRows && s.MissionLedger == s.MissionGrants &&
		s.HeldLedger == s.HeldRows && s.UnsettledHolds == 0
}

// verifyStageE runs stage E's invariants.
func (a *EconomyAdmin) verifyStageE(ctx context.Context, v *LedgerVerification) error {
	s := &v.StageEInvariants
	one := func(into *int64, what, sql string, args ...any) error {
		if err := a.q.QueryRow(ctx, sql, args...).Scan(into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", what, err)
		}
		return nil
	}
	credited := `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
	              WHERE e.reason = $1 AND a.kind = $2 AND e.amount > 0`
	checks := []struct {
		into *int64
		what string
		sql  string
		args []any
	}{
		{&s.HospitalFeeLedger, "hospital fees", credited, []any{"hospital_fee", "city_treasury"}},
		{&s.HospitalFeeRows, "city treatments", `SELECT COALESCE(SUM(price), 0)::bigint FROM hospital_treatments WHERE provider = 'city'`, nil},
		{&s.TreatmentFeeLedger, "clinic fees", credited, []any{"treatment_fee", "company_treasury"}},
		{&s.TreatmentFeeRows, "clinic treatments", `SELECT COALESCE(SUM(price), 0)::bigint FROM hospital_treatments WHERE provider = 'clinic'`, nil},
		{&s.UnpaidTreatments, "unpaid treatments", `
			SELECT count(*) FROM hospital_treatments t
			 WHERE t.price > 0 AND NOT EXISTS (SELECT 1 FROM ledger_entries e
			        WHERE e.transaction_id = t.ledger_transaction_id AND e.reason IN ('hospital_fee', 'treatment_fee'))`, nil},
		{&s.MedicineJournal, "medicine used", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'treatment'`, nil},
		{&s.MedicineRows, "medicine treatments", `SELECT COALESCE(SUM(medicine_units), 0)::bigint FROM hospital_treatments`, nil},
		{&s.OrphanFactionAccounts, "faction banks' owners", `
			SELECT count(*) FROM accounts a WHERE a.kind = 'faction_treasury'
			   AND NOT EXISTS (SELECT 1 FROM factions f WHERE f.id = a.owner_id)`, nil},
		{&s.DisbandedWithMoney, "disbanded factions", `
			SELECT count(*) FROM factions f JOIN accounts a ON a.kind = 'faction_treasury' AND a.owner_id = f.id
			 WHERE f.status = 'disbanded' AND a.balance <> 0`, nil},
		{&s.StrayFactionMoves, "faction bank reasons", `
			SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE a.kind = 'faction_treasury'
			   AND e.reason NOT IN ('faction_deposit', 'faction_withdrawal', 'crime_proceeds')`, nil},
		{&s.HeistLedger, "organised crime proceeds", `
			SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e
			 WHERE e.reason = 'crime_proceeds' AND e.reference_type = 'faction_operations' AND e.amount > 0`, nil},
		{&s.HeistRows, "organised crime takes", `SELECT COALESCE(SUM(take), 0)::bigint FROM faction_operations`, nil},
		{&s.CutLedger, "faction cuts", `
			SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = 'crime_proceeds' AND a.kind = 'faction_treasury'`, nil},
		{&s.CutRows, "faction cut rows", `SELECT COALESCE(SUM(faction_cut), 0)::bigint FROM faction_operations`, nil},
		{&s.MissionLedger, "mission rewards", credited, []any{"mission_reward", "player_cash"}},
		{&s.MissionRows, "completed missions", `
			SELECT COALESCE(SUM(reward_cash), 0)::bigint FROM mission_assignments WHERE status = 'completed'`, nil},
		{&s.MissionGrants, "mission grants", `SELECT COALESCE(SUM(amount), 0)::bigint FROM reward_grants WHERE source = 'mission'`, nil},
		{&s.MissionPlayerDayMax, "a player's mission day", `
			SELECT COALESCE(MAX(t), 0)::bigint FROM (
			    SELECT SUM(reward_cash) AS t FROM mission_assignments WHERE status = 'completed'
			     GROUP BY player_id, (ended_at AT TIME ZONE 'UTC')::date) d`, nil},
		{&s.MissionEconomyDayMax, "the economy's mission day", `
			SELECT COALESCE(MAX(t), 0)::bigint FROM (
			    SELECT SUM(reward_cash) AS t FROM mission_assignments WHERE status = 'completed'
			     GROUP BY (ended_at AT TIME ZONE 'UTC')::date) d`, nil},
		{&s.HeldLedger, "held payments in the ledger", `
			SELECT ((SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE reason = 'payment_hold' AND amount > 0)
			      - (SELECT COALESCE(SUM(amount), 0) FROM ledger_entries
			          WHERE reason IN ('payment_release', 'payment_return') AND amount > 0))::bigint`, nil},
		{&s.HeldRows, "held payments", `SELECT COALESCE(SUM(amount), 0)::bigint FROM payment_holds WHERE status = 'held'`, nil},
		{&s.UnsettledHolds, "settled holds", `
			SELECT count(*) FROM payment_holds h
			 WHERE h.status <> 'held' AND NOT EXISTS (SELECT 1 FROM ledger_entries e
			        WHERE e.transaction_id = h.settle_transaction_id AND e.reason IN ('payment_release', 'payment_return'))`, nil},
	}
	for _, c := range checks {
		if err := one(c.into, c.what, c.sql, c.args...); err != nil {
			return err
		}
	}
	return nil
}
