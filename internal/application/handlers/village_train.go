package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/rail"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The trains (docs/adr/0075 7.3, 7.4). A train does not move on a tick: when it leaves a stop it works out how long the next stretch takes
// and its arrival is a durable game action; at the stop it unloads and loads, and either leaves again or waits (and says why).
//
//   - A leg is one departure and its arrival; the train's leg number is the fence: an event for any other leg, or an arrival that finds the
//     train waiting, changes nothing twice.
//   - The track between two stops is taken as a whole (all sections or none, in id order) and given back at the stop, so two trains never
//     stand on one section, and a passing loop holds two.
//   - A train waits for the track (blocked), for the team's wheat (no_feed), for the station to open (station_closed) and for broken
//     track (track_broken); a train that waits longer than mining.stuck_hours raises rail.stuck once.

// railEventPayload is what a train's event carries: the train and the leg it is for.
type railEventPayload struct {
	ID           string `json:"id"`
	SettlementID string `json:"settlement_id"`
	Leg          int64  `json:"leg"`
}

// scheduleRailEvent puts a train's next act on the game clock.
func (h *VillageHandler) scheduleRailEvent(ctx context.Context, tx application.Tx, t application.RailTrain, at, now time.Time) (string, error) {
	payload, err := json.Marshal(railEventPayload{ID: t.ID, SettlementID: t.OwnerID, Leg: t.LegNo})
	if err != nil {
		return "", err
	}
	id := h.ids.NewID()
	return id, tx.GameActions().Schedule(ctx, application.GameAction{ID: id, ActionType: RailEventActionType, ActorType: "system", ReferenceType: "rail_trains", ReferenceID: t.ID,
		Payload: payload, StartedAt: now, FinishAt: at})
}

// stationOpen says whether a station was open today: its stationmaster and loaders paid and its stove fed. With the daily services off
// every station is open.
func (h *VillageHandler) stationOpen(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, buildingID string) (bool, error) {
	if !h.service.enabled() {
		return true, nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return false, err
	}
	day, err := h.SettleServiceDay(ctx, tx, snap, s, buildings)
	if err != nil || day == nil {
		return false, err
	}
	return day.HeldPost(buildingID), nil
}

// railStart sets a train with a schedule going: it serves the stop it stands at and leaves for the next.
func (h *VillageHandler) railStart(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, trainID string) error {
	t, err := h.ownTrain(ctx, tx, s, trainID)
	if err != nil {
		return err
	}
	if t.Status != application.TrainIdle {
		return refuseVillage(village.RailRunning, village.AddrRail)
	}
	line, err := tx.Rail().Line(ctx, t.LineID, false)
	if err != nil {
		return err
	}
	if line.Status != application.LineOpen {
		return refuseVillage(village.RailNotOpen, village.AddrRail)
	}
	steps, err := tx.Rail().Schedule(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(steps) < 2 || steps[0].StopID == steps[len(steps)-1].StopID {
		return refuseVillage(village.RailBadSchedule, village.AddrRail)
	}
	t.AtStop, t.SchedPos, t.Served, t.Status, t.WaitReason, t.WaitingSince, t.StuckNotified = steps[0].StopID, 0, false, application.TrainWaiting, "", nil, false
	if err := tx.Rail().SetTrain(ctx, *t); err != nil {
		return err
	}
	return h.trainAct(ctx, tx, meta, snap, t.ID, t.LegNo, h.now())
}

// railHalt stops an idle-able train: it must stand at a stop, empty.
func (h *VillageHandler) railHalt(ctx context.Context, tx application.Tx, s application.FoundedSettlement, trainID string) error {
	t, err := h.ownTrain(ctx, tx, s, trainID)
	if err != nil {
		return err
	}
	if t.Status != application.TrainWaiting {
		return refuseVillage(village.RailRunning, village.AddrRail)
	}
	loads, err := tx.Haul().LoadsOf(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(loads) > 0 {
		return refuseVillage(village.RailRunning, village.AddrRail)
	}
	t.Status, t.WaitReason, t.NextEventAt, t.WaitingSince, t.StuckNotified = application.TrainIdle, "", nil, nil, false
	t.LegNo++ // an event of the leg it waited on finds another leg and does nothing
	return tx.Rail().SetTrain(ctx, *t)
}

// RailEvent handles rail.event from the SCHEDULER: a train arriving, or trying again.
func (h *VillageHandler) RailEvent(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, err
	}
	var in railEventPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, err
		}
	}
	if in.ID == "" {
		in.ID = req.ReferenceID
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return h.trainAct(ctx, tx, meta, snap, in.ID, in.Leg, h.now())
	})
}

