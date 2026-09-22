package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// DedupTTL is the DEFAULT retention for an update id.
//
// The value a deployment runs is dedup.ttl in configs/config.yml, injected
// through NewDeduplicator. This constant is what a caller gets if it passes
// nothing usable, and it is what config.Defaults() mirrors.
//
// Twenty-four hours is chosen against Telegram's own behaviour: getUpdates
// redelivers an update until it is confirmed by offset, and after a gateway
// crash the backlog Telegram replays is bounded by its retention, not by ours.
// A short TTL (minutes) would let a redelivery after a long outage be
// processed a second time — charging a player twice for one command. A much
// longer TTL only costs memory for keys nobody will ever look at again.
const DedupTTL = 24 * time.Hour

// Deduplicator suppresses Telegram updates that arrive more than once.
type Deduplicator struct {
	client *Client
	ttl    time.Duration
}

var _ application.Deduplicator = (*Deduplicator)(nil)

// NewDeduplicator returns a deduplicator that remembers an update id for ttl.
//
// The retention is a parameter and not a package decision: it is an
// operational value an operator tunes against memory and against how long a
// Telegram backlog may be replayed, and it is declared once in
// configs/config.yml. A non-positive ttl falls back to DedupTTL rather than
// writing keys that never expire, which would slowly consume the instance.
//
// There is deliberately no constructor that picks the TTL for the caller: one
// existed, every caller used it, and the configured value was therefore never
// reaching Redis.
func NewDeduplicator(c *Client, ttl time.Duration) *Deduplicator {
	if ttl <= 0 {
		ttl = DedupTTL
	}
	return &Deduplicator{client: c, ttl: ttl}
}

// Seen reports whether this (botID, updateID) pair was already handled and
// records it if not.
//
// The test and the write are one SET NX, not a GET followed by a SET: two
// gateway instances can receive the same redelivered update at the same
// instant, and with a read-then-write both would see "not seen" and both would
// execute the command. SET NX makes exactly one of them win inside Redis.
//
// A dedup failure is reported rather than swallowed. Treating an unreachable
// Redis as "not seen" would quietly disable replay protection at the very
// moment the system is already degraded; the caller decides whether to shed
// the update or fail loudly.
func (d *Deduplicator) Seen(ctx context.Context, botID string, updateID int64) (bool, error) {
	key := dedupKey(botID, updateID)

	stored, err := d.client.Raw().SetNX(ctx, key, "1", d.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis: dedup check for bot %s update %d: %w", botID, updateID, err)
	}

	// stored is true when this call created the key, which means nobody had
	// seen the update before. "Seen" is therefore the negation.
	return !stored, nil
}
