package handlers

import (
	"context"
	"encoding/hex"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Goods, as every handler that moves them does it: what a player carries,
// bringing new units into the world from a recorded origin, and ending them.
// Every movement goes through Tx.Items and its journal.

// Dice is a uniform source of whole numbers, as the crime engine takes it:
// Roll(n) in [0, n). Production passes crypto/rand.
type Dice interface {
	Roll(n int64) int64
}

// serialLength is how many characters a piece's serial has: short enough for
// a button's address, long enough never to repeat by chance.
const serialLength = 10

// newSerial draws a piece's serial from a fresh id: upper-case hex.
func newSerial(ids IDGenerator) string {
	raw := strings.ReplaceAll(ids.NewID(), "-", "")
	if b, err := hex.DecodeString(raw); err == nil && len(b) >= serialLength/2 {
		return strings.ToUpper(hex.EncodeToString(b[len(b)-serialLength/2:]))
	}
	return strings.ToUpper(raw[:min(len(raw), serialLength)])
}

// carried reads what a player carries, as the inventory rules take it.
func carried(ctx context.Context, tx application.Tx, playerID string) ([]application.Stack, []application.Piece, []inventory.Holding, error) {
	stacks, pieces, err := tx.Items().Holdings(ctx, playerID, application.HoldCarried)
	if err != nil {
		return nil, nil, nil, err
	}
	return stacks, pieces, holdings(stacks, pieces), nil
}

// holdings converts stacks and pieces to the inventory rules' holdings.
func holdings(stacks []application.Stack, pieces []application.Piece) []inventory.Holding {
	out := make([]inventory.Holding, 0, len(stacks)+len(pieces))
	for _, s := range stacks {
		out = append(out, inventory.Holding{Item: s.Item, Qty: s.Qty})
	}
	for _, p := range pieces {
		out = append(out, inventory.Holding{Item: p.Item, Instance: p.ID, Qty: 1, UsesLeft: p.UsesLeft})
	}
	return out
}

// origin is where new goods come from: the row that brings them into the
// world, recorded as the journal's reference and a piece's provenance.
type origin struct {
	kind   string // application.OriginSupply, OriginLoot, OriginGrant
	reason application.ItemReason
	// refType and refID name the row: a shop sale, a crime, a grant.
	refType, refID string
}

// provenance is the item rule's view of an origin.
func (o origin) provenance() item.Provenance {
	switch o.kind {
	case application.OriginSupply:
		return item.Provenance{SupplyID: o.refID}
	case application.OriginLoot:
		return item.Provenance{LootID: o.refID}
	}
	return item.Provenance{RewardGrantID: o.refID}
}

// bring brings qty units of a good into the world, carried by playerID: a
// stack grows, or qty new pieces are made at a quality drawn from the good's
// range (dice may be nil: the middle of the range). It returns the pieces
// made. Nothing is made without an origin (item.NewInstance refuses it).
func bring(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, dice Dice,
	playerID, code string, qty int64, quality int, o origin, now time.Time,
) ([]application.Piece, error) {
	def, ok := snap.ItemDef(code)
	if !ok {
		return nil, errors.Internal(stderrors.New("handlers: goods named that the content does not have: " + code))
	}
	move := application.ItemMove{
		Item: code, Qty: qty, To: playerID, ToHolding: application.HoldCarried,
		Reason: o.reason, ReferenceType: o.refType, ReferenceID: o.refID, At: now,
	}
	if def.Form != string(inventory.Unique) {
		move.ID = ids.NewID()
		return nil, tx.Items().Move(ctx, move)
	}
	arch, ok := snap.Archetype(def.Archetype)
	if !ok {
		return nil, errors.Internal(stderrors.New("handlers: good names an archetype the content does not have: " + code))
	}
	lo, hi := def.QualityRange()
	var made []application.Piece
	for i := int64(0); i < qty; i++ {
		q := quality
		if q < 0 {
			q = (lo + hi) / 2
			if dice != nil && hi > lo {
				q = lo + int(dice.Roll(int64(hi-lo+1)))
			}
		}
		serial := newSerial(ids)
		inst, err := item.NewInstance(arch, serial, "", q, o.provenance())
		if err != nil {
			return nil, errors.Internal(err)
		}
		p := application.Piece{
			ID: ids.NewID(), Serial: inst.Serial, Item: code, Archetype: inst.Archetype, Quality: inst.Quality,
			UsesLeft: def.Durability, OwnerID: playerID, Holding: application.HoldCarried,
			Origin: o.kind, OriginRef: o.refID, CreatedAt: now,
		}
		m := move
		m.ID, m.Qty = ids.NewID(), 1
		if err := tx.Items().CreatePiece(ctx, p, m); err != nil {
			return nil, err
		}
		made = append(made, p)
	}
	return made, nil
}

// itemNamed is a good as a screen names it.
func itemNamed(snap *content.Snapshot, code string) screens.Named {
	def, _ := snap.ItemDef(code)
	return screens.Named{Code: code, Name: def.Name}
}

// isPieceRef reports whether a button's argument names a piece (its serial)
// rather than a good (its code): a serial is upper case.
func isPieceRef(s string) bool {
	return len(s) == serialLength && strings.ToUpper(s) == s
}
