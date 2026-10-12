package postgres

import (
	"context"
	"fmt"
)

// verifySpecial runs the checks of special projects, intelligence and the licence market when migration 0155 is applied (docs/adr/0077 13).
func (a *EconomyAdmin) verifySpecial(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.special_projects') IS NOT NULL`).Scan(&s.Special); err != nil {
		return fmt.Errorf("postgres: looking for the special projects: %w", err)
	}
	if !s.Special {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		// the gifts to the funds in the ledger are the gift rows (a pledge or the treasury's allocation, each a credit to a project fund)
		{&s.ProjectGiftLedger, "gifts in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason IN ('project_pledge', 'project_fund_allocation') AND a.kind = 'project_fund' AND e.amount > 0`},
		{&s.ProjectGiftRows, "gifts in the rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM project_pledges`},
		// a stage once done is never undone: the rows of the stages are 1..k
		{&s.ProjectStagesBroken, "projects whose stages are not contiguous", `SELECT count(*) FROM special_projects p WHERE
			(SELECT count(*) FROM project_stages s WHERE s.project_id = p.id) <> CASE WHEN p.status = 'finished' THEN p.stages ELSE p.stage - 1 END
			OR (SELECT COALESCE(max(stage), 0) FROM project_stages s WHERE s.project_id = p.id) <> CASE WHEN p.status = 'finished' THEN p.stages ELSE p.stage - 1 END`},
		{&s.ProjectCancelledFunds, "cancelled projects with money in the fund", `SELECT count(*) FROM special_projects p JOIN accounts a ON a.kind = 'project_fund' AND a.owner_id = p.id
			WHERE p.status = 'cancelled' AND a.balance <> 0`},
		// an escrow of a project's contract holds the bonds of the standing bids while it is open, the unpaid price and the winner's bond while it
		// runs, and nothing after
		{&s.ProjectEscrowBroken, "contract escrows", `SELECT count(*) FROM project_contracts c LEFT JOIN accounts a ON a.kind = 'project_escrow' AND a.owner_id = c.id WHERE COALESCE(a.balance, 0) <> (CASE c.status
			WHEN 'open' THEN COALESCE((SELECT sum(bond) FROM project_contract_bids b WHERE b.contract_id = c.id AND b.status = 'standing'), 0)
			WHEN 'awarded' THEN COALESCE(c.price, 0) - COALESCE((SELECT sum(paid) FROM project_contract_stages s WHERE s.contract_id = c.id), 0)
				+ COALESCE((SELECT sum(bond) FROM project_contract_bids b WHERE b.contract_id = c.id AND b.status = 'won'), 0)
			ELSE 0 END)`},
		// the fees and the wages of the bureau and the cost of sabotage in the ledger are what the operations say
		{&s.IntelFeeLedger, "bureau fees in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'intel_op_fee' AND e.amount > 0`},
		{&s.IntelFeeRows, "bureau fees in the rows", `SELECT COALESCE(SUM(fee), 0)::bigint FROM intel_ops WHERE kind NOT IN ('delay_work', 'spoil_stock', 'blind_post', 'skip_service_day')`},
		{&s.IntelWageLedger, "agents' wages in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'intel_agent_wage' AND e.amount > 0`},
		{&s.IntelWageRows, "agents' wages in the rows", `SELECT COALESCE(SUM(wage), 0)::bigint FROM intel_ops`},
		{&s.SabotageCostLedger, "sabotage costs in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'sabotage_cost' AND e.amount > 0`},
		{&s.SabotageCostRows, "sabotage costs in the rows", `SELECT COALESCE(SUM(fee), 0)::bigint FROM intel_ops WHERE kind IN ('delay_work', 'spoil_stock', 'blind_post', 'skip_service_day')`},
		// a report is made only by a clean or partial operation, and holds band names (words), never a figure
		{&s.IntelReportBlown, "reports of operations that were blown", `SELECT count(*) FROM intel_reports r JOIN intel_ops o ON o.id = r.op_id WHERE o.outcome = 'blown'`},
		{&s.IntelReportFigures, "report fields that hold a figure", `SELECT count(*) FROM intel_reports r, jsonb_each_text(r.fields) f WHERE f.value ~ '[0-9]' OR f.key ~ '[0-9]'`},
		// a blown operation that a post noticed ended in an expulsion
		{&s.IntelNoExpulsion, "detected operations with no expulsion", `SELECT count(*) FROM intel_ops o WHERE EXISTS (SELECT 1 FROM intel_exposures e WHERE e.op_id = o.id AND e.detected)
			AND NOT EXISTS (SELECT 1 FROM intel_expulsions x WHERE x.op_id = o.id)`},
		// the most acts of sabotage one sponsor did in any seven days
		{&s.SabotageMaxWeek, "acts in a week", `SELECT COALESCE(max(c), 0)::bigint FROM (SELECT count(*) OVER (PARTITION BY sponsor_id ORDER BY at RANGE BETWEEN INTERVAL '7 days' PRECEDING AND CURRENT ROW) c FROM sabotage_acts) x`},
		// an auction's escrow holds the standing bids while it is open and nothing after; an application's escrow holds its fee while it is pending
		{&s.LicenceEscrowBroken, "licence escrows", `SELECT (SELECT count(*) FROM licence_auctions u LEFT JOIN accounts a ON a.kind = 'licence_escrow' AND a.owner_id = u.id
				WHERE COALESCE(a.balance, 0) <> CASE WHEN u.status = 'open' THEN COALESCE((SELECT sum(amount) FROM licence_bids b WHERE b.auction_id = u.id AND b.status = 'standing'), 0) ELSE 0 END)
			+ (SELECT count(*) FROM licence_applications p LEFT JOIN accounts a ON a.kind = 'licence_escrow' AND a.owner_id = p.id
				WHERE COALESCE(a.balance, 0) <> CASE WHEN p.status = 'pending' THEN p.fee ELSE 0 END)`},
		{&s.LicenceAwardLedger, "auction prices in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'licence_auction_award' AND e.amount > 0`},
		{&s.LicenceAwardRows, "auction prices in the rows", `SELECT COALESCE(SUM(price), 0)::bigint FROM licence_auctions WHERE status = 'awarded'`},
		// a revoked licence has a cause or a refund; a live licence is not long past its term; one winner for a right
		{&s.LicenceRevokedNoCause, "revoked licences with no cause", `SELECT count(*) FROM licences WHERE status = 'revoked' AND (cause IS NULL OR cause = '')`},
		{&s.LicenceStaleLive, "live licences long past their term", `SELECT count(*) FROM licences WHERE status IN ('active', 'notice') AND term_end < now() - interval '1 day'`},
		{&s.LicenceAuctionManyWinners, "auctions with more than one winner", `SELECT count(*) FROM (SELECT auction_id FROM licence_bids WHERE status = 'won' GROUP BY 1 HAVING count(*) > 1) x`},
		// the largest share one holder has of the live licences of a kind in a settlement, where the kind has five or more (basis points)
		{&s.LicenceMaxShareBPS, "largest share", `SELECT COALESCE(max(share), 0)::bigint FROM (SELECT count(*) * 10000 / sum(count(*)) OVER (PARTITION BY issuer_id, kind) AS share,
				sum(count(*)) OVER (PARTITION BY issuer_id, kind) AS total FROM licences WHERE status IN ('active', 'notice') GROUP BY issuer_id, kind, holder_kind, holder_id) x WHERE total >= 5`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
