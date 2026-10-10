package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/domain/landroad"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The land beyond the first grid (docs/adr/0044 5.5, "a road opens the land it
// reaches"; migration 0110).
//
// The grid a village is founded with is the first block of land: a dense
// [y][x] array that every older rule (access, placement, roads) indexes. Land
// opened by roads has no edge and no shape, so it is kept apart: a sparse set
// of lots (the road cells of every plan, and the frontage lots along them),
// addressed by the same absolute lot coordinates, which may be negative.
//
// landPicture is both together. Where a rule needs a grid (CanPlace, the
// access router, the street planner) it is handed a WINDOW: a small dense
// grid over the lots it cares about, built from whichever side each lot lies
// on, with coordinates shifted so the window starts at 0. The domain rules do
// not change and do not know the land has no edge.

// landPicture is the village's land as one request reads it.
type landPicture struct {
	s        application.FoundedSettlement
	grid     settlementbuilding.Grid
	existing []application.SettlementBuildingInstance
	side     int
	frame    wsettle.LotFrame
	plans    map[string]application.RoadPlanRow
	cells    map[landroad.Lot]application.RoadCellRow
	open     map[landroad.Lot]application.OpenLotRow
	// occ is, for the lots beyond the grid, the building type standing on each.
	occ map[[2]int]string
	// held is the owner of every private lot, in the grid and beyond it.
	held map[[2]int]string
	// network is every road lot of the first grid (and the civic hall).
	network [][2]int
	// land is the trees and rocks standing on the lots a road opened (docs/adr/0065); nil when the land model is off.
	land *application.LandView
}

// frameOf is the settlement's land frame on the world.
func (h *VillageHandler) frameOf(w *worldgen.World, s application.FoundedSettlement) wsettle.LotFrame {
	pt := w.Cells[s.WorldCellID].Point
	return wsettle.FrameOfGrid(w, pt.LatDeg, pt.LonDeg, s.GridShiftX, s.GridShiftY, s.GridGrowth, h.gridSide(s))
}

// picture reads the land: the grid, what stands on it, and the roads' land.
func (h *VillageHandler) picture(ctx context.Context, tx application.Tx, w *worldgen.World, s application.FoundedSettlement,
	lots []application.SettlementLot,
) (*landPicture, error) {
	grid, existing, err := h.grid(ctx, tx, w, s)
	if err != nil {
		return nil, err
	}
	p := &landPicture{
		s: s, grid: grid, existing: existing, side: len(grid), frame: h.frameOf(w, s),
		plans: map[string]application.RoadPlanRow{}, cells: map[landroad.Lot]application.RoadCellRow{},
		open: map[landroad.Lot]application.OpenLotRow{}, occ: map[[2]int]string{}, held: make(map[[2]int]string, len(lots)),
	}
	plans, err := tx.Citizens().Plans(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	for _, pl := range plans {
		p.plans[pl.ID] = pl
	}
	if len(plans) > 0 {
		cells, err := tx.Citizens().Cells(ctx, s.CityID)
		if err != nil {
			return nil, err
		}
		for _, c := range cells {
			p.cells[landroad.Lot{X: c.X, Y: c.Y}] = c
			// a drawn road that runs through the first grid keeps its lots out
			// of the sale until it is laid
			if p.inGrid(c.X, c.Y) && !c.Built() {
				p.grid[c.Y][c.X].Reserved = true
			}
		}
		open, err := tx.Citizens().OpenLots(ctx, s.CityID)
		if err != nil {
			return nil, err
		}
		for _, o := range open {
			p.open[landroad.Lot{X: o.X, Y: o.Y}] = o
		}
		if lv, lerr := h.landViewOf(ctx, tx, w, s, open); lerr != nil {
			return nil, lerr
		} else if len(lv.Lots) > 0 {
			p.land = lv
		}
	}
	snap := h.content.Current()
	for _, b := range existing {
		if !b.Holds() {
			continue
		}
		fw, fh := 1, 1
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			fw, fh = def.FootprintW, def.FootprintH
		}
		for dy := 0; dy < fh; dy++ {
			for dx := 0; dx < fw; dx++ {
				if x, y := b.LotX+dx, b.LotY+dy; !p.inGrid(x, y) {
					p.occ[[2]int{x, y}] = b.TypeCode
				}
			}
		}
	}
	for _, l := range lots {
		p.held[[2]int{l.X, l.Y}] = l.OwnerID
	}
	p.network = h.networkLots(existing)
	return p, nil
}

