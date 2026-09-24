package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/gateway/input"
)

// InputStore keeps the free-text inputs players owe a command
// (internal/gateway/input).
type InputStore struct {
	client *Client
}

var _ input.Store = (*InputStore)(nil)

// NewInputStore returns the store.
func NewInputStore(c *Client) *InputStore { return &InputStore{client: c} }

// takeInputSrc removes and returns a waiting input, but only the one the
// given prompt asked for when a prompt is given. Read, check and delete are
// one script, so a reply delivered twice, or seen by two gateway instances,
// fills the command once.
const takeInputSrc = `
local v = redis.call("GET", KEYS[1])
if not v then
	return false
end
if ARGV[1] ~= "0" then
	local bar = string.find(v, "|", 1, true)
	if not bar or string.sub(v, 1, bar - 1) ~= ARGV[1] then
		return false
	end
end
redis.call("DEL", KEYS[1])
return v
`

var takeInput = goredis.NewScript(takeInputSrc)

// Arm starts the cooldown before the next prompt, reporting false when one is
// still running. SET NX: two presses at once arm it once.
func (s *InputStore) Arm(ctx context.Context, key input.Key, cooldown time.Duration) (bool, error) {
	ok, err := s.client.Raw().SetNX(ctx, inputCooldownKey(key), "1", cooldown).Result()
	if err != nil {
		return false, fmt.Errorf("redis: arming an input prompt: %w", err)
	}
	return ok, nil
}

// Put records a waiting input, replacing any other.
func (s *InputStore) Put(ctx context.Context, key input.Key, value string, ttl time.Duration) error {
	if err := s.client.Raw().Set(ctx, inputKey(key), value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: recording a waiting input: %w", err)
	}
	return nil
}

// Take removes and returns the waiting input; see takeInputSrc.
func (s *InputStore) Take(ctx context.Context, key input.Key, prompt int64) (string, error) {
	v, err := takeInput.Run(ctx, s.client.Raw(), []string{inputKey(key)}, strconv.FormatInt(prompt, 10)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis: taking a waiting input: %w", err)
	}
	str, _ := v.(string)
	return str, nil
}

// Drop removes the waiting input, if any.
func (s *InputStore) Drop(ctx context.Context, key input.Key) error {
	if err := s.client.Raw().Del(ctx, inputKey(key)).Err(); err != nil {
		return fmt.Errorf("redis: dropping a waiting input: %w", err)
	}
	return nil
}
