package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/haul"
	"github.com/mrjvadi/torncity/internal/domain/rail"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The railway (docs/adr/0075 7). A line is drawn between two stations over the world's tiles with the road router and a rail class
// (a gentler grade), is laid in sections that cost sleepers, rails and stone and builders' hours, and then trains run on it by
// schedule (village_train.go).
//
//   - WHO WORKS THERE. Builders lay the track (hours, paid by the treasury); a station has a stationmaster and loaders (a daily service);
//     a train has a draught team bought from the neutral city and a crew of the owner (the team's feed is the running cost).
//   - WHAT IT CONSUMES. Sleepers, rails and stone for every 30 m lot; wheat for the team on every leg; the station's firewood.
//   - WHAT IT PROVIDES. Goods moved between settlements: the owner's own on schedule, others' by booking at the line's rate a tonne-kilometre.
//   - WHAT BREAKS. A section worn below the broken mark stops the line; a train waits for track another holds, for feed and for a closed
//     station, and says which; a line past its train cap takes no more trains.

// RailEventActionType is the game action of a train's next act (arrival or retry).
const RailEventActionType = "rail_event"

// VillageRailRequest is the payload of settlement.rail; the arguments after the act are named in village/rail.go.
type VillageRailRequest struct {
	Action string `json:"action,omitempty"`
	Code   string `json:"code,omitempty"`
	N      string `json:"n,omitempty"`
	M      string `json:"m,omitempty"`
	K      string `json:"k,omitempty"`
}

// maxScheduleSteps is the longest schedule (docs/adr/0075 7.3).
const maxScheduleSteps = 12

func railClassOf(d content.RailClassDef, cfg application.MiningRules) rail.Class {
	lm := map[string]int64{}
	for k, v := range d.LotMaterials {
		lm[k] = int64(v)
	}
	return rail.Class{Code: d.Code, MaxConsistKg: int64(d.MaxConsistKG), WagonKg: int64(d.WagonKG), SpeedKmh: int64(d.SpeedKmh), MaxGradeBPS: int64(d.MaxGradeBPS),
		LotMaterials: lm, LotWorkHours: int64(d.LotWorkHours), LotMinutes: int64(d.LotMinutes), DecayBPSPerDay: int64(d.DecayBPSPerDay),
		BrokenBelowBPS: cfg.CartBrokenBPS, MaxTrains: d.MaxTrains, Teams: 1}
}

