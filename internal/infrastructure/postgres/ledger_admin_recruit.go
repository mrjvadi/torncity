package postgres

import (
	"context"
	"fmt"
)

// RecruitInvariants are the checks of `admin economy verify` for specialist
// recruitment (docs/adr/0027-specialist-recruitment.md): every flow of its
// five reasons in the ledger matches the rows that caused it, each
// advertising fee reached its own city's treasury, each period's pay has its
// own ledger transaction, and every hire is one specialist of its campaign.
type RecruitInvariants struct {
	// Advertising: the ledger, the fees paid per city, the campaigns' totals.
	AdLedger, AdRows, AdCampaigns int64
	// AdMisrouted are fees without their own transfer to their city.
	AdMisrouted int64
	// Salaries: the ledger and the payments; UnbackedPay are payments with
	// no ledger transaction of their amount.
	SalaryLedger, SalaryRows, UnbackedPay int64
	// Hire and contract money: the ledger and the specialists' rows.
	SigningLedger, SigningRows       int64
	RelocationLedger, RelocationRows int64
	EquityLedger, EquityRows         int64
	// HiresBroken are campaigns whose hired count is not their specialists,
	// and hired candidates without exactly one specialist.
	HiresBroken int64
}

func (s RecruitInvariants) ok() bool {
	return s.AdLedger == s.AdRows && s.AdRows == s.AdCampaigns && s.AdMisrouted == 0 &&
		s.SalaryLedger == s.SalaryRows && s.UnbackedPay == 0 && s.SigningLedger == s.SigningRows &&
		s.RelocationLedger == s.RelocationRows && s.EquityLedger == s.EquityRows && s.HiresBroken == 0
}

// verifyRecruit runs recruitment's invariants.
func (a *EconomyAdmin) verifyRecruit(ctx context.Context, v *LedgerVerification) error {
	s := &v.RecruitInvariants
	credited := func(reason string) string {
		return `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = '` + reason + `' AND amount > 0`
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.AdLedger, "advertising fees", credited("recruitment_ad")},
		{&s.AdRows, "fees paid per city", `SELECT COALESCE(SUM(amount), 0)::bigint FROM recruit_ad_fees`},
		{&s.AdCampaigns, "campaigns' fees", `SELECT COALESCE(SUM(ad_fee), 0)::bigint FROM recruit_campaigns`},
		{&s.AdMisrouted, "fees without their transfer", `
			SELECT count(*) FROM recruit_ad_fees f
			 WHERE f.amount > 0 AND NOT EXISTS (
			     SELECT 1 FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			      WHERE e.transaction_id = f.ledger_transaction_id AND e.reason = 'recruitment_ad'
			        AND e.amount = f.amount AND a.kind = 'city_treasury' AND a.owner_id = f.city_id)`},
		{&s.SalaryLedger, "specialists' pay", credited("specialist_salary")},
		{&s.SalaryRows, "payments", `SELECT COALESCE(SUM(amount), 0)::bigint FROM npc_staff_payments`},
		{&s.UnbackedPay, "payments without their transaction", `
			SELECT count(*) FROM npc_staff_payments p
			 WHERE NOT EXISTS (
			     SELECT 1 FROM ledger_entries e
			      WHERE e.transaction_id = p.ledger_transaction_id AND e.reason = 'specialist_salary'
			        AND e.reference_type = 'npc_staff' AND e.reference_id = p.staff_id AND e.amount = p.amount)`},
		{&s.SigningLedger, "signing bonuses", credited("specialist_signing")},
		{&s.SigningRows, "signing bonuses paid", `SELECT COALESCE(SUM(signing_paid), 0)::bigint FROM npc_staff`},
		{&s.RelocationLedger, "moves", credited("specialist_relocation")},
		{&s.RelocationRows, "moves paid", `SELECT COALESCE(SUM(relocation_paid), 0)::bigint FROM npc_staff`},
		{&s.EquityLedger, "phantom shares", credited("specialist_equity")},
		{&s.EquityRows, "phantom shares paid", `SELECT COALESCE(SUM(equity_paid), 0)::bigint FROM npc_staff`},
		{&s.HiresBroken, "hires", `
			SELECT (SELECT count(*) FROM recruit_campaigns c
			         WHERE c.hired <> (SELECT count(*) FROM npc_staff s WHERE s.campaign_id = c.id))
			     + (SELECT count(*) FROM recruit_candidates d
			         WHERE (d.status = 'hired') <> EXISTS (SELECT 1 FROM npc_staff s WHERE s.candidate_id = d.id))`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
