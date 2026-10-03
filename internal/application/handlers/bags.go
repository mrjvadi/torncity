package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/carry"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds what a player can carry (docs/adr/0046-bags-merchants-
// currency-exchange.md section 4; rules in internal/domain/carry): the bags
// they wear, the space and load their goods take, the wear a bag earns by the
// game day, and the one free sack of the rollout.
//
// # What is enforced, and what is not (as built)
//
// Space is checked where ADR 0046 says "no room, no buy": a purchase at the
// village shop (village_shop.go). Goods that arrive any other way (the city
// shops, the market, loot, a gift, a production order) are not refused for
// lack of room yet, because ADR 0040 section 6's personal capacity is not
// built for them: a player may hold more than their capacity, and nothing is
// ever taken from a bag. The weight limit is the same: a hard limit on a shop
// purchase; the comfortable load is shown but costs nothing yet (ADR 0036
// section 4.6's energy cost needs a walking rule that does not exist).
//
// # Wear is settled when a bag is used
//
// A bag loses WearPerDay points for every game day it is carried at least half
// full. Nothing records a bag's fill in the past, so the wear is charged when
// the bag's capacity is next read (a purchase, the inventory screen), for the
// days since it was last charged, judged on the fill at that moment. Charging
// twice for one day is impossible: player_bags.worn_through_day is the fence.

// carryEnv is the carry rules and the game clock they run on.
type carryEnv struct {
	rules carry.Rules
	clock gametime.Clock
}

func (e carryEnv) enabled() bool { return e.rules.Base > 0 && e.clock.Validate() == nil }

// wornBag is a worn bag with its piece and content.
type wornBag struct {
	worn  application.WornBag
	piece application.Piece
	def   content.ItemDef
	bag   carry.Bag
}

// carryState is a player's carrying, as of one moment.
type carryState struct {
	bags                      []wornBag
	usedSpace, usedG          int64
	capacity, comfortG, hardG int64
	// reservedSpace and reservedG are the room goods keep without being in the
	// bags: a listing in escrow and the open bids (room.go).
	reservedSpace, reservedG int64
}

// carryBags is the bags as the rules take them.
func (s carryState) carryBags() []carry.Bag {
	out := make([]carry.Bag, 0, len(s.bags))
	for _, b := range s.bags {
		out = append(out, b.bag)
	}
	return out
}

func (s carryState) room(r carry.Rules) carry.Room {
	return r.RoomLeft(s.carryBags(), s.usedSpace+s.reservedSpace, s.usedG+s.reservedG)
}

// load reads what the player carries. With settle it also charges the wear the
// bags have earned since they were last charged (the caller holds the owner's
// lock, so two requests never charge the same day twice).
func (e carryEnv) load(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string,
	now time.Time, settle bool,
) (carryState, error) {
	stacks, pieces, _, err := carried(ctx, tx, playerID)
	if err != nil {
		return carryState{}, err
	}
	worn, err := tx.Bags().Worn(ctx, playerID)
	if err != nil {
		return carryState{}, err
	}
	byID := make(map[string]application.Piece, len(pieces))
	for _, p := range pieces {
		byID[p.ID] = p
	}
	var st carryState
	wornIDs := map[string]bool{}
	for _, w := range worn {
		p, ok := byID[w.PieceID]
		def, _ := snap.ItemDef(p.Item)
		bag, isBag := def.CarryBag(p.UsesLeft)
		if !ok || !isBag || string(bag.Slot) != w.Slot {
			// The piece left its wearer (given, dropped, sold, escrowed) or the content changed:
			// it is not worn any more.
			if err := tx.Bags().TakeOffPiece(ctx, w.PieceID); err != nil {
				return carryState{}, err
			}
			continue
		}
		wornIDs[p.ID] = true
		st.bags = append(st.bags, wornBag{worn: w, piece: p, def: def, bag: bag})
	}
	// What is carried takes room, except the bags that are on.
	for _, s := range stacks {
		st.usedSpace += s.Qty * snap.BulkOf(s.Item)
		st.usedG += s.Qty * snap.WeightOf(s.Item)
	}
	for _, p := range pieces {
		st.usedG += snap.WeightOf(p.Item)
		if !wornIDs[p.ID] {
			st.usedSpace += snap.BulkOf(p.Item)
		}
	}
	st.refresh(e.rules)
	if e.enabled() {
		if err := e.reserve(ctx, tx, snap, playerID, &st); err != nil {
			return carryState{}, err
		}
	}
	if !settle || len(st.bags) == 0 || !e.enabled() {
		return st, nil
	}
	today := e.clock.DayAt(now)
	for i := range st.bags {
		b := &st.bags[i]
		points, through := e.rules.WearDue(b.worn.WornThroughDay, today, st.usedSpace, st.capacity)
		if through != b.worn.WornThroughDay {
			if err := tx.Bags().SetWornThrough(ctx, playerID, b.worn.Slot, through); err != nil {
				return carryState{}, err
			}
			b.worn.WornThroughDay = through
		}
		if points > 0 && b.piece.UsesLeft > 0 {
			left := max(b.piece.UsesLeft-int(points), 0)
			if err := tx.Items().SetUses(ctx, b.piece.ID, left); err != nil {
				return carryState{}, err
			}
			b.piece.UsesLeft = left
			b.bag.Wear = left
		}
	}
	st.refresh(e.rules)
	return st, nil
}