// Rail handles settlement.rail.
func (h *VillageHandler) Rail(ctx context.Context, meta envelope.Metadata, req VillageRailRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.RailView
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
		notice := ""
		var quote *village.RailQuoteLine
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, quote, err = h.railAct(ctx, tx, meta, snap, s, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.railView(ctx, tx, snap, s, p)
		view.Notice, view.Quote = notice, quote
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Rail(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) railAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act string, req VillageRailRequest,
) (string, *village.RailQuoteLine, error) {
	if !h.haulOpen() {
		return "", nil, refuseVillage(village.RailNotYet, village.AddrRail)
	}
	code, n, m, k := strings.TrimSpace(req.Code), strings.TrimSpace(req.N), strings.TrimSpace(req.M), strings.TrimSpace(req.K)
	need := charter.RailDraw
	if act == village.RailActionBook || act == village.RailActionCancel {
		need = charter.HaulOrder
	}
	if _, err := h.requireVillage(ctx, tx, s, p.ID, need); err != nil {
		return "", nil, err
	}
	switch act {
	case village.RailActionPlan, village.RailActionDraw:
		d, err := h.railDraft(ctx, tx, snap, s, code, n, m)
		if err != nil {
			return "", nil, err
		}
		if act == village.RailActionPlan {
			return village.RailNoticeQuote, d.quoteLine(snap), nil
		}
		return village.RailNoticeDrawn, nil, h.railDraw(ctx, tx, meta, s, p, d)
	case village.RailActionBuild:
		return village.RailNoticeBuilt, nil, h.railBuild(ctx, tx, meta, snap, s, p, code, n)
	case village.RailActionTrain:
		return village.RailNoticeTrain, nil, h.railTrain(ctx, tx, meta, snap, s, p, code, n, m)
	case village.RailActionStep:
		return village.RailNoticeStep, nil, h.railStep(ctx, tx, s, code, n, m, k)
	case village.RailActionClear:
		return village.RailNoticeCleared, nil, h.railClear(ctx, tx, s, code)
	case village.RailActionStart:
		return village.RailNoticeStarted, nil, h.railStart(ctx, tx, meta, snap, s, code)
	case village.RailActionHalt:
		return village.RailNoticeHalted, nil, h.railHalt(ctx, tx, s, code)
	case village.RailActionRate:
		return village.RailNoticeRate, nil, h.railRate(ctx, tx, s, code, n)
	case village.RailActionLoop:
		return village.RailNoticeLoop, nil, h.railLoop(ctx, tx, s, code, n)
	case village.RailActionBook:
		return village.RailNoticeBooked, nil, h.railBook(ctx, tx, meta, snap, s, p, code, n, m, k)
	case village.RailActionCancel:
		return village.RailNoticeCancel, nil, h.railCancel(ctx, tx, s, code)
	}
	return "", nil, refuseVillage(village.RailBadArgs, village.AddrRail)
}

// railDraft is a line planned and not yet stored.
type railDraft struct {
	class     content.RailClassDef
	fromStop  application.SettlementBuildingInstance
	toStop    application.SettlementBuildingInstance
	fromSet   application.FoundedSettlement
	toSet     application.FoundedSettlement
	segs      []application.RailSegment
	route     roads.Route
	lots      int64
	materials map[string]int64
	wage      int64
	minutes   int64
}

func (d *railDraft) quoteLine(snap *content.Snapshot) *village.RailQuoteLine {
	return &village.RailQuoteLine{Class: named(d.class.Code, d.class.Name), From: named(d.fromSet.Code, d.fromSet.Name), To: named(d.toSet.Code, d.toSet.Name), Sections: len(d.segs), Lots: d.lots,
		LengthM: int64(d.route.LengthM), MaxGradeBPS: int64(d.route.MaxGradeBPS), Materials: materialLinesOf(snap, d.materials), Wage: d.wage, Minutes: d.minutes}
}

// railStationOf reads a finished rail station and the settlement it stands in.
func (h *VillageHandler) railStationOf(ctx context.Context, tx application.Tx, id string) (application.SettlementBuildingInstance, application.FoundedSettlement, error) {
	b, err := tx.SettlementBuildings().Get(ctx, id)
	if err != nil || b == nil || b.TypeCode != "rail_station" || b.Status != "complete" {
		return application.SettlementBuildingInstance{}, application.FoundedSettlement{}, refuseVillage(village.RailNoStation, village.AddrRail)
	}
	set, err := tx.Settlements().ByID(ctx, b.SettlementID)
	return *b, set, err
}

// railDraft plans a line of a class between two stations: the route over the tiles, its sections and what laying it costs.
func (h *VillageHandler) railDraft(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, class, fromID, toID string) (*railDraft, error) {
	cd, ok := snap.RailClass(class)
	if !ok {
		return nil, refuseVillage(village.RailNoClass, village.AddrRail)
	}
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return nil, err
	}
	if cd.Requires != nil {
		var missing []string
		for _, k := range cd.Requires.Knowledge {
			if _, have := st.Owned[k]; !have {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			buildings, _ := tx.SettlementBuildings().List(ctx, s.CityID)
			stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
			if err != nil {
				return nil, err
			}
			pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
			if err != nil {
				return nil, err
			}
			return nil, needsRefusal(village.VillagePrerequisite, "rail", named(class, cd.Name), pc.knowledgeNeeds(missing), village.AddrRail)
		}
	}
	a, aSet, err := h.railStationOf(ctx, tx, fromID)
	if err != nil {
		return nil, err
	}
	if aSet.CityID != s.CityID {
		return nil, refuseVillage(village.RailNotOwner, village.AddrRail)
	}
	b, bSet, err := h.railStationOf(ctx, tx, toID)
	if err != nil {
		return nil, err
	}
	if a.ID == b.ID || aSet.CityID == bSet.CityID {
		return nil, refuseVillage(village.RailSameStation, village.AddrRail)
	}
	route, err := h.routeBetween(ctx, snap, aSet.WorldCellID, bSet.WorldCellID, roads.Class{Code: cd.Code, MaxGradeBPS: cd.MaxGradeBPS, BridgeMaxSpanM: cd.BridgeMaxSpanM, Fords: false})
	if err != nil {
		var nb *roads.NoBridgeError
		switch {
		case stderrors.As(err, &nb):
			return nil, refuseVillage(village.RailNoBridge, village.AddrRail)
		case stderrors.Is(err, roads.ErrNoRoute), stderrors.Is(err, roads.ErrInvalid):
			return nil, refuseVillage(village.RailNoRoute, village.AddrRail)
		}
		return nil, err
	}
	if err := h.passageCheck(ctx, tx, snap, aSet, bSet, route.Tiles, village.AddrRail); err != nil {
		return nil, err
	}
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	d := &railDraft{class: cd, fromStop: a, toStop: b, fromSet: aSet, toSet: bSet, route: route, materials: map[string]int64{}}
	segs, err := tileSegments(w, route.Tiles)
	if err != nil {
		return nil, err
	}
	d.segs = segs
	rc := railClassOf(cd, h.mining)
	for _, sg := range segs {
		d.lots += rail.Lots(sg.LengthM)
	}
	for it, per := range rc.LotMaterials {
		d.materials[it] = per * d.lots
	}
	d.wage = d.lots * rc.LotWorkHours * h.mining.RailWorkHourWage
	d.minutes = d.lots * rc.LotMinutes
	return d, nil
}

// tileSegments cuts a route over tiles into sections: one for every step between two tiles.
func tileSegments(w *worldgen.World, tiles []worldgen.TileID) ([]application.RailSegment, error) {
	if len(tiles) < 2 {
		return nil, roads.ErrNoRoute
	}
	src := worldgen.NewChunkTileSource(w, 256)
	straight, diag := int64(w.TileLengthM()), int64(w.TileDiagonalM())
	var out []application.RailSegment
	for i := 1; i < len(tiles); i++ {
		a, b := tiles[i-1], tiles[i]
		fa, ax, ay := a.Parts()
		fb, bx, by := b.Parts()
		length := straight
		if fa == fb && ax != bx && ay != by {
			length = diag
		}
		ta, err := src.TileAt(a)
		if err != nil {
			return nil, err
		}
		tb, err := src.TileAt(b)
		if err != nil {
			return nil, err
		}
		dh := int64(tb.Elevation) - int64(ta.Elevation)
		if dh < 0 {
			dh = -dh
		}
		out = append(out, application.RailSegment{Seq: i, Tile: int64(b), LengthM: length, GradeBPS: dh * 10000 / length})
	}
	return out, nil
}

// railDraw stores a line, unbuilt, with its two stations; the sections are laid by build.
func (h *VillageHandler) railDraw(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, d *railDraft) error {
	now := h.now()
	lineID := h.ids.NewID()
	segs := make([]application.RailSegment, len(d.segs))
	for i, sg := range d.segs {
		sg.ID, sg.LineID = h.ids.NewID(), lineID
		segs[i] = sg
	}
	stops := []application.RailStop{
		{ID: h.ids.NewID(), LineID: lineID, AfterSeq: 0, BuildingID: d.fromStop.ID, SettlementID: d.fromSet.CityID},
		{ID: h.ids.NewID(), LineID: lineID, AfterSeq: len(segs), BuildingID: d.toStop.ID, SettlementID: d.toSet.CityID},
	}
	name := d.fromSet.Name + " - " + d.toSet.Name
	if err := tx.Rail().AddLine(ctx, application.RailLine{ID: lineID, Name: name, OwnerKind: "settlement", OwnerID: s.CityID, Home: s.CityID, Class: d.class.Code,
		RatePerTonneKm: h.mining.DefaultRatePerTonneKm, SectionsTotal: len(segs), LengthM: d.route.LengthM, CreatedBy: p.ID, CreatedAt: now}, segs, stops); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "rail_line_drawn", s.CityID, map[string]any{"settlement_id": s.CityID, "line_id": lineID, "class": d.class.Code, "sections": len(segs), "length_m": d.route.LengthM})
}

