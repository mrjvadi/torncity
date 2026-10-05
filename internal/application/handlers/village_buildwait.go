package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// What a client shows for "how long will this take to build" (owner question of the UI,
// 2026-10-05). A building's `build_time` is WORKER EFFORT: the worker-minutes of labour
// it needs, content authored as hours of one crew. It is not a wait. The wait a player
// can expect is the shifts that effort needs, spread over the crew the settlement has
// right now, times the real length of a shift:
//
//	shifts  = ceil(worker-minutes / points one NPC shift adds)
//	crew    = the NPC labourers free to work (at least one: a player can always work)
//	seconds = ceil(shifts / crew) * the real length of a shift
//
// It is an estimate: more hands (players working, a bigger pool) shorten it, a
// shortage of materials or money lengthens it. Without the labour market (an older
// wiring) the building is raised by a timer and the wait is the build time itself.

// buildWaiter reads the crew once and answers for any building.
func (h *VillageHandler) buildWaiter(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
) (func(def settlementbuilding.Def) village.BuildWaitView, error) {
	if !h.labor.Enabled() {
		return func(def settlementbuilding.Def) village.BuildWaitView {
			return village.BuildWaitView{Seconds: int64(h.scale.RealWait(def.BuildTime) / time.Second)}
		}, nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	m, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return nil, err
	}
	crew := max(m.line.Available, 1)
	shift := h.shiftWait()
	points := h.labor.Points(h.labor.NPCProductivityBPS)
	return func(def settlementbuilding.Def) village.BuildWaitView {
		work := h.labor.WorkRequired(int64(def.BuildTime / time.Minute))
		shifts := labor.ShiftsNeeded(work, points)
		rounds := (shifts + crew - 1) / crew
		return village.BuildWaitView{Seconds: rounds * int64(shift/time.Second), Shifts: shifts, Crew: crew, ShiftSeconds: int64(shift / time.Second)}
	}, nil
}
