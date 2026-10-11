package postgres

import (
	"context"
	"fmt"
)

// verifyRaids runs the checks of raids, falls and the settlements' insurance funds when migration 0152 is applied (docs/adr/0074 12): the
// filing fees, the cash and goods raids took, the tributes, the cash and goods falls cost in the ledger and in the item journal are what the
// rows say; a fall's money legs only ever left a player's carried cash; no raid ended without a closing row; no detection or loot names a raid
// that is not there. The cap of a fall (a share of the player's assets) is compared by the operator against the config
// (FallMaxShareBPS is the largest share any fall took).
func (a *EconomyAdmin) verifyRaids(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.settlement_raids') IS NOT NULL`).Scan(&s.Raids); err != nil {
		return fmt.Errorf("postgres: looking for the raids: %w", err)
	}
	if !s.Raids {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.RaidFeeLedger, "raid fees in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'raid_fee' AND a.kind = 'system_sink' AND e.amount > 0`},
		{&s.RaidFeeRows, "raid fees in the rows", `SELECT COALESCE(SUM(fee), 0)::bigint FROM settlement_raids`},
		{&s.RaidLootCashLedger, "raid cash in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'raid_loot_cash' AND a.kind = 'city_treasury' AND e.amount > 0`},
		{&s.RaidLootCashRows, "raid cash in the rows", `SELECT COALESCE(SUM(qty), 0)::bigint FROM settlement_raid_loot WHERE kind = 'cash'`},
		{&s.RaidLootItemJournal, "raid goods in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'raid_loot'`},
		{&s.RaidLootItemRows, "raid goods in the rows", `SELECT COALESCE(SUM(qty), 0)::bigint FROM settlement_raid_loot WHERE kind = 'item'`},
		{&s.RaidTributeLedger, "tribute in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'raid_tribute' AND a.kind = 'city_treasury' AND e.amount > 0`},
		{&s.RaidTributeRows, "tribute in the rows", `SELECT COALESCE(SUM(qty), 0)::bigint FROM settlement_raid_loot WHERE kind = 'tribute'`},
		{&s.FallCashLedger, "fall cash in the ledger", `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'death_cash_transfer' AND a.kind = 'city_treasury' AND e.amount > 0`},
		{&s.FallCashRows, "fall cash in the rows", `SELECT COALESCE(SUM(lost_cash), 0)::bigint FROM falls`},
		{&s.FallGoodsJournal, "fall goods in the item journal", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'death_drop'`},
		{&s.FallGoodsRows, "fall goods in the rows", `SELECT COALESCE(SUM(g.value::bigint), 0)::bigint FROM falls f, jsonb_each_text(f.lost_goods) g(key, value)`},
		{&s.FallMaxShareBPS, "the share of assets a fall took", `SELECT COALESCE(MAX(lost_worth * 10000 / NULLIF(net_assets, 0)), 0)::bigint FROM falls WHERE NOT cooling`},
		{&s.FallStrayLegs, "fall cash legs that left anything but a player's cash", `SELECT count(*) FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			WHERE e.reason = 'death_cash_transfer' AND e.amount < 0 AND a.kind <> 'player_cash'`},
		{&s.RaidWithoutOutcome, "raids closed with no outcome", `SELECT count(*) FROM settlement_raids WHERE status IN ('resolved', 'settled') AND (outcome IS NULL OR resolved_at IS NULL)`},
		{&s.RaidOrphans, "raid rows that name no raid", `SELECT (SELECT count(*) FROM settlement_raid_loot l WHERE NOT EXISTS (SELECT 1 FROM settlement_raids r WHERE r.id = l.raid_id))
			+ (SELECT count(*) FROM settlement_raid_detections d WHERE NOT EXISTS (SELECT 1 FROM settlement_raids r WHERE r.id = d.raid_id))`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