// inGrid reports whether a lot is in the first grid.
func (p *landPicture) inGrid(x, y int) bool { return x >= 0 && y >= 0 && x < p.side && y < p.side }

// innerHeld counts the private lots in the first grid.
func (p *landPicture) innerHeld() int {
	n := 0
	for k := range p.held {
		if p.inGrid(k[0], k[1]) {
			n++
		}
	}
	return n
}

// hasOuter reports whether any road has been drawn.
func (p *landPicture) hasOuter() bool { return len(p.plans) > 0 }

// outerLot is the lot at (x, y) beyond the grid, as the placement rules see
// it: a road cell is taken (and, until it is laid, reserved); a lot a road
// opened is as it was sampled; every other lot is closed.
func (p *landPicture) outerLot(x, y int) settlementbuilding.Lot {
	l := landroad.Lot{X: x, Y: y}
	if c, ok := p.cells[l]; ok {
		return settlementbuilding.Lot{Buildable: true, Occupied: true, Reserved: !c.Built()}
	}
	_, stands := p.occ[[2]int{x, y}]
	if o, ok := p.open[l]; ok {
		tags := append([]string(nil), o.Tags...)
		if o.Reason == application.OpenLotSteep {
			tags = append(tags, "sloped_lot")
		}
		lot := settlementbuilding.Lot{Buildable: o.Buildable, Occupied: stands, TerrainTags: tags}
		if p.land != nil {
			if ll, ok := p.land.Lots[land.Pos{X: x, Y: y}]; ok {
				lot.Obstructed = ll.Obstructed
				if ll.Rocks > 0 {
					lot.TerrainTags = append(lot.TerrainTags, "rocky_lot")
				}
			}
		}
		return lot
	}
	// land no road reaches
	return settlementbuilding.Lot{Occupied: true}
}

// lotAt is the lot at (x, y) wherever it lies.
func (p *landPicture) lotAt(x, y int) settlementbuilding.Lot {
	if p.inGrid(x, y) {
		return p.grid[y][x]
	}
	return p.outerLot(x, y)
}

// window is a dense copy of the rectangle [x0, x0+wd) x [y0, y0+ht) of the
// land, indexed from its corner.
func (p *landPicture) window(x0, y0, wd, ht int) settlementbuilding.Grid {
	g := make(settlementbuilding.Grid, ht)
	for j := 0; j < ht; j++ {
		g[j] = make([]settlementbuilding.Lot, wd)
		for i := 0; i < wd; i++ {
			l := p.lotAt(x0+i, y0+j)
			l.TerrainTags = append([]string(nil), l.TerrainTags...)
			g[j][i] = l
		}
	}
	return g
}

