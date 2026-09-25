// Package moderation answers the gateway's question before it publishes a
// command: may this Telegram user play here right now?
//
// An operator mutes a player (no commands in groups) or bans them (no
// commands anywhere) from the web panel, for a time or until lifted
// (migrations/0035). The gateway asks for every command it is about to
// publish, so the answer must be cheap: it is cached in Redis for a short
// TTL (panel.moderation_cache_ttl), and a moderation takes effect within
// that long. A cache or database failure lets the command through — a
// broken cache must not stop the game — and is logged by the caller.
package moderation

import (
	"context"
	"time"
)

// Standing is what moderation stands against a user.
type Standing struct {
	Muted, Banned bool
	// Until is when the earliest standing moderation ends; zero for none,
	// or for one that stands until lifted.
	Until time.Time
}

// Source reads the standing from the source of truth.
type Source interface {
	Standing(ctx context.Context, telegramUserID int64, now time.Time) (Standing, error)
}

// Cache keeps standings for a while.
type Cache interface {
	// Get returns the cached standing and whether there was one.
	Get(ctx context.Context, telegramUserID int64) (Standing, bool, error)
	Set(ctx context.Context, telegramUserID int64, s Standing, ttl time.Duration) error
}

// Checker answers Blocks with the cache in front of the source.
type Checker struct {
	Source Source
	Cache  Cache
	TTL    time.Duration
	Now    func() time.Time
}

// Verdict is the answer for one command.
type Verdict int

const (
	// Allowed lets the command through.
	Allowed Verdict = iota
	// Muted drops a command sent in a group.
	Muted
	// Banned drops any command.
	Banned
)

// Blocks decides one command: from a user, in a group or not. err is set
// when the answer could not be read; the verdict is then Allowed.
func (c *Checker) Blocks(ctx context.Context, telegramUserID int64, inGroup bool) (Verdict, error) {
	if c == nil || c.Source == nil || telegramUserID == 0 {
		return Allowed, nil
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	s, err := c.standing(ctx, telegramUserID, now)
	if err != nil {
		return Allowed, err
	}
	return verdict(s, inGroup, now), nil
}

func (c *Checker) standing(ctx context.Context, id int64, now time.Time) (Standing, error) {
	if c.Cache != nil {
		if s, ok, err := c.Cache.Get(ctx, id); err == nil && ok {
			return s, nil
		}
	}
	s, err := c.Source.Standing(ctx, id, now)
	if err != nil {
		return Standing{}, err
	}
	if c.Cache != nil && c.TTL > 0 {
		ttl := c.TTL
		// A moderation ending within the TTL is not remembered past its end.
		if !s.Until.IsZero() && s.Until.Sub(now) < ttl {
			ttl = max(s.Until.Sub(now), time.Second)
		}
		_ = c.Cache.Set(ctx, id, s, ttl)
	}
	return s, nil
}

// verdict applies a standing to one command.
func verdict(s Standing, inGroup bool, now time.Time) Verdict {
	if !s.Until.IsZero() && !now.Before(s.Until) {
		// Cached past its end: the source will say so on the next read.
		return Allowed
	}
	switch {
	case s.Banned:
		return Banned
	case s.Muted && inGroup:
		return Muted
	}
	return Allowed
}
