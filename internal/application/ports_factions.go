package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of factions (migrations/0023,
// docs/adr/0023-health-missions-factions.md): a faction, its members and
// ranks, its invitations and applications, and its organised crimes. Its
// bank is a faction_treasury account of the ledger. The rules are
// internal/domain/faction; the parameters are content (factions.yml).

// FactionReference is the reference_type of every faction money movement;
// the reference id is the faction's.
const FactionReference = "factions"

// FactionOperationReference is the reference_type of an organised crime's
// scheduled action and of its take.
const FactionOperationReference = "faction_operations"

// FactionCrimeActionType is an organised crime reaching its end:
// faction.resolve. It must stay equal to the action type the scheduler
// routes.
const FactionCrimeActionType = "faction_crime"

// Statuses, exactly as the migration's CHECKs spell them.
const (
	FactionActive    = "active"
	FactionDisbanded = "disbanded"

	RequestInvite    = "invite"
	RequestApply     = "apply"
	RequestPending   = "pending"
	RequestAccepted  = "accepted"
	RequestDeclined  = "declined"
	RequestWithdrawn = "withdrawn"

	HeistGathering = "gathering"
	HeistRunning   = "running"
	HeistSucceeded = "succeeded"
	HeistEscaped   = "escaped"
	HeistCaught    = "caught"
	HeistCalledOff = "called_off"
)

// Faction is a factions row.
type Faction struct {
	ID               string
	Code             string
	Name             string
	NameKey          string
	CityID           string
	LeaderID         string
	Status           string
	FoundingFee      int64
	RegistrationTxID string
	// ChatID, ChatBotID and ChatLanguage are the Telegram group the faction
	// is linked to, the bot that serves it and its language; ChatID 0 for
	// none.
	ChatID         int64
	ChatBotID      string
	ChatLanguage   string
	ContentVersion int
	FoundedAt      time.Time
	DisbandedAt    *time.Time
	UpdatedAt      time.Time
}

// Active reports whether the faction stands.
func (f Faction) Active() bool { return f.Status == FactionActive }

// FactionMember is a faction_members row.
type FactionMember struct {
	PlayerID  string
	FactionID string
	Rank      string
	JoinedAt  time.Time
}

// FactionLine is a faction as a city's list shows it.
type FactionLine struct {
	Faction Faction
	Members int
}

// FactionRequest is a faction_requests row: an invitation or an
// application.
type FactionRequest struct {
	ID        string
	No        int64
	FactionID string
	PlayerID  string
	Kind      string
	Status    string
	ByPlayer  string
	CreatedAt time.Time
	DecidedBy string
	DecidedAt *time.Time
}

// FactionOperation is a faction_operations row: an organised crime.
type FactionOperation struct {
	ID             string
	No             int64
	FactionID      string
	Crime          string
	CityID         string
	Place          string
	PlannedBy      string
	Status         string
	ChanceBPS      int
	Take           int64
	FactionCut     int64
	GameActionID   string
	GatherUntil    time.Time
	LaunchedAt     *time.Time
	ResolvesAt     *time.Time
	ResolvedAt     *time.Time
	ContentVersion int
	CreatedAt      time.Time
}

// Open reports whether it still gathers or runs.
func (o FactionOperation) Open() bool {
	return o.Status == HeistGathering || o.Status == HeistRunning
}

// CrewMember is a faction_operation_crew row.
type CrewMember struct {
	OperationID string
	PlayerID    string
	Rank        string
	Share       int64
	JoinedAt    time.Time
}

// FactionRepository persists factions. Reach it through Tx.Factions, so a
// faction changes with the money that moved for it.
type FactionRepository interface {
	// Create records a faction and its founding leader. A name another
	// active faction holds is ErrFactionNameTaken; a founder already in a
	// faction is ErrAlreadyInFaction.
	Create(ctx context.Context, f Faction, leader FactionMember) error
	// ByCode returns a faction by its public code, or ErrFactionNotFound.
	ByCode(ctx context.Context, code string) (*Faction, error)
	// ByID returns a faction, or ErrFactionNotFound.
	ByID(ctx context.Context, id string) (*Faction, error)
	// ByChat returns the active faction linked to a group, or
	// ErrFactionNotFound.
	ByChat(ctx context.Context, chatID int64) (*Faction, error)
	// Lock returns a faction locked for the rest of the transaction.
	Lock(ctx context.Context, id string) (*Faction, error)
	// Save writes a faction back.
	Save(ctx context.Context, f Faction) error
	// List lists a city's active factions with their member counts, in name
	// order; with cityID empty, every city's.
	List(ctx context.Context, cityID string, limit int) ([]FactionLine, error)
	// CodeTaken reports whether a public code is in use.
	CodeTaken(ctx context.Context, code string) (bool, error)

	// Membership returns the player's membership, or ErrNotInFaction.
	Membership(ctx context.Context, playerID string) (*FactionMember, error)
	// Members lists a faction's members, by rank then joining.
	Members(ctx context.Context, factionID string) ([]FactionMember, error)
	// AddMember adds a member; a player already in a faction is
	// ErrAlreadyInFaction.
	AddMember(ctx context.Context, m FactionMember) error
	// SetRank changes a member's rank.
	SetRank(ctx context.Context, factionID, playerID, rank string) error
	// RemoveMember removes a member, or returns ErrNotInFaction.
	RemoveMember(ctx context.Context, factionID, playerID string) error

	// Request records a pending invitation or application; one already
	// pending for the pair is ErrRequestPending.
	Request(ctx context.Context, r FactionRequest) (FactionRequest, error)
	// RequestByNo returns one request, locked, or ErrRequestNotFound.
	RequestByNo(ctx context.Context, no int64) (*FactionRequest, error)
	// DecideRequest moves a pending request to status, or returns
	// ErrRequestNotFound when it is not pending.
	DecideRequest(ctx context.Context, id, status, by string, at time.Time) error
	// Pending lists a faction's pending requests, oldest first.
	Pending(ctx context.Context, factionID string) ([]FactionRequest, error)
	// PendingOf lists a player's pending requests, oldest first.
	PendingOf(ctx context.Context, playerID string) ([]FactionRequest, error)
	// WithdrawPendingOf withdraws every pending request of a player.
	WithdrawPendingOf(ctx context.Context, playerID string, at time.Time) error

	// OpenOperation returns a faction's organised crime gathering or
	// running, or ErrNoOperation.
	OpenOperation(ctx context.Context, factionID string) (*FactionOperation, error)
	// Operation returns one operation, locked, or ErrNoOperation.
	Operation(ctx context.Context, id string) (*FactionOperation, error)
	// Plan records a gathering operation with its number. A faction with
	// one open already is ErrOperationOpen.
	Plan(ctx context.Context, o FactionOperation) (FactionOperation, error)
	// SaveOperation writes an operation back.
	SaveOperation(ctx context.Context, o FactionOperation) error
	// AddCrew adds a crew member; one already in is ErrAlreadyInCrew.
	AddCrew(ctx context.Context, c CrewMember) error
	// RemoveCrew drops a crew member who cannot go when the job is launched.
	RemoveCrew(ctx context.Context, operationID, playerID string) error
	// Crew lists an operation's crew, in joining order.
	Crew(ctx context.Context, operationID string) ([]CrewMember, error)
	// SetShare records a crew member's share of the take.
	SetShare(ctx context.Context, operationID, playerID string, share int64) error
	// RunningCrew returns the open operation a player is in the crew of,
	// or ErrNoOperation.
	RunningCrew(ctx context.Context, playerID string) (*FactionOperation, error)
	// RecentOperations lists a faction's operations, most recent first.
	RecentOperations(ctx context.Context, factionID string, limit int) ([]FactionOperation, error)
}

// Faction refusals.
var (
	ErrFactionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrFactionNotFound", "no such faction")
	ErrFactionNameTaken = errors.Sentinel(errors.CodeConflict,
		"application.ErrFactionNameTaken", "another faction has that name")
	ErrAlreadyInFaction = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyInFaction", "already in a faction")
	ErrNotInFaction = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotInFaction", "not in a faction")
	ErrRequestPending = errors.Sentinel(errors.CodeConflict,
		"application.ErrRequestPending", "a request is already waiting")
	ErrRequestNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrRequestNotFound", "no such request")
	ErrNoOperation = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoOperation", "no organised crime")
	ErrOperationOpen = errors.Sentinel(errors.CodeConflict,
		"application.ErrOperationOpen", "an organised crime is already planned")
	ErrAlreadyInCrew = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyInCrew", "already in the crew")
)
