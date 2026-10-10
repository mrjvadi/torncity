package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// WithLand switches the land model on: trees and rocks stand on the lots, the ring of commons lies round the grid, a lot with
// either cannot be built on (docs/adr/0065).
func (h *VillageHandler) WithLand(r application.LandRules) *VillageHandler {
	h.landRules = r
	return h
}

// landViewOf is the land of a settlement now: the grid, the ring and the lots its roads opened (open may be nil).
func (h *VillageHandler) landViewOf(ctx context.Context, tx application.Tx, w *worldgen.World, s application.FoundedSettlement,
	open []application.OpenLotRow,
) (*application.LandView, error) {
	def, ok := h.content.Current().Land()
	if !ok || !h.landRules.Enabled() {
		return &application.LandView{Rules: h.landRules, Lots: map[land.Pos]*application.LandLot{}}, nil
	}
	deltas, err := tx.Land().Rows(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	saplings, err := tx.Land().Saplings(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	return application.BuildLand(w, s, h.gridSide(s), open, h.occupiedLots(buildings), def, h.landRules, deltas, saplings, h.now()), nil
}

// occupiedLots are the lots the buildings hold (a building holds its whole footprint, turned as it was placed).
func (h *VillageHandler) occupiedLots(buildings []application.SettlementBuildingInstance) map[land.Pos]bool {
	snap := h.content.Current()
	out := map[land.Pos]bool{}
	for _, b := range buildings {
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
				out[land.Pos{X: b.LotX + dx, Y: b.LotY + dy}] = true
			}
		}
	}
	return out
}

// withObstacles adds to the refusal of a footprint with trees or rocks on it what stands there and whether the viewer may order
// the clearing (the owner of a private lot, or whoever holds land.clear); any other refusal is returned as it is.
func (h *VillageHandler) withObstacles(ctx context.Context, tx application.Tx, s application.FoundedSettlement, def settlementbuilding.Def,
	x, y int, playerID string, r *villageRefusal, cause error,
) error {
	if !stderrors.Is(cause, settlementbuilding.ErrObstructed) {
		return r
	}
	w, err := h.world(ctx)
	if err != nil {
		return err
	}
	open, err := tx.Citizens().OpenLots(ctx, s.CityID)
	if err != nil {
		return err
	}
	lv, err := h.landViewOf(ctx, tx, w, s, open)
	if err != nil {
		return err
	}
	view := &village.ObstacleView{X: -1, Y: -1}
	for _, q := range footprintOf(def, x, y) {
		if l, ok := lv.Lots[land.Pos{X: q[0], Y: q[1]}]; ok && l.Obstructed {
			if view.X < 0 {
				view.X, view.Y = q[0], q[1]
			}
			view.Trees += l.Trees
			view.Rocks += l.Rocks
		}
	}
	view.CanOrder = h.mayClear(ctx, tx, s, playerID, land.Pos{X: view.X, Y: view.Y})
	r.obstacles = view
	return r
}

// mayClear reports whether the player may order the clearing of a lot: whoever holds the land.clear permission, or the owner of
// the lot himself.
func (h *VillageHandler) mayClear(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, p land.Pos) bool {
	if ok, err := h.mayVillage(ctx, tx, s, playerID, charter.LandClear); err == nil && ok {
		return true
	}
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return false
	}
	for _, l := range lots {
		if l.X == p.X && l.Y == p.Y && l.OwnerID == playerID {
			return true
		}
	}
	return false
}
