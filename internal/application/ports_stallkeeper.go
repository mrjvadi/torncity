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

// ReasonStallKeeperDayWage is the fixed day wage of a keeper hired by the day: out of the owner's cash into the sink, once for
// each local day the keeper holds the seat (ADR 0062 addendum).
const ReasonStallKeeperDayWage Reason = "stall_keeper_day_wage"

func init() {
	knownReasons[ReasonStallKeeperWage] = struct{}{}
	knownReasons[ReasonStallKeeperDayWage] = struct{}{}
}

// The two ways a keeper is paid, chosen by the owner when he hires.
const (
	KeeperPayShare = "share"
	KeeperPayWage  = "wage"
)

// Why a hire ended.
const (
	KeeperEndedDismissed  = "dismissed"
	KeeperEndedWageUnpaid = "wage_unpaid"
)

// StallKeeperWageReference is the reference type of a day wage's ledger transaction.
const StallKeeperWageReference = "stall_keepers"

// StallKeeper is one hire.
type StallKeeper struct {
	ID           string
	SettlementID string
	OwnerID      string
	// Pay is KeeperPayShare (ShareBPS of each sale made in the owner's absence) or KeeperPayWage (DailyWage minor units each
	// local day); the other number is 0.
	Pay         string
	ShareBPS    int64
	DailyWage   int64
	HiredAt     time.Time
	EndedAt     *time.Time
	EndedReason string
}

// StallKeeperRepository persists the hires. Reach it through Tx.StallKeepers.
type StallKeeperRepository interface {
	// Hire opens a hire, or reports false when the owner already has a keeper in the settlement.
	Hire(ctx context.Context, k StallKeeper) (bool, error)
	// End closes the owner's open hire with the reason, or reports false when there was none.
	End(ctx context.Context, settlementID, ownerID string, at time.Time, reason string) (bool, error)
	// LastEnded is the owner's latest ended hire in the settlement, nil when none.
	LastEnded(ctx context.Context, settlementID, ownerID string) (*StallKeeper, error)
	// OpenWages lists the open hires paid by the day.
	OpenWages(ctx context.Context, settlementID string) ([]StallKeeper, error)
	// Takings sums what the owner's stalls sold while he was away since the instant (sold: the notional of the trades marked
	// away; cut: the keeper's share of them plus the day wages charged in that time).
	Takings(ctx context.Context, settlementID, ownerID string, since time.Time) (sold, cut int64, err error)
	// ClaimWage is the fence of a day wage: it records the keeper's wage for the local day and reports true once, false
	// when the day was already charged. The caller posts the ledger transaction it names.
	ClaimWage(ctx context.Context, k StallKeeper, day, amount int64, ledgerTx string, at time.Time) (bool, error)
	// OfOwner is the owner's open hire in the settlement, nil when none.
	OfOwner(ctx context.Context, settlementID, ownerID string) (*StallKeeper, error)
	// Open counts the open hires in the settlement (each holds a seat of the labour pool).
	Open(ctx context.Context, settlementID string) (int64, error)
	// OfOwners maps the owners that have an open hire to it.
	OfOwners(ctx context.Context, settlementID string, ownerIDs []string) (map[string]StallKeeper, error)
}