// ownLine reads a line of the settlement's, locked.
func (h *VillageHandler) ownLine(ctx context.Context, tx application.Tx, s application.FoundedSettlement, id string) (*application.RailLine, error) {
	l, err := tx.Rail().Line(ctx, id, true)
	if stderrors.Is(err, application.ErrLineNotFound) {
		return nil, refuseVillage(village.RailNoLine, village.AddrRail)
	}
	if err != nil {
		return nil, err
	}
	if l.Home != s.CityID {
		return nil, refuseVillage(village.RailNotOwner, village.AddrRail)
	}
	return l, nil
}

// railBuild orders the next sections of a line laid: sleepers, rails and stone out of the stock, builders' hours out of the treasury.
func (h *VillageHandler) railBuild(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player, lineID, count string) error {
	l, err := h.ownLine(ctx, tx, s, lineID)
	if err != nil {
		return err
	}
	cd, ok := snap.RailClass(l.Class)
	if !ok {
		return refuseVillage(village.RailNoClass, village.AddrRail)
	}
	if open, err := tx.Mines().OpenWorks(ctx, application.WorkRailSections, l.ID); err != nil {
		return err
	} else if len(open) > 0 {
		return refuseVillage(village.RailWorksInHand, village.AddrRail)
	}
	segs, err := tx.Rail().Segments(ctx, l.ID)
	if err != nil {
		return err
	}
	var unbuilt []application.RailSegment
	for _, sg := range segs {
		if sg.BuiltAt == nil {
			unbuilt = append(unbuilt, sg)
		}
	}
	if len(unbuilt) == 0 {
		return refuseVillage(village.RailNothingToLay, village.AddrRail)
	}
	n := min(len(unbuilt), 10)
	if v, err := strconv.Atoi(count); err == nil && v > 0 {
		n = min(v, len(unbuilt))
	}
	rc := railClassOf(cd, h.mining)
	var lots int64
	for _, sg := range unbuilt[:n] {
		lots += rail.Lots(sg.LengthM)
	}
	materials := map[string]int64{}
	for it, per := range rc.LotMaterials {
		materials[it] = per * lots
	}
	wage := lots * rc.LotWorkHours * h.mining.RailWorkHourWage
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	now := h.now()
	workID := h.ids.NewID()
	txID, err := h.payWorks(ctx, tx, snap, s, worksPay{Materials: materials, Wage: wage, Reason: application.ReasonRailWorksWage, RefType: "build_works", RefID: workID, Back: village.AddrRail}, now)
	if err != nil {
		return err
	}
	due := now.Add(time.Duration(lots*rc.LotMinutes) * time.Minute)
	if err := h.orderWorksAs(ctx, tx, application.BuildWork{ID: workID, SettlementID: s.CityID, Kind: application.WorkRailSections, RefID: l.ID, Materials: materials, Wage: wage, WageTx: txID, OrderedBy: p.ID, DueAt: due,
		Param: map[string]any{"sections": n}}, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "rail_build_ordered", s.CityID, map[string]any{"settlement_id": s.CityID, "line_id": l.ID, "sections": n, "due_at": due.UTC().Format(time.RFC3339)})
}

