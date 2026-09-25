package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/gateway/moderation"
)

// ModerationCache keeps a Telegram user's moderation standing for a short
// while, so the gateway asks the database at most once per TTL per user.
// The value is "<muted>|<banned>|<until unix, 0 for none>".
type ModerationCache struct {
	client *Client
}

var _ moderation.Cache = (*ModerationCache)(nil)

// NewModerationCache returns the cache.
func NewModerationCache(c *Client) *ModerationCache { return &ModerationCache{client: c} }

const moderationPrefix = "gateway:moderation:"

func moderationKey(telegramUserID int64) string {
	return moderationPrefix + strconv.FormatInt(telegramUserID, 10)
}

// Get returns the cached standing, if there is one.
func (c *ModerationCache) Get(ctx context.Context, id int64) (moderation.Standing, bool, error) {
	v, err := c.client.Raw().Get(ctx, moderationKey(id)).Result()
	if errors.Is(err, goredis.Nil) {
		return moderation.Standing{}, false, nil
	}
	if err != nil {
		return moderation.Standing{}, false, fmt.Errorf("redis: reading a moderation: %w", err)
	}
	s, ok := decodeStanding(v)
	return s, ok, nil
}

// Set keeps a standing for ttl.
func (c *ModerationCache) Set(ctx context.Context, id int64, s moderation.Standing, ttl time.Duration) error {
	if err := c.client.Raw().Set(ctx, moderationKey(id), encodeStanding(s), ttl).Err(); err != nil {
		return fmt.Errorf("redis: keeping a moderation: %w", err)
	}
	return nil
}

func encodeStanding(s moderation.Standing) string {
	var until int64
	if !s.Until.IsZero() {
		until = s.Until.Unix()
	}
	return fmt.Sprintf("%t|%t|%d", s.Muted, s.Banned, until)
}

func decodeStanding(v string) (moderation.Standing, bool) {
	parts := strings.Split(v, "|")
	if len(parts) != 3 {
		return moderation.Standing{}, false
	}
	muted, err1 := strconv.ParseBool(parts[0])
	banned, err2 := strconv.ParseBool(parts[1])
	until, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return moderation.Standing{}, false
	}
	s := moderation.Standing{Muted: muted, Banned: banned}
	if until > 0 {
		s.Until = time.Unix(until, 0)
	}
	return s, true
}
