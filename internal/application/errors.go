package application

import "github.com/mrjvadi/torncity/internal/shared/errors"

// ErrPlayerNotFound is returned by PlayerRepository when no player exists for
// the given Telegram user.
var ErrPlayerNotFound = errors.NotFound("player not found")