// railTrain makes a train of wagons and a draught team on a line: the wagons' materials and hours and the team's price are paid at once.
func (h *VillageHandler) railTrain(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player, lineID, wagonsText, name string) error {
	l, err := h.ownLine(ctx, tx, s, lineID)
	if err != nil {
		return err
	}
	cd, ok := snap.RailClass(l.Class)
	if !ok {
		return refuseVillage(village.RailNoClass, village.AddrRail)
	}
	rc := railClassOf(cd, h.mining)
	wagons, err := strconv.Atoi(wagonsText)
	if err != nil || wagons < 1 {
		return refuseVillage(village.RailBadArgs, village.AddrRail)
	}
	if int64(wagons) > rc.Wagons() {
		return refuseVillage(village.RailTooManyWagons, village.AddrRail)
	}
	trains, err := tx.Rail().TrainsOn(ctx, l.ID)
	if err != nil {
		return err
	}
	if len(trains) >= rc.MaxTrains {
		return refuseVillage(village.RailTooManyTrains, village.AddrRail)
	}
	stops, err := tx.Rail().Stops(ctx, l.ID)
	if err != nil || len(stops) == 0 {
		return err
	}
	materials := map[string]int64{}
	for it, q := range cd.WagonMaterials {
		materials[it] = int64(q) * int64(wagons)
	}
	wage := int64(cd.WagonHours) * int64(wagons) * h.mining.RailWorkHourWage
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	now := h.now()
	trainID := h.ids.NewID()
	if _, err := h.payWorks(ctx, tx, snap, s, worksPay{Materials: materials, Wage: wage + cd.TeamPrice, Reason: application.ReasonTeamPurchase, RefType: "rail_trains", RefID: trainID, Back: village.AddrRail}, now); err != nil {
		return err
	}
	if name == "" {
		name = l.Name + " " + strconv.Itoa(len(trains)+1)
	}
	at := ""
	for _, st := range stops {
		if st.SettlementID == s.CityID {
			at = st.ID
		}
	}
	if err := tx.Rail().AddTrain(ctx, application.RailTrain{ID: trainID, LineID: l.ID, OwnerKind: "settlement", OwnerID: s.CityID, Name: name, Wagons: wagons, AtStop: at, CreatedAt: now}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "rail_train_made", s.CityID, map[string]any{"settlement_id": s.CityID, "line_id": l.ID, "train_id": trainID, "wagons": wagons})
}

