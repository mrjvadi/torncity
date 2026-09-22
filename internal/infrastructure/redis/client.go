package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// Client owns the connection pool to Redis.
//
// It is a thin wrapper rather than a bare *goredis.Client so that the rest of
// the codebase depends on this package instead of on the driver, and so a
// future swap to a different client (or to a cluster client) changes one file.
type Client struct {
	rdb *goredis.Client
}

// New parses a redis:// URL, dials it and verifies the connection.
//
// The PING is not ceremony: go-redis connects lazily, so without it a wrong
// address or password surfaces at the first player command instead of at boot,
// which is exactly when nobody is watching the logs.
func New(ctx context.Context, url string) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		// The URL may carry a password, so only the parse failure is
		// reported, never the URL itself.
		return nil, fmt.Errorf("redis: invalid connection url: %w", err)
	}

	rdb := goredis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		// Close what was opened; a failed constructor must not leak a pool.
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Raw exposes the underlying client for adapters in this package.
func (c *Client) Raw() *goredis.Client { return c.rdb }

// Close releases the connection pool.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}