func (s *carryState) refresh(r carry.Rules) {
	bags := s.carryBags()
	s.capacity = r.Capacity(bags)
	s.comfortG, s.hardG = r.Load(bags)
}

// carryView is the state as the inventory screen shows it.
func (e carryEnv) carryView(snap *content.Snapshot, st carryState) ([]plife.BagSlotLine, plife.CarryLine) {
	bySlot := map[string]*plife.WornBagLine{}
	for _, b := range st.bags {
		bySlot[b.worn.Slot] = &plife.WornBagLine{
			Item: itemNamed(snap, b.piece.Item), Serial: b.piece.Serial,
			FullSpace: b.bag.Space, Space: b.bag.EffectiveSpace(e.rules),
			Wear: b.piece.UsesLeft, WearMax: b.def.Durability, Torn: b.bag.Torn(),
			ComfortKg: b.def.Bag.ComfortKg, HardKg: b.def.Bag.HardKg,
		}
	}
	var slots []plife.BagSlotLine
	for _, sl := range carry.Slots() {
		slots = append(slots, plife.BagSlotLine{Slot: string(sl), Bag: bySlot[string(sl)]})
	}
	return slots, plife.CarryLine{Used: st.usedSpace, Reserved: st.reservedSpace, Capacity: st.capacity, Base: e.rules.Base,
		LoadG: st.usedG, ComfortG: st.comfortG, HardG: st.hardG}
}

// WithCarry gives the inventory the carry rules and the game clock; without
// them it shows no bags (a wiring without the ADR 0046 block).
func (h *InventoryHandler) WithCarry(rules carry.Rules, clock gametime.Clock) *InventoryHandler {
	h.carry = carryEnv{rules: rules, clock: clock}
	return h
}

// BagRequest names a bag piece (its serial) to put on, or a slot to empty.
type BagRequest struct {
	Item string `json:"item,omitempty"`
	Slot string `json:"slot,omitempty"`
}

// Wear handles inventory.bag.wear: the player puts a bag piece they carry on,
// in the slot it belongs to. A bag already on in that slot goes back in the
// pack. Putting on the bag that is on already changes nothing.
func (h *InventoryHandler) Wear(ctx context.Context, meta envelope.Metadata, req BagRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if !h.carry.enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the bags are not wired"))
	}
	snap := h.content.Current()
	lang := meta.Language
	var view plife.ItemDetailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		code, _, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if piece == nil {
			return refuseItem(plife.ItemRefusedNotHeld, itemNamed(snap, req.Item))
		}
		def, _ := snap.ItemDef(code)
		if def.Bag == nil {
			return refuseItem(plife.ItemRefusedNotBag, itemNamed(snap, code))
		}
		now := h.now()
		if err := tx.Bags().Wear(ctx, application.WornBag{PlayerID: p.ID, Slot: def.Bag.Slot, PieceID: piece.ID,
			WornThroughDay: h.carry.clock.DayAt(now), WornAt: now}); err != nil {
			return err
		}
		if err := appendItemEvent(ctx, tx, meta, "bag_worn", p.ID, map[string]any{
			"player_id": p.ID, "item": code, "slot": def.Bag.Slot, "serial": piece.Serial, "content_version": snap.Version(),
		}); err != nil {
			return err
		}
		view, err = h.detail(ctx, tx, snap, p, code, 1, piece)
		return err
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return plife.ItemDetail(presentation.Ctx{Lang: lang}, view), nil
}

