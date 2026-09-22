package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/mrjvadi/torncity/internal/application"
	sharederrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// releaseTimeout bounds the unlock round trip.
//
// Release takes no context because the port's signature is `func()`, and it
// must still work when the request context that acquired the lock has already
// been cancelled — otherwise a cancelled request would hold its player's lock
// until the TTL expired, blocking that player for the full lease.
const releaseTimeout = 3 * time.Second

// tokenBytes is the size of the random fencing token. Sixteen bytes is far
// beyond any chance of two live holders drawing the same value.
const tokenBytes = 16

// compareAndDeleteSrc releases a lock only if the caller still owns it.
// It is held as a named constant so a test can assert that the guard is still
// there; go-redis does not expose a compiled script's source.
//
// A plain DEL is unsafe and the failure is not hypothetical: a handler that
// overruns its TTL loses the lock, a second handler acquires it, and the first
// one then finishes and DELs a lock that now belongs to somebody else. Two
// handlers run the same player's critical section concurrently and the lock
// has bought nothing. Comparing the token before deleting makes the release a
// no-op once ownership has moved on, and the comparison and the delete must be
// one atomic step, which on Redis means a script.
const compareAndDeleteSrc = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`

var compareAndDelete = goredis.NewScript(compareAndDeleteSrc)

// compareAndExpireSrc extends a lease only if the caller still owns it.
//
// Same reasoning as compareAndDeleteSrc: renewing a key whose token has changed
// would hand a lost lease back to its previous holder.
const compareAndExpireSrc = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`

var compareAndExpire = goredis.NewScript(compareAndExpireSrc)

// ErrLockHeld reports that another holder owns the player's lock.
//
// It is a Conflict, not an Internal error: nothing is broken, the caller
// simply lost a race and the player should be told to wait rather than shown
// a generic failure.
var ErrLockHeld = sharederrors.Conflict("that action is already in progress")

// PlayerLocker serialises critical operations for one player.
type PlayerLocker struct {
	client *Client
}

var _ application.PlayerLocker = (*PlayerLocker)(nil)

// NewPlayerLocker returns a locker backed by c.
func NewPlayerLocker(c *Client) *PlayerLocker { return &PlayerLocker{client: c} }

// Lock acquires the player's lock, returning a release function.
//
// The lock always carries a TTL. If this process is killed mid-handler the key
// expires on its own; a lock that outlived its owner forever would take a
// player out of the game until an operator noticed.
//
// The returned release is safe to call more than once — sync.Once collapses
// the extra calls — because the natural call site is a defer that may sit
// alongside an explicit early release.
func (l *PlayerLocker) Lock(ctx context.Context, playerID string, ttl time.Duration) (func(), error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("redis: player lock ttl must be positive, got %s", ttl)
	}

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("redis: player lock token: %w", err)
	}

	key := playerLockKey(playerID)

	acquired, err := l.client.Raw().SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: acquiring lock for player %s: %w", playerID, err)
	}
	if !acquired {
		return nil, ErrLockHeld
	}

	rdb := l.client.Raw()
	var once sync.Once

	return func() {
		once.Do(func() {
			// A fresh context: see releaseTimeout. The error is deliberately
			// dropped, because the port gives release nowhere to report one
			// and the TTL is the backstop — a failed release costs a delay,
			// never correctness.
			releaseCtx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
			defer cancel()

			_ = compareAndDelete.Run(releaseCtx, rdb, []string{key}, token).Err()
		})
	}, nil
}

// newToken returns a random ownership token.
func newToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
