package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the port of research capacity and speed (migrations/0134_research_capacity; docs/adr/0048). The rules
// are internal/domain/research (pure) and handlers/village_research.go; the buildings, their staff posts and upkeep are
// content (building_functions.yml: library, laboratory, academy).

// The money and goods of the research day (ADR 0009 section 2 lists each reason).
const (
	// ReasonScholarWage pays a player scholar a day's wage from the settlement treasury.
	ReasonScholarWage Reason = "scholar_wage"
	// ReasonScholarWageNPC pays an NPC scholar of the labour pool a day's wage from the treasury into the sink: an NPC
	// is not a player, so the wage leaves the economy.
	ReasonScholarWageNPC Reason = "scholar_wage_npc"
	// ItemResearchUpkeep is an end: the paper, fuel and reagents a research building uses up on a working day.
	ItemResearchUpkeep ItemReason = "research_upkeep"
)

func init() {
	knownReasons[ReasonScholarWage] = struct{}{}
	knownReasons[ReasonScholarWageNPC] = struct{}{}
	itemReasons[ItemResearchUpkeep] = true
}

// ResearchDayReference is the ledger and item-journal reference type of a research day.
const ResearchDayReference = "research_days"

var (
	// ErrResearchPostHeld means the player already holds a scholar's post.
	ErrResearchPostHeld = errors.Sentinel(errors.CodeConflict, "application.ErrResearchPostHeld", "the player already holds a scholar's post")
	// ErrResearchPactOpen means the two settlements already have a pact, proposed or active.
	ErrResearchPactOpen = errors.Sentinel(errors.CodeConflict, "application.ErrResearchPactOpen", "the two settlements already have a research pact")
	// ErrResearchPactNotFound means no such pact.
	ErrResearchPactNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrResearchPactNotFound", "no such research pact")
)

// ResearchDayBuilding is one research building as one local day found it.
type ResearchDayBuilding struct {
	BuildingID, TypeCode string
	// Skills are the scholarship levels of the scholars on duty; Staffed whether the building worked that day.
	Skills  []int
	Staffed bool
	// Idle says why it did not work (no_scholars, no_wage, no_upkeep); empty when it did.
	Idle string
}

// ResearchDay is one local day of a settlement's research buildings: which were staffed and by whom, what the scholars
// were paid and how much the day's upkeep took out of the stock.
type ResearchDay struct {
	SettlementID string
	Day          int64
	// Buildings is how many research buildings stood; Staffed how many worked.
	Buildings, Staffed  int64
	ScholarsPlayer      int64
	ScholarsNPC         int64
	WagePlayer, WageNPC int64
	// LedgerPlayerTx and LedgerNPCTx are the wages' transactions, empty when none was paid.
	LedgerPlayerTx, LedgerNPCTx string
	UpkeepUnits                 int64
	At                          time.Time
	Rows                        []ResearchDayBuilding
}

// ResearchPost is a player's post as a scholar in a research building.
type ResearchPost struct {
	BuildingID   string
	PlayerID     string
	SettlementID string
	Since        time.Time
}

// Research pact statuses.
const (
	PactProposed = "proposed"
	PactActive   = "active"
	PactDeclined = "declined"
	PactEnded    = "ended"
)

// ResearchPact is a research-sharing pact between two settlements.
type ResearchPact struct {
	ID         string
	A, B       string
	Status     string
	ProposedBy string
	ProposedAt time.Time
	AnsweredAt *time.Time
	EndedAt    *time.Time
}

// Other returns the settlement on the other side of the pact.
func (p ResearchPact) Other(settlementID string) string {
	if p.A == settlementID {
		return p.B
	}
	return p.A
}

// ResearchRepository persists research capacity: the running projects, the research days, the scholars' posts, the
// settlement's experience and the sharing pacts. Reach it through Tx.Research.
type ResearchRepository interface {
	// LockSettlement serialises the research of one settlement for the rest of the transaction: the number of running
	// projects is checked against the capacity under it, so two replicas cannot both take the last slot.
	LockSettlement(ctx context.Context, settlementID string) error
	// Running lists the running projects, oldest first.
	Running(ctx context.Context, settlementID string) ([]SettlementResearch, error)

	// Day returns one day's row with its building rows, or nil; Last the latest settled day, or nil.
	Day(ctx context.Context, settlementID string, day int64) (*ResearchDay, error)
	Last(ctx context.Context, settlementID string) (*ResearchDay, error)
	// RecordDay is the fence: it writes the day and its rows, or reports false when the day already has one.
	RecordDay(ctx context.Context, d ResearchDay) (bool, error)

	// Posts lists the scholar posts of a settlement; PostOf the one a player holds, or nil.
	Posts(ctx context.Context, settlementID string) ([]ResearchPost, error)
	PostOf(ctx context.Context, playerID string) (*ResearchPost, error)
	// TakePost writes a post (ErrResearchPostHeld when the player has one); LeavePost removes the player's post and
	// reports whether there was one.
	TakePost(ctx context.Context, p ResearchPost) error
	LeavePost(ctx context.Context, playerID string) (bool, error)

	// Experience is the settlement's points by field. AddExperience adds (idempotency is the caller's: it is called in
	// the transaction that finishes the shift). SpendExperience takes points off a field when it has them.
	Experience(ctx context.Context, settlementID string) (map[string]int64, error)
	AddExperience(ctx context.Context, settlementID, field string, points int64, at time.Time) error
	SpendExperience(ctx context.Context, settlementID, field string, points int64, at time.Time) (bool, error)
	// AddDailyExperience adds points to a field from a day source (watch, health, market, research, teaching), once per
	// settlement, field, source and local day: the row of experience_days is the fence. It reports whether it added.
	AddDailyExperience(ctx context.Context, settlementID, field, source string, day, points int64, at time.Time) (bool, error)

	// HolderShares is, for every item some settlement holds, the share of settlements holding it in basis points
	// (the periodically refreshed aggregate of ADR 0031).
	HolderShares(ctx context.Context) (map[string]int64, error)
	// PartnersHolding counts the active pact partners of the settlement that hold the item.
	PartnersHolding(ctx context.Context, settlementID, code string) (int, error)

	// Pacts lists the settlement's pacts that are proposed or active (either side), newest first.
	Pacts(ctx context.Context, settlementID string) ([]ResearchPact, error)
	PactByID(ctx context.Context, id string) (*ResearchPact, error)
	// ProposePact writes a proposal (ErrResearchPactOpen when the two already have an open pact).
	ProposePact(ctx context.Context, p ResearchPact) error
	// AnswerPact accepts or declines a proposed pact; EndPact ends an active one. Each reports whether it changed
	// the row, so a repeated command changes nothing.
	AnswerPact(ctx context.Context, id string, accept bool, at time.Time) (bool, error)
	EndPact(ctx context.Context, id string, at time.Time) (bool, error)
}
