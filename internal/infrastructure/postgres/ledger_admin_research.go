package postgres

import (
	"context"
	"fmt"
)

// verifyResearch runs the checks of research capacity when migration 0134 is applied (docs/adr/0048): a research
// day is exactly what its building rows and its two wage transactions say; the scholars' wages in the ledger are the
// wages of the days; the upkeep the item journal says left the stores is what the days drew; a project's slot is a
// research building of its own settlement; a scholar's post is in a building of the settlement it names.
func (a *EconomyAdmin) verifyResearch(ctx context.Context, v *LedgerVerification) error {
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.research_days') IS NOT NULL`).Scan(&v.VillageInvariants.Research); err != nil {
		return fmt.Errorf("postgres: looking for research capacity: %w", err)
	}
	if !v.VillageInvariants.Research {
		return nil
	}
	s := &v.VillageInvariants
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.ResearchWageLedger, "scholar wages in the ledger", `
			SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason IN ('scholar_wage', 'scholar_wage_npc') AND amount > 0`},
		{&s.ResearchWageRows, "scholar wages in the research days", `
			SELECT COALESCE(SUM(wage_player + wage_npc), 0)::bigint FROM research_days`},
		{&s.ResearchWageMismatched, "scholar wage transactions", `
			SELECT (SELECT count(*) FROM research_days d WHERE d.wage_player > 0 AND
			         ((SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.ledger_player_tx AND e.amount > 0) <> d.wage_player
			       OR (SELECT count(*) FROM ledger_entries e WHERE e.transaction_id = d.ledger_player_tx AND e.reason = 'scholar_wage'
			             AND e.reference_type = 'research_days' AND e.reference_id = d.settlement_id) < 2))
			     + (SELECT count(*) FROM research_days d WHERE d.wage_npc > 0 AND
			         ((SELECT COALESCE(SUM(e.amount), 0) FROM ledger_entries e WHERE e.transaction_id = d.ledger_npc_tx AND e.amount > 0) <> d.wage_npc
			       OR (SELECT count(*) FROM ledger_entries e WHERE e.transaction_id = d.ledger_npc_tx AND e.reason = 'scholar_wage_npc'
			             AND e.reference_type = 'research_days' AND e.reference_id = d.settlement_id) <> 2))`},
		{&s.ResearchDayBroken, "research days against their building rows", `
			SELECT count(*) FROM research_days d
			 WHERE d.buildings <> (SELECT count(*) FROM research_day_buildings b WHERE b.settlement_id = d.settlement_id AND b.day = d.day)
			    OR d.staffed <> (SELECT count(*) FROM research_day_buildings b WHERE b.settlement_id = d.settlement_id AND b.day = d.day AND b.staffed)
			    OR d.scholars_player + d.scholars_npc <> (SELECT COALESCE(SUM(cardinality(b.skills)), 0) FROM research_day_buildings b
			         WHERE b.settlement_id = d.settlement_id AND b.day = d.day)`},
		{&s.ResearchUpkeepLedger, "research upkeep in the item journal", `
			SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'research_upkeep'`},
		{&s.ResearchUpkeepRows, "research upkeep in the research days", `
			SELECT COALESCE(SUM(upkeep_units), 0)::bigint FROM research_days`},
		{&s.ResearchSlotBroken, "projects against their slots", `
			SELECT count(*) FROM settlement_research r
			 WHERE r.slot_ref <> 'free' AND NOT EXISTS
			       (SELECT 1 FROM settlement_buildings b WHERE b.id::text = r.slot_ref AND b.settlement_id = r.settlement_id)`},
		{&s.ResearchPostBroken, "scholar posts against their buildings", `
			SELECT count(*) FROM research_posts p
			 WHERE NOT EXISTS (SELECT 1 FROM settlement_buildings b WHERE b.id = p.building_id AND b.settlement_id = p.settlement_id)`},
		{&s.ResearchPactBroken, "research pacts", `
			SELECT count(*) FROM research_pacts
			 WHERE (status = 'active' AND answered_at IS NULL) OR (status = 'ended' AND ended_at IS NULL)
			    OR (status IN ('proposed', 'active') AND ended_at IS NOT NULL)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
