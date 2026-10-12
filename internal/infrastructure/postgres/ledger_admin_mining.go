package postgres

import (
	"context"
	"fmt"
)

// verifyMining runs the checks of mining, carriage and rail when migration 0153 is applied (docs/adr/0075 14): what was taken from a deposit is what
// the shifts brought up; no deposit gave more than its reach (compared by the operator against the config); goods on the road are exactly the
// goods that left a store and have not reached one; no section of track holds more trains than it can; a freight fee in the ledger is what its
// booking says.
func (a *EconomyAdmin) verifyMining(ctx context.Context, v *LedgerVerification) error {
	s := &v.VillageInvariants
	if err := a.q.QueryRow(ctx, `SELECT to_regclass('public.ore_deposits') IS NOT NULL`).Scan(&s.Mining); err != nil {
		return fmt.Errorf("postgres: looking for the mines: %w", err)
	}
	if !s.Mining {
		return nil
	}
	for _, c := range []struct {
		into *int64
		what string
		sql  string
	}{
		{&s.MineTaken, "the ore taken from the deposits", `SELECT COALESCE(SUM(taken), 0)::bigint FROM ore_deposits`},
		{&s.MineExtracted, "the ore the extraction rows say", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM ore_extractions`},
		{&s.MineShiftMismatch, "extraction rows that are not what their shift delivered", `SELECT count(*) FROM ore_extractions e JOIN settlement_shifts s ON s.id = e.shift_id
			WHERE e.quantity <> COALESCE((s.produced->>e.item_code)::bigint, -1)`},
		{&s.MineMaxTakenBPS, "the largest share of a deposit's reserve taken", `SELECT COALESCE(MAX(taken * 10000 / NULLIF(reserve, 0)), 0)::bigint FROM ore_deposits`},
		{&s.HaulOut, "goods that left a store for the road", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'haul_out'`},
		{&s.HaulIn, "goods that arrived from the road", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_movements WHERE reason = 'haul_in'`},
		{&s.HaulOnRoad, "goods in the open loads", `SELECT COALESCE(SUM(quantity), 0)::bigint FROM haul_loads WHERE arrived_at IS NULL`},
		{&s.HaulCartNoAction, "cart loads with no arrival scheduled", `SELECT count(*) FROM haul_loads WHERE arrived_at IS NULL AND carrier_kind = 'cart' AND game_action_id IS NULL`},
		{&s.RailOverheld, "sections that hold more trains than they can", `SELECT count(*) FROM (SELECT g.segment_id FROM rail_reservations g JOIN rail_segments s ON s.id = g.segment_id
			GROUP BY g.segment_id, s.loop HAVING count(*) > CASE WHEN s.loop THEN 2 ELSE 1 END) x`},
		{&s.RailHeldIdle, "sections held by a train that is not running", `SELECT count(*) FROM rail_reservations g JOIN rail_trains t ON t.id = g.train_id WHERE t.status <> 'running'`},
		{&s.FreightBroken, "freight bookings whose fee is not in the ledger", `SELECT count(*) FROM haul_orders o WHERE o.mode = 'rail' AND o.fee > 0 AND o.fee_tx IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.reference_id = o.id AND e.reason = 'freight_fee' AND e.amount = o.fee)`},
	} {
		if err := a.q.QueryRow(ctx, c.sql).Scan(c.into); err != nil {
			return fmt.Errorf("postgres: checking %s: %w", c.what, err)
		}
	}
	return nil
}