// trainAct is one act of a train for the leg its event names: arrive and serve the stop, then try to leave.
func (h *VillageHandler) trainAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, trainID string, leg int64, now time.Time) error {
	t, err := tx.Rail().Train(ctx, trainID, true)
	if stderrors.Is(err, application.ErrTrainNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !rail.Apply(t.LegNo, leg) || t.Status == application.TrainIdle {
		return nil
	}
	if t.NextEventAt != nil && t.Status == application.TrainRunning && now.Before(*t.NextEventAt) {
		return nil
	}
	line, err := tx.Rail().Line(ctx, t.LineID, false)
	if err != nil {
		return err
	}
	cd, ok := snap.RailClass(line.Class)
	if !ok {
		return nil
	}
	rc := railClassOf(cd, h.mining)
	segs, err := tx.Rail().Segments(ctx, line.ID)
	if err != nil {
		return err
	}
	stops, err := tx.Rail().Stops(ctx, line.ID)
	if err != nil {
		return err
	}
	stopByID := map[string]application.RailStop{}
	for _, st := range stops {
		stopByID[st.ID] = st
	}
	steps, err := tx.Rail().Schedule(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(steps) < 2 {
		return nil
	}
	if t.Status == application.TrainRunning {
		// the train arrives at the stop it was heading for
		if err := tx.Rail().Release(ctx, t.ID); err != nil {
			return err
		}
		t.AtStop, t.Served, t.Status = steps[t.SchedPos%len(steps)].StopID, false, application.TrainWaiting
	}
	cur := stopByID[t.AtStop]
	curSet, err := tx.Settlements().ByID(ctx, cur.SettlementID)
	if err != nil {
		return err
	}
	// the stop is served once: unloading, the bookings, the owner's own loading
	if !t.Served {
		open, err := h.stationOpen(ctx, tx, snap, curSet, cur.BuildingID)
		if err != nil {
			return err
		}
		if !open {
			return h.trainWait(ctx, tx, meta, t, village.RailWaitClosed, now)
		}
		if err := h.serveStop(ctx, tx, meta, snap, *t, rc, cur, steps, stopByID, now); err != nil {
			return err
		}
		t.Served = true
		t.SchedPos = (t.SchedPos + 1) % len(steps)
	}
	next := stopByID[steps[t.SchedPos].StopID]
	if next.ID == cur.ID {
		t.SchedPos = (t.SchedPos + 1) % len(steps)
		next = stopByID[steps[t.SchedPos].StopID]
	}
	route := segmentRange(segs, cur.AfterSeq, next.AfterSeq)
	// worn track slows the train and broken track stops it: the wear of the days since the last pass is laid on first
	for i := range route {
		if err := h.wearSection(ctx, tx, rc, &route[i], now, curSet.Zone()); err != nil {
			return err
		}
	}
	if _, berr := rail.Block(domainSegments(route), rc.BrokenBelowBPS); berr != nil {
		return h.trainWait(ctx, tx, meta, t, village.RailWaitBroken, now)
	}
	// the team's feed comes out of the stock of the stop it leaves; it is looked for before the track is taken and eaten after
	if h.mining.TeamFeedWheat > 0 {
		if err := tx.Items().LockOrg(ctx, application.SettlementOrg(curSet.CityID)); err != nil {
			return err
		}
		stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(curSet.CityID), application.HoldWarehouse)
		if err != nil {
			return err
		}
		var wheat int64
		for _, st := range stacks {
			if st.Item == "wheat" {
				wheat += st.Qty
			}
		}
		if wheat < h.mining.TeamFeedWheat {
			return h.trainWait(ctx, tx, meta, t, village.RailWaitNoFeed, now)
		}
	}
	ids := make([]string, len(route))
	for i, sg := range route {
		ids[i] = sg.ID
	}
	ok2, _, err := tx.Rail().Reserve(ctx, t.ID, t.LegNo+1, ids, now)
	if err != nil {
		return err
	}
	if !ok2 {
		return h.trainWait(ctx, tx, meta, t, village.RailWaitBlocked, now)
	}
	if h.mining.TeamFeedWheat > 0 {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: "wheat", Qty: h.mining.TeamFeedWheat, FromOrg: application.SettlementOrg(curSet.CityID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemTeamFeed, ReferenceType: "rail_trains", ReferenceID: t.ID, At: now}); err != nil {
			return err
		}
	}
	wait := time.Duration(steps[(t.SchedPos+len(steps)-1)%len(steps)].WaitMinutes) * time.Minute
	arrive := now.Add(wait + rc.Duration(domainSegments(route), h.mining.RailWornBPS))
	t.LegNo++
	t.Status, t.Served, t.WaitReason, t.WaitingSince, t.StuckNotified = application.TrainRunning, false, "", nil, false
	t.NextEventAt = &arrive
	action, err := h.scheduleRailEvent(ctx, tx, *t, arrive, now)
	if err != nil {
		return err
	}
	t.GameActionID = action
	if err := tx.Rail().SetTrain(ctx, *t); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "rail_train_left", curSet.CityID, map[string]any{"settlement_id": curSet.CityID, "train_id": t.ID, "line_id": line.ID, "leg": t.LegNo, "arrives_at": arrive.UTC().Format(time.RFC3339)})
}