// ownTrain reads a train of the settlement, locked, with its line.
func (h *VillageHandler) ownTrain(ctx context.Context, tx application.Tx, s application.FoundedSettlement, id string) (*application.RailTrain, error) {
	t, err := tx.Rail().Train(ctx, id, true)
	if stderrors.Is(err, application.ErrTrainNotFound) {
		return nil, refuseVillage(village.RailNoTrain, village.AddrRail)
	}
	if err != nil {
		return nil, err
	}
	if t.OwnerID != s.CityID {
		return nil, refuseVillage(village.RailNotOwner, village.AddrRail)
	}
	return t, nil
}

// railStep adds a stop to the end of a train's schedule: n is the stop's id, m the operation, k the item (item:floor:wait).
func (h *VillageHandler) railStep(ctx context.Context, tx application.Tx, s application.FoundedSettlement, trainID, stopID, op, spec string) error {
	t, err := h.ownTrain(ctx, tx, s, trainID)
	if err != nil {
		return err
	}
	if t.Status != application.TrainIdle {
		return refuseVillage(village.RailRunning, village.AddrRail)
	}
	if op != application.StepLoad && op != application.StepUnload && op != application.StepPass {
		return refuseVillage(village.RailBadSchedule, village.AddrRail)
	}
	stops, err := tx.Rail().Stops(ctx, t.LineID)
	if err != nil {
		return err
	}
	found := false
	for _, st := range stops {
		found = found || st.ID == stopID
	}
	if !found {
		return refuseVillage(village.RailNoStop, village.AddrRail)
	}
	steps, err := tx.Rail().Schedule(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(steps) >= maxScheduleSteps {
		return refuseVillage(village.RailScheduleLong, village.AddrRail)
	}
	if len(steps) > 0 && steps[len(steps)-1].StopID == stopID {
		return refuseVillage(village.RailBadSchedule, village.AddrRail)
	}
	step := application.RailStep{TrainID: t.ID, StopID: stopID, Op: op}
	if spec != "" {
		parts := strings.Split(spec, ":")
		step.Item = parts[0]
		if len(parts) > 1 {
			if v, err := strconv.ParseInt(parts[1], 10, 64); err == nil && v >= 0 {
				step.StockFloor = v
			}
		}
		if len(parts) > 2 {
			if v, err := strconv.Atoi(parts[2]); err == nil && v >= 0 {
				step.WaitMinutes = v
			}
		}
	}
	if op == application.StepLoad && step.Item == "" {
		return refuseVillage(village.RailBadSchedule, village.AddrRail)
	}
	return tx.Rail().SetSchedule(ctx, t.ID, append(steps, step))
}

func (h *VillageHandler) railClear(ctx context.Context, tx application.Tx, s application.FoundedSettlement, trainID string) error {
	t, err := h.ownTrain(ctx, tx, s, trainID)
	if err != nil {
		return err
	}
	if t.Status != application.TrainIdle {
		return refuseVillage(village.RailRunning, village.AddrRail)
	}
	return tx.Rail().SetSchedule(ctx, t.ID, nil)
}

func (h *VillageHandler) railRate(ctx context.Context, tx application.Tx, s application.FoundedSettlement, lineID, rate string) error {
	l, err := h.ownLine(ctx, tx, s, lineID)
	if err != nil {
		return err
	}
	v, err := strconv.ParseInt(rate, 10, 64)
	if err != nil || v < 0 || v > 1_000_000 {
		return refuseVillage(village.RailNoRate, village.AddrRail)
	}
	return tx.Rail().SetRate(ctx, l.ID, v)
}

// railLoop makes a section of the owner's line a passing loop (or not): two trains may stand on a loop.
func (h *VillageHandler) railLoop(ctx context.Context, tx application.Tx, s application.FoundedSettlement, segmentID, on string) error {
	var lineID string
	lines, err := tx.Rail().LinesOf(ctx, s.CityID)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if l.Home != s.CityID {
			continue
		}
		segs, err := tx.Rail().Segments(ctx, l.ID)
		if err != nil {
			return err
		}
		for _, sg := range segs {
			if sg.ID == segmentID {
				lineID = l.ID
			}
		}
	}
	if lineID == "" {
		return refuseVillage(village.RailNoLine, village.AddrRail)
	}
	trains, err := tx.Rail().TrainsOn(ctx, lineID)
	if err != nil {
		return err
	}
	for _, t := range trains {
		if t.Status != application.TrainIdle {
			return refuseVillage(village.RailRunning, village.AddrRail)
		}
	}
	return tx.Rail().SetLoop(ctx, segmentID, on == "1")
}

