package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the K2/W5 write paths (docs/adr/0031-knowledge-and-
// village-progression.md sections 4, 5, 6): what a settlement knows, its
// literacy, its running research, and its buildings under construction.
// Reached through Tx.SettlementKnowledge and Tx.SettlementBuildings so each
// commits with the money and materials that moved for it, the same rule
// every other port in this package already keeps.

// Village-level game_actions.action_type values (ADR 0031 section 6):
// researching a knowledge item, the settlement's own literacy diffusion
// tick (self-rescheduling, never a one-shot), and a building's
// construction reaching its end. internal/workers/scheduler/routes.go
// carries the identical string literals under its own names — the two
// layers never import each other, the same duplication ResearchActionType
// (ports_production.go) and ActionTypeResearch (routes.go) already accept.
const (
	SettlementResearchActionType = "settlement_research"
	SettlementTeachActionType    = "settlement_teach"
	SettlementBuildActionType    = "settlement_build"
)

// Settlement knowledge/construction sentinels.
var (
	// ErrKnowledgeNotFound means the code names no item the active content
	// declares.
	ErrKnowledgeNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrKnowledgeNotFound", "the settlement does not know of this")

	// ErrSettlementResearchBusy means the settlement already has a research
	// project running (ADR 0031 section 4.2: one at a time).
	ErrSettlementResearchBusy = errors.Sentinel(errors.CodeConflict,
		"application.ErrSettlementResearchBusy", "the settlement is already researching something")

	// ErrSettlementAlreadyResearched means this exact code was already
	// researched (or is being researched) by this settlement once before.
	ErrSettlementAlreadyResearched = errors.Sentinel(errors.CodeConflict,
		"application.ErrSettlementAlreadyResearched", "the settlement has already researched this")

	// ErrSettlementResearchNotFound means the research row named does not
	// exist — a stale or forged reference.
	ErrSettlementResearchNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrSettlementResearchNotFound", "no such research")

	// ErrBuildingTypeNotFound means the code names no building the active
	// content declares.
	ErrBuildingTypeNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrBuildingTypeNotFound", "no such building")

	// ErrLotOccupied means the chosen lot is already claimed — a
	// concurrent placement won a race CanPlace's own in-memory check could
	// not see.
	ErrLotOccupied = errors.Sentinel(errors.CodeConflict,
		"application.ErrLotOccupied", "that lot is already occupied")

	// ErrBuildingNotFound means the settlement building row named does not
	// exist.
	ErrBuildingNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrBuildingNotFound", "no such building stands here")

	// ErrBuildingNotDemolishable means the building is not in a state
	// demolish may act on (already demolished, or not yet complete —
	// ADR 0028 section 6.2: a QUEUED placement is cancelled, never
	// "demolished"; this port does not implement queued-cancellation, K2/W5
	// go straight to building on a successful placement, see
	// internal/application/handlers/village.go's own note).
	ErrBuildingNotDemolishable = errors.Sentinel(errors.CodeConflict,
		"application.ErrBuildingNotDemolishable", "that building cannot be demolished")
)

// SettlementKnowledgeOwned is one settlement_knowledge_owned row.
type SettlementKnowledgeOwned struct {
	SettlementID string
	Code         string
	// AcquiredVia is "founding", "bought", "researched" or "licensed".
	AcquiredVia string
	AcquiredAt  time.Time
}

// SettlementResearch is one settlement_research row.
type SettlementResearch struct {
	ID                  string
	SettlementID        string
	Code                string
	Status              string // "running" | "done"
	Cost                int64
	LedgerTransactionID string
	GameActionID        string
	StartedBy           string
	StartedAt           time.Time
	FinishAt            time.Time
	CompletedAt         *time.Time
}

// SettlementBuildingInstance is one settlement_buildings row (migration
// 0043, extended by 0046 with the demolished status).
type SettlementBuildingInstance struct {
	ID           string
	SettlementID string
	TypeCode     string
	LotX, LotY   int
	// Status is "queued", "building", "complete" or "demolished". K2/W5's
	// own write path never writes "queued" — cost is paid and construction
	// starts in the same moment a placement is accepted (ADR 0028 section
	// 6.3: "all or nothing... at the moment the build is queued" is read
	// here as "queued IS starting", since this build does not yet offer a
	// separate pay-now-build-later step); "queued" is kept in the status
	// vocabulary only because the founding kit's own placements and a
	// later phase may still use it.
	Status       string
	QueuedAt     time.Time
	CompletedAt  *time.Time
	DemolishedAt *time.Time
}

// Complete reports whether the building has finished construction (whether
// or not it has since been demolished — a demolished building WAS built).
func (b SettlementBuildingInstance) Complete() bool {
	return b.Status == "complete" || b.Status == "demolished"
}

// SettlementKnowledgeRepository is the transactional port behind ADR 0031's
// knowledge state: what a settlement owns, its literacy, its running
// research, and the scarcity price's own periodically refreshed aggregate.
type SettlementKnowledgeRepository interface {
	// Owned lists everything the settlement holds, in no particular order.
	Owned(ctx context.Context, settlementID string) ([]SettlementKnowledgeOwned, error)

	// Grant records the settlement as holding code, idempotently: a second
	// grant of the same code to the same settlement is a no-op (ON
	// CONFLICT DO NOTHING), which is what makes the founding grant and a
	// redelivered research/buy command safe under at-least-once delivery.
	// fresh reports whether this call is what actually wrote the row.
	Grant(ctx context.Context, g SettlementKnowledgeOwned) (fresh bool, err error)

	// RunningResearch returns the settlement's one running project, or nil.
	RunningResearch(ctx context.Context, settlementID string) (*SettlementResearch, error)

	// StartResearch writes a new running research row. It refuses
	// ErrSettlementResearchBusy (another is already running) and
	// ErrSettlementAlreadyResearched (this code was researched before).
	StartResearch(ctx context.Context, r SettlementResearch) error

	// Research returns one research row by id, or ErrSettlementResearchNotFound.
	Research(ctx context.Context, id string) (*SettlementResearch, error)

	// FinishResearch marks a running research done, idempotently: called
	// again on an already-done row, it changes nothing and reports no
	// error.
	FinishResearch(ctx context.Context, id string, at time.Time) error

	// EnsureLiteracy creates the settlement's literacy row at 0 if it does
	// not exist yet (ON CONFLICT DO NOTHING) — called once, at founding.
	EnsureLiteracy(ctx context.Context, settlementID string, at time.Time) error

	// Literacy returns the settlement's own literacy_share_bps and the
	// action id its next teach tick is pending under (empty if none is
	// scheduled — should not happen once EnsureLiteracy/the first schedule
	// has run, but read defensively rather than assumed).
	Literacy(ctx context.Context, settlementID string) (shareBPS int, pendingActionID string, err error)

	// AdvanceLiteracy applies one diffusion step's already-computed result
	// and stamps the NEXT teach tick's action id as the new fence, in one
	// statement: the settlement_teach handler computes newShareBPS with
	// settlementknowledge.AdvanceLiteracy (a pure function, this package
	// does not reach for it) and hands the two numbers here. Idempotent
	// under redelivery via the caller's own pendingActionID check before
	// calling this (see the handler).
	AdvanceLiteracy(ctx context.Context, settlementID string, newShareBPS int, nextActionID string, at time.Time) error

	// HoldersShareBPS reads code's holders/total_settlements from the
	// periodically refreshed aggregate (settlement_knowledge_holder_counts),
	// in basis points — settlementknowledge.HoldersShareBPS's own output,
	// precomputed. Zero (no row yet, or code never held) is the correct
	// "as scarce as it gets" input ScarcityPrice already treats specially.
	HoldersShareBPS(ctx context.Context, code string) (int64, error)

	// RefreshHolderCounts recomputes the whole aggregate from
	// settlement_knowledge_owned in one statement, upserting every code's
	// row. Idempotent and safe to call from as many replicas, as often, as
	// call it — see settlement_knowledge_holder_counts' own migration
	// comment for why this is not a hot row despite being refreshed often.
	RefreshHolderCounts(ctx context.Context, at time.Time) error
}

// SettlementBuildingRepository is the transactional port behind ADR 0028
// section 6's construction queue for a settlement's own lot grid.
type SettlementBuildingRepository interface {
	// List returns every building (any status) a settlement has, for the
	// grid's own occupancy and for the build-progress/overview screens.
	List(ctx context.Context, settlementID string) ([]SettlementBuildingInstance, error)

	// Get returns one building by id, or ErrBuildingNotFound.
	Get(ctx context.Context, id string) (*SettlementBuildingInstance, error)

	// RunningCount is how many of the settlement's buildings are status
	// "building" right now — ADR 0028 section 6.3's concurrent-build cap
	// input.
	RunningCount(ctx context.Context, settlementID string) (int, error)

	// Place writes a new building row, status "building" (see
	// SettlementBuildingInstance.Status's own doc: cost is paid and
	// construction starts in the same command). Refuses ErrLotOccupied if
	// the lot(s) are already claimed — the database's own backstop behind
	// CanPlace's in-memory check, exactly the founding kit's own
	// settlement_buildings_lot_unique constraint.
	Place(ctx context.Context, b SettlementBuildingInstance) error

	// Complete marks a building complete, idempotently: called again on an
	// already-complete (or demolished) row, it changes nothing.
	Complete(ctx context.Context, id string, at time.Time) error

	// Demolish marks a complete building demolished. Refuses
	// ErrBuildingNotDemolishable for anything not currently "complete".
	Demolish(ctx context.Context, id string, at time.Time) error
}
