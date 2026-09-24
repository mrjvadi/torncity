package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the ports of city places (migrations/0015_places.up.sql):
// where a player stands inside their city, and a walk between two places.
// The rules are internal/domain/place; the places are content (places.yml).

// PlaceMoveActionType is a walk reaching its end: place.arrive. It must stay
// equal to the action type the scheduler routes.
const PlaceMoveActionType = "place_move"

// PlaceMoveReference is the reference_type of a walk's scheduled action.
const PlaceMoveReference = "place_moves"

// Walk statuses, exactly as place_moves_status_check spells them.
const (
	MoveMoving    = "moving"
	MoveArrived   = "arrived"
	MoveCancelled = "cancelled"
)

// PlaceMove is a place_moves row: one walk.
type PlaceMove struct {
	ID           string
	PlayerID     string
	CityID       string
	From, To     string
	Status       string
	Energy       int
	GameActionID string
	StartedAt    time.Time
	ArrivesAt    time.Time
	ArrivedAt    *time.Time
}

// PlaceRepository persists where players stand. Reach it through Tx.Places,
// so a walk commits with the energy it cost and its scheduled end.
type PlaceRepository interface {
	// Where returns the place code the player stands at: "" for the default
	// place of their city (never moved, or before places existed).
	Where(ctx context.Context, playerID string) (string, error)
	// Put records that the player now stands at code (an arrival, a shift).
	Put(ctx context.Context, playerID, code string, at time.Time) error

	// ActiveMove returns the player's walk in progress, or ErrNotMoving.
	// It takes no lock.
	ActiveMove(ctx context.Context, playerID string) (*PlaceMove, error)
	// StartMove records a walk. A second walk in progress is
	// ErrAlreadyMoving, whatever raced to start it.
	StartMove(ctx context.Context, m PlaceMove) error
	// FinishMove ends a walk in progress and puts the player at its
	// destination, in one statement; a walk not in progress any more is
	// ErrNotMoving.
	FinishMove(ctx context.Context, id string, at time.Time) (*PlaceMove, error)
	// CancelMove ends a walk in progress without arriving (the player was
	// taken elsewhere); a walk not in progress is ErrNotMoving.
	CancelMove(ctx context.Context, id string, at time.Time) error

	// Headcount counts the players standing at each place of a city — not
	// travelling, not walking, not in jail — keyed by place code, "" for
	// the default place.
	Headcount(ctx context.Context, cityID string) (map[string]int, error)
}

// Place refusals.
var (
	// ErrNotMoving means no walk is in progress.
	ErrNotMoving = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotMoving", "no walk in progress")

	// ErrAlreadyMoving means the player is already walking somewhere. The
	// detail "remaining_seconds" is the real time left.
	ErrAlreadyMoving = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyMoving", "already on the way somewhere")
)
