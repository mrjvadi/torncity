package application

import (
	"context"
	"time"
)

// The port of the stall keepers (migration 0139; docs/adr/0062): the NPC a stall owner hires so that his stall sells while
// he is away. The rules are handlers/stall_keeper.go and the trade settlement in handlers/market.go.

// ReasonStallKeeperWage pays the keeper his share of a sale made while the owner was away: out of the seller's proceeds
// (the buyer's escrow) into the sink, like the other NPC wages.
const ReasonStallKeeperWage Reason = "stall_keeper_wage"

func init() { knownReasons[ReasonStallKeeperWage] = struct{}{} }

// StallKeeper is one hire.
type StallKeeper struct {
	ID           string
	SettlementID string
	OwnerID      string
	ShareBPS     int64
	HiredAt      time.Time
	EndedAt      *time.Time
}

// StallKeeperRepository persists the hires. Reach it through Tx.StallKeepers.
type StallKeeperRepository interface {
	// Hire opens a hire, or reports false when the owner already has a keeper in the settlement.
	Hire(ctx context.Context, k StallKeeper) (bool, error)
	// End closes the owner's open hire, or reports false when there was none.
	End(ctx context.Context, settlementID, ownerID string, at time.Time) (bool, error)
	// OfOwner is the owner's open hire in the settlement, nil when none.
	OfOwner(ctx context.Context, settlementID, ownerID string) (*StallKeeper, error)
	// Open counts the open hires in the settlement (each holds a seat of the labour pool).
	Open(ctx context.Context, settlementID string) (int64, error)
	// OfOwners maps the owners that have an open hire to it.
	OfOwners(ctx context.Context, settlementID string, ownerIDs []string) (map[string]StallKeeper, error)
}
