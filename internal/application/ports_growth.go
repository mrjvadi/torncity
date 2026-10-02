package application

import (
	"context"
	"time"
)

// Phase G1 of ADR 0044 (docs/adr/0044-organic-growth-alliances-countries.md
// section 11): what a settlement has, read for the capability computation, and
// the meter of the places where that answer differs from the tier's.

// SettlementStanding is the rows the capability computation reads: every
// building of the settlement (any status) and everything it knows.
type SettlementStanding struct {
	Buildings []SettlementBuildingInstance
	Knowledge []SettlementKnowledgeOwned
}

// Founded reports a settlement a group founded: a content city (the neutral
// city) has neither rows, and capabilities are not computed for it.
func (s SettlementStanding) Founded() bool { return len(s.Buildings)+len(s.Knowledge) > 0 }

// GrowthStandingReader reads a settlement's standing OUTSIDE a transaction,
// for the gates that hold no unit of work (the service gate). A gate that is
// already inside a unit of work reads tx.SettlementBuildings and
// tx.SettlementKnowledge instead, never this: no pool call inside uow.Do.
type GrowthStandingReader interface {
	Standing(ctx context.Context, settlementID string) (SettlementStanding, error)
}

// GrowthDisagreement is one place the tier answer and the capability answer
// differ: the row of growth_disagreements (migration 0101).
type GrowthDisagreement struct {
	SettlementID string
	// Site is the gate that asked; Kind and Code the availability entry.
	Site, Kind, Code string
	TierAnswer       bool
	CapabilityAnswer bool
	// Missing is what the capabilities lack, "kind:code" pairs, comma-joined.
	Missing string
	// Count is how many times this process saw it since its last flush.
	Count int64
	At    time.Time
}

// GrowthDisagreementStore keeps the meter's rows. Writes go through the pool,
// from a background flush, never from inside a unit of work.
type GrowthDisagreementStore interface {
	// Record adds the counts, one row per (settlement, site, entry), idempotent
	// in shape: a repeat raises seen_count and moves last_seen_at.
	Record(ctx context.Context, rows []GrowthDisagreement) error
	// List reads the rows, most seen first, at most limit of them.
	List(ctx context.Context, limit int) ([]GrowthDisagreementRow, error)
}

// GrowthDisagreementRow is a stored disagreement.
type GrowthDisagreementRow struct {
	GrowthDisagreement
	FirstSeen time.Time
	LastSeen  time.Time
}
