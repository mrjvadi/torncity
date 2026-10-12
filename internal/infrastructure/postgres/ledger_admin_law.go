package postgres

import (
	"context"
	"fmt"
)

// verifyLaw runs the checks of police, courts, informants, the hidden board, laundering and customs when migration 0157 is applied (docs/adr/0078 3.16).
func (a *EconomyAdmin) verifyLaw(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.law_cases') IS NOT NULL`).Scan(&s.Law); err != nil {
		return fmt.Errorf("postgres: looking for the law tables: %w", err)
	}
	if !s.Law {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		// the dirty balance pays nothing legal and clean money never comes out of it except by laundering: every entry on a dirty account carries a reason
		// of the short list for its direction
		{&s.LawDirtyStray, "dirty entries with a reason of their own", `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE a.kind = 'player_dirty' AND ((e.amount < 0 AND e.reason NOT IN ('bribe', 'black_trade', 'escrow_hold', 'launder', 'forfeiture', 'forfeiture_police_share', 'bribe_forfeit'))
				OR (e.amount > 0 AND e.reason NOT IN ('bribe', 'black_trade', 'escrow_scam', 'escrow_refund')))`},
		// a pot of the board holds exactly what the held escrow or the unfinished batch it belongs to holds, and nothing after
		{&s.LawPotBroken, "pots of the hidden board", `SELECT (SELECT count(*) FROM law_escrows e LEFT JOIN accounts a ON a.kind = 'escrow_black' AND a.owner_id = e.id
				WHERE COALESCE(a.balance, 0) <> CASE WHEN e.status = 'held' THEN e.amount + e.fee ELSE 0 END)
			+ (SELECT count(*) FROM law_launder_batches b LEFT JOIN accounts a ON a.kind = 'escrow_black' AND a.owner_id = b.id
				WHERE COALESCE(a.balance, 0) <> CASE WHEN b.state IN ('placed', 'layered') THEN b.amount ELSE 0 END)`},
		// the price of what was released is what the seller's dirty balance received
		{&s.LawTradeLedger, "released trades in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'black_trade' AND a.kind = 'player_dirty' AND e.amount > 0`},
		{&s.LawTradeRows, "released trades in the rows", `SELECT COALESCE(SUM(amount), 0)::bigint FROM law_escrows WHERE status = 'released'`},
		// fines taken by a court are the fines the sentences say were paid
		{&s.LawFineLedger, "fines in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'fine_verdict' AND e.amount > 0`},
		{&s.LawFineRows, "fines in the rows", `SELECT COALESCE(SUM(fine_paid), 0)::bigint FROM law_sentences WHERE kind = 'fine'`},
		// the penalty of the customs, in the treasury and the police share, is what the seized loads say
		{&s.LawPenaltyLedger, "customs penalties in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.amount > 0 AND e.reference_type = 'law_loads'
			AND e.reason IN ('customs_penalty', 'forfeiture_police_share')`},
		{&s.LawPenaltyRows, "customs penalties in the rows", `SELECT COALESCE(SUM(penalty), 0)::bigint FROM law_loads WHERE status = 'seized'`},
		// the fee of an integrated batch is what left for the sink
		{&s.LawLaunderFeeLedger, "laundering fees in the ledger", `SELECT COALESCE(SUM(e.amount), 0)::bigint FROM ledger_entries e WHERE e.reason = 'launder_fee' AND e.amount > 0`},
		{&s.LawLaunderFeeRows, "laundering fees in the rows", `SELECT COALESCE(SUM(fee), 0)::bigint FROM law_launder_batches WHERE state = 'integrated'`},
		// every jail sentence names a jail and a cell, and no two prisoners share one
		{&s.LawJailNoCell, "jail sentences with no cell", `SELECT count(*) FROM law_sentences WHERE kind = 'jail' AND (jail_building IS NULL OR cell IS NULL)`},
		{&s.LawCellShared, "cells with two prisoners", `SELECT count(*) FROM (SELECT jail_building, cell FROM law_sentences WHERE kind = 'jail' AND status = 'active' GROUP BY 1, 2 HAVING count(*) > 1) x`},
		// a sentence follows a guilty verdict, of the trial or of the appeal, and a verdict has a case that reached a hearing
		{&s.LawSentenceNoVerdict, "sentences with no guilty verdict", `SELECT count(*) FROM law_sentences s WHERE NOT EXISTS (SELECT 1 FROM law_verdicts v WHERE v.id = s.verdict_id AND v.guilty)`},
		{&s.LawVerdictNoHearing, "verdicts of a case that never reached a hearing", `SELECT count(*) FROM law_verdicts v WHERE NOT EXISTS (SELECT 1 FROM law_case_stages g WHERE g.case_id = v.case_id AND g.stage = 'hearing')`},
		// an appeal verdict follows an appeal, and a party appeals once
		{&s.LawAppealNoAppeal, "appeal verdicts with no appeal", `SELECT count(*) FROM law_verdicts v WHERE v.instance = 2 AND NOT EXISTS (SELECT 1 FROM law_appeals a WHERE a.case_id = v.case_id)`},
		// a warrant was signed on evidence that was not only informants' tips
		{&s.LawWarrantTipsOnly, "warrants resting on tips alone", `SELECT count(*) FROM law_warrants w WHERE w.status IN ('signed', 'used')
			AND NOT EXISTS (SELECT 1 FROM law_evidence e WHERE e.case_id = w.case_id AND e.proper AND e.kind <> 'informant_report' AND e.at <= w.at + interval '1 year')`},
		// a bribe accepted moved money, and none lowers a rung to nothing
		{&s.LawBribeNoMoney, "accepted bribes with no money moved", `SELECT count(*) FROM law_bribes b WHERE b.status IN ('accepted', 'found')
			AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.reference_type = 'law_bribes' AND e.reference_id = b.id AND e.reason = 'bribe')`},
		{&s.LawBribeMaxEffect, "the largest effect of a bribe", `SELECT COALESCE(max(effect_bps), 0)::bigint FROM law_bribes`},
		// nothing of the mature content is on the board while the switch is off
		{&s.LawMatureOff, "mature listings while the switch is off", `SELECT count(*) FROM law_listings l WHERE l.mature
			AND COALESCE((SELECT value FROM operator_switches WHERE key = 'mature'), 'off') <> 'on'`},
		// a live informant has a reliability in range and a record of the first contact; the record is never changed
		{&s.LawInformantNoRecord, "informants with no first contact", `SELECT count(*) FROM law_informants i WHERE NOT EXISTS (SELECT 1 FROM law_informant_contacts c WHERE c.informant_id = i.id AND c.kind = 'recruit')`},
		// the stages of a case are one after the other: a decided case was charged and heard
		{&s.LawStagesBroken, "cases whose stages skip", `SELECT count(*) FROM law_cases c WHERE (c.state IN ('hearing', 'decided', 'appealed', 'final') AND c.decided_at IS NOT NULL AND NOT EXISTS
			(SELECT 1 FROM law_case_stages g WHERE g.case_id = c.id AND g.stage = 'charged'))`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
