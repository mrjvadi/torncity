package application

import "github.com/mrjvadi/torncity/internal/shared/errors"

// Phase 1 sentinels.
//
// Each is a condition a player can reach by pressing a button, not a fault.
// They carry a Code so the presentation layer can pick the right message
// without matching on text.
var (
	ErrCityNotFound      = errors.NotFound("city not found")
	ErrNoActiveTravel    = errors.NotFound("no journey in progress")
	ErrAlreadyTravelling = errors.Conflict("already on a journey")
	ErrSkillNotFound     = errors.NotFound("skill not found")
	ErrNotFriends        = errors.NotFound("not friends")
	ErrAlreadyFriends    = errors.Conflict("already friends")
)
