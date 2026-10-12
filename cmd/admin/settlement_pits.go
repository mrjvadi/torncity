package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/mining"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// settlementPits is the check to run before the iron pits begin to draw on their deposits (docs/adr/0075 9): it lists every founded
// settlement's finished iron pits against the deposit under its cell: what the surface holds, how long it lasts if the pits are worked
// for every minute of the day (the most a pit can dig), and whether that is under mining.min_surface_days. A pit must wear out gradually and
// never close overnight: a settlement listed LOW would see its pits thin and stop within days of the rule date, and the supervisor should
// raise mining.reserve_divisor's inverse (lower the divisor) or the surface share before setting the rule date. Read-only.
func settlementPits(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement pits: loading configuration: %w", err)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	row, w, err := activeWorldFor(ctx, postgres.NewWorldRepository(pool), cfg)
	if err != nil {
		return fmt.Errorf("settlement pits: %w", err)
	}
	pack, err := content.Load(contentDir())
	if err != nil {
		return fmt.Errorf("settlement pits: loading content: %w", err)
	}
	var pit content.SettlementBuildingDef
	for _, d := range pack.SettlementBuildings {
		if d.Mining != nil && !d.Mining.Deep {
			pit = d
		}
	}
	if pit.Code == "" {
		return fmt.Errorf("settlement pits: the content has no pit that digs a deposit")
	}
	rules := settlementcfg.Mining(cfg.Mining).Rules
	medium := pit.Mining.ItemOf("medium")
	perShift := pit.Produces[medium]
	shiftMin := int64(30)
	if m := shiftMinutes(pit); m > 0 {
		shiftMin = m
	}
	perPitDay := perShift * int64(pit.Workers) * (24 * 60 / shiftMin) // the most a pit digs: every slot working all day
	sites, err := postgres.NewSettlementOps(pool).Sites(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("settlement pits: %w", err)
	}
	fmt.Printf("the surface share is %d bps of a deposit of reserve / %d units; a pit digs at most %d units a day (%d a shift, %d slots, %d-minute shifts)\n\n",
		cfg.Mining.SurfaceShareBPS, cfg.Mining.ReserveDivisor, perPitDay, perShift, pit.Workers, shiftMin)
	low := 0
	for _, s := range sites {
		var pits int64
		if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = $2 AND status = 'complete'`, s.ID, pit.Code).Scan(&pits); err != nil {
			return fmt.Errorf("settlement pits: %s: %w", s.Name, err)
		}
		if pits == 0 {
			continue
		}
		dep, ok := wsettle.DepositOf(w, s.CellID, pit.Mining.Resource)
		if !ok {
			fmt.Printf("%-28s %d pit(s)  NO DEPOSIT under the cell: the pit will be refused after the rule date\n", s.Name, pits)
			low++
			continue
		}
		reach := rules.Reachable(dep.Reserve, mining.DepthSurface)
		days := reach / max(pits*perPitDay, 1)
		var taken int64
		_ = pool.Raw().QueryRow(ctx, `SELECT COALESCE((SELECT taken FROM ore_deposits WHERE deposit_id = $1), 0)`, dep.ID).Scan(&taken)
		status := "ok"
		if days < cfg.Mining.MinSurfaceDays {
			status = "LOW"
			low++
		}
		fmt.Printf("%-28s %d pit(s)  surface %d units, taken %d, lasts %d days at full work (band %s, grade %d)  %s\n",
			s.Name, pits, reach, taken, days, rules.BandOf(dep.GradePermille), dep.GradePermille, status)
	}
	// the poorest deposit a settlement could be founded on
	var worst int64 = -1
	for _, d := range w.Deposits {
		if d.ResourceCode != pit.Mining.Resource {
			continue
		}
		if r := rules.Reachable(d.Reserve, mining.DepthSurface); worst < 0 || r < worst {
			worst = r
		}
	}
	if worst >= 0 {
		fmt.Printf("\nthe poorest %s deposit of the world holds %d units on the surface: %d days at full work for one pit\n", pit.Mining.Resource, worst, worst/max(perPitDay, 1))
	}
	if low > 0 {
		return fmt.Errorf("settlement pits: %d settlements would see their pits run dry inside %d days of full work, or have no deposit: do not set the rule date before this is settled", low, cfg.Mining.MinSurfaceDays)
	}
	fmt.Printf("\nevery live pit holds at least %d days of full work on its surface\n", cfg.Mining.MinSurfaceDays)
	return nil
}

func shiftMinutes(d content.SettlementBuildingDef) int64 {
	if dur, ok := parseMinutes(d.Shift); ok {
		return dur
	}
	return 30
}

func parseMinutes(s string) (int64, bool) {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return int64(d / time.Minute), true
}
