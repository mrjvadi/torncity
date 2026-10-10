package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/farm"
)

// The port of the farm cycle (migration 0144; docs/adr/0067): the open crop of each farm and the settlement's statute of the
// miller's toll. The stage of a crop is read from these rows and the clock (domain/farm), never ticked.

// FarmRules are the settings of the farm cycle (config settlement.farm_*).
type FarmRules struct {
	// RuleAt is when the farms began to work in cycles: sow, tend, harvest. A farm that stood before it keeps its flat shift
	// (12 wheat) for GraceDays real days after it; the zero value switches the cycle off (every farm keeps the flat shift).
	RuleAt    time.Time
	GraceDays int64
}

// Enabled reports whether the farm cycle is on.
func (r FarmRules) Enabled() bool { return !r.RuleAt.IsZero() }

// GraceUntil is when the flat shift of a farm that stood before the rule date ends; the zero time when there is no grace.
func (r FarmRules) GraceUntil() time.Time {
	if r.RuleAt.IsZero() {
		return time.Time{}
	}
	return r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0)))
}

// OnCycle reports whether the farm works in cycles at now: the rule date has come, and the farm was raised after it or its
// grace is over.
func (r FarmRules) OnCycle(b SettlementBuildingInstance, now time.Time) bool {
	if !r.Enabled() || now.Before(r.RuleAt) {
		return false
	}
	since := b.QueuedAt
	if b.CompletedAt != nil {
		since = *b.CompletedAt
	}
	if !since.Before(r.RuleAt) {
		return true
	}
	return !now.Before(r.GraceUntil())
}

// FarmRepository persists the crops. Reach it through Tx.Farm.
type FarmRepository interface {
	// Lock serialises the workers of one farm (the transaction-scoped advisory lock).
	Lock(ctx context.Context, buildingID string) error
	// Open is the crop in the ground of the farm; nil when there is none.
	Open(ctx context.Context, buildingID string) (*farm.Cycle, error)
	// Latest is the farm's latest crop, closed or not; nil when it never had one.
	Latest(ctx context.Context, buildingID string) (*farm.Cycle, error)
	// Insert stores a new crop.
	Insert(ctx context.Context, c farm.Cycle) error
	// Save writes the counters of a crop.
	Save(ctx context.Context, c farm.Cycle) error
	// Close ends a crop.
	Close(ctx context.Context, id string, at time.Time, result string) error
	// OpenIn lists the open crops of the settlement.
	OpenIn(ctx context.Context, settlementID string) ([]farm.Cycle, error)
	// MillToll is the settlement's statute of the miller's toll; set is false when the settlement has never set one.
	MillToll(ctx context.Context, settlementID string) (bps int64, set bool, err error)
	// SetMillToll stores the statute.
	SetMillToll(ctx context.Context, settlementID string, bps int64, by string, at time.Time) error
}

// FarmReader is what the layout needs of the crops.
type FarmReader interface {
	OpenIn(ctx context.Context, settlementID string) ([]farm.Cycle, error)
}

// FarmRipeActionType is the game action that wakes the crews waiting for a crop when it ripens; its reference is the crop.
const (
	FarmRipeActionType = "farm_ripe"
	FarmReference      = "farm_cycle"
)

// What a farm shift did (settlement_shifts.farm_phase).
const (
	FarmPhaseSow     = "sow"
	FarmPhaseTend    = "tend"
	FarmPhaseHarvest = "harvest"
	// FarmPhaseGrind is a mill shift that ground a citizen's own grain (settlement_shifts.custom_for).
	FarmPhaseGrind = "grind"
)
