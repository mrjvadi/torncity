package application

import "github.com/mrjvadi/torncity/internal/shared/errors"

// ErrPlayerNotFound is returned by PlayerRepository when no player exists for
// the given Telegram user.
//
// Named rather than a bare NotFound so a caller branching on it cannot also
// catch every other not-found in the system. See errors_phase1.go.
var ErrPlayerNotFound = errors.Sentinel(errors.CodeNotFound,
	"application.ErrPlayerNotFound", "player not found")
