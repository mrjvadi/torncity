package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// VillageClearRequest is the payload of settlement.clear.order and settlement.clear.cancel: the lot (signed lot coordinates,
// x east, y north, the grid is 0..side-1) and, for an order, what to clear.
type VillageClearRequest struct {
	X    string `json:"x"`
	Y    string `json:"y"`
	What string `json:"what,omitempty"` // trees, rocks or all (default all)
}

func (r VillageClearRequest) pos() (land.Pos, bool) {
	x, ex := strconv.Atoi(strings.TrimSpace(r.X))
	y, ey := strconv.Atoi(strings.TrimSpace(r.Y))
	return land.Pos{X: x, Y: y}, ex == nil && ey == nil
}

// ClearOrder handles settlement.clear.order: the head (or whoever holds land.clear) for the commons and the treasury's lots, the
// owner for his own lot. The crews take an ordered lot before any other; on a citizen's lot the goods are his and he pays the
// crew (clearing_fee). Setting the same order twice changes nothing.
func (h *VillageHandler) ClearOrder(ctx context.Context, meta envelope.Metadata, req VillageClearRequest) (*presentation.Response, error) {
	return h.clearing(ctx, meta, req, false)
}

// ClearCancel handles settlement.clear.cancel: the order on a lot is taken back by whoever may give it.
func (h *VillageHandler) ClearCancel(ctx context.Context, meta envelope.Metadata, req VillageClearRequest) (*presentation.Response, error) {
	return h.clearing(ctx, meta, req, true)
}

func (h *VillageHandler) clearing(ctx context.Context, meta envelope.Metadata, req VillageClearRequest, cancel bool) (*presentation.Response, error) {
	lang := meta.Language
	var out village.ClearOrderView
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
		pos, ok := req.pos()
		if !ok {
			return refuseVillage(village.LotInvalid, village.AddrLand)
		}
		if err := tx.Land().Lock(ctx, s.CityID); err != nil {
			return err
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		open, err := tx.Citizens().OpenLots(ctx, s.CityID)
		if err != nil {
			return err
		}
		view, err := h.landViewOf(ctx, tx, w, s, open)
		if err != nil {
			return err
		}
		lot, ok := view.Lots[pos]
		if !ok || lot.Occupied {
			return refuseVillage(village.LotNothing, village.AddrLand)
		}
		// who may give the order: the owner of a citizen's lot, else whoever holds land.clear
		owner := ""
		lots, err := tx.Citizens().Lots(ctx, s.CityID)
		if err != nil {
			return err
		}
		for _, o := range lots {
			if o.X == pos.X && o.Y == pos.Y {
				owner = o.OwnerID
			}
		}
		if owner != "" {
			if owner != p.ID {
				return refuseVillage(village.LotNotYours, village.AddrLand)
			}
		} else if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.LandClear); err != nil {
			return err
		}
		trees, rocks := true, true
		switch strings.TrimSpace(req.What) {
		case "trees":
			rocks = false
		case "rocks":
			trees = false
		}
		out = village.ClearOrderView{Village: s.Name, X: pos.X, Y: pos.Y, Trees: lot.Trees, Rocks: lot.Rocks, Private: owner != ""}
		if cancel {
			trees, rocks = false, false
			out.Cancelled = true
		} else {
			trees, rocks = trees && lot.Trees > 0, rocks && lot.Rocks > 0
			if !trees && !rocks {
				return refuseVillage(village.LotNothing, village.AddrLand)
			}
		}
		out.OrderTrees, out.OrderRocks = trees, rocks
		now := h.now()
		if err := tx.Land().Order(ctx, s.CityID, pos, trees, rocks, p.ID, now); err != nil {
			return err
		}
		return h.appendBuildingEvent(ctx, tx, meta, s, "land_changed", map[string]any{
			"settlement_id": s.CityID, "kind": "ordered", "x": pos.X, "y": pos.Y,
		})
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.ClearOrder(h.screen(meta, lang), out), nil
}
