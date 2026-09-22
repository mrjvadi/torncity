package application

import "github.com/mrjvadi/torncity/internal/shared/errors"

// Phase 1 sentinels.
//
// Each is a condition a player can reach by pressing a button, not a fault.
//
// Every one is built with errors.Sentinel and a unique id rather than with a
// bare per-code helper. Without the id, errors.Is matched on the code alone,
// so ErrAlreadyTravelling and ErrAlreadyFriends — both conflicts — were the
// same error as far as any caller could tell, and a handler branching on one
// silently caught the other. The id is what makes a branch mean what it says.
var (
	ErrCityNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrCityNotFound", "city not found")

	ErrNoActiveTravel = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNoActiveTravel", "no journey in progress")

	ErrAlreadyTravelling = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyTravelling", "already on a journey")

	ErrSkillNotFound = errors.Sentinel(errors.CodeNotFound,
		"application.ErrSkillNotFound", "skill not found")

	ErrNotFriends = errors.Sentinel(errors.CodeNotFound,
		"application.ErrNotFriends", "not friends")

	ErrAlreadyFriends = errors.Sentinel(errors.CodeConflict,
		"application.ErrAlreadyFriends", "already friends")
)