// TakeOff handles inventory.bag.off: the bag in a slot goes back in the pack.
// An empty slot changes nothing.
func (h *InventoryHandler) TakeOff(ctx context.Context, meta envelope.Metadata, req BagRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if !h.carry.enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the bags are not wired"))
	}
	if !carry.Slot(req.Slot).Valid() {
		return nil, errors.InvalidInput("a bag slot is belt or back")
	}
	snap := h.content.Current()
	lang := meta.Language
	var resp *presentation.Response
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		st, err := h.carry.load(ctx, tx, snap, p.ID, h.now(), true)
		if err != nil {
			return err
		}
		for _, b := range st.bags {
			if b.worn.Slot != req.Slot {
				continue
			}
			if err := tx.Bags().TakeOff(ctx, p.ID, req.Slot); err != nil {
				return err
			}
			if err := appendItemEvent(ctx, tx, meta, "bag_off", p.ID, map[string]any{
				"player_id": p.ID, "item": b.piece.Item, "slot": req.Slot, "serial": b.piece.Serial, "content_version": snap.Version(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	resp, err = h.Show(ctx, meta, PageRequest{})
	_ = lang
	return resp, err
}

// bagDetail is what a bag piece adds to its detail view.
func (h *InventoryHandler) bagDetail(snap *content.Snapshot, def content.ItemDef, st carryState, piece *application.Piece) *plife.BagDetail {
	if def.Bag == nil || piece == nil {
		return nil
	}
	d := &plife.BagDetail{Slot: def.Bag.Slot, Space: def.Bag.Space, ComfortKg: def.Bag.ComfortKg, HardKg: def.Bag.HardKg,
		Torn: def.Durability > 0 && piece.UsesLeft <= 0}
	for _, b := range st.bags {
		if b.piece.ID == piece.ID {
			d.Worn = true
		}
	}
	d.RepairCost = h.carry.rules.RepairCost(def.BasePrice, piece.UsesLeft, def.Durability)
	return d
}

// --- the free sack of the rollout -------------------------------------------

// StartingBagItem is the good every player who existed at the rollout is given
// once (ADR 0046 section 4.1 rule 3).
const StartingBagItem = "bag_sack"

// StartingGrantReference names the item journal's reference of the grant.
const StartingGrantReference = "starting_bag_grant"

// BagGranter gives the starting bag. It is a service of the admin tool, not a
// player command, and it is safe to run again and from two places at once.
type BagGranter struct {
	uow   application.UnitOfWork
	ids   IDGenerator
	items func() *content.Snapshot
	env   carryEnv
	now   func() time.Time
}

// NewBagGranter wires the granter.
func NewBagGranter(uow application.UnitOfWork, ids IDGenerator, source ContentSource, rules carry.Rules,
	clock gametime.Clock, now func() time.Time,
) *BagGranter {
	if uow == nil || ids == nil || source == nil {
		panic("handlers: NewBagGranter requires a unit of work, ids and content")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &BagGranter{uow: uow, ids: ids, items: source.Current, env: carryEnv{rules: rules, clock: clock}, now: now}
}

// GrantReport counts what a run did.
type GrantReport struct {
	Granted, Skipped int
}

// GrantOne gives one player the starting sack and puts it on, in one
// transaction, unless they have had it. It reports whether it gave one.
func (g *BagGranter) GrantOne(ctx context.Context, playerID string) (bool, error) {
	snap := g.items()
	def, ok := snap.ItemDef(StartingBagItem)
	if !ok || def.Bag == nil {
		return false, errors.Internal(stderrors.New("handlers: the content has no " + StartingBagItem))
	}
	gave := false
	err := g.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		gave = false
		// The owner's lock queues two runs on the same player: the second finds the grant written.
		if err := tx.Items().LockOwner(ctx, playerID); err != nil {
			return err
		}
		done, err := tx.Bags().Granted(ctx, playerID)
		if err != nil || done {
			return err
		}
		now := g.now()
		grantID := g.ids.NewID()
		made, err := bring(ctx, tx, snap, g.ids, nil, playerID, StartingBagItem, 1, -1, origin{
			kind: application.OriginGrant, reason: application.ItemGrant, refType: StartingGrantReference, refID: grantID,
		}, now)
		if err != nil {
			return err
		}
		if len(made) != 1 {
			return errors.Internal(stderrors.New("handlers: the starting bag was not made"))
		}
		fresh, err := tx.Bags().RecordGrant(ctx, playerID, made[0].ID, now)
		if err != nil {
			return err
		}
		if !fresh {
			return errors.Internal(stderrors.New("handlers: the starting bag was granted twice"))
		}
		gave = true
		// Put it on, unless the player already wears a bag in that slot.
		worn, err := tx.Bags().Worn(ctx, playerID)
		if err != nil {
			return err
		}
		for _, w := range worn {
			if w.Slot == def.Bag.Slot {
				return nil
			}
		}
		return tx.Bags().Wear(ctx, application.WornBag{PlayerID: playerID, Slot: def.Bag.Slot, PieceID: made[0].ID,
			WornThroughDay: g.env.clock.DayAt(now), WornAt: now})
	})
	return gave, err
}

// GrantAll gives the starting bag to every player who has not had it, a page at
// a time. maxPlayers 0 means all. It can be stopped and run again.
func (g *BagGranter) GrantAll(ctx context.Context, page, maxPlayers int, progress func(GrantReport)) (GrantReport, error) {
	if page < 1 {
		page = 100
	}
	var rep GrantReport
	after := ""
	for {
		var ids []string
		if err := g.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			ids, err = tx.Bags().WithoutGrant(ctx, after, page)
			return err
		}); err != nil {
			return rep, err
		}
		if len(ids) == 0 {
			return rep, nil
		}
		for _, id := range ids {
			gave, err := g.GrantOne(ctx, id)
			if err != nil {
				return rep, err
			}
			if gave {
				rep.Granted++
			} else {
				rep.Skipped++
			}
			if maxPlayers > 0 && rep.Granted >= maxPlayers {
				return rep, nil
			}
		}
		after = ids[len(ids)-1]
		if progress != nil {
			progress(rep)
		}
	}
}
