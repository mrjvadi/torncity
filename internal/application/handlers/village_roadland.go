package handlers

import (
	"context"
	stderrors "errors"
	"math"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/landroad"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Drawing a road out of the first grid (docs/adr/0044 5.5 and the owner
// decision of 2026-10-03, "a road opens the land it reaches").
//
// The holder of the village's top office (until the charter carries road.draw)
// points at a place; the road is routed over the world's tiles (the ADR 0042
// router), refined lot by lot inside that corridor, and quoted. Confirming the
// quote stores a PLAN, free of charge: the plan reserves its corridor, claims
// the tiles it runs on against other settlements, and opens the lots along it
// for sale. Nothing is built, and nothing is charged, until a buyer takes a lot
// the road serves: the buyer pays for the stretch of road up to the lot, in
// the same purchase (village_lotaccess.go BuyLot, layAutoRoads).

// VillageRoadRequest is the payload of settlement.road.plan.
type VillageRoadRequest struct {
	// To is the lot the road is drawn to ("x-y", a village.LotToken; a negative
	// coordinate is written "m3").
	To string `json:"to,omitempty"`
	// From is the lot the road starts beside: a road of the village or its hall.
	// Empty: the nearest one.
	From string `json:"from,omitempty"`
	// Class is the road class (content roads.yml); empty: the footpath.
	Class   string `json:"class,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// VillageRoadCancelRequest is the payload of settlement.road.cancel.
type VillageRoadCancelRequest struct {
	ID string `json:"id"`
}

// defaultRoadClass is the class every village can draw from the start.
const defaultRoadClass = "path"

// roadDraft is a road routed and priced, ready to show or to store.
type roadDraft struct {
	plan  application.RoadPlanRow
	cells []application.RoadCellRow
	open  []application.OpenLotRow
	view  village.RoadQuoteView
}

// RoadPlan handles settlement.road.plan. Without confirm it answers the quote
// and changes nothing; with it, the plan is stored.
func (h *VillageHandler) RoadPlan(ctx context.Context, meta envelope.Metadata, req VillageRoadRequest) (*presentation.Response, error) {
	lang := meta.Language
	var quote, planned *village.RoadQuoteView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if ok, err := h.mayDrawRoads(ctx, tx, s, p.ID); err != nil {
			return err
		} else if !ok {
			return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier))
		}
		confirmed := strings.TrimSpace(req.Confirm) == village.VillageBuildConfirm
		if confirmed {
			if err := tx.Citizens().LockLots(ctx, s.CityID); err != nil {
				return err
			}
		}
		toX, toY, _, ok := village.ParseLotTokenAny(req.To)
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrLand)
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
		draft, err := h.draftRoad(ctx, tx, w, pic, p, req, landroad.Lot{X: toX, Y: toY})
		if err != nil {
			return err
		}
		if !confirmed {
			quote = &draft.view
			return nil
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		draft.plan.ID = h.ids.NewID()
		draft.plan.CreatedAt = now
		for i := range draft.cells {
			draft.cells[i].PlanID = draft.plan.ID
		}
		for i := range draft.open {
			draft.open[i].PlanID = draft.plan.ID
		}
		if err := tx.Citizens().InsertPlan(ctx, draft.plan, draft.cells, draft.open); err != nil {
			return err
		}
		draft.view.PlanID = draft.plan.ID
		planned = &draft.view
		return h.appendBuildingEvent(ctx, tx, meta, s, "land_changed", map[string]any{
			"settlement_id": s.CityID, "kind": "road_planned", "plan_id": draft.plan.ID, "lots": len(draft.cells), "opened": len(draft.open),
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	switch {
	case planned != nil:
		return village.RoadPlanned(c, *planned), nil
	case quote != nil:
		return village.RoadQuote(c, *quote), nil
	}
	return h.Land(ctx, meta)
}

// roadClassState reads which road classes the village has the research for.
func (h *VillageHandler) roadClassOptions(ctx context.Context, tx application.Tx, s application.FoundedSettlement, snap *content.Snapshot,
) ([]village.RoadClassOption, map[string]bool, error) {
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	have := map[string]bool{}
	for _, o := range owned {
		have[o.Code] = true
	}
	var opts []village.RoadClassOption
	avail := map[string]bool{}
	for _, c := range snap.RoadClasses() {
		opt := village.RoadClassOption{Class: named(c.Code, h.roadClassName(c.Code)), Available: true,
			LotCost: landroad.Price(1, 0, h.autoRoadCost, h.citizen.CrossingLotCost, h.surfaceBPS(c.Code))}
		var needs []string
		if c.Requires != nil {
			needs = append(needs, c.Requires.Knowledge...)
		}
		needs = append(needs, c.PlannedKnowledge...)
		for _, k := range needs {
			if !have[k] {
				opt.Available = false
				opt.Missing = append(opt.Missing, named(k, k))
			}
		}
		avail[c.Code] = opt.Available
		opts = append(opts, opt)
	}
	return opts, avail, nil
}

// draftRoad routes, prices and shapes a road to the lot to.
func (h *VillageHandler) draftRoad(ctx context.Context, tx application.Tx, w *worldgen.World, pic *landPicture,
	p *application.Player, req VillageRoadRequest, to landroad.Lot,
) (*roadDraft, error) {
	s := pic.s
	snap := h.content.Current()
	planner, ok := snap.RoadPlanner()
	if !ok {
		return nil, refuseVillage(village.VillageNotAvailable, village.AddrLand)
	}
	classCode := strings.TrimSpace(req.Class)
	if classCode == "" {
		classCode = defaultRoadClass
	}
	cdef, ok := snap.RoadClass(classCode)
	if !ok {
		return nil, refuseVillage(village.VillageNotFound, village.AddrLand)
	}
	options, avail, err := h.roadClassOptions(ctx, tx, s, snap)
	if err != nil {
		return nil, err
	}
	if !avail[classCode] {
		return nil, refuseVillage(village.RoadClassLocked, village.AddrLand)
	}

	// what already carries a road of the village
	roadSet := map[landroad.Lot]bool{}
	anchors := map[landroad.Lot]bool{}
	for _, b := range pic.existing {
		if !b.Holds() {
			continue
		}
		switch b.TypeCode {
		case "road":
			roadSet[landroad.Lot{X: b.LotX, Y: b.LotY}] = true
			anchors[landroad.Lot{X: b.LotX, Y: b.LotY}] = true
		case "civic_hall":
			def := wsettleDef(snap, b)
			for _, q := range footprintOf(def, b.LotX, b.LotY) {
				anchors[landroad.Lot{X: q[0], Y: q[1]}] = true
			}
		}
	}
	for l := range pic.cells {
		anchors[l] = true
	}
	if len(anchors) == 0 {
		return nil, refuseVillage(village.RoadNoNetwork, village.AddrLand)
	}
	if anchors[to] {
		return nil, refuseVillage(village.RoadSame, village.AddrLand)
	}
	from, hasFrom := landroad.Lot{}, false
	if fx, fy, _, ok := village.ParseLotTokenAny(req.From); ok {
		from, hasFrom = landroad.Lot{X: fx, Y: fy}, true
		if !anchors[from] {
			return nil, refuseVillage(village.RoadNoNetwork, village.AddrLand)
		}
	}
	if !hasFrom {
		best, bestD := landroad.Lot{}, math.MaxInt
		for a := range anchors {
			d := (a.X-to.X)*(a.X-to.X) + (a.Y-to.Y)*(a.Y-to.Y)
			if d < bestD || (d == bestD && (a.Y < best.Y || (a.Y == best.Y && a.X < best.X))) {
				best, bestD = a, d
			}
		}
		from = best
	}

	ground := landroad.NewWorldGround(w, pic.frame, planner.TerrainBPS)
	foreign, err := h.foreignTiles(ctx, tx, w, s, ground, from, to, planner.SearchMarginM)
	if err != nil {
		return nil, err
	}
	lotForeign := func(l landroad.Lot) bool { return foreign[ground.Tile(l)] }
	if lotForeign(to) {
		return nil, refuseVillage(village.RoadForeign, village.AddrLand)
	}
	blocked := func(l landroad.Lot) bool {
		if _, isCell := pic.cells[l]; isCell || roadSet[l] {
			return false
		}
		k := [2]int{l.X, l.Y}
		if _, held := pic.held[k]; held {
			return true
		}
		if pic.inGrid(l.X, l.Y) {
			return pic.grid[l.Y][l.X].Occupied
		}
		if _, stands := pic.occ[k]; stands {
			return true
		}
		return lotForeign(l)
	}
	if blocked(to) {
		return nil, refuseVillage(village.RoadEndBlocked, village.AddrLand)
	}
	params := roads.DefaultParams()
	if planner.SearchMarginM > 0 {
		params.SearchMarginM = planner.SearchMarginM
	}
	if planner.MaxExpansions > 0 {
		params.MaxExpansions = planner.MaxExpansions
	}
	if planner.BridgeCostRatio > 0 {
		params.BridgeCostRatio = planner.BridgeCostRatio
	}
	if planner.StreamCrossingSteps > 0 {
		params.StreamCrossingSteps = planner.StreamCrossingSteps
	}
	path, _, err := landroad.Plan(landroad.Request{
		W: w, Src: worldgen.NewChunkTileSource(w, 256), Ground: ground,
		Class:   landroad.Class{Code: cdef.Code, MaxGradeBPS: cdef.MaxGradeBPS, BridgeMaxSpanM: cdef.BridgeMaxSpanM, Fords: cdef.Fords},
		Planner: params, HighMountainM: planner.HighMountainM, HighMountainBPS: planner.HighMountainBPS,
		From: from, To: to,
		TileBlocked: func(t worldgen.TileID) bool { return foreign[t] },
		Blocked:     blocked,
		Cheap: func(l landroad.Lot) bool {
			_, isCell := pic.cells[l]
			return isCell || roadSet[l]
		},
		MaxLots: h.citizen.RoadPlanMaxLots, StreamRun: h.citizen.MaxCrossing, CorridorRing: h.citizen.RoadCorridorRing,
	})
	if err != nil {
		var be *landroad.BridgeError
		switch {
		case stderrors.As(err, &be):
			return nil, refuseVillage(village.RoadNoBridge, village.AddrLand)
		case stderrors.Is(err, landroad.ErrWater):
			return nil, refuseVillage(village.RoadWater, village.AddrLand)
		case stderrors.Is(err, landroad.ErrNoRoute):
			return nil, refuseVillage(village.RoadNoRoute, village.AddrLand)
		case stderrors.Is(err, landroad.ErrTooLong):
			return nil, refuseVillage(village.RoadTooLong, village.AddrLand)
		case stderrors.Is(err, landroad.ErrInvalid):
			return nil, refuseVillage(village.RoadSame, village.AddrLand)
		}
		return nil, err
	}

	// the new cells: the steps that are not a road yet, each hanging from the
	// step before it when that one is a lot of a drawn road
	d := &roadDraft{}
	prev := from
	newSet := map[landroad.Lot]bool{}
	var crossings int
	for _, st := range path.Steps {
		l := st.Lot
		_, isCell := pic.cells[l]
		if isCell || roadSet[l] {
			prev = l
			continue
		}
		tile := ground.Tile(l)
		face, gx, gy := tile.Parts()
		row := application.RoadCellRow{
			SettlementID: s.CityID, X: l.X, Y: l.Y, Seq: len(d.cells), ElevationM: st.ElevationM,
			Tile: application.TileKey{Face: int(face), GX: gx, GY: gy},
		}
		if _, prevIsCell := pic.cells[prev]; prevIsCell || newSet[prev] {
			row.HasParent, row.ParentX, row.ParentY = true, prev.X, prev.Y
		}
		switch st.Water {
		case landroad.WaterStream:
			row.Water = application.RoadWaterStream
			crossings++
		case landroad.WaterRiver:
			row.Water = application.RoadWaterRiver
			crossings++
		}
		d.cells = append(d.cells, row)
		newSet[l] = true
		prev = l
	}
	if len(d.cells) == 0 {
		return nil, refuseVillage(village.RoadSame, village.AddrLand)
	}

	// the lots along the whole road network of drawn cells open for sale
	steepM := float64(h.citizen.RoadSteepSlopeM)
	allRoad := make([]landroad.Lot, 0, len(pic.cells)+len(newSet))
	for l := range pic.cells {
		allRoad = append(allRoad, l)
	}
	for l := range newSet {
		allRoad = append(allRoad, l)
	}
	frontage := landroad.Frontage(allRoad, h.citizen.RoadFrontageDepth, func(l landroad.Lot) bool {
		return pic.inGrid(l.X, l.Y) || lotForeign(l) || roadSet[l]
	})
	sampler := wsettle.NewLotSampler(w, pic.frame, s.WorldCellID)
	var usable, water, steep int
	for _, o := range frontage {
		if _, has := pic.open[o.Lot]; has {
			continue
		}
		t := sampler.At(o.Lot.X, o.Lot.Y)
		tile := ground.Tile(o.Lot)
		face, gx, gy := tile.Parts()
		row := application.OpenLotRow{
			SettlementID: s.CityID, X: o.Lot.X, Y: o.Lot.Y, ServesX: o.Serves.X, ServesY: o.Serves.Y, Dist: o.Dist,
			Buildable: true, HeightM: t.ElevationM, SlopeM: t.SlopeM, Biome: t.Biome, Water: t.Stream,
			Tags: append([]string(nil), t.Tags...), Tile: application.TileKey{Face: int(face), GX: gx, GY: gy},
		}
		switch {
		case !t.Buildable:
			row.Buildable, row.Reason = false, application.OpenLotWater
			water++
		case t.SlopeM > steepM:
			row.Buildable, row.Reason = false, application.OpenLotSteep
			row.Tags = append(row.Tags, "sloped_lot")
			steep++
		default:
			usable++
		}
		d.open = append(d.open, row)
	}
	if h.citizen.RoadOpenLotsMax > 0 && len(pic.open)+len(d.open) > h.citizen.RoadOpenLotsMax {
		return nil, refuseVillage(village.RoadOpenCap, village.AddrLand)
	}

	// the quote
	dry := len(d.cells) - crossings
	bps := h.surfaceBPS(classCode)
	lotCost := landroad.Price(1, 0, h.autoRoadCost, h.citizen.CrossingLotCost, bps)
	crossCost := landroad.Price(0, 1, h.autoRoadCost, h.citizen.CrossingLotCost, bps)
	lotM := ground.F.LotM
	d.plan = application.RoadPlanRow{
		SettlementID: s.CityID, DrawnBy: p.ID, Class: classCode, Surface: classCode,
		Lots: len(d.cells), CrossingLots: crossings, ClimbM: int(math.Round(path.ClimbM)), LengthM: int(math.Round(float64(len(d.cells)) * lotM)),
		FromX: from.X, FromY: from.Y, ToX: to.X, ToY: to.Y,
	}
	if d.plan.LengthM < 1 {
		d.plan.LengthM = 1
	}
	d.view = village.RoadQuoteView{
		SettlementName: s.Name, SettlementID: s.CityID,
		From: village.LotRef{X: from.X, Y: from.Y}, To: village.LotRef{X: to.X, Y: to.Y},
		Class: named(classCode, h.roadClassName(classCode)), Options: options,
		Lots: d.plan.Lots, Crossings: crossings, LengthM: d.plan.LengthM, ClimbM: d.plan.ClimbM, MaxGradeBPS: path.MaxGradeBPS,
		LotCost: lotCost, CrossingCost: crossCost, FullCost: landroad.Price(dry, crossings, h.autoRoadCost, h.citizen.CrossingLotCost, bps),
		Opens: len(d.open), Usable: usable, Water: water, Steep: steep,
	}
	for _, c := range d.cells {
		d.view.Path = append(d.view.Path, village.LotRef{X: c.X, Y: c.Y})
	}
	for _, o := range d.open {
		st := village.LandFree
		switch o.Reason {
		case application.OpenLotWater:
			st = village.LandWater
		case application.OpenLotSteep:
			st = village.LandSteep
		}
		d.view.OpenCells = append(d.view.OpenCells, village.LandCell{X: o.X, Y: o.Y, State: st})
	}
	return d, nil
}

// wsettleDef is the domain definition of a standing building, turned as it
// was placed; a type the content no longer declares is one lot.
func wsettleDef(snap *content.Snapshot, b application.SettlementBuildingInstance) settlementbuilding.Def {
	def := settlementbuilding.Def{FootprintW: 1, FootprintH: 1}
	if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
		def = d.Def()
		if b.Rotated {
			def = def.Rotate()
		}
	}
	return def
}

// foreignTiles is the set of tiles closed to this village's roads: the ground
// round another settlement's first grid, and the tiles another settlement's
// drawn roads run on. A road carries its settlement's claim with it (ADR 0044
// 5.5); until a settlement draws a road there, the open land is first come.
func (h *VillageHandler) foreignTiles(ctx context.Context, tx application.Tx, w *worldgen.World, s application.FoundedSettlement,
	ground *landroad.WorldGround, from, to landroad.Lot, searchMarginM int,
) (map[worldgen.TileID]bool, error) {
	out := map[worldgen.TileID]bool{}
	others, err := tx.Settlements().ExistingForWorld(ctx, s.WorldID)
	if err != nil {
		return nil, err
	}
	buf := int32(h.citizen.RoadForeignBufferTiles)
	if buf < 0 {
		buf = 0
	}
	for _, o := range others {
		if o.WorldCellID == s.WorldCellID || int(o.WorldCellID) >= len(w.Cells) || o.WorldCellID < 0 {
			continue
		}
		pt := w.Cells[o.WorldCellID].Point
		c := w.TileOfLatLon(pt.LatDeg, pt.LonDeg)
		for dx := -buf; dx <= buf; dx++ {
			for dy := -buf; dy <= buf; dy++ {
				out[w.Offset(c, dx, dy)] = true
			}
		}
	}
	// tiles other settlements' roads hold, inside the router's search box
	tf, tgx, tgy := ground.Tile(from).Parts()
	uf, ugx, ugy := ground.Tile(to).Parts()
	if tf == uf {
		margin := int32(searchMarginM/w.TileLengthM()) + 3
		keys, err := tx.Citizens().ForeignTiles(ctx, s.CityID, int(tf), min32(tgx, ugx)-margin, max32(tgx, ugx)+margin, min32(tgy, ugy)-margin, max32(tgy, ugy)+margin)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			out[worldgen.MakeTileID(int8(k.Face), k.GX, k.GY)] = true
		}
	}
	return out, nil
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// RoadCancel handles settlement.road.cancel: the holder who drew it takes a
// road back while nothing is laid on it and no lot along it is sold or built
// on. Its open lots close; the claim on its tiles ends with it.
func (h *VillageHandler) RoadCancel(ctx context.Context, meta envelope.Metadata, req VillageRoadCancelRequest) (*presentation.Response, error) {
	lang := meta.Language
	var done *village.RoadCancelledView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		if ok, err := h.mayDrawRoads(ctx, tx, s, p.ID); err != nil {
			return err
		} else if !ok {
			return application.ErrNotOfficeHolder.WithDetail("office", officeFor(s.Tier))
		}
		if err := tx.Citizens().LockLots(ctx, s.CityID); err != nil {
			return err
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
		id := strings.TrimSpace(req.ID)
		plan, ok := pic.plans[id]
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrLand)
		}
		for _, c := range pic.cells {
			if c.PlanID == id && c.Built() {
				return refuseVillage(village.RoadInUse, village.AddrLand)
			}
			// a road drawn from this one hangs on it
			if c.PlanID != id && c.HasParent {
				if parent, ok := pic.cells[landroad.Lot{X: c.ParentX, Y: c.ParentY}]; ok && parent.PlanID == id {
					return refuseVillage(village.RoadInUse, village.AddrLand)
				}
			}
		}
		for k, o := range pic.open {
			if o.PlanID != id {
				continue
			}
			if _, held := pic.held[[2]int{k.X, k.Y}]; held {
				return refuseVillage(village.RoadInUse, village.AddrLand)
			}
			if _, stands := pic.occ[[2]int{k.X, k.Y}]; stands {
				return refuseVillage(village.RoadInUse, village.AddrLand)
			}
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		if closed, err := tx.Citizens().CancelPlan(ctx, id, now); err != nil {
			return err
		} else if !closed {
			return refuseVillage(village.VillageNotFound, village.AddrLand)
		}
		// the lots the remaining roads still serve stay open, under the road
		// that serves them now
		remaining := make([]landroad.Lot, 0, len(pic.cells))
		cellPlan := map[landroad.Lot]string{}
		for l, c := range pic.cells {
			if c.PlanID != id {
				remaining = append(remaining, l)
				cellPlan[l] = c.PlanID
			}
		}
		var want []application.OpenLotRow
		for _, o := range landroad.Frontage(remaining, h.citizen.RoadFrontageDepth, func(l landroad.Lot) bool {
			return pic.inGrid(l.X, l.Y)
		}) {
			row, ok := pic.open[o.Lot]
			if !ok {
				continue // never opened: a cancel opens nothing
			}
			row.PlanID, row.ServesX, row.ServesY, row.Dist = cellPlan[o.Serves], o.Serves.X, o.Serves.Y, o.Dist
			want = append(want, row)
		}
		if err := tx.Citizens().SyncOpenLots(ctx, s.CityID, want); err != nil {
			return err
		}
		done = &village.RoadCancelledView{SettlementName: s.Name, PlanID: id, Lots: plan.Lots}
		return h.appendBuildingEvent(ctx, tx, meta, s, "land_changed", map[string]any{
			"settlement_id": s.CityID, "kind": "road_cancelled", "plan_id": id,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done != nil {
		return village.RoadCancelled(h.screen(meta, lang), *done), nil
	}
	return h.Land(ctx, meta)
}
