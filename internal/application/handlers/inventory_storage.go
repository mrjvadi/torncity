package handlers

import (
	"context"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// «انبار من» and the holding slot (storage and market audit P1; ADR 0040 6.2):
//
//   - inventory.store puts carried goods in the player's own home store; it is
//     done where the player holds a house, cottage or shed (a settlement where
//     they own a storing building), and refused when the store is full.
//   - inventory.fetch takes goods back into the bags; no room, no fetch.
//   - inventory.claim takes goods out of the holding slot into the bags, as
//     many as fit: a reward that did not fit is never lost, it waits there.
//
// None of these creates or destroys anything: the item journal records a
// change of place (reasons stored, fetched, claimed).

// StorageRequest names a good (its code) or a piece (its serial) and how many.
type StorageRequest struct {
	Item  string `json:"item"`
	Qty   string `json:"qty,omitempty"`
	Nonce string `json:"nonce,omitempty"`
}

// heldIn finds what the player holds of a good or piece in one holding.
func heldIn(ctx context.Context, tx application.Tx, playerID, holding, ref string) (code string, qty int64, pieces []application.Piece, err error) {
	stacks, ps, err := tx.Items().Holdings(ctx, playerID, holding)
	if err != nil {
		return "", 0, nil, err
	}
	if isPieceRef(ref) {
		for _, p := range ps {
			if p.Serial == ref {
				return p.Item, 1, []application.Piece{p}, nil
			}
		}
		return "", 0, nil, nil
	}
	for _, s := range stacks {
		if s.Item == ref {
			qty = s.Qty
		}
	}
	for _, p := range ps {
		if p.Item == ref {
			pieces = append(pieces, p)
		}
	}
	if qty > 0 || len(pieces) > 0 {
		return ref, qty + int64(len(pieces)), pieces, nil
	}
	return "", 0, nil, nil
}

// moveUnits is the one place the three commands move goods: n units of code
// from one holding to another, stack units first and then pieces by serial.
func (h *InventoryHandler) moveUnits(ctx context.Context, tx application.Tx, playerID, from, to string, reason application.ItemReason,
	code string, n int64, stackQty int64, pieces []application.Piece,
) (int64, error) {
	now := h.now()
	var moved int64
	if take := min(n, stackQty); take > 0 {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: take,
			From: playerID, FromHolding: from, To: playerID, ToHolding: to, Reason: reason, At: now}); err != nil {
			return moved, err
		}
		moved += take
	}
	for _, p := range pieces {
		if moved >= n {
			break
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: code, PieceID: p.ID, Qty: 1,
			From: playerID, FromHolding: from, To: playerID, ToHolding: to, Reason: reason, At: now}); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// storageUnits splits what heldIn found into stack units and pieces.
func storageUnits(qty int64, pieces []application.Piece) (stackQty int64) {
	return qty - int64(len(pieces))
}

// Store handles inventory.store: carried goods into the home store.
func (h *InventoryHandler) Store(ctx context.Context, meta envelope.Metadata, req StorageRequest) (*presentation.Response, error) {
	return h.shuffle(ctx, meta, req, application.HoldCarried, application.HoldHome, application.ItemStored)
}

// Fetch handles inventory.fetch: goods from the home store into the bags.
func (h *InventoryHandler) Fetch(ctx context.Context, meta envelope.Metadata, req StorageRequest) (*presentation.Response, error) {
	return h.shuffle(ctx, meta, req, application.HoldHome, application.HoldCarried, application.ItemFetched)
}

// Claim handles inventory.claim: goods from the holding slot into the bags.
func (h *InventoryHandler) Claim(ctx context.Context, meta envelope.Metadata, req StorageRequest) (*presentation.Response, error) {
	return h.shuffle(ctx, meta, req, application.HoldClaim, application.HoldCarried, application.ItemClaimed)
}

