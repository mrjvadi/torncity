package postgres

import (
	"context"
	"fmt"
)

// verifyHouseholds runs the checks of the NPC households when migration 0151 is applied (docs/adr/0073 section 6): no wallet is below zero,
// a household that is gone holds nothing, no household money comes from the source (the households only move money that was already
// there), a settlement that has flipped pays no NPC wage into the sink, every living household has been settled for the settlement's
// latest finished day, the pool of a live settlement is not read from the formula (the earners are what the pool is made of: checked by
// the integration tests, which know the housing), no household owns a lot or an office (they have no player id), and every item the
// households ended refers to a household.
func (a *EconomyAdmin) verifyHouseholds(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.households') IS NOT NULL`).Scan(&s.Households); err != nil {
		return fmt.Errorf("postgres: looking for the households: %w", err)
	}
	if !s.Households {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.HouseholdWalletBelowZero, "household wallets below zero", `SELECT count(*) FROM accounts WHERE kind = 'household_wallet' AND balance < 0`},
		{&s.HouseholdGoneHolding, "money held by households that are gone", `
			SELECT COALESCE(SUM(a.balance), 0)::bigint FROM accounts a JOIN households h ON h.id = a.owner_id
			 WHERE a.kind = 'household_wallet' AND h.state = 'gone'`},
		{&s.HouseholdFromSource, "household money from the source", `
			SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.amount < 0 AND a.kind = 'system_source' AND e.transaction_id IN (
			       SELECT transaction_id FROM ledger_entries WHERE reason IN ('household_welcome', 'household_purchase', 'household_income_tax',
			                                                                     'household_rent', 'household_depart'))`},
		{&s.HouseholdWageToSink, "NPC wages into the sink after a flip", `
			SELECT count(*) FROM ledger_entries e
			  JOIN accounts a ON a.id = e.account_id AND a.kind = 'system_sink'
			  JOIN ledger_entries src ON src.transaction_id = e.transaction_id AND src.amount < 0
			  JOIN accounts sa ON sa.id = src.account_id AND sa.kind = 'city_treasury'
			  JOIN household_settlements f ON f.settlement_id = sa.owner_id AND f.state = 'live' AND f.flipped_at IS NOT NULL
			 WHERE e.amount > 0 AND e.created_at >= f.flipped_at
			   AND e.reason IN ('labor_wage_npc', 'shopkeeper_wage', 'storekeeper_wage', 'market_clerk_wage', 'service_wage', 'scholar_wage_npc', 'teacher_wage_npc', 'settlement_wage', 'trainer_wage')
			   AND EXISTS (SELECT 1 FROM households h WHERE h.settlement_id = f.settlement_id AND h.state = 'living' AND h.earners > 0)`},
		{&s.HouseholdUnsettled, "living households not settled for the latest finished day", `
			SELECT count(*) FROM households h
			  JOIN (SELECT settlement_id, MAX(day) AS day FROM household_days WHERE done_at IS NOT NULL GROUP BY settlement_id) d ON d.settlement_id = h.settlement_id
			 WHERE h.state <> 'gone' AND h.last_day < d.day`},
		{&s.HouseholdItemOrphans, "household item movements without a household", `
			SELECT count(*) FROM item_movements m
			 WHERE m.reason IN ('household_eaten', 'household_drunk', 'household_burned')
			   AND (m.reference_type <> 'households' OR NOT EXISTS (SELECT 1 FROM households h WHERE h.id = m.reference_id))`},
		{&s.HouseholdShopOrphans, "household shop sales without a household", `
			SELECT count(*) FROM village_shop_sales s WHERE s.household_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM households h WHERE h.id = s.household_id)`},
		{&s.HouseholdTickDup, "household days with two rows for one settlement", `
			SELECT count(*) FROM (SELECT household_id, day FROM household_ticks GROUP BY 1, 2 HAVING count(*) > 1) x`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
