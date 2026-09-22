// Package lease gives one gateway instance the exclusive right to poll one
// bot.
//
// MASTER_PROMPT section 57 states the problem and the shape of the answer: a
// key such as gateway:bot-lease:bot01 with a TTL, renewed by whoever holds it.
// Two gateway instances calling getUpdates for the same bot is not a harmless
// race — each call acknowledges updates for the other, so updates are lost,
// not duplicated, and the loss is silent.
//
// The TTL is what makes section 58 work. When a gateway dies it renews
// nothing, the key expires on its own, and another instance claims the bot
// without an operator being involved. That is also why Release exists: a clean
// shutdown hands the bot back in milliseconds instead of leaving it dark for a
// whole TTL.
//
// # What this package does and does not contain
//
// The Lease interface is declared here and implemented over Redis by
// infrastructure. This package contributes the Keeper: the loop that acquires,
// renews on a ticker, and releases when its context is cancelled. That loop is
// small and easy to get subtly wrong — releasing a lease someone else now
// holds, renewing forever after losing it, or skipping the release because the
// context used for it was the one that was just cancelled — so it is written
// once, here, with tests.
package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Defaults for a Keeper.
const (
	// DefaultTTL is how long a claim survives without a renewal. It bounds
	// how long a crashed gateway's bots stay dark, so it is short.
	DefaultTTL = 30 * time.Second

	// DefaultRenewDivisor sets the renew interval to TTL/3 when none is
	// given. Three attempts inside one TTL means two consecutive failures
	// (a Redis failover, a GC pause) are survivable without losing the bot.
	DefaultRenewDivisor = 3

	// DefaultReleaseTimeout bounds the release that runs after the keeper's
	// context is already cancelled. Shutdown must not hang on it; the TTL is
	// the fallback if it fails.
	DefaultReleaseTimeout = 5 * time.Second
)

// Failures.
var (
	ErrNoLease   = errors.New("lease: a lease implementation is required")
	ErrNoBotKey  = errors.New("lease: bot key is required")
	ErrNoOwnerID = errors.New("lease: owner id is required")
	ErrBadTTL    = errors.New("lease: ttl must be positive")

	// ErrNotAcquired means another gateway instance holds this bot. It is a
	// normal outcome in a cluster, not a fault: the caller moves on to the
	// next bot.
	ErrNotAcquired = errors.New("lease: bot is already leased by another instance")

	// ErrLost means a renewal was refused, so this instance no longer holds
	// the bot. Polling must stop immediately; someone else is polling now.
	ErrLost = errors.New("lease: lease lost")
)

// Lease is the distributed lock behind a bot.
//
// Every method takes the ownerID, and implementations must check it: Renew and
// Release may only affect a lease this owner actually holds. Without that
// check, a keeper that lost its lease during a network partition would happily
// delete or extend the new holder's claim — the classic way a lock stops being
// a lock.
type Lease interface {
	// Acquire claims botKey for ownerID for ttl. It returns false, with no
	// error, when the bot is already claimed by someone else.
	Acquire(ctx context.Context, botKey, ownerID string, ttl time.Duration) (acquired bool, err error)

	// Renew extends a lease this owner holds. It returns false, with no
	// error, when the owner no longer holds it (it expired, or was taken).
	Renew(ctx context.Context, botKey, ownerID string, ttl time.Duration) (renewed bool, err error)

	// Release drops a lease this owner holds. Releasing a lease that is gone
	// or belongs to somebody else is not an error: it must be safe to call on
	// a shutdown path that cannot check first.
	Release(ctx context.Context, botKey, ownerID string) error
}

// KeeperConfig configures a Keeper.
type KeeperConfig struct {
	// Lease is the distributed lock. Required.
	Lease Lease

	// BotKey is the bot being claimed, e.g. "bot01". Required.
	BotKey string

	// OwnerID identifies this gateway instance. Required, and it must be
	// unique across the cluster: two instances sharing an owner id would each
	// believe they hold the bot, which is the failure the lease prevents.
	OwnerID string

	// TTL is how long a claim survives without renewal. Zero means DefaultTTL.
	TTL time.Duration

	// RenewEvery is the renewal interval. Zero means TTL/DefaultRenewDivisor.
	// It must be shorter than TTL, or the lease expires between renewals.
	RenewEvery time.Duration

	// ReleaseTimeout bounds the release on shutdown. Zero means
	// DefaultReleaseTimeout.
	ReleaseTimeout time.Duration

	// Ticker exists so tests can drive renewals without waiting. Optional;
	// nil uses time.NewTicker.
	Ticker func(d time.Duration) (<-chan time.Time, func())
}