// networkIn lists the road lots (the grid's network and every cell of every
// plan, laid or not) inside the rectangle, in window coordinates.
func (p *landPicture) networkIn(x0, y0, wd, ht int) [][2]int {
	var out [][2]int
	in := func(x, y int) bool { return x >= x0 && y >= y0 && x < x0+wd && y < y0+ht }
	for _, n := range p.network {
		if in(n[0], n[1]) {
			out = append(out, [2]int{n[0] - x0, n[1] - y0})
		}
	}
	for l := range p.cells {
		if in(l.X, l.Y) {
			out = append(out, [2]int{l.X - x0, l.Y - y0})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][1] != out[j][1] {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out
}

// windowMargin is how far past a lot (or a footprint) a window reaches: the
// frontage band and a lane, so a lot's whole way to the road is inside it.
func (h *VillageHandler) windowMargin() int {
	d := h.citizen.RoadFrontageDepth
	if d < 1 {
		d = 1
	}
	return d + 2
}

// planClass is the road class of the plan a cell belongs to.
func (p *landPicture) planClass(c application.RoadCellRow) string {
	return p.plans[c.PlanID].Class
}

// surfaceBPS is the price of a lot of a class against the base price.
func (h *VillageHandler) surfaceBPS(class string) int {
	if class == "track" && h.citizen.RoadTrackCostBPS > 0 {
		return h.citizen.RoadTrackCostBPS
	}
	return 10000
}

// chainTo lists the unlaid cells that must be laid so the road cell at l works
// (root first), and what laying them costs.
func (h *VillageHandler) chainTo(p *landPicture, l landroad.Lot) (cells []landroad.Lot, roads, crossings int, cost int64) {
	plan := make(map[landroad.Lot]landroad.Cell, len(p.cells))
	for k, c := range p.cells {
		plan[k] = landroad.Cell{Lot: k, Parent: landroad.Lot{X: c.ParentX, Y: c.ParentY}, HasParent: c.HasParent, Built: c.Built()}
	}
	todo, ok := landroad.ToBuild(plan, l)
	if !ok {
		return nil, 0, 0, 0
	}
	// each cell is priced by the surface of its own plan
	for _, k := range todo {
		c := p.cells[k]
		r, x := 1, 0
		if c.Water != application.RoadWaterNone {
			r, x = 0, 1
		}
		roads += r
		crossings += x
		cost += landroad.Price(r, x, h.autoRoadCost, h.citizen.CrossingLotCost, h.surfaceBPS(p.planClass(c)))
	}
	return todo, roads, crossings, cost
}

// touchedCell is the plan cell beside l that costs least to make work: the
// cell a lane or a building meets. ok is false when no plan cell is beside it
// (the first grid's own network serves).
func (h *VillageHandler) touchedCell(p *landPicture, around [][2]int) (cell landroad.Lot, ok bool) {
	var best landroad.Lot
	var bestCost int64
	for _, a := range around {
		for _, d := range [4][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}} {
			l := landroad.Lot{X: a[0] + d[0], Y: a[1] + d[1]}
			if _, isCell := p.cells[l]; !isCell {
				continue
			}
			_, _, _, c := h.chainTo(p, l)
			if !ok || c < bestCost || (c == bestCost && (l.Y < best.Y || (l.Y == best.Y && l.X < best.X))) {
				best, bestCost, ok = l, c, true
			}
		}
	}
	return best, ok
}

// outerAccess is how a lot beyond the grid is served: the lane over open lots
// from the lot to a road cell (the same router as the grid's), plus the stretch
// of drawn road that is not laid yet between that cell and the built network.
// Both are laid, and paid, by the buyer. Path is the lane then the stretch, in
// absolute lot coordinates; Cost is both.
func (h *VillageHandler) outerAccess(p *landPicture, lot [2]int, asker string) settlementbuilding.Access {
	m := h.windowMargin()
	x0, y0, wd, ht := lot[0]-m, lot[1]-m, 2*m+1, 2*m+1
	win := p.window(x0, y0, wd, ht)
	held := map[[2]int]string{}
	for k, v := range p.held {
		if k[0] >= x0 && k[1] >= y0 && k[0] < x0+wd && k[1] < y0+ht {
			held[[2]int{k[0] - x0, k[1] - y0}] = v
		}
	}
	net := p.networkIn(x0, y0, wd, ht)
	reserved := map[[2]int]bool{}
	for j := range win {
		for i := range win[j] {
			if win[j][i].Reserved {
				reserved[[2]int{i, j}] = true
			}
		}
	}
	rules := h.accessRules()
	am := settlementbuilding.AccessMap{Grid: win, Network: net, Held: held, Reserved: reserved}
	a := am.Plan([2]int{lot[0] - x0, lot[1] - y0}, asker, false, rules)
	if !a.Feasible() {
		return settlementbuilding.Access{Kind: settlementbuilding.AccessNone}
	}
	lane := make([][2]int, len(a.Path))
	for i, q := range a.Path {
		lane[i] = [2]int{q[0] + x0, q[1] + y0}
	}
	end := lot
	if len(lane) > 0 {
		end = lane[len(lane)-1]
	}
	out := settlementbuilding.Access{Path: lane, Crossings: a.Crossings, Cost: a.Cost}
	if cell, ok := h.touchedCell(p, [][2]int{end}); ok {
		chain, _, crossings, cost := h.chainTo(p, landroad.Lot{X: cell.X, Y: cell.Y})
		for _, c := range chain {
			out.Path = append(out.Path, [2]int{c.X, c.Y})
		}
		out.Crossings += crossings
		out.Cost += cost
	}
	switch {
	case len(out.Path) == 0:
		out.Kind = settlementbuilding.AccessRoad
	case out.Crossings > 0:
		out.Kind = settlementbuilding.AccessNeedsBridge
	default:
		out.Kind = settlementbuilding.AccessNeedsRoad
	}
	return out
}

// canPlaceOn is settlementbuilding.CanPlace over the land: the grid when the
// whole footprint lies in it, else a window over the footprint.
func (p *landPicture) canPlaceOn(def settlementbuilding.Def, x, y int, st settlementbuilding.Standing) error {
	if x >= 0 && y >= 0 && x+def.FootprintW <= p.side && y+def.FootprintH <= p.side {
		return settlementbuilding.CanPlace(def, p.grid, x, y, st)
	}
	win := p.window(x, y, def.FootprintW, def.FootprintH)
	return settlementbuilding.CanPlace(def, win, 0, 0, st)
}

// footprintBeyond reports whether any lot of a footprint lies beyond the grid.
func (p *landPicture) footprintBeyond(def settlementbuilding.Def, x, y int) bool {
	return !(x >= 0 && y >= 0 && x+def.FootprintW <= p.side && y+def.FootprintH <= p.side)
}

// autoRoadPlan is the roads a placement lays and what they cost.
type autoRoadPlan struct {
	Path [][2]int
	Fee  int64
}

// planOuterRoads is planAutoRoads for a building whose footprint reaches
// beyond the first grid: the lane from the building to the nearest road cell
// over open lots (never over a lot anybody owns), plus the stretch of drawn
// road that is still only planned between that cell and the built network.
func (h *VillageHandler) planOuterRoads(p *landPicture, def settlementbuilding.Def, x, y int) (autoRoadPlan, error) {
	m := h.windowMargin()
	x0, y0 := x-m, y-m
	wd, ht := def.FootprintW+2*m, def.FootprintH+2*m
	win := p.window(x0, y0, wd, ht)
	fpWin := make([][2]int, 0, def.FootprintW*def.FootprintH)
	for _, q := range footprintOf(def, x, y) {
		fpWin = append(fpWin, [2]int{q[0] - x0, q[1] - y0})
		win[q[1]-y0][q[0]-x0].Occupied = true
	}
	// a road never crosses a lot somebody owns
	for k := range p.held {
		if i, j := k[0]-x0, k[1]-y0; i >= 0 && j >= 0 && i < wd && j < ht {
			win[j][i].Occupied = true
		}
	}
	net := p.networkIn(x0, y0, wd, ht)
	if len(net) == 0 {
		return autoRoadPlan{}, refuseVillage(village.VillageNoRoad)
	}
	lane, err := settlementbuilding.PlanRoads(win, fpWin, net, nil)
	if stderrors.Is(err, settlementbuilding.ErrNoRoadAccess) {
		return autoRoadPlan{}, refuseVillage(village.VillageNoRoad)
	}
	if err != nil {
		return autoRoadPlan{}, err
	}
	out := autoRoadPlan{Fee: int64(len(lane)) * h.autoRoadCost}
	var around [][2]int
	for _, q := range lane {
		abs := [2]int{q[0] + x0, q[1] + y0}
		out.Path = append(out.Path, abs)
	}
	if len(lane) > 0 {
		around = [][2]int{out.Path[len(out.Path)-1]}
	} else {
		around = footprintOf(def, x, y)
	}
	if cell, ok := h.touchedCell(p, around); ok {
		chain, _, _, cost := h.chainTo(p, landroad.Lot{X: cell.X, Y: cell.Y})
		for _, c := range chain {
			out.Path = append(out.Path, [2]int{c.X, c.Y})
		}
		out.Fee += cost
	}
	return out, nil
}

// mayDrawRoads reports whether a player may draw roads out of the village: whoever
// holds the charter's road.draw permission (ADR 0044 section 6).
func (h *VillageHandler) mayDrawRoads(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string) (bool, error) {
	return h.mayVillage(ctx, tx, s, playerID, charter.RoadDraw)
}

// outerCells lists the land the roads opened for a viewer: every road cell
// and every open lot, with who holds it. Prices of the free ones are read from
// the access router while the count stays small (see lotAccessScanMax).
func (h *VillageHandler) outerCells(p *landPicture, sc *citizenScope, names map[string]string) (cells []village.LandCell, free, served int) {
	priced := len(p.open) <= lotAccessScanMax*4
	keys := make([]landroad.Lot, 0, len(p.cells)+len(p.open))
	for l := range p.cells {
		keys = append(keys, l)
	}
	for l := range p.open {
		keys = append(keys, l)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Y != keys[j].Y {
			return keys[i].Y < keys[j].Y
		}
		return keys[i].X < keys[j].X
	})
	for _, l := range keys {
		cell := village.LandCell{X: l.X, Y: l.Y}
		if c, ok := p.cells[l]; ok {
			cell.State = village.LandPlanned
			if c.Built() {
				cell.State = village.LandRoad
				cell.Building = "road"
			}
			cells = append(cells, cell)
			continue
		}
		o := p.open[l]
		owner, isOwned := sc.lotAt(l.X, l.Y)
		cell.Building = p.occ[[2]int{l.X, l.Y}]
		switch {
		case isOwned && owner.OwnerID == sc.p.ID:
			cell.State = village.LandMine
		case isOwned:
			cell.State = village.LandTaken
			cell.Owner = names[owner.OwnerID]
		case o.Reason == application.OpenLotWater:
			cell.State = village.LandWater
		case o.Reason == application.OpenLotSteep:
			cell.State = village.LandSteep
		case cell.Building != "":
			cell.State = village.LandBuilding
		default:
			cell.State = village.LandFree
			free++
			if priced {
				a := h.outerAccess(p, [2]int{l.X, l.Y}, sc.p.ID)
				setCellAccess(&cell, a)
				if a.Feasible() {
					served++
				}
			}
		}
		cells = append(cells, cell)
	}
	return cells, free, served
}

