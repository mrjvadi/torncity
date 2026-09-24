package redis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/gateway/groups"
)

// LinkStore keeps the deep links that do not fit Telegram's start parameter
// (internal/gateway/groups, LinkPayload) under a short random token, for as
// long as the link is meant to work.
type LinkStore struct {
	client *Client
}

var _ groups.LinkStore = (*LinkStore)(nil)

// NewLinkStore returns the store.
func NewLinkStore(c *Client) *LinkStore { return &LinkStore{client: c} }

// linkTokenBytes is the randomness of a token: 16 bytes, 22 characters of
// base64url, unguessable and well inside the start parameter.
const linkTokenBytes = 16

// Put keeps value under a fresh token for ttl.
func (s *LinkStore) Put(ctx context.Context, value string, ttl time.Duration) (string, error) {
	var b [linkTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("redis: a deep link token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	if err := s.client.Raw().Set(ctx, deepLinkKey(token), value, ttl).Err(); err != nil {
		return "", fmt.Errorf("redis: keeping a deep link: %w", err)
	}
	return token, nil
}

// Get returns what a token keeps, "" once it has expired.
func (s *LinkStore) Get(ctx context.Context, token string) (string, error) {
	v, err := s.client.Raw().Get(ctx, deepLinkKey(token)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis: reading a deep link: %w", err)
	}
	return v, nil
}