// segmentRange is the sections a train crosses between two stops, in seq order.
func segmentRange(segs []application.RailSegment, fromAfter, toAfter int) []application.RailSegment {
	lo, hi := min(fromAfter, toAfter), max(fromAfter, toAfter)
	var out []application.RailSegment
	for _, sg := range segs {
		if sg.Seq > lo && sg.Seq <= hi {
			out = append(out, sg)
		}
	}
	return out
}

func domainSegments(segs []application.RailSegment) []rail.Segment {
	out := make([]rail.Segment, len(segs))
	for i, sg := range segs {
		out[i] = rail.Segment{ID: sg.ID, LengthM: sg.LengthM, ConditionBPS: sg.ConditionBPS, Loop: sg.Loop, Built: sg.BuiltAt != nil}
	}
	return out
}

// railBook books a consignment on a line: goods of the settlement's own store go to a station of the line; the fee is the line's rate a
// tonne-kilometre over the distance, paid to the owner now (refunded if the booking is cancelled before the goods are loaded).
func (h *VillageHandler) railBook(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
	lineID, qtyText, item, toStopID string,
) error {
	qty, err := strconv.ParseInt(qtyText, 10, 64)
	if err != nil || qty <= 0 || item == "" {
		return refuseVillage(village.RailBadArgs, village.AddrRail)
	}
	l, err := tx.Rail().Line(ctx, lineID, false)
	if stderrors.Is(err, application.ErrLineNotFound) {
		return refuseVillage(village.RailNoLine, village.AddrRail)
	}
	if err != nil {
		return err
	}
	if l.Status != application.LineOpen {
		return refuseVillage(village.RailNotOpen, village.AddrRail)
	}
	stops, err := tx.Rail().Stops(ctx, l.ID)
	if err != nil {
		return err
	}
	var from, to *application.RailStop
	for i := range stops {
		if stops[i].SettlementID == s.CityID && from == nil {
			from = &stops[i]
		}
		if stops[i].ID == toStopID {
			to = &stops[i]
		}
	}
	if from == nil || to == nil || from.ID == to.ID {
		return refuseVillage(village.RailNoStop, village.AddrRail)
	}
	segs, err := tx.Rail().Segments(ctx, l.ID)
	if err != nil {
		return err
	}
	dist := rail.Length(domainSegments(segmentRange(segs, from.AfterSeq, to.AfterSeq)))
	kg := qty * itemWeightG(snap, item) / 1000
	// some train of the line must be able to take it: it stops at both stations and carries that much
	trains, err := tx.Rail().TrainsOn(ctx, l.ID)
	if err != nil {
		return err
	}
	cd, _ := snap.RailClass(l.Class)
	rc := railClassOf(cd, h.mining)
	served := false
	for _, t := range trains {
		steps, err := tx.Rail().Schedule(ctx, t.ID)
		if err != nil {
			return err
		}
		hasFrom, hasTo := false, false
		for _, st := range steps {
			hasFrom = hasFrom || st.StopID == from.ID
			hasTo = hasTo || st.StopID == to.ID
		}
		if hasFrom && hasTo && rc.CapacityKg(int64(t.Wagons)) >= kg {
			served = true
		}
	}
	if !served {
		return refuseVillage(village.RailNoCapacity, village.AddrRail)
	}
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return err
	}
	var have int64
	for _, st := range stacks {
		if st.Item == item {
			have += st.Qty
		}
	}
	if have < qty {
		return refuseVillage(village.RailBookStock, village.AddrRail)
	}
	fee := haul.FreightFee(l.RatePerTonneKm, kg, dist)
	now := h.now()
	orderID := h.ids.NewID()
	feeTx := ""
	if fee > 0 && l.Home != s.CityID {
		bal, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if bal < fee {
			return refuseVillage(village.RailNoMoney, village.AddrRail)
		}
		payer, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		owner, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, l.Home)
		if err != nil {
			return err
		}
		feeTx = h.ids.NewID()
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: feeTx, Reason: application.ReasonFreightFee, CreatedAt: now, ReferenceType: "haul_orders", ReferenceID: orderID,
			Entries: []application.LedgerEntry{{AccountID: payer.ID, Amount: moneyOf(-fee)}, {AccountID: owner.ID, Amount: moneyOf(fee)}}}); err != nil {
			return err
		}
	}
	if err := tx.Haul().AddOrder(ctx, application.HaulOrder{ID: orderID, From: s.CityID, To: to.SettlementID, OrderedBy: p.ID, PayerKind: "settlement", PayerID: s.CityID, Item: item, Qty: qty, Mode: "rail",
		LineID: l.ID, FromStop: from.ID, ToStop: to.ID, Fee: fee, FeeTx: feeTx, Status: application.HaulOpen, CreatedAt: now}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "rail_booked", s.CityID, map[string]any{"settlement_id": s.CityID, "line_id": l.ID, "order_id": orderID, "item": item, "qty": qty, "fee": fee})
}