// roadLines lists the drawn roads with how far each has got.
func (p *landPicture) roadLines(snapName func(string) string, owned map[[2]int]bool) []village.RoadPlanLine {
	ids := make([]string, 0, len(p.plans))
	for id := range p.plans {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := p.plans[ids[i]], p.plans[ids[j]]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	lines := make([]village.RoadPlanLine, 0, len(ids))
	byPlan := map[string]*village.RoadPlanLine{}
	for _, id := range ids {
		pl := p.plans[id]
		lines = append(lines, village.RoadPlanLine{ID: id, Class: named(pl.Class, snapName(pl.Class)), To: village.LotRef{X: pl.ToX, Y: pl.ToY}, Cancellable: true})
	}
	for i := range lines {
		byPlan[lines[i].ID] = &lines[i]
	}
	for _, c := range p.cells {
		l := byPlan[c.PlanID]
		if l == nil {
			continue
		}
		l.Lots++
		if c.Built() {
			l.Built++
			l.Cancellable = false
		}
	}
	for k, o := range p.open {
		l := byPlan[o.PlanID]
		if l == nil || !o.Buildable {
			continue
		}
		if owned[[2]int{k.X, k.Y}] {
			l.Sold++
			l.Cancellable = false
		} else if _, stands := p.occ[[2]int{k.X, k.Y}]; !stands {
			l.Open++
		}
	}
	return lines
}

// heldSet is the set of lots somebody owns.
func (p *landPicture) heldSet() map[[2]int]bool {
	out := make(map[[2]int]bool, len(p.held))
	for k := range p.held {
		out[k] = true
	}
	return out
}

// roadClassName is a road class's name from the content.
func (h *VillageHandler) roadClassName(code string) string {
	if c, ok := h.content.Current().RoadClass(code); ok && c.Name != "" {
		return c.Name
	}
	return code
}

// canPlaceAnywhere is settlementbuilding.CanPlace over the whole land: the
// first grid, or, when the footprint reaches beyond it, the lots a road
// opened. A footprint out of the open land is refused as out of bounds.
func (h *VillageHandler) canPlaceAnywhere(ctx context.Context, tx application.Tx, s application.FoundedSettlement,
	def settlementbuilding.Def, grid settlementbuilding.Grid, x, y int, st settlementbuilding.Standing,
) error {
	if x >= 0 && y >= 0 && x+def.FootprintW <= len(grid) && y+def.FootprintH <= len(grid) {
		return settlementbuilding.CanPlace(def, grid, x, y, st)
	}
	w, err := h.world(ctx)
	if err != nil {
		return err
	}
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return err
	}
	pic, err := h.picture(ctx, tx, w, s, lots)
	if err != nil {
		return err
	}
	// a footprint that touches land no road opened is out of bounds
	for _, q := range footprintOf(def, x, y) {
		if pic.inGrid(q[0], q[1]) {
			continue
		}
		l := landroad.Lot{X: q[0], Y: q[1]}
		_, open := pic.open[l]
		_, road := pic.cells[l]
		if !open && !road {
			return settlementbuilding.ErrOutOfBounds
		}
	}
	return pic.canPlaceOn(def, x, y, st)
}