func (h *InventoryHandler) shuffle(ctx context.Context, meta envelope.Metadata, req StorageRequest, from, to string,
	reason application.ItemReason,
) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			return nil // a pressed twice: the first press did it
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		code, have, pieces, err := heldIn(ctx, tx, p.ID, from, strings.TrimSpace(req.Item))
		if err != nil {
			return err
		}
		if have == 0 {
			return refuseItem(plife.ItemRefusedNotHeld, itemNamed(snap, req.Item))
		}
		want := min(parseQty(req.Qty), have)
		stackQty := storageUnits(have, pieces)

		switch {
		case to == application.HoldHome:
			// Into the store: only where the player holds a storing building, and
			// only what the store has space for.
			capacity, where, err := homeCapacity(ctx, tx, snap, p.ID)
			if err != nil {
				return err
			}
			if capacity == 0 || p.CityID == nil || !where[*p.CityID] {
				return refuseItem(plife.ItemRefusedNoHome, itemNamed(snap, code))
			}
			used, _, _, err := homeUsed(ctx, tx, snap, p.ID)
			if err != nil {
				return err
			}
			if bulk := snap.BulkOf(code); bulk > 0 {
				want = min(want, max(capacity-used, 0)/bulk)
			}
			if want < 1 {
				return refuseItem(plife.ItemRefusedHomeFull, itemNamed(snap, code))
			}
		case from == application.HoldHome:
			// Out of the store: also only at home.
			_, where, err := homeCapacity(ctx, tx, snap, p.ID)
			if err != nil {
				return err
			}
			if p.CityID == nil || !where[*p.CityID] {
				return refuseItem(plife.ItemRefusedNoHome, itemNamed(snap, code))
			}
			fallthrough
		default:
			// Into the bags: no room, no fetch; a claim takes as many as fit.
			fits := want
			if h.carry.enabled() {
				st, err := h.carry.load(ctx, tx, snap, p.ID, h.now(), false)
				if err != nil {
					return err
				}
				room := st.room(h.carry.rules)
				if bulk := snap.BulkOf(code); bulk > 0 {
					fits = min(fits, room.FreeSpace/bulk)
				}
				if g := snap.WeightOf(code); g > 0 {
					fits = min(fits, room.FreeG/g)
				}
			}
			if fits < 1 {
				return h.carry.fit(ctx, tx, snap, p.ID, code, want)
			}
			want = fits
		}
		_, err = h.moveUnits(ctx, tx, p.ID, from, to, reason, code, want, stackQty, pieces)
		return err
	})
	if resp, ok := noRoomResponse(err, lang, snap, "inventory:show"); ok {
		return resp, nil
	}
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return h.Show(ctx, meta, PageRequest{})
}

// storageView reads the home store and the holding slot for the inventory
// screen: the store's space, whether the player is at home, and what waits.
func (h *InventoryHandler) storageView(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	designs func([]application.Piece) (map[string]string, error),
) (home *plife.HomeStoreView, claims []plife.InventoryLine, err error) {
	capacity, where, err := homeCapacity(ctx, tx, snap, p.ID)
	if err != nil {
		return nil, nil, err
	}
	used, stacks, pieces, err := homeUsed(ctx, tx, snap, p.ID)
	if err != nil {
		return nil, nil, err
	}
	if capacity > 0 || used > 0 {
		names, err := designs(pieces)
		if err != nil {
			return nil, nil, err
		}
		home = &plife.HomeStoreView{Capacity: capacity, Used: used,
			Here: p.CityID != nil && where[*p.CityID], Lines: inventoryLines(snap, stacks, pieces, names)}
	}
	cs, cp, err := tx.Items().Holdings(ctx, p.ID, application.HoldClaim)
	if err != nil {
		return nil, nil, err
	}
	if len(cs)+len(cp) > 0 {
		names, err := designs(cp)
		if err != nil {
			return nil, nil, err
		}
		claims = inventoryLines(snap, cs, cp, names)
	}
	return home, claims, nil
}