// trainWait leaves a train waiting at a stop with the reason, and tries again soon; the first time it has waited too long it says so once.
func (h *VillageHandler) trainWait(ctx context.Context, tx application.Tx, meta envelope.Metadata, t *application.RailTrain, reason string, now time.Time) error {
	t.Status, t.WaitReason = application.TrainWaiting, reason
	if t.WaitingSince == nil {
		since := now
		t.WaitingSince = &since
	}
	retry := now.Add(time.Duration(max(h.mining.RailRetryMinutes, 1)) * time.Minute)
	t.NextEventAt = &retry
	if !t.StuckNotified && now.Sub(*t.WaitingSince) >= time.Duration(h.mining.StuckHours)*time.Hour {
		t.StuckNotified = true
		if err := appendVillageEvent(ctx, tx, meta, "rail_stuck", t.OwnerID, map[string]any{"settlement_id": t.OwnerID, "train_id": t.ID, "reason": reason}); err != nil {
			return err
		}
	}
	action, err := h.scheduleRailEvent(ctx, tx, *t, retry, now)
	if err != nil {
		return err
	}
	t.GameActionID = action
	return tx.Rail().SetTrain(ctx, *t)
}

// wearSection lays on a section the wear of the days since it was last worn to.
func (h *VillageHandler) wearSection(ctx context.Context, tx application.Tx, rc rail.Class, sg *application.RailSegment, now time.Time, zone time.Duration) error {
	today := mineDay(now, zone)
	if sg.WearDay == 0 {
		sg.WearDay = today
		return tx.Rail().SetSegmentCondition(ctx, sg.ID, sg.ConditionBPS, today)
	}
	if today <= sg.WearDay {
		return nil
	}
	days := min(today-sg.WearDay, 365)
	sg.ConditionBPS = max(sg.ConditionBPS-days*rc.DecayBPSPerDay, 0)
	sg.WearDay = today
	return tx.Rail().SetSegmentCondition(ctx, sg.ID, sg.ConditionBPS, today)
}

