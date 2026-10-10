package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/land"
)

// The port of the land model (migration 0143; docs/adr/0065): the deltas of a settlement's lots and its saplings. What
// stands on an untouched lot is domain/land's pure function, not stored.

// LandChange is an addition to a lot's delta.
type LandChange struct {
	Trees, Rocks int
	// RockWork is set absolutely when SetRockWork is true.
	RockWork    int
	SetRockWork bool
	// Anchor, when set, starts natural regrowth from it if the lot has none running.
	Anchor *time.Time
}

// LandRepository persists the land. Reach it through Tx.Land.
type LandRepository interface {
	// Rows lists the lots of the settlement that were ever touched.
	Rows(ctx context.Context, settlementID string) ([]land.Delta, error)
	// Saplings lists the saplings of the settlement.
	Saplings(ctx context.Context, settlementID string) ([]land.Sapling, error)
	// Lock serialises the choosers of a target of one settlement (the transaction-scoped advisory lock).
	Lock(ctx context.Context, settlementID string) error
	// Apply adds a change to a lot's delta, creating the row.
	Apply(ctx context.Context, settlementID string, p land.Pos, c LandChange, at time.Time) error
	// Plant stores a sapling.
	Plant(ctx context.Context, settlementID string, p land.Pos, id, shiftID string, plantedAt, readyAt time.Time) error
	// Order sets or clears the clearing orders of a lot.
	Order(ctx context.Context, settlementID string, p land.Pos, trees, rocks bool, by string, at time.Time) error
}

// LandReader is what the layout needs of the land.
type LandReader interface {
	Rows(ctx context.Context, settlementID string) ([]land.Delta, error)
	Saplings(ctx context.Context, settlementID string) ([]land.Sapling, error)
}

// What a shift did to the land (settlement_shifts.land_kind).
const (
	LandKindTree    = "tree"
	LandKindRock    = "rock"
	LandKindSapling = "sapling"
)

// ReasonClearingFee is what the owner of a citizen's lot pays for the shift of the village's crew that clears it: the wage of the
// shift, out of his cash into the treasury (docs/adr/0065).
const ReasonClearingFee Reason = "clearing_fee"

func init() { knownReasons[ReasonClearingFee] = struct{}{} }