// railCancel cancels a booking that is still open (nothing loaded) and gives the fee back.
func (h *VillageHandler) railCancel(ctx context.Context, tx application.Tx, s application.FoundedSettlement, orderID string) error {
	o, err := tx.Haul().Order(ctx, orderID, true)
	if stderrors.Is(err, application.ErrHaulOrderNotFound) || (err == nil && o.From != s.CityID) {
		return refuseVillage(village.RailNoBooking, village.AddrRail)
	}
	if err != nil {
		return err
	}
	now := h.now()
	moved, err := tx.Haul().SetOrderStatus(ctx, o.ID, application.HaulOpen, application.HaulCancelled, now)
	if err != nil {
		return err
	}
	if !moved {
		return refuseVillage(village.RailNoBooking, village.AddrRail)
	}
	if o.Fee > 0 && o.FeeTx != "" {
		l, err := tx.Rail().Line(ctx, o.LineID, false)
		if err != nil {
			return err
		}
		payer, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		owner, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, l.Home)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: application.ReasonFreightFee, CreatedAt: now, ReferenceType: "haul_orders", ReferenceID: o.ID,
			Entries: []application.LedgerEntry{{AccountID: owner.ID, Amount: moneyOf(-o.Fee)}, {AccountID: payer.ID, Amount: moneyOf(o.Fee)}}}); err != nil {
			return err
		}
	}
	return nil
}

