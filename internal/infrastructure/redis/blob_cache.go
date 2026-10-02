package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const blobPrefix = "client:blob:"

// BlobCache keeps small byte values for a while (a fetched profile photo).
type BlobCache struct{ client *Client }

// NewBlobCache returns the cache.
func NewBlobCache(c *Client) *BlobCache { return &BlobCache{client: c} }

// Get returns the value under key; found is false when there is none.
func (b *BlobCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := b.client.Raw().Get(ctx, blobPrefix+key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("redis: reading %s: %w", key, err)
	}
	return v, true, nil
}

// Set stores the value for ttl.
func (b *BlobCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := b.client.Raw().Set(ctx, blobPrefix+key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: storing %s: %w", key, err)
	}
	return nil
}
