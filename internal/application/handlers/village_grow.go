package handlers

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds settlement.grid.grow: the head buys more land.
//
// A village owns as much land as it pays for (docs/adr/0033 section 8.4, as
// the owner corrected it): nothing ties the grid to the tier, so a village can
// be large and a city small. What limits growth is the world (a lot of water
// or steep ground stays unbuildable, sampled by the very sampler the rest of
// the game uses; other settlements sit more than settlement.min_spawn_distance_km
// away, far beyond any grid) and the price, which rises with each expansion
// (settlement.grid_lot_price, settlement.grid_price_step_bps), plus a technical
// bound on a grid's side (settlement.grid_max_lots) that keeps a layout a sane
// size to sample, send and draw - not a game rule.
//
// One step adds a column on the east edge and a row on the north edge, so the
// side grows by one and every stored lot coordinate stays valid (lot (0,0),
// the south-west corner, never moves; wsettle.GridCentreGrown). The step is a
// compare-and-set on the village's growth count, so two replicas (or a
// double press) can never both buy the same step.

// VillageGrowRequest is a press on the land purchase: the first (unconfirmed)
// shows the price and what the strip holds, the second buys it.
type VillageGrowRequest struct {
	Confirm string `json:"confirm,omitempty"`
}

// GrowGrid handles settlement.grid.grow.
func (h *VillageHandler) GrowGrid(ctx context.Context, meta envelope.Metadata, req VillageGrowRequest) (*presenter.Response, error) {
	lang := meta.Language
	var confirmView *screens.GridGrowView
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
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		side := h.gridSide(s)
		if h.gridMaxLots <= 0 || side+1 > h.gridMaxLots {
			return refuseVillage(screens.VillageGridMax)
		}
		price := wsettle.GrowthPrice(side, s.GridGrowth, h.gridLotPrice, h.gridPriceStepBPS)

		if strings.TrimSpace(req.Confirm) != screens.VillageBuildConfirm {
			// What the strip holds: sampled on the grid as it would be.
			w, err := h.world(ctx)
			if err != nil {
				return err
			}
			grown := s
			grown.GridGrowth++
			grid, _, err := h.grid(ctx, tx, w, grown)
			if err != nil {
				return err
			}
			buildable := 0
			for y := 0; y <= side; y++ {
				for x := 0; x <= side; x++ {
					if (x == side || y == side) && grid[y][x].Buildable {
						buildable++
					}
				}
			}
			treasury, err := treasuryBalance(ctx, tx, s.CityID)
			if err != nil {
				return err
			}
			confirmView = &screens.GridGrowView{
				SettlementName: s.Name, Side: side, NewSide: side + 1, LotsGained: wsettle.GrowthLots(side),
				BuildableGained: buildable, Price: price, Treasury: treasury,
			}
			return nil
		}

		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		if price > 0 {
			if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonSettlementConstruction, price, now); err != nil {
				return err
			}
		}
		if err := tx.Settlements().GrowGrid(ctx, s.CityID, s.GridGrowth, s.GridGrowth+1); err != nil {
			if stderrors.Is(err, application.ErrGridGrowthConflict) {
				return refuseVillage(screens.VillageBusy)
			}
			return err
		}
		s.GridGrowth++
		return h.appendBuildingEvent(ctx, tx, meta, s, "grid_grown", map[string]any{
			"settlement_id": s.CityID, "grid_lots": h.gridSide(s), "growth": s.GridGrowth,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return screens.GridGrowConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.BuildMenu(ctx, meta)
}
