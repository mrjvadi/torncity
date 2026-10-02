package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// settlementLandlocked is the read-only report behind docs/adr/0043: every
// private lot with nothing built on it that no road touches, how a road could
// reach it (public land, water, the owner's own land) and what that costs. It
// changes nothing: the owner of each lot chooses, in the game, between the
// connection, a road through their own land and a refund (settlement.lot.repair).
func settlementLandlocked(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("settlement landlocked: loading configuration: %w", err)
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	row, w, err := activeWorldFor(ctx, postgres.NewWorldRepository(pool), cfg)
	if err != nil {
		return fmt.Errorf("settlement landlocked: %w", err)
	}
	pack, err := content.Load(contentDir())
	if err != nil {
		return fmt.Errorf("settlement landlocked: loading content: %w", err)
	}
	sites, err := postgres.NewSettlementOps(pool).Sites(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("settlement landlocked: %w", err)
	}
	rules := settlementbuilding.AccessRules{
		RoadLotCost: cfg.Settlement.AutoRoadCost, CrossingLotCost: cfg.Settlement.LotAccessCrossingCost, MaxCrossing: cfg.Settlement.LotAccessMaxCrossing,
	}
	total, locked := 0, 0
	for _, s := range sites {
		n, l, err := landlockedOf(ctx, pool, cfg, pack, w, s, rules)
		if err != nil {
			return fmt.Errorf("settlement landlocked: %s: %w", s.Name, err)
		}
		total += n
		locked += l
	}
	fmt.Printf("\n%d private bare lots, %d with no road touching them\n", total, locked)
	return nil
}

func landlockedOf(ctx context.Context, pool *postgres.Pool, cfg *config.Config, pack *content.Pack, w *worldgen.World,
	s postgres.SettlementSite, rules settlementbuilding.AccessRules,
) (bare, locked int, err error) {
	q := pool.Raw()
	var growth int
	if err := q.QueryRow(ctx, `SELECT grid_growth FROM cities WHERE id = $1::uuid`, s.ID).Scan(&growth); err != nil {
		return 0, 0, err
	}
	side := wsettle.GridLotsGrown(s.Tier, cfg.Settlement.VillageGridLots, growth)
	cell := w.Cells[s.CellID]
	lat, lon := wsettle.GridCentreGrown(w, cell.Point.LatDeg, cell.Point.LonDeg, s.ShiftX, s.ShiftY, growth)
	sampled := wsettle.SampleGrid(w, lat, lon, side, s.CellID)
	grid := make(settlementbuilding.Grid, side)
	for y := range grid {
		grid[y] = make([]settlementbuilding.Lot, side)
		for x := range grid[y] {
			grid[y][x] = settlementbuilding.Lot{Buildable: sampled[y][x].Buildable, TerrainTags: sampled[y][x].Tags}
		}
	}
	defs := map[string]settlementbuilding.Def{}
	for _, d := range pack.SettlementBuildings {
		defs[d.Code] = d.Def()
	}
	var network [][2]int
	built := map[[2]int]bool{}
	brows, err := q.Query(ctx, `SELECT type_code, lot_x, lot_y, rotated FROM settlement_buildings
		WHERE settlement_id = $1::uuid AND status NOT IN ('demolished', 'cancelled')`, s.ID)
	if err != nil {
		return 0, 0, err
	}
	for brows.Next() {
		var code string
		var x, y int
		var rotated bool
		if err := brows.Scan(&code, &x, &y, &rotated); err != nil {
			brows.Close()
			return 0, 0, err
		}
		def, ok := defs[code]
		if !ok {
			def = settlementbuilding.Def{FootprintW: 1, FootprintH: 1}
		}
		if rotated {
			def = def.Rotate()
		}
		for dy := 0; dy < def.FootprintH; dy++ {
			for dx := 0; dx < def.FootprintW; dx++ {
				p := [2]int{x + dx, y + dy}
				if p[1] < side && p[0] < side {
					grid[p[1]][p[0]].Occupied = true
					built[p] = true
					if code == "road" || code == "civic_hall" {
						network = append(network, p)
					}
				}
			}
		}
	}
	brows.Close()
	held := map[[2]int]string{}
	type lotRow struct {
		x, y  int
		owner string
		price int64
	}
	var lots []lotRow
	lrows, err := q.Query(ctx, `SELECT lot_x, lot_y, owner_id::text, price FROM settlement_lots
		WHERE settlement_id = $1::uuid AND released_at IS NULL ORDER BY lot_y, lot_x`, s.ID)
	if err != nil {
		return 0, 0, err
	}
	for lrows.Next() {
		var l lotRow
		if err := lrows.Scan(&l.x, &l.y, &l.owner, &l.price); err != nil {
			lrows.Close()
			return 0, 0, err
		}
		held[[2]int{l.x, l.y}] = l.owner
		lots = append(lots, l)
	}
	lrows.Close()
	reserved := map[[2]int]bool{}
	rrows, err := q.Query(ctx, `SELECT lot_x, lot_y FROM settlement_road_reserve WHERE settlement_id = $1::uuid`, s.ID)
	if err != nil {
		return 0, 0, err
	}
	for rrows.Next() {
		var x, y int
		if err := rrows.Scan(&x, &y); err != nil {
			rrows.Close()
			return 0, 0, err
		}
		if y < side && x < side {
			reserved[[2]int{x, y}] = true
			grid[y][x].Reserved = true
		}
	}
	rrows.Close()
	m := settlementbuilding.AccessMap{Grid: grid, Network: network, Held: held, Reserved: reserved}

	sort.SliceStable(lots, func(i, j int) bool { return lots[i].owner < lots[j].owner })
	printed := false
	for _, l := range lots {
		p := [2]int{l.x, l.y}
		if built[p] || l.y >= side || l.x >= side {
			continue
		}
		bare++
		a := m.Plan(p, l.owner, false, rules)
		if a.Kind == settlementbuilding.AccessRoad {
			continue
		}
		locked++
		if !printed {
			fmt.Printf("%s (%s) %s, grid %dx%d\n", s.Name, s.Code, s.ID, side, side)
			printed = true
		}
		carve := m.Plan(p, l.owner, true, rules)
		fmt.Printf("  lot (%d,%d) owner %s paid %d: %s", l.x, l.y, l.owner, l.price, a.Kind)
		if a.Feasible() {
			fmt.Printf(", road of %d lots (%d crossing water) costs %d", len(a.Path), a.Crossings, a.Cost)
		} else if carve.Feasible() {
			fmt.Printf(", only through the owner's own land (%d lots carved) for %d", len(carve.Carved), carve.Cost)
		}
		fmt.Println("; refund on offer:", l.price)
	}
	return bare, locked, nil
}
