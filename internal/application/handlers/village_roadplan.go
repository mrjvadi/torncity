package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
)

// Automatic roads (docs: internal/domain/settlementbuilding/roads.go for the
// street planner itself). Placing a building lays the road that connects it to
// the village network in the same transaction: the roads are finished at once
// (no construction timer - there is nothing to wait for on a lot of gravel),
// cost settlement.auto_road_cost per lot (0 by default), carry the same road
// upkeep as any road, and ride in the same build_started event (auto_roads) so
// a client draws them with the building and the layout version covers both.

// footprintOf lists the lots a building of def placed at (x,y) covers.
func footprintOf(def settlementbuilding.Def, x, y int) [][2]int {
	out := make([][2]int, 0, def.FootprintW*def.FootprintH)
	for dy := 0; dy < def.FootprintH; dy++ {
		for dx := 0; dx < def.FootprintW; dx++ {
			out = append(out, [2]int{x + dx, y + dy})
		}
	}
	return out
}

// planAutoRoads is the lots of road a new building needs, none when it is a
// road itself or already touches the network, or a refusal when no road could
// reach it. grid is left with the building's footprint marked.
func (h *VillageHandler) planAutoRoads(ctx context.Context, tx application.Tx, s application.FoundedSettlement, code string,
	def settlementbuilding.Def, grid settlementbuilding.Grid, x, y int,
) (autoRoadPlan, error) {
	if code == "road" {
		return autoRoadPlan{}, nil
	}
	if x < 0 || y < 0 || x+def.FootprintW > len(grid) || y+def.FootprintH > len(grid) {
		// land beyond the first grid: the lane to the nearest drawn road, and
		// the stretch of that road not laid yet
		w, err := h.world(ctx)
		if err != nil {
			return autoRoadPlan{}, err
		}
		lots, err := tx.Citizens().Lots(ctx, s.CityID)
		if err != nil {
			return autoRoadPlan{}, err
		}
		pic, err := h.picture(ctx, tx, w, s, lots)
		if err != nil {
			return autoRoadPlan{}, err
		}
		return h.planOuterRoads(pic, def, x, y)
	}
	path, err := h.planInnerRoads(ctx, tx, s, code, def, grid, x, y)
	return autoRoadPlan{Path: path, Fee: int64(len(path)) * h.autoRoadCost}, err
}

// planInnerRoads is planAutoRoads inside the first grid.
func (h *VillageHandler) planInnerRoads(ctx context.Context, tx application.Tx, s application.FoundedSettlement, code string,
	def settlementbuilding.Def, grid settlementbuilding.Grid, x, y int,
) ([][2]int, error) {
	fp := footprintOf(def, x, y)
	for _, p := range fp {
		grid[p[1]][p[0]].Occupied = true
	}
	// A road never crosses a lot a resident owns (docs/adr/0033 section 4.5).
	if h.citizen.enabled() {
		if owned, err := privateLotSet(ctx, tx, s.CityID); err != nil {
			return nil, err
		} else {
			for p := range owned {
				if p[1] < len(grid) && p[0] < len(grid[p[1]]) {
					grid[p[1]][p[0]].Occupied = true
				}
			}
		}
	}
	rows, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	var roads, hall [][2]int
	for _, b := range rows {
		if !b.Holds() {
			continue
		}
		switch b.TypeCode {
		case "road":
			roads = append(roads, [2]int{b.LotX, b.LotY})
		case "civic_hall":
			bd := def
			if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
				bd = d.Def()
				if b.Rotated {
					bd = bd.Rotate()
				}
			}
			hall = append(hall, footprintOf(bd, b.LotX, b.LotY)...)
		}
	}
	path, err := settlementbuilding.PlanRoads(grid, fp, roads, hall)
	if stderrors.Is(err, settlementbuilding.ErrNoRoadAccess) {
		return nil, refuseVillage(village.VillageNoRoad)
	}
	return path, err
}

// layAutoRoads writes the planned road lots as finished road buildings and
// returns them for the event.
func (h *VillageHandler) layAutoRoads(ctx context.Context, tx application.Tx, settlementID string, path [][2]int, now time.Time,
) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(path))
	for _, p := range path {
		id := h.ids.NewID()
		done := now
		if err := tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
			ID: id, SettlementID: settlementID, TypeCode: "road", LotX: p[0], LotY: p[1],
			Status: "complete", QueuedAt: now, CompletedAt: &done,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotOccupied) {
				return nil, refuseVillage(village.VillageOccupied)
			}
			return nil, err
		}
		out = append(out, map[string]any{"building_id": id, "lot_x": p[0], "lot_y": p[1]})
	}
	// a lot of a drawn road (docs/adr/0044 5.5) is laid by this: the plan keeps
	// the book of what is built, once
	if len(path) > 0 {
		if _, err := tx.Citizens().MarkBuilt(ctx, settlementID, path, now); err != nil {
			return nil, err
		}
	}
	return out, nil
}
