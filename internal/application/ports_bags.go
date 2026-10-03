package application

import (
	"context"
	"time"
)

// This file holds the port of worn bags (migrations/0108_bags,
// 0110_starting_bag_grant; docs/adr/0046 section 4): which of a player's pieces
// they have put on, in which slot, and the starting grant of the rollout. The
// rules are internal/domain/carry; the wear points of a bag are its piece's
// uses_left (ItemRepository.SetUses).

// WornBag is a bag a player has put on.
type WornBag struct {
	PlayerID string
	// Slot is carry.SlotBelt or carry.SlotBack.
	Slot    string
	PieceID string
	// WornThroughDay is the last game day whose wear was charged.
	WornThroughDay int64
	WornAt         time.Time
}

// BagRepository persists worn bags. Reach it through Tx.Bags, so a bag is
// worn, mended or taken off with the goods and money that moved with it.
type BagRepository interface {
	// Worn returns the player's worn bags, belt first.
	Worn(ctx context.Context, playerID string) ([]WornBag, error)
	// Wear puts a piece on in a slot, replacing whatever was worn there.
	Wear(ctx context.Context, b WornBag) error
	// TakeOff removes the bag in a slot; nothing worn there is no failure.
	TakeOff(ctx context.Context, playerID, slot string) error
	// TakeOffPiece removes a piece wherever it is worn: a piece that leaves
	// its owner (given, dropped, sold, escrowed) is not worn any more.
	TakeOffPiece(ctx context.Context, pieceID string) error
	// SetWornThrough records that a slot's wear is charged through a day.
	SetWornThrough(ctx context.Context, playerID, slot string, day int64) error

	// Granted reports whether the player has had the starting bag.
	Granted(ctx context.Context, playerID string) (bool, error)
	// RecordGrant is the fence of the starting grant: it writes the player's
	// row, or reports false when they already have one.
	RecordGrant(ctx context.Context, playerID, pieceID string, at time.Time) (bool, error)
	// WithoutGrant lists up to limit players who have not had the starting
	// bag, oldest first, after the given player id (keyset paging).
	WithoutGrant(ctx context.Context, after string, limit int) ([]string, error)
}
