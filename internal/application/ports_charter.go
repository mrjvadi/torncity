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
	Since                              time.Time
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
	Audit(ctx context.Context, r CharterAuditRow) error
	AuditList(ctx context.Context, settlementID string, limit int) ([]CharterAuditRow, error)
}
