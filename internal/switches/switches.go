// Package switches answers one question an operator can flip at runtime,
// without a redeploy: what is switch X set to, right now?
//
// It is the generic half of migrations/0041_runtime_switches: a small
// key/value table in Postgres holds each switch's current value with who
// set it, when and why (internal/infrastructure/postgres.SwitchOps), and
// this package caches the answer in Redis for a short TTL, exactly as
// internal/gateway/moderation caches a player's standing — because both are
// asked on every command the gateway is about to publish, and both must
// answer fast.
//
// # Fail open, deliberately
//
// A cache miss falls back to the database; a database failure falls back to
// the given default and returns the error for the caller to log. A broken
// switch must never take the game down, so "cannot tell" always means
// "behave as if the switch were left at its default" — which for
// telegram_play is "on": Telegram play keeps working exactly as it does
// today unless an operator can be positively confirmed to have turned it
// off.
//
// This package also names the two switches this feature ships
// (KeyTelegramPlay, KeyTelegramNotices) and their valid values, so the
// gateway, the notifier, the CLI and the panel check against one table
// instead of four copies of the same strings.
package switches

import (
	"context"
	"time"
)

// The switches this feature reads and writes. A future switch is one more
// constant here, one more row in operator_switches — the storage and the
// caching beneath it need no change.
const (
	// KeyTelegramPlay controls whether a command from Telegram is played or
	// answered with a redirect to the web game (PlayOn, PlayGroupsOff,
	// PlayOff).
	KeyTelegramPlay = "telegram_play"
	// KeyTelegramNotices controls whether Telegram notices still go out
	// while telegram_play is off (NoticesOn, NoticesOff).
	KeyTelegramNotices = "telegram_notices"
)

// telegram_play's values.
const (
	PlayOn        = "on"
	PlayGroupsOff = "groups_off"
	PlayOff       = "off"
)

// telegram_notices's values.
const (
	NoticesOn  = "on"
	NoticesOff = "off"
)

// ValidPlayMode reports whether v is one telegram_play accepts.
func ValidPlayMode(v string) bool {
	switch v {
	case PlayOn, PlayGroupsOff, PlayOff:
		return true
	default:
		return false
	}
}

// ValidNoticeMode reports whether v is one telegram_notices accepts.
func ValidNoticeMode(v string) bool {
	return v == NoticesOn || v == NoticesOff
}

// Blocked reports whether a command arriving in this chat must be refused
// with the redirect, given telegram_play's current mode.
func Blocked(mode string, inGroup bool) bool {
	switch mode {
	case PlayOff:
		return true
	case PlayGroupsOff:
		return inGroup
	default:
		return false
	}
}

// Source reads a switch's current value from the database.
// internal/infrastructure/postgres.SwitchOps implements it. found is false
// for a switch nobody has ever set; the caller's default applies.
type Source interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
}

// Cache keeps a switch's value for a while, alongside when it was cached —
// the age a health line reports. internal/infrastructure/redis.SwitchCache
// implements it.
type Cache interface {
	Get(ctx context.Context, key string) (value string, cachedAt time.Time, ok bool, err error)
	Set(ctx context.Context, key, value string, cachedAt time.Time, ttl time.Duration) error
}

// Reader answers Get with the cache in front of the database.
type Reader struct {
	Source Source
	Cache  Cache
	TTL    time.Duration
	// Now exists so tests can fix the clock; nil means time.Now.
	Now func() time.Time
}

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Get returns key's effective value: def when nothing says otherwise —
// nobody has set the switch, or its cache and its database are both
// unreachable. age is how long ago the answer was cached; zero for an
// answer read fresh from the database or a default from failure. err is
// set only on failure, for the caller to log; the value returned alongside
// it is still the correct one to act on (the default).
func (r *Reader) Get(ctx context.Context, key, def string) (value string, age time.Duration, err error) {
	if r == nil || r.Source == nil {
		return def, 0, nil
	}
	now := r.now()
	if r.Cache != nil {
		if v, at, ok, cerr := r.Cache.Get(ctx, key); cerr == nil && ok {
			return v, now.Sub(at), nil
		}
	}
	v, found, serr := r.Source.Get(ctx, key)
	if serr != nil {
		return def, 0, serr
	}
	if !found {
		v = def
	}
	if r.Cache != nil && r.TTL > 0 {
		_ = r.Cache.Set(ctx, key, v, now, r.TTL)
	}
	return v, 0, nil
}