// pictureOf reads the land for a settlement without a citizen scope.
func (h *VillageHandler) pictureOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (*landPicture, error) {
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	return h.picture(ctx, tx, w, s, lots)
}

// outerLotCells is the placement picker's view of the land beyond the grid:
// every road cell and every lot a road opened, with the state it has and
// whether the building being placed fits with its corner there. own, when
// given, says which lots are the viewer's.
func (h *VillageHandler) outerLotCells(p *landPicture, fits func(x, y int) bool, own func(x, y int) bool) []village.LotCell {
	keys := make([]landroad.Lot, 0, len(p.cells)+len(p.open))
	for l := range p.cells {
		keys = append(keys, l)
	}
	for l := range p.open {
		keys = append(keys, l)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Y != keys[j].Y {
			return keys[i].Y < keys[j].Y
		}
		return keys[i].X < keys[j].X
	})
	out := make([]village.LotCell, 0, len(keys))
	for _, l := range keys {
		cell := village.LotCell{X: l.X, Y: l.Y}
		if c, ok := p.cells[l]; ok {
			cell.State = village.LotPlanned
			if c.Built() {
				cell.State = village.LotRoad
			}
			out = append(out, cell)
			continue
		}
		o := p.open[l]
		switch {
		case o.Reason == application.OpenLotWater:
			cell.State = village.LotWater
		case o.Reason == application.OpenLotSteep:
			cell.State = village.LotSteep
		case p.occ[[2]int{l.X, l.Y}] != "":
			cell.State = village.LotOccupied
		default:
			cell.State = village.LotFree
		}
		if own != nil {
			cell.Own = own(l.X, l.Y)
		}
		cell.Fits = cell.State == village.LotFree && fits(l.X, l.Y)
		out = append(out, cell)
	}
	return out
}

// layoutMark is the mark the member layout versions fold in: who owns what,
// and which roads are drawn and laid.
func (h *VillageHandler) layoutMark(ctx context.Context, tx application.Tx, settlementID string,
	lots []application.SettlementLot, priv []application.PrivateBuilding,
) (string, error) {
	mark := application.TenureMark(lots, priv)
	plans, err := tx.Citizens().Plans(ctx, settlementID)
	if err != nil || len(plans) == 0 {
		return mark, err
	}
	cells, err := tx.Citizens().Cells(ctx, settlementID)
	if err != nil {
		return "", err
	}
	return application.JoinMarks(mark, application.LandMark(plans, cells)), nil
}
