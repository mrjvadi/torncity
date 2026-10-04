package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/charter"
)

// The charter's storage (docs/adr/0044 section 6; migration 0121). Reach it
// through Tx.Charters so an edit, its seats and its audit row commit together.

// CharterSeat is one player sitting in one office.
type CharterSeat struct {
	ID, OfficeID, HolderID, AppointedBy string
	Since                               time.Time
	// TermEnds is when an elected seat's term runs out (zero: no term).
	TermEnds time.Time
}

// CharterBallot is an election, a recall vote or an amendment vote.
type CharterBallot struct {
	ID, SettlementID, Kind, OfficeID, TargetPlayerID, OpenedBy, Status, ActionID string
	// Proposal is the change an amendment carries (JSON), applied when it carries.
	Proposal []byte
	// Result is what the count wrote (JSON), empty while open.
	Result            []byte
	OpensAt, ClosesAt time.Time
	SettledAt         time.Time
	// Eligible is how many residents could vote when it opened.
	Eligible int
}

// CharterCandidate stands in an election.
type CharterCandidate struct {
	PlayerID, Name, Code string
	StoodAt              time.Time
}

// CharterPetition asks for a recall vote of one holder.
type CharterPetition struct {
	ID, SettlementID, OfficeID, TargetPlayerID, StartedBy, Status, BallotID string
	CreatedAt                                                               time.Time
	Signatures                                                              int
}

// CharterAuditRow is one line of the append-only charter log (rail R3).
type CharterAuditRow struct {
	ID, SettlementID, ActorID, Action, OfficeID string
	Detail                                      map[string]any
	At                                          time.Time
}

// CharterPerson is a resident as the charter names them.
type CharterPerson struct{ ID, Name, Code string }

// CharterRepository stores a settlement's offices, seats and audit.
type CharterRepository interface {
	// Lock serialises every edit of one settlement's charter for the rest of the
	// transaction (two replicas cannot both pass a rail on the same state).
	Lock(ctx context.Context, settlementID string) error
	// Offices lists the open offices; none means the default charter applies.
	Offices(ctx context.Context, settlementID string) ([]charter.Office, error)
	// SaveOffice inserts the office or updates the one with the same ID.
	SaveOffice(ctx context.Context, settlementID string, o charter.Office, createdBy string, at time.Time) error
	CloseOffice(ctx context.Context, officeID string, at time.Time) error
	// Seats lists the active seats of the settlement's open offices.
	Seats(ctx context.Context, settlementID string) ([]CharterSeat, error)
	// OfficesOf lists the open offices the player sits in.
	OfficesOf(ctx context.Context, settlementID, playerID string) ([]charter.Office, error)
	// Seat puts the player in the office; false when they already sit there.
	Seat(ctx context.Context, s CharterSeat) (bool, error)
	// EndSeat takes the player out of the office; false when they did not sit there.
	EndSeat(ctx context.Context, officeID, holderID, reason string, at time.Time) (bool, error)
	// EndSeatsOf ends every seat of an office (it closes).
	EndSeatsOf(ctx context.Context, officeID, reason string, at time.Time) error
	// ResidentByCode finds an active resident of the settlement by public code, nil for none.
	ResidentByCode(ctx context.Context, settlementID, code string) (*CharterPerson, error)
	// ExpiredSeats lists the elected seats whose term has ended by `now`.
	ExpiredSeats(ctx context.Context, settlementID string, now time.Time) ([]CharterSeat, error)
	// Eligible counts the active residents who have lived in the settlement since
	// `since` or earlier (the electorate); IsEligible asks it of one player.
	Eligible(ctx context.Context, settlementID string, since time.Time) (int, error)
	IsEligible(ctx context.Context, settlementID, playerID string, since time.Time) (bool, error)
	// Ballots.
	OpenBallot(ctx context.Context, b CharterBallot) (bool, error)
	Ballot(ctx context.Context, id string) (*CharterBallot, error)
	OpenBallots(ctx context.Context, settlementID string) ([]CharterBallot, error)
	SettleBallot(ctx context.Context, id, status string, result []byte, at time.Time) (bool, error)
	RecentBallots(ctx context.Context, settlementID string, limit int) ([]CharterBallot, error)
	// RecalledSince says whether a recall vote removed this holder from this office at or after `since`.
	RecalledSince(ctx context.Context, officeID, playerID string, since time.Time) (bool, error)
	// LastRecall is when a recall vote about this holder in this office last ended;
	// zero for never.
	LastRecall(ctx context.Context, officeID, playerID string) (time.Time, error)
	Candidates(ctx context.Context, ballotID string) ([]CharterCandidate, error)
	AddCandidate(ctx context.Context, ballotID, playerID string, at time.Time) (bool, error)
	// Cast records a secret vote; false when the voter had voted already.
	Cast(ctx context.Context, ballotID, voterID, choice string, at time.Time) (bool, error)
	Tally(ctx context.Context, ballotID string) (map[string]int64, error)
	HasVoted(ctx context.Context, ballotID, voterID string) (bool, error)
	// Petitions.
	OpenPetition(ctx context.Context, p CharterPetition) (bool, error)
	PetitionOf(ctx context.Context, officeID, targetPlayerID string) (*CharterPetition, error)
	Petition(ctx context.Context, id string) (*CharterPetition, error)
	OpenPetitions(ctx context.Context, settlementID string) ([]CharterPetition, error)
	Sign(ctx context.Context, petitionID, signerID string, at time.Time) (bool, error)
	SetPetition(ctx context.Context, id, status, ballotID string) error
	Audit(ctx context.Context, r CharterAuditRow) error
	AuditList(ctx context.Context, settlementID string, limit int) ([]CharterAuditRow, error)
}
