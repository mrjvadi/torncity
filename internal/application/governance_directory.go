package application

import (
	"context"
	"time"
)

// This file holds the read side of player-held offices
// (docs/adr/0015-player-held-offices.md) that the player screens show: who
// sits in which seat, which levers exist, and the public record of every
// change. None of it is a policy value. A lever's value is only ever read
// through PolicyReader.Get, which is what decides it; the directory answers
// the facts around it — names, seats, dates — that the resolver has no reason
// to carry.

// PolicyChangeRecord is one row of the public history of lever changes: who
// changed what, from which value to which, and when it took or takes effect.
type PolicyChangeRecord struct {
	ID             string
	JurisdictionID string
	LeverCode      string
	// OfficeCode is the office the change was made from, as it was then.
	OfficeCode    string
	SetByPlayerID string
	OldValue      int64
	NewValue      int64
	SetAt         time.Time
	EffectiveAt   time.Time
}

// PlayerName is how a screen names another player: the public code, which is
// the identifier a player is meant to see, and the display name.
type PlayerName struct {
	PublicCode  string
	DisplayName string
}

// GovernanceDirectory is the read-only port behind the city and office
// screens. It reads committed facts and writes nothing, so it runs on its own
// connection rather than inside a unit of work, like CityRepository.
type GovernanceDirectory interface {
	// JurisdictionByCode finds a jurisdiction by its level and code, or
	// returns ErrJurisdictionNotFound.
	JurisdictionByCode(ctx context.Context, kind, code string) (Jurisdiction, error)
	// Ancestry returns the jurisdiction and every ancestor below the world,
	// nearest first. An unknown id returns ErrJurisdictionNotFound.
	Ancestry(ctx context.Context, id string) ([]Jurisdiction, error)
	// Levers returns every lever of the active content, ordered by code.
	Levers(ctx context.Context) ([]LeverDefinition, error)
	// Offices returns every office of the active content, ordered by code.
	Offices(ctx context.Context) ([]OfficeDefinition, error)
	// Seats returns every seat of the given jurisdictions, ordered by
	// office and seat.
	Seats(ctx context.Context, jurisdictionIDs []string) ([]Office, error)
	// SeatsHeldBy returns every seat the player holds, anywhere.
	SeatsHeldBy(ctx context.Context, playerID string) ([]Office, error)
	// LastPolicyChange is when the lever last changed in the jurisdiction,
	// in effect or not; nil if never. The cooldown runs from it.
	LastPolicyChange(ctx context.Context, jurisdictionID, leverCode string) (*time.Time, error)
	// PolicyHistory returns one page of the public record of changes in the
	// given jurisdictions, newest announcement first, and how many there are
	// in all.
	PolicyHistory(ctx context.Context, jurisdictionIDs []string, limit, offset int) ([]PolicyChangeRecord, int, error)
	// PlayerNames names players by id. An id that names nobody is absent
	// from the map.
	PlayerNames(ctx context.Context, ids []string) (map[string]PlayerName, error)
}

// ActingForChain answers who may act for a chain of offices: the first office
// in it with a held seat, and whether it acts as a deputy. It is the same
// walk ResolvePolicy makes for a lever, offered for an office that holds no
// lever, so a screen naming who acts for a vacant seat and the resolver
// deciding who may move a lever can never disagree.
func ActingForChain(chain []OfficeLink) *ActingOffice { return actingOffice(chain) }
