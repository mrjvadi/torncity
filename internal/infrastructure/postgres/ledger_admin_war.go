package postgres

import (
	"context"
	"fmt"
)

// WarInvariants are war's checks of `admin economy verify`
// (docs/adr/0022-military-and-diplomacy.md, part two): equipment lost in
// battle has left the books consistently — its piece is gone from the world
// through the item journal, once — every piece in an operation belongs to an
// operation under way, every occupied city stands under the country that
// holds it, and the ledger moved exactly what the defence periods say the
// war levy and the repairs cost.
type WarInvariants struct {
	// LostNotGone are asset rows marked destroyed or expended whose piece is
	// still in the world; LostJournal and LostRows the journal's rows that
	// ended state pieces in war and the asset rows that say so.
	LostNotGone           int64
	LostJournal, LostRows int64
	// StrayCommitted are pieces committed to no operation under way.
	StrayCommitted int64
	// MisplacedCities are occupied cities not under their controller.
	MisplacedCities int64
	// The ledger against the defence periods.
	WarLevyLedger, WarLevyRows int64
	RepairLedger, RepairRows   int64
}

func (w WarInvariants) ok() bool {
	return w.LostNotGone == 0 && w.LostJournal == w.LostRows && w.StrayCommitted == 0 && w.MisplacedCities == 0 &&
		w.WarLevyLedger == w.WarLevyRows && w.RepairLedger == w.RepairRows
}

// verifyWar runs war's invariants.
func (a *EconomyAdmin) verifyWar(ctx context.Context, v *LedgerVerification) error {
	w := &v.WarInvariants
	if err := a.q.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM military_assets x
		         WHERE x.status IN ('destroyed', 'expended')
		           AND NOT EXISTS (SELECT 1 FROM item_pieces i WHERE i.id = x.piece_id AND i.holding = 'gone')),
		       (SELECT count(*) FROM item_movements m
		         WHERE m.reason IN ('destroyed', 'expended') AND m.from_org_kind = 'state'),
		       (SELECT count(*) FROM military_assets x WHERE x.status IN ('destroyed', 'expended')),
		       (SELECT count(*) FROM military_assets x
		         WHERE x.status = 'committed'
		           AND NOT EXISTS (SELECT 1 FROM war_operations o WHERE o.id = x.operation_id AND o.status = 'launched')),
		       (SELECT count(*) FROM city_control cc JOIN cities c ON c.id = cc.city_id
		          JOIN jurisdictions j ON j.id = c.jurisdiction_id
		         WHERE j.parent_id IS DISTINCT FROM cc.controller_country_id)`).Scan(
		&w.LostNotGone, &w.LostJournal, &w.LostRows, &w.StrayCommitted, &w.MisplacedCities); err != nil {
		return fmt.Errorf("postgres: checking equipment lost in war: %w", err)
	}
	sums := []struct {
		ledger, rows *int64
		reason, sign string
		sql          string
	}{
		{&w.WarLevyLedger, &w.WarLevyRows, "war_levy", "> 0", `SELECT COALESCE(SUM(war_levy), 0)::bigint FROM military_periods`},
		{&w.RepairLedger, &w.RepairRows, "military_repair", "< 0", `SELECT COALESCE(SUM(repairs), 0)::bigint FROM military_periods`},
	}
	for _, s := range sums {
		// s.sign is one of two fixed strings of this file, never input.
		if err := a.q.QueryRow(ctx, `
			SELECT COALESCE(ABS(SUM(e.amount)), 0)::bigint
			  FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
			 WHERE e.reason = $1 AND a.kind = 'defence_fund' AND e.amount `+s.sign, s.reason).Scan(s.ledger); err != nil {
			return fmt.Errorf("postgres: summing %s in the ledger: %w", s.reason, err)
		}
		if err := a.q.QueryRow(ctx, s.sql).Scan(s.rows); err != nil {
			return fmt.Errorf("postgres: summing %s rows: %w", s.reason, err)
		}
	}
	return nil
}
