package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of elections (migrations/0018_elections.up.sql).
// An election fills the seats of one office in one jurisdiction: candidates
// stand, residents vote once and in secret, and one scheduled count fills the
// seats and vacates whoever held them.

// Election statuses.
const (
	ElectionOpen    = "open"
	ElectionCounted = "counted"
)

// Election action types: the vote opening, the scheduled count, and the next
// election opening when a term runs out. They must stay in step with the
// scheduler's routes.
const (
	ElectionVotingActionType = "election_voting"
	ElectionCountActionType  = "election_count"
	ElectionOpenActionType   = "election_open"
	// ElectionReference is game_actions.reference_type of both.
	ElectionReference = "elections"
)

// Election is an elections row.
type Election struct {
	ID              string
	No              int64
	OfficeCode      string
	JurisdictionID  string
	Seats           int
	Status          string
	OpensAt         time.Time
	CandidacyEndsAt time.Time
	VotingEndsAt    time.Time
	CountActionID   string
	ContentVersion  int
	// VotingOpenedAt is when the vote opened and was announced; nil before.
	VotingOpenedAt *time.Time
	CountedAt      *time.Time
	VotesCast      int64
}

// ElectionCandidate is an election_candidates row.
type ElectionCandidate struct {
	ElectionID           string
	PlayerID             string
	StoodAt              time.Time
	Deposit              int64
	DepositMethod        string
	DepositTransactionID string
	// Set by the count; nil before it.
	Votes           *int64
	Elected         *bool
	Seat            *int
	DepositReturned *bool
}

// ElectionRepository persists elections. Reach it through Tx.Elections.
type ElectionRepository interface {
	// Open records an election and returns it with its number. A second
	// open election of one office in one jurisdiction is
	// ErrElectionUnderWay.
	Open(ctx context.Context, e Election) (Election, error)
	// Election returns one election, locked, by id or public number, or
	// ErrElectionNotFound.
	Election(ctx context.Context, id string) (*Election, error)
	ElectionByNo(ctx context.Context, no int64) (*Election, error)
	// Latest returns the latest election of an office in a jurisdiction,
	// or ErrElectionNotFound.
	Latest(ctx context.Context, office, jurisdictionID string) (*Election, error)
	// ForJurisdictions lists the open elections and the latest counted one
	// of each office in the jurisdictions given, open first.
	ForJurisdictions(ctx context.Context, jurisdictionIDs []string, limit int) ([]Election, error)
	Candidates(ctx context.Context, electionID string) ([]ElectionCandidate, error)
	// Stand records a candidacy; a second one is ErrAlreadyStanding.
	Stand(ctx context.Context, c ElectionCandidate) error
	// Vote records that voterID voted and, apart, a ballot for candidateID
	// with no voter on it. A second vote is ErrAlreadyVoted.
	Vote(ctx context.Context, electionID, voterID, candidateID, ballotID string) error
	// Voted reports whether a player has voted in an election.
	Voted(ctx context.Context, electionID, playerID string) (bool, error)
	// Tally counts the ballots per candidate.
	Tally(ctx context.Context, electionID string) (map[string]int64, error)
	// RecordCount writes the count: each candidate's votes, seat, deposit,
	// and the election counted.
	RecordCount(ctx context.Context, e Election, candidates []ElectionCandidate) error
	// MarkVotingOpened records that the vote opened, once: false when it
	// already had.
	MarkVotingOpened(ctx context.Context, electionID string, at time.Time) (bool, error)
	// AuditSeat records a seat a count vacated or filled in the audit log,
	// as an operator's appointment is recorded.
	AuditSeat(ctx context.Context, a SeatAudit) error
}

// SeatAudit is one seat an election's count changed.
type SeatAudit struct {
	// Action is ElectionAuditVacate or ElectionAuditElect.
	Action        string
	Jurisdiction  Jurisdiction
	Before, After Office
	// ElectionNo names the election in the audit row's reason.
	ElectionNo int64
	At         time.Time
}

// Audit actions and actor of the seats an election changes.
const (
	ElectionAuditVacate = "office.vacate"
	ElectionAuditElect  = "office.elect"
	ElectionAuditActor  = "election"
)

// Election refusals.
var (
	ErrElectionNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrElectionNotFound", "no such election")
	ErrElectionUnderWay = errors.Sentinel(errors.CodeConflict,
		"application.ErrElectionUnderWay", "an election for this office is already under way")
	ErrAlreadyStanding = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyStanding", "already a candidate")
	ErrAlreadyVoted = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyVoted", "already voted")
)
