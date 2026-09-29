package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/application"
)

// Presence (docs/adr/0030-realtime-interest-and-presence.md section 3.1) and
// the per-settlement event counter of the settlement channel (section 1.1).
// Both live only in Redis: presence expires by itself, and the counter is
// re-seeded ahead of the clock if Redis ever loses it.

const (
	presencePrefix          = "presence:"
	settlementVersionPrefix = "settlement:version:"
	settlementEventPrefix   = "settlement:event:"
)

func presenceKey(playerID string) string { return presencePrefix + playerID }

// Presence is the heartbeat store: a key per player with a TTL. A player who
// stops beating simply expires; there is no sign-off.
type Presence struct {
	client *Client
	ttl    time.Duration
}

var _ application.PresenceStore = (*Presence)(nil)

// NewPresence returns a store whose keys live for ttl after the last beat
// (realtime.presence_ttl).
func NewPresence(c *Client, ttl time.Duration) *Presence { return &Presence{client: c, ttl: ttl} }

// Beat marks the player online. The value is the beat's Unix time, only for
// an operator looking with redis-cli; nothing reads it.
func (p *Presence) Beat(ctx context.Context, playerID string, at time.Time) error {
	if err := p.client.Raw().Set(ctx, presenceKey(playerID), at.Unix(), p.ttl).Err(); err != nil {
		return fmt.Errorf("redis: presence beat: %w", err)
	}
	return nil
}

// Online says which of the players have a live key: one MGET.
func (p *Presence) Online(ctx context.Context, playerIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(playerIDs))
	if len(playerIDs) == 0 {
		return out, nil
	}
	keys := make([]string, len(playerIDs))
	for i, id := range playerIDs {
		keys[i] = presenceKey(id)
	}
	vals, err := p.client.Raw().MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: presence read: %w", err)
	}
	for i, v := range vals {
		if v != nil {
			out[playerIDs[i]] = true
		}
	}
	return out, nil
}

// SettlementVersions hands out the monotonically increasing version stamped
// on every publication of a settlement's channel, so a client can tell it
// missed one and fetch the layout again.
type SettlementVersions struct {
	client *Client
	// EventTTL is how long an event id remembers the version it was given,
	// so a redelivered event is stamped with the same number.
	eventTTL time.Duration
	now      func() time.Time
}

// NewSettlementVersions returns the counter. eventTTL should outlast the
// stream's redelivery horizon.
func NewSettlementVersions(c *Client, eventTTL time.Duration) *SettlementVersions {
	return &SettlementVersions{client: c, eventTTL: eventTTL, now: time.Now}
}

// assign is the whole idempotent step in one script: an event id that was
// already stamped returns its number; otherwise the settlement's counter
// moves up by one (seeded at the current Unix millisecond when it does not
// exist, so a flushed Redis can only make versions jump forward, never back)
// and the number is remembered for the event id.
var assign = goredis.NewScript(`
local seen = redis.call('GET', KEYS[2])
if seen then return tonumber(seen) end
if redis.call('EXISTS', KEYS[1]) == 0 then redis.call('SET', KEYS[1], ARGV[2]) end
local v = redis.call('INCR', KEYS[1])
redis.call('SET', KEYS[2], v, 'EX', ARGV[1])
return v
`)

// Assign returns the version of eventID in settlementID's channel: the same
// number every time it is asked for the same event.
func (v *SettlementVersions) Assign(ctx context.Context, settlementID, eventID string) (int64, error) {
	tag := "{settlement:" + settlementID + "}"
	n, err := assign.Run(ctx, v.client.Raw(),
		[]string{settlementVersionPrefix + tag, settlementEventPrefix + tag + ":" + eventID},
		int64(v.eventTTL/time.Second), strconv.FormatInt(v.now().UnixMilli(), 10)).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis: stamping a settlement event: %w", err)
	}
	return n, nil
}

// Current is the last version handed out for the settlement, or 0 when none
// has been.
func (v *SettlementVersions) Current(ctx context.Context, settlementID string) (int64, error) {
	s, err := v.client.Raw().Get(ctx, settlementVersionPrefix+"{settlement:"+settlementID+"}").Result()
	if errors.Is(err, goredis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("redis: reading a settlement version: %w", err)
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n, nil
}
