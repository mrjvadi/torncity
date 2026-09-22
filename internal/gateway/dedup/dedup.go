// Package dedup drops Telegram updates the gateway has already handled.
//
// MASTER_PROMPT section 17 fixes the key: (bot_id, telegram_update_id). Those
// two are enough because an update id is unique per bot, and they are keyed
// together for exactly that reason and no other — this is not an exception to
// the global-identity rule, because the key identifies an UPDATE, not a
// player.
//
// Duplicates are not rare. Long polling re-delivers everything after the last
// acknowledged offset, so a gateway that dies between handling an update and
// advancing its offset will see that update again on restart, and a bot moved
// between gateway instances (ADR 0003 decision 5) can see it a third time.
//
// # Why this is a wrapper and not an implementation
//
// The store is application.Deduplicator, implemented over Redis by
// infrastructure. What this package adds is the decision that the gateway
// makes around it: what to do when the store itself is unavailable. That
// decision is one line of code and several lines of reasoning, which is
// precisely the kind of thing that must not be re-improvised at each call
// site.
//
// # Fail open, deliberately
//
// When the store cannot answer, Allow returns true: the update is processed
// and the error is returned alongside it for the caller to log.
//
// The alternative, dropping updates while Redis is down, means the gateway
// silently eats player commands during an incident — and a command that never
// happened cannot be retried by anyone, because the player has no idea it was
// lost. Processing an update twice is recoverable: section 19 makes delivery
// at-least-once anyway, and section 18 requires sensitive actions to be
// idempotent at the command level, where the player lock and the idempotency
// table are. Deduplication here is an optimisation that saves work; the
// correctness guarantee lives downstream. Optimisations fail open.
package dedup

import (
	"context"
	"errors"

	"github.com/mrjvadi/torncity/internal/application"
)

// Failures.
var (
	ErrNoStore = errors.New("dedup: a deduplicator is required")

	// ErrNoBotID rejects an empty bot id. Empty would merge the update-id
	// spaces of every bot in the fleet into one, and two bots that happen to
	// be at the same update id would start cancelling each other's traffic.
	ErrNoBotID = errors.New("dedup: bot id is required")
)

// Filter decides whether an update is worth processing. It wraps
// application.Deduplicator, whose Redis implementation belongs to
// infrastructure.
type Filter struct {
	store application.Deduplicator
}

// New builds a Filter.
func New(store application.Deduplicator) (*Filter, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	return &Filter{store: store}, nil
}

// Allow reports whether this update should be processed.
//
// It returns false only when the store positively confirms the update was
// already handled. On any error it returns true together with that error: see
// the package doc for why the failure mode is to process, not to drop. A
// caller should log the error and carry on.
func (f *Filter) Allow(ctx context.Context, botID string, updateID int64) (bool, error) {
	if botID == "" {
		return false, ErrNoBotID
	}

	seen, err := f.store.Seen(ctx, botID, updateID)
	if err != nil {
		return true, err
	}
	return !seen, nil
}
