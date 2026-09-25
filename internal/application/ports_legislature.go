package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of legislatures (migration 0024,
// docs/adr/0024-property-and-politics.md): proposals put to a body's vote —
// a lever it decides, a change it must confirm, an action it must approve —
// and each seat's vote. The rules are internal/domain/legislature; who
// sits in a body is the governance package's.

// Proposal kinds, exactly as the migration's CHECK spells them.
const (
	ProposalLever  = "lever"
	ProposalAction = "action"
)

// Proposal statuses.
const (
	ProposalOpen   = "open"
	ProposalPassed = "passed"
	ProposalFailed = "failed"
	// ProposalLapsed is a proposal that passed and could not be applied: the
	// lever changed meanwhile, or the action no longer made sense.
	ProposalLapsed = "lapsed"
)

// Votes.
const (
	VoteYes = "yes"
	VoteNo  = "no"
)

// ProposalReference is the reference_type of a proposal's scheduled close.
const ProposalReference = "proposals"

// LegislatureCloseActionType is the game_actions type of a proposal's
// window ending.
const LegislatureCloseActionType = "legislature_close"

// Proposal is a proposals row.
type Proposal struct {
	ID             string
	No             int64
	Kind           string
	JurisdictionID string
	// Subject is the lever or action code.
	Subject string
	// Value is a scalar lever's proposed value; Allocation an allocation
	// lever's shares; Args an action's arguments.
	Value      *int64
	Allocation map[string]int64
	Args       map[string]string

	ProposedBy       string
	ProposerOfficeID string
	ProposerOffice   string
	// Body is the office whose members vote; Rule, Threshold and Quorum how
	// it decides; Seats how many seats it had at the opening.
	Body, Rule, Threshold, Quorum string
	Seats                         int

	Status        string
	OpenedAt      time.Time
	ClosesAt      time.Time
	CloseActionID string
	DecidedAt     *time.Time
	YesVotes      *int
	NoVotes       *int
	LapseReason   string
	PolicyValueID string

	ContentVersion int
}

// ProposalVote is one seat's vote.
type ProposalVote struct {
	ProposalID string
	OfficeID   string
	PlayerID   string
	Vote       string
	CastAt     time.Time
}

// Legislature sentinels.
var (
	// ErrBillNotFound means no proposal has that id or number.
	ErrBillNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrBillNotFound", "no such proposal before the body")
	// ErrBillUnderWay means the same lever or action already has an
	// open proposal in that place.
	ErrBillUnderWay = errors.Sentinel(errors.CodeConflict,
		"application.ErrBillUnderWay", "that is already being voted on")
)

// LegislatureRepository persists proposals and votes. Reach it through
// Tx.Legislature, so a vote commits with the decision it settles.
type LegislatureRepository interface {
	// Open writes a new proposal and returns it with its public number. It
	// returns ErrBillUnderWay when the subject has one open there.
	Open(ctx context.Context, p Proposal) (Proposal, error)
	// Proposal reads one by id, locked FOR UPDATE when lock; or
	// ErrBillNotFound.
	Proposal(ctx context.Context, id string, lock bool) (*Proposal, error)
	// ProposalByNo reads one by its public number, like Proposal.
	ProposalByNo(ctx context.Context, no int64, lock bool) (*Proposal, error)
	// OpenFor returns the open proposal of a subject in a place, nil for
	// none.
	OpenFor(ctx context.Context, jurisdictionID, kind, subject string) (*Proposal, error)
	// List returns the proposals of the given places: every open one, then
	// the latest decided, at most limit in all.
	List(ctx context.Context, jurisdictionIDs []string, limit int) ([]Proposal, error)
	// CastVote records a seat's vote; false when that seat or that player
	// has already voted on it.
	CastVote(ctx context.Context, v ProposalVote) (bool, error)
	// Votes returns every vote cast on a proposal, in the order cast.
	Votes(ctx context.Context, proposalID string) ([]ProposalVote, error)
	// Decide writes a proposal's outcome — status, decided_at, the count,
	// a lapse's reason, the change it made — only while it is open; false
	// when it was not.
	Decide(ctx context.Context, p Proposal) (bool, error)
}
