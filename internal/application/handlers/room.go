package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/carry"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// This file is the one check "does it fit?" before goods are credited to a
// player's carried goods (storage and market audit F2, P1; ADR 0040 section 6.3,
// ADR 0046 section 4). Every door goods come in by calls it:
//
//   - A purchase or an order the player makes (a city shop, a company's
//     market, a bid on the player market) is REFUSED with the room it lacks:
//     "no room, no buy". A bid reserves its room at placement, so a fill never
//     fails afterwards.
//   - Goods that arrive without the player asking (a mission reward, a crime's
//     loot, a gift, a won auction, a returned stolen piece) are put in the
//     HOLDING SLOT when they do not fit (HoldClaim). They wait there, still the
//     player's, until room is made and they are claimed. Nothing is lost.
//   - Goods that already had their room (an escrowed listing coming back, a
//     bid's reserved fill) are delivered whatever happens: the room was held.
//     A player can be over capacity only because a bag tore or came off, never
//     because something came in.
//
// What takes room: everything carried (except a bag that is worn), everything
// in escrow (a listing keeps its place until it is sold), and the open bids'
// reserved units. The holding slot and the home store take none.

// roomReserveScan is how many of a player's latest orders are read to find the
// open bids; MaxOpen orders is far below it.
const roomReserveScan = 100

// carryEnvOf is the carry rules and the game clock a handler checks room with
// (its WithCarry). Without them it checks nothing: a wiring without the ADR
// 0046 block, and the older tests.
func carryEnvOf(rules carry.Rules, clock gametime.Clock) carryEnv {
	return carryEnv{rules: rules, clock: clock}
}

// noRoomError is a refusal for lack of room.
type noRoomError struct {
	Item string
	Qty  int64
	// NeedSpace and FreeSpace are in «جا»; NeedG and FreeG in grams.
	NeedSpace, FreeSpace int64
	NeedG, FreeG         int64
	// Heavy is true when the load, not the space, stopped it.
	Heavy bool
	cause error
}

func (e *noRoomError) Error() string { return "handlers: no room to carry: " + e.cause.Error() }
func (e *noRoomError) Unwrap() error { return e.cause }

// Short is the space the goods lack.
func (e *noRoomError) Short() int64 {
	if e.Heavy {
		return 0
	}
	return max(e.NeedSpace-e.FreeSpace, 0)
}

// asNoRoom reports whether err is a refusal for lack of room.
func asNoRoom(err error) (*noRoomError, bool) {
	var n *noRoomError
	if stderrors.As(err, &n) {
		return n, true
	}
	return nil, false
}

// reserve reads what keeps its room without being in the bags: the escrow and
// the open bids. It adds them to the state.
func (e carryEnv) reserve(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string, st *carryState) error {
	stacks, pieces, err := tx.Items().Holdings(ctx, playerID, application.HoldEscrow)
	if err != nil {
		return err
	}
	for _, s := range stacks {
		st.reservedSpace += s.Qty * snap.BulkOf(s.Item)
		st.reservedG += s.Qty * snap.WeightOf(s.Item)
	}
	for _, p := range pieces {
		st.reservedSpace += snap.BulkOf(p.Item)
		st.reservedG += snap.WeightOf(p.Item)
	}
	orders, err := tx.Market().PlayerOrders(ctx, playerID, roomReserveScan)
	if err != nil {
		return err
	}
	for _, o := range orders {
		if o.Status != application.OrderOpen || o.Side != "buy" {
			continue
		}
		left := max(o.Qty-o.Filled, 0)
		st.reservedSpace += left * snap.BulkOf(o.Item)
		st.reservedG += left * snap.WeightOf(o.Item)
	}
	return nil
}

// fit checks that qty more units of a good fit in the player's bags, as of now,
// and returns a *noRoomError when they do not. The owner's lock is taken first,
// so two purchases on two replicas cannot both take the last room.
func (e carryEnv) fit(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, code string, qty int64) error {
	if !e.enabled() || qty <= 0 {
		return nil
	}
	if err := tx.Items().LockOwner(ctx, playerID); err != nil {
		return err
	}
	st, err := e.load(ctx, tx, snap, playerID, time.Time{}, false)
	if err != nil {
		return err
	}
	need, grams := qty*snap.BulkOf(code), qty*snap.WeightOf(code)
	room := st.room(e.rules)
	if cause := room.Fits(need, grams); cause != nil {
		return &noRoomError{Item: code, Qty: qty, NeedSpace: need, FreeSpace: room.FreeSpace,
			NeedG: grams, FreeG: room.FreeG, Heavy: stderrors.Is(cause, carry.ErrTooHeavy), cause: cause}
	}
	return nil
}

// arrival says where goods that arrive go: the bags when they fit, else the
// holding slot.
func (e carryEnv) arrival(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, code string, qty int64) (string, error) {
	err := e.fit(ctx, tx, snap, playerID, code, qty)
	if err == nil {
		return application.HoldCarried, nil
	}
	if _, ok := asNoRoom(err); ok {
		return application.HoldClaim, nil
	}
	return "", err
}

// homeCapacity is the space of the player's own storing buildings: every
// finished house, cottage or shed they hold gives its personal_storage (ADR
// 0040 6.2). The home store is one per player; the goods are physically in the
// buildings, so it is reached from a settlement where the player holds one.
func homeCapacity(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string) (capacity int64, settlements map[string]bool, err error) {
	held, err := tx.Citizens().HeldBy(ctx, playerID)
	if err != nil {
		return 0, nil, err
	}
	settlements = map[string]bool{}
	for _, s := range held {
		for _, b := range s.Buildings {
			if b.Status != "complete" {
				continue
			}
			def, ok := snap.SettlementBuildingDef(b.TypeCode)
			if !ok {
				continue
			}
			for _, eff := range def.BuildingEffects() {
				if eff.Target == "personal_storage" && eff.Op == item.EffectAdd {
					capacity += eff.Value
					settlements[s.SettlementID] = true
				}
			}
		}
	}
	return capacity, settlements, nil
}

// homeUsed is the space what the player keeps at home takes.
func homeUsed(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string) (used int64, stacks []application.Stack, pieces []application.Piece, err error) {
	stacks, pieces, err = tx.Items().Holdings(ctx, playerID, application.HoldHome)
	if err != nil {
		return 0, nil, nil, err
	}
	for _, s := range stacks {
		used += s.Qty * snap.BulkOf(s.Item)
	}
	for _, p := range pieces {
		used += snap.BulkOf(p.Item)
	}
	return used, stacks, pieces, nil
}

// noRoomResponse answers a request refused for lack of room, with the screen
// that says what is missing; false when err is some other error. back is the
// address the screen leads back to.
func noRoomResponse(err error, lang string, snap *content.Snapshot, back string) (*presentation.Response, bool) {
	n, ok := asNoRoom(err)
	if !ok {
		return nil, false
	}
	nm := itemNamed(snap, n.Item)
	return economy.NoRoom(presentation.Ctx{Lang: lang}, economy.NoRoomView{
		Item: presentation.Named{Code: nm.Code, Name: nm.Name}, Qty: n.Qty,
		NeedSpace: n.NeedSpace, FreeSpace: n.FreeSpace, Short: n.Short(), NeedG: n.NeedG, FreeG: n.FreeG,
		Heavy: n.Heavy, Back: back,
	}), true
}
