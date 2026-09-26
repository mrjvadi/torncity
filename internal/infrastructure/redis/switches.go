package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/switches"
)

// SwitchCache keeps an operator switch's value for a short while, so the
// gateway, the notifier and the CLI ask the database at most once per TTL
// per switch. The value is "<cached at, unix seconds>|<value>".
type SwitchCache struct {
	client *Client
}

var _ switches.Cache = (*SwitchCache)(nil)

// NewSwitchCache returns the cache.
func NewSwitchCache(c *Client) *SwitchCache { return &SwitchCache{client: c} }

const switchPrefix = "switch:"

func switchKey(key string) string { return switchPrefix + key }

// Get returns the cached value and when it was cached, if there is one.
func (c *SwitchCache) Get(ctx context.Context, key string) (string, time.Time, bool, error) {
	v, err := c.client.Raw().Get(ctx, switchKey(key)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("redis: reading a switch: %w", err)
	}
	value, at, ok := decodeSwitch(v)
	return value, at, ok, nil
}

// Set keeps value for ttl, stamped with cachedAt.
func (c *SwitchCache) Set(ctx context.Context, key, value string, cachedAt time.Time, ttl time.Duration) error {
	if err := c.client.Raw().Set(ctx, switchKey(key), encodeSwitch(value, cachedAt), ttl).Err(); err != nil {
		return fmt.Errorf("redis: keeping a switch: %w", err)
	}
	return nil
}

func encodeSwitch(value string, at time.Time) string {
	return strconv.FormatInt(at.Unix(), 10) + "|" + value
}

func decodeSwitch(v string) (string, time.Time, bool) {
	ts, value, found := strings.Cut(v, "|")
	if !found {
		return "", time.Time{}, false
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	return value, time.Unix(unix, 0), true
}