// serveStop is the work at a stop: the goods bound for this settlement come off, the bookings waiting here go on, and a stop with a
// loading order loads the owner's own goods above the floor.
func (h *VillageHandler) serveStop(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, t application.RailTrain, rc rail.Class, cur application.RailStop,
	steps []application.RailStep, stops map[string]application.RailStop, now time.Time,
) error {
	org := application.SettlementOrg(cur.SettlementID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return err
	}
	loads, err := tx.Haul().LoadsOf(ctx, t.ID)
	if err != nil {
		return err
	}
	var carried int64
	for _, l := range loads {
		if l.To == cur.SettlementID {
			l.DueAt = now
			if err := h.deliverRailLoad(ctx, tx, meta, l, now); err != nil {
				return err
			}
			continue
		}
		carried += l.Kg
	}
	capacity := rc.CapacityKg(int64(t.Wagons))
	// bookings waiting here, for a settlement the train's schedule reaches
	orders, err := tx.Haul().OpenOrdersAt(ctx, t.LineID, cur.ID)
	if err != nil {
		return err
	}
	reaches := map[string]bool{}
	for _, st := range steps {
		reaches[stops[st.StopID].SettlementID] = true
	}
	for _, o := range orders {
		if !reaches[o.To] {
			continue
		}
		kg := o.Qty * itemWeightG(snap, o.Item) / 1000
		if carried+kg > capacity {
			continue
		}
		if moved, err := tx.Haul().SetOrderStatus(ctx, o.ID, application.HaulOpen, application.HaulMoving, now); err != nil {
			return err
		} else if !moved {
			continue
		}
		if err := h.loadOnTrain(ctx, tx, t, cur.SettlementID, o.To, o.Item, o.Qty, kg, o.ID, now); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				// the goods are not in the store: the booking waits
				if _, rerr := tx.Haul().SetOrderStatus(ctx, o.ID, application.HaulMoving, application.HaulOpen, now); rerr != nil {
					return rerr
				}
				continue
			}
			return err
		}
		carried += kg
	}
	// the owner's own loading order
	var step *application.RailStep
	for i := range steps {
		if steps[i].StopID == cur.ID && steps[i].Op == application.StepLoad && steps[i].Item != "" {
			step = &steps[i]
		}
	}
	if step != nil && t.OwnerID == cur.SettlementID {
		dest := ""
		for i := 1; i <= len(steps); i++ {
			st := steps[(step.Seq+i)%len(steps)]
			if st.Op == application.StepUnload && stops[st.StopID].SettlementID != cur.SettlementID {
				dest = stops[st.StopID].SettlementID
				break
			}
		}
		if dest != "" {
			stacks, _, err := tx.Items().OrgHoldings(ctx, org, application.HoldWarehouse)
			if err != nil {
				return err
			}
			var have int64
			for _, st := range stacks {
				if st.Item == step.Item {
					have += st.Qty
				}
			}
			w := max(itemWeightG(snap, step.Item), 1)
			qty := min(max(have-step.StockFloor, 0), (capacity-carried)*1000/w)
			if qty > 0 {
				if err := h.loadOnTrain(ctx, tx, t, cur.SettlementID, dest, step.Item, qty, qty*w/1000, "", now); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// loadOnTrain takes goods out of a store onto a train: haul_out now, the load row until it is unloaded.
func (h *VillageHandler) loadOnTrain(ctx context.Context, tx application.Tx, t application.RailTrain, from, to, item string, qty, kg int64, orderID string, now time.Time) error {
	if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: item, Qty: qty, FromOrg: application.SettlementOrg(from), FromHolding: application.HoldWarehouse,
		Reason: application.ItemHaulOut, ReferenceType: "rail_trains", ReferenceID: t.ID, At: now}); err != nil {
		return err
	}
	return tx.Haul().AddLoad(ctx, application.HaulLoad{ID: h.ids.NewID(), OrderID: orderID, CarrierKind: "rail", CarrierID: t.ID, Item: item, Qty: qty, Kg: kg,
		From: from, To: to, DepartedAt: now, DueAt: now})
}

// deliverRailLoad unloads a load at its settlement: haul_in, the load closed, the booking delivered.
func (h *VillageHandler) deliverRailLoad(ctx context.Context, tx application.Tx, meta envelope.Metadata, l application.HaulLoad, now time.Time) error {
	return h.deliverLoad(ctx, tx, meta, l.ID, now)
}
