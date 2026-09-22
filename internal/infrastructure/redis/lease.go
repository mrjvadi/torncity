package redis

import (
	"context"
	"fmt"
	"time"
)

// BotLease decides which gateway instance polls which bot.
//
// Telegram allows exactly one active getUpdates consumer per bot token: a
// second poller does not split the work, it steals updates from the first, and
// the two instances then see interleaved halves of every conversation. The
// lease is what makes "one poller per bot" true across a fleet of gateway
// instances that know nothing about each other.
//
// It is a lease and not a lock because the holder is expected to keep it for
// as long as it lives: Acquire once, Renew on a timer well inside the TTL, and
// Release on a clean shutdown so another instance can take over immediately
// instead of waiting out the TTL.
type BotLease struct {
	client *Client
}

// NewBotLease returns a lease manager backed by c.
func NewBotLease(c *Client) *BotLease { return &BotLease{client: c} }

// Acquire tries to take the lease on botKey for holder.
//
// It reports false, nil when another instance already holds it. That is an
// ordinary outcome during a rolling deploy, not a failure, so it is not an
// error: the caller moves on to the next bot.
//
// holder identifies the gateway instance and becomes the fencing token, so it
// must be unique per instance (an instance id, not a hostname that a restarted
// pod could reuse while the old lease is still alive).
func (l *BotLease) Acquire(ctx context.Context, botKey, holder string, ttl time.Duration) (bool, error) {
	if err := validateLease(botKey, holder, ttl); err != nil {
		return false, err
	}

	ok, err := l.client.Raw().SetNX(ctx, botLeaseKey(botKey), holder, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis: acquiring lease for bot %s: %w", botKey, err)
	}
	return ok, nil
}

// Renew extends the lease, but only while holder still owns it.
//
// A bare EXPIRE would be the same bug as a bare DEL in the player lock: an
// instance that paused long enough to lose its lease would push the TTL of a
// key another instance now owns, and both would believe they may poll. The
// compare and the expiry are one script so ownership cannot change between
// the check and the write.
//
// A false return means the lease was lost. The caller must stop polling that
// bot immediately rather than retry, because somebody else is already on it.
func (l *BotLease) Renew(ctx context.Context, botKey, holder string, ttl time.Duration) (bool, error) {
	if err := validateLease(botKey, holder, ttl); err != nil {
		return false, err
	}

	// PEXPIRE takes milliseconds, which is also what lets a sub-second TTL be
	// expressed at all.
	millis := ttl.Milliseconds()
	if millis <= 0 {
		millis = 1
	}

	res, err := compareAndExpire.Run(ctx, l.client.Raw(), []string{botLeaseKey(botKey)}, holder, millis).Int64()
	if err != nil {
		return false, fmt.Errorf("redis: renewing lease for bot %s: %w", botKey, err)
	}
	return res == 1, nil
}

// Release gives up the lease, but only if holder still owns it.
//
// Releasing a lease somebody else has since taken would let a third instance
// acquire it while the second is still polling, which is the exact duplicate
// this type exists to prevent. Releasing a lease that was already lost is a
// silent no-op and not an error: the outcome the caller wanted — not holding
// it — is already true.
func (l *BotLease) Release(ctx context.Context, botKey, holder string) error {
	if botKey == "" || holder == "" {
		return fmt.Errorf("redis: lease release requires a bot key and a holder")
	}

	if err := compareAndDelete.Run(ctx, l.client.Raw(), []string{botLeaseKey(botKey)}, holder).Err(); err != nil {
		return fmt.Errorf("redis: releasing lease for bot %s: %w", botKey, err)
	}
	return nil
}

// validateLease rejects arguments that could only ever produce an unsafe
// lease: an empty holder would make every instance look like every other, and
// a non-positive TTL would create a key that never expires, stranding a bot
// permanently if its holder died.
func validateLease(botKey, holder string, ttl time.Duration) error {
	switch {
	case botKey == "":
		return fmt.Errorf("redis: lease requires a bot key")
	case holder == "":
		return fmt.Errorf("redis: lease requires a holder id")
	case ttl <= 0:
		return fmt.Errorf("redis: lease ttl must be positive, got %s", ttl)
	}
	return nil
}