// railView builds the rail screen.
func (h *VillageHandler) railView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) (village.RailView, error) {
	v := village.RailView{Village: s.Name, Open: h.haulOpen(), MayDraw: hasPermission(ctx, tx, s, p.ID, charter.RailDraw), MayOrder: hasPermission(ctx, tx, s, p.ID, charter.HaulOrder)}
	if !h.mining.Enabled() {
		return v, nil
	}
	now := h.now()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	for _, b := range buildings {
		if b.TypeCode == "rail_station" && b.Status == "complete" {
			open, _ := h.stationOpen(ctx, tx, snap, s, b.ID)
			v.Stations = append(v.Stations, village.RailStopLine{ID: b.ID, Building: named(b.ID, "rail_station"), Settlement: named(s.Code, s.Name), Open: open})
		}
	}
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return v, err
	}
	for _, cd := range snap.RailClasses() {
		line := village.RailClassLine{Class: named(cd.Code, cd.Name), SpeedKmh: int64(cd.SpeedKmh), ConsistKg: int64(cd.MaxConsistKG), WagonKg: int64(cd.WagonKG), MaxGradeBPS: int64(cd.MaxGradeBPS),
			LotCost: materialLinesOf(snap, intQty(cd.LotMaterials)), LotHours: int64(cd.LotWorkHours), LotMinutes: int64(cd.LotMinutes), TeamPrice: cd.TeamPrice, WagonCost: materialLinesOf(snap, intQty(cd.WagonMaterials))}
		if cd.Requires != nil {
			for _, k := range cd.Requires.Knowledge {
				if _, have := st.Owned[k]; !have {
					line.Missing = append(line.Missing, named(k, k))
				}
			}
		}
		line.CanLay = len(line.Missing) == 0
		v.Classes = append(v.Classes, line)
	}
	settlements := map[string]presentation.Named{}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	for _, f := range founded {
		settlements[f.CityID] = named(f.Code, f.Name)
	}
	lines, err := tx.Rail().LinesOf(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	stopLine := map[string]village.RailStopLine{}
	for _, l := range lines {
		cd, _ := snap.RailClass(l.Class)
		segs, err := tx.Rail().Segments(ctx, l.ID)
		if err != nil {
			return v, err
		}
		stops, err := tx.Rail().Stops(ctx, l.ID)
		if err != nil {
			return v, err
		}
		ll := village.RailLineLine{ID: l.ID, Name: l.Name, Class: named(cd.Code, cd.Name), Status: l.Status, Sections: l.SectionsTotal, Built: l.SectionsBuilt, LengthM: int64(l.LengthM), RatePerTonneKm: l.RatePerTonneKm,
			Mine: l.Home == s.CityID, WorstConditionBPS: 10000}
		for _, sg := range segs {
			if sg.BuiltAt != nil {
				ll.WorstConditionBPS = min(ll.WorstConditionBPS, sg.ConditionBPS)
			}
			if sg.Loop {
				ll.Loops = append(ll.Loops, sg.Seq)
			}
		}
		for _, sp := range stops {
			open := false
			if sp.SettlementID == s.CityID {
				open, _ = h.stationOpen(ctx, tx, snap, s, sp.BuildingID)
			}
			line := village.RailStopLine{ID: sp.ID, Building: named(sp.BuildingID, "rail_station"), Settlement: settlements[sp.SettlementID], AfterSeq: sp.AfterSeq, Open: open}
			ll.Stops = append(ll.Stops, line)
			stopLine[sp.ID] = line
		}
		if open, err := tx.Mines().OpenWorks(ctx, application.WorkRailSections, l.ID); err != nil {
			return v, err
		} else if len(open) > 0 {
			ll.Laying = true
			due := open[0].DueAt
			ll.DueAt = &due
		}
		trains, err := tx.Rail().TrainsOn(ctx, l.ID)
		if err != nil {
			return v, err
		}
		ll.Trains = len(trains)
		v.Lines = append(v.Lines, ll)
		rc := railClassOf(cd, h.mining)
		for _, t := range trains {
			tl := village.RailTrainLine{ID: t.ID, Line: l.ID, Name: t.Name, Status: t.Status, Wagons: t.Wagons, CapacityKg: rc.CapacityKg(int64(t.Wagons)), Heading: t.Heading, Pos: t.SchedPos,
				NextAt: t.NextEventAt, Reason: t.WaitReason, Mine: t.OwnerID == s.CityID, Legs: t.LegNo}
			if sp, ok := stopLine[t.AtStop]; ok {
				tl.At = sp.Settlement
			}
			tl.Stuck = t.StuckNotified
			steps, err := tx.Rail().Schedule(ctx, t.ID)
			if err != nil {
				return v, err
			}
			for _, stp := range steps {
				tl.Steps = append(tl.Steps, village.RailStepLine{Seq: stp.Seq, Stop: stopLine[stp.StopID], Op: stp.Op, Item: named(stp.Item, snapItemName(snap, stp.Item)), StockFloor: stp.StockFloor, WaitMinutes: stp.WaitMinutes})
			}
			loads, err := tx.Haul().LoadsOf(ctx, t.ID)
			if err != nil {
				return v, err
			}
			for _, ld := range loads {
				tl.Cargo = append(tl.Cargo, village.RailCargoLine{Item: named(ld.Item, snapItemName(snap, ld.Item)), Qty: ld.Qty, To: settlements[ld.To]})
			}
			v.Trains = append(v.Trains, tl)
		}
	}
	orders, err := tx.Haul().OrdersOf(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, o := range orders {
		if o.Mode != "rail" {
			continue
		}
		v.Bookings = append(v.Bookings, village.RailBookingLine{ID: o.ID, Line: o.LineID, Item: named(o.Item, snapItemName(snap, o.Item)), Qty: o.Qty, From: settlements[o.From], To: settlements[o.To], Fee: o.Fee, Status: o.Status})
	}
	sort.SliceStable(v.Lines, func(i, j int) bool { return v.Lines[i].ID < v.Lines[j].ID })
	_ = now
	return v, nil
}
