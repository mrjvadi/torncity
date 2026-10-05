package handlers

import (
	"context"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Giving to the settlement's stock and taking from it (storage and market audit
// F10, P2; web map 6.3 «کمک به انبار»):
//
//   - DONATE. Any player standing in the settlement gives goods they carry to the
//     stock: a gift, like the cash donation of settlement.donate. It is refused
//     when the item's class has no room, with the room it lacks. Item journal:
//     reason donated, carried to the stock.
//   - TAKE. Only the holder of the head's office takes goods out (the permission
//     is the office, ADR 0015: a charter `storage.take` will refine it with the
//     charters of roadmap 2.1). They go to the taker's bags, so no room, no take.
//     Item journal: reason taken, stock to carried.
//
// Both queue behind the stock's lock, are idempotent per request, and write an
// outbox event, so the settlement's news and the audit see them.

// VillageStockMoveRequest names a good and how many.
type VillageStockMoveRequest struct {
	Item string `json:"item"`
	Qty  string `json:"qty,omitempty"`
}

// StockDonate handles settlement.stock.donate.
func (h *VillageHandler) StockDonate(ctx context.Context, meta envelope.Metadata, req VillageStockMoveRequest) (*presentation.Response, error) {
	return h.stockMove(ctx, meta, req, true)
}

// StockTake handles settlement.stock.take.
func (h *VillageHandler) StockTake(ctx context.Context, meta envelope.Metadata, req VillageStockMoveRequest) (*presentation.Response, error) {
	return h.stockMove(ctx, meta, req, false)
}

func (h *VillageHandler) stockMove(ctx context.Context, meta envelope.Metadata, req VillageStockMoveRequest, donate bool) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
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
		if donate {
			if p.CityID == nil || *p.CityID != s.CityID {
				return refuseVillage(village.VillageNotResident, village.AddrMaterials)
			}
		} else if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.StorageTake); err != nil {
			return err
		}
		code := strings.TrimSpace(req.Item)
		if _, ok := snap.ComponentDef(code); !ok {
			if _, ok := snap.ItemDef(code); !ok {
				return refuseVillage(village.VillageNotFound, village.AddrMaterials)
			}
		}
		qty := parseQty(req.Qty)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		org := application.SettlementOrg(s.CityID)
		// The stock and the player's goods queue in a fixed order: the stock first.
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
		if err != nil {
			return err
		}
		now := h.now()
		move := application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: qty, At: now,
			ReferenceType: "settlements", ReferenceID: s.CityID}
		if donate {
			have, err := carriedQty(ctx, tx, p.ID, code)
			if err != nil {
				return err
			}
			if have < qty {
				return refuseVillage(village.VillageNotEnough, village.AddrMaterials)
			}
			if qty > stock.freeUnits(code) {
				r := refuseVillage(village.VillageStorageFull, village.AddrMaterials)
				r.missing = qty*stock.Bulk(code) - stock.freeSpace(stock.ClassOf(code))
				return r
			}
			move.From, move.FromHolding, move.ToOrg, move.ToHolding, move.Reason =
				p.ID, application.HoldCarried, org, application.HoldWarehouse, application.ItemDonated
		} else {
			if stock.Units[code] < qty {
				return refuseVillage(village.VillageNotEnough, village.AddrMaterials)
			}
			if err := h.carryEnv().fit(ctx, tx, snap, p.ID, code, qty); err != nil {
				return err
			}
			move.FromOrg, move.FromHolding, move.To, move.ToHolding, move.Reason =
				org, application.HoldWarehouse, p.ID, application.HoldCarried, application.ItemTaken
		}
		if err := tx.Items().Move(ctx, move); err != nil {
			return err
		}
		name := "stock_donated"
		if !donate {
			name = "stock_taken"
		}
		return appendVillageEvent(ctx, tx, meta, name, s.CityID, map[string]any{
			"settlement_id": s.CityID, "item": code, "quantity": qty, "player_id": p.ID,
		})
	})
	if resp, ok := noRoomResponse(err, lang, snap, village.AddrMaterials); ok {
		return resp, nil
	}
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.materialsScreen(ctx, meta, nil)
}

// carryEnv is the room check of the village handler: the carry rules and the
// clock of the shop's wiring.
func (h *VillageHandler) carryEnv() carryEnv { return carryEnvOf(h.shop.Carry, h.shop.Clock) }

// carriedQty is how many units of a good the player carries as stacks.
func carriedQty(ctx context.Context, tx application.Tx, playerID, code string) (int64, error) {
	stacks, _, err := tx.Items().Holdings(ctx, playerID, application.HoldCarried)
	if err != nil {
		return 0, err
	}
	for _, s := range stacks {
		if s.Item == code {
			return s.Qty, nil
		}
	}
	return 0, nil
}
