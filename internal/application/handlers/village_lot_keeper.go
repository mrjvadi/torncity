package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The stall keeper (ADR 0062, plan A3c). A stall sells for its owner while he is in the settlement. A market trader who
// has to leave hires a keeper: in the real market a stallholder takes on an assistant who minds the stall and is paid by
// the day or by a share of the takings. Here the keeper is a person of the settlement's labour pool (like the shopkeeper
// and the market clerk), who holds one seat while he is hired and is paid a share of each sale made while the owner is
// away, out of the seller's proceeds (ledger reason stall_keeper_wage, to the sink). The share is fixed when the owner
// hires him (config trade.stall_keeper_share_bps). One keeper serves all of an owner's stalls in the settlement.
//
//   - WHO WORKS THERE. The keeper, an NPC of the pool; no pool seat free: no hire.
//   - WHAT IT CONSUMES. One seat of the pool while hired; the share of every sale made in the owner's absence.
//   - WHAT IT PROVIDES. The owner's asks stay on the book while he is away.
//   - WHAT BREAKS. No keeper: the stall is shut while the owner is away (as before). Dismissing him ends it at once.

// WithStallKeeper gives the village handler the share a hired keeper takes (basis points).
func (h *VillageHandler) WithStallKeeper(shareBPS int64) *VillageHandler {
	h.keeperShare = shareBPS
	return h
}

// stallKeeperLine is the keeper block of a stall the viewer owns; nil on any other building.
func (h *VillageHandler) stallKeeperLine(ctx context.Context, tx application.Tx, lc *lotCtx) (*village.LotKeeperLine, error) {
	if lc.owner.private == nil || lc.f == nil || !hasStaffRole(lc.k, lc.f.Function, "stall_keeper") {
		return nil, nil
	}
	line := &village.LotKeeperLine{ShareBPS: h.keeperShare}
	hire, err := tx.StallKeepers().OfOwner(ctx, lc.s.CityID, lc.p.ID)
	if err != nil {
		return nil, err
	}
	if hire != nil {
		line.Hired, line.ShareBPS = true, hire.ShareBPS
		return line, nil
	}
	free, err := h.keeperSeatsFree(ctx, tx, lc.k.snap, lc.s, lc.buildings)
	if err != nil {
		return nil, err
	}
	line.SeatsFree = free
	switch {
	case h.keeperShare <= 0:
		line.Reason = "off"
	case free < 1:
		line.Reason = "no_seat"
	default:
		line.Can = true
	}
	return line, nil
}

func hasStaffRole(k lotKit, function, role string) bool {
	for _, st := range k.defs[function].Staff {
		if st.Role == role {
			return true
		}
	}
	return false
}

// keeperSeatsFree is the people of the labour pool still free to be hired.
func (h *VillageHandler) keeperSeatsFree(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (int64, error) {
	market, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return 0, err
	}
	return market.claims.Available(), nil
}

// keeperAct hires or dismisses the owner's stall keeper.
func (h *VillageHandler) keeperAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, lc *lotCtx, view *village.LotManageView, action string, confirmed bool) error {
	if lc.owner.private == nil || !hasStaffRole(lc.k, lc.f.Function, "stall_keeper") {
		return refuseVillage(village.LotNoKeeper, village.AddrLotManage)
	}
	now := h.now()
	switch action {
	case village.LotActionKeeperHire:
		line, err := h.stallKeeperLine(ctx, tx, lc)
		if err != nil {
			return err
		}
		view.Stage = village.LotAsk
		view.Keeper = line
		if line.Hired {
			return refuseVillage(village.LotKeeperNone, village.AddrLotManage)
		}
		if !confirmed {
			return nil
		}
		if !line.Can {
			return refuseVillage(village.LotKeeperNone, village.AddrLotManage)
		}
		fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			view.Stage = village.LotDone
			return nil
		}
		if _, err := tx.StallKeepers().Hire(ctx, application.StallKeeper{ID: h.ids.NewID(), SettlementID: lc.s.CityID, OwnerID: lc.p.ID,
			ShareBPS: h.keeperShare, HiredAt: now}); err != nil {
			return err
		}
	case village.LotActionKeeperEnd:
		if !confirmed {
			view.Stage = village.LotAsk
			return nil
		}
		fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
		if err != nil {
			return err
		}
		if fresh {
			if _, err := tx.StallKeepers().End(ctx, lc.s.CityID, lc.p.ID, now); err != nil {
				return err
			}
		}
	}
	view.Stage = village.LotDone
	return h.refreshDetail(ctx, tx, lc, view)
}
