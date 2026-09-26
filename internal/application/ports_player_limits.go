package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds the port of a player's limit overrides (migrations/
// 0032_player_limits): an operator's per-player exception to a tuning cap
// set in config — today, only company.max_per_player, granted by
// `admin player limit` (cmd/admin/panel.go) through internal/operator.Ops.
// SetCompanyLimit. The rest of config stays global; this is the one door an
// operator may open for one player at a time, and every use of it is
// audited like any other operator action.

// PlayerLimit is one player_limits row. Unlimited and MaxCompanies are
// mutually exclusive, as the migration's CHECK enforces: Unlimited carries
// no number, and a numeric cap always does.
type PlayerLimit struct {
	PlayerID string
	// MaxCompanies is the cap granted, nil when Unlimited is true.
	MaxCompanies *int
	// Unlimited lifts the company-count cap entirely.
	Unlimited bool
	// GrantedBy and Reason name who granted the override and why, for the
	// player card; the audit row carries them too.
	GrantedBy string
	Reason    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PlayerLimitRepository persists per-player overrides of tuning caps. Reach
// it through Tx.PlayerLimits, so an override is read inside the same unit of
// work as the check it gates.
type PlayerLimitRepository interface {
	// Get reads a player's override, or ErrNoPlayerLimit when they have
	// none — the caller then applies the config default.
	Get(ctx context.Context, playerID string) (*PlayerLimit, error)
	// Set grants an override: unlimited company founding when unlimited is
	// true (maxCompanies is then ignored and stored as none), otherwise a
	// cap of *maxCompanies, which must not be nil. It replaces any override
	// the player already has.
	Set(ctx context.Context, playerID string, maxCompanies *int, unlimited bool, grantedBy, reason string, now time.Time) error
	// Clear removes a player's override, restoring the config default.
	Clear(ctx context.Context, playerID string) error
}

// ErrNoPlayerLimit is a player with no override on record.
var ErrNoPlayerLimit = errors.Sentinel(errors.CodeNotFound,
	"application.ErrNoPlayerLimit", "no limit override for this player")
