package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
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
) ([][2]int, error) {
	if code == "road" {
		return nil, nil
	}
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
		return nil, refuseVillage(screens.VillageNoRoad)
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
				return nil, refuseVillage(screens.VillageOccupied)
			}
			return nil, err
		}
		out = append(out, map[string]any{"building_id": id, "lot_x": p[0], "lot_y": p[1]})
	}
	return out, nil
}