// Keeper holds one bot's lease for as long as its context lives.
type Keeper struct {
	lease   Lease
	botKey  string
	ownerID string

	ttl            time.Duration
	renewEvery     time.Duration
	releaseTimeout time.Duration

	ticker func(d time.Duration) (<-chan time.Time, func())
}

// LeaseKeeper is the name MASTER_PROMPT section 57 uses. It is the same type.
type LeaseKeeper = Keeper

// NewKeeper validates cfg and builds a Keeper. It claims nothing yet; Run
// does that.
func NewKeeper(cfg KeeperConfig) (*Keeper, error) {
	if cfg.Lease == nil {
		return nil, ErrNoLease
	}
	if strings.TrimSpace(cfg.BotKey) == "" {
		return nil, ErrNoBotKey
	}
	if strings.TrimSpace(cfg.OwnerID) == "" {
		return nil, ErrNoOwnerID
	}
	if cfg.TTL < 0 {
		return nil, ErrBadTTL
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	renewEvery := cfg.RenewEvery
	if renewEvery <= 0 {
		renewEvery = ttl / DefaultRenewDivisor
	}
	if renewEvery <= 0 || renewEvery >= ttl {
		// A renewal interval at or beyond the TTL means the lease expires
		// before it is renewed, and the bot would flap between instances.
		return nil, fmt.Errorf("lease: renew interval %s must be shorter than the ttl %s", renewEvery, ttl)
	}
	releaseTimeout := cfg.ReleaseTimeout
	if releaseTimeout <= 0 {
		releaseTimeout = DefaultReleaseTimeout
	}
	tick := cfg.Ticker
	if tick == nil {
		tick = realTicker
	}

	return &Keeper{
		lease:          cfg.Lease,
		botKey:         cfg.BotKey,
		ownerID:        cfg.OwnerID,
		ttl:            ttl,
		renewEvery:     renewEvery,
		releaseTimeout: releaseTimeout,
		ticker:         tick,
	}, nil
}

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// BotKey reports which bot this keeper is for.
func (k *Keeper) BotKey() string { return k.botKey }

// Run acquires the lease and holds it until ctx is done.
//
// It blocks. The caller starts the bot's poll loop once Run has acquired and
// stops it as soon as Run returns, whatever the reason.
//
// Returns:
//
//   - ErrNotAcquired when another instance holds the bot. Nothing was claimed
//     and nothing needs releasing.
//   - ErrLost when a renewal was refused. The lease now belongs to somebody
//     else, so it is deliberately NOT released: releasing here would delete
//     the new holder's claim.
//   - nil when ctx was cancelled. The lease is released first, so another
//     instance can take the bot immediately instead of waiting out the TTL.
//   - the underlying error when Acquire or Renew fails outright.
func (k *Keeper) Run(ctx context.Context) error {
	acquired, err := k.lease.Acquire(ctx, k.botKey, k.ownerID, k.ttl)
	if err != nil {
		return fmt.Errorf("lease: acquiring %q: %w", k.botKey, err)
	}
	if !acquired {
		return fmt.Errorf("lease: %q: %w", k.botKey, ErrNotAcquired)
	}

	ticks, stop := k.ticker(k.renewEvery)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			// The context that ended this loop cannot be the one that
			// releases the lease: it is already cancelled, and every call
			// made with it would fail instantly. WithoutCancel keeps the
			// values (a request id, a trace id) and drops the cancellation.
			k.release(ctx)
			return nil

		case <-ticks:
			renewed, err := k.lease.Renew(ctx, k.botKey, k.ownerID, k.ttl)
			if err != nil {
				if ctx.Err() != nil {
					// Shutdown raced the renewal. Treat it as a clean stop.
					k.release(ctx)
					return nil
				}
				return fmt.Errorf("lease: renewing %q: %w", k.botKey, err)
			}
			if !renewed {
				return fmt.Errorf("lease: %q: %w", k.botKey, ErrLost)
			}
		}
	}
}

// release hands the bot back on a context of its own. A failure here is not
// returned: the caller is shutting down and can do nothing about it, and the
// TTL guarantees the bot becomes claimable anyway.
func (k *Keeper) release(ctx context.Context) {
	// WithoutCancel keeps whatever values the context carries (a trace id,
	// for instance) while dropping the cancellation that just fired.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), k.releaseTimeout)
	defer cancel()

	_ = k.lease.Release(releaseCtx, k.botKey, k.ownerID)
}
