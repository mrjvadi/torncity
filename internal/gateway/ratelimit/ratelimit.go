// Package ratelimit paces outbound Bot API calls, with one independent gate
// per bot.
//
// # Why one gate per bot is the entire point
//
// ADR 0001 is explicit about what a fleet of bots does and does not buy. It
// does not raise Telegram's per-chat limit, because that limit is per chat.
// What it buys is fault isolation: when bot07 is told to wait eight seconds,
// bot07's queue stops and everybody playing through bot03 keeps playing. With
// a single shared limiter, one flood-wait would freeze the whole game, which
// is precisely the failure mode the fleet exists to prevent.
//
// So the invariant this package must never break is: state is keyed by bot,
// and PauseFor stalls one key. There is no global gate here, deliberately.
// MASTER_PROMPT section 16 says the same thing in one line: if bot07 gets a
// retry_after, only bot07 is limited.
//
// # Why the default is 25 per second
//
// The FAQ figure quoted in ADR 0001 is "about 30 messages per second" for bulk
// notifications, and the word is "about": it is not a contract, Telegram
// enforces it on its own clock, and a burst over the line is answered with
// 429, not with backpressure. A limiter set to the exact documented ceiling
// will cross it, because our clock, the network and the local Bot API server
// (ADR 0003, which changes nothing about rate limits) each add jitter in the
// wrong direction. DefaultRate leaves that headroom. It is a default, not a
// law: telegram_bots.rate_limit is per bot (MASTER_PROMPT section 4), and
// SetRate applies it.
//
// The per-chat limit of one message per second is a different constraint and
// is not this package's job: this gate is per bot, and a per-chat gate belongs
// with the sender that knows which chat it is writing to.
package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultRate is the per-bot allowance in messages per second. See the
	// package doc for why it sits below the documented ~30/s figure.
	DefaultRate = 25

	// DefaultBurst is how many messages may be sent back to back after an
	// idle period. One means strictly paced output. ADR 0001 records that
	// Telegram tolerates "short bursts" but answers them with 429 often
	// enough that spending the allowance evenly is the safer default.
	DefaultBurst = 1
)

// ErrNoBotKey rejects an empty key. An empty key would silently merge every
// bot into one bucket and destroy the isolation this package exists for, so it
// is a hard error rather than a tolerated default.
var ErrNoBotKey = errors.New("ratelimit: bot key is required")

// Config builds a Limiter. The zero value is valid and yields DefaultRate,
// DefaultBurst and the real clock.
type Config struct {
	// Rate is the default allowance, in messages per second, for a bot with
	// no rate of its own. Zero or negative means DefaultRate.
	Rate int

	// Burst is the bucket depth. Zero or negative means DefaultBurst.
	Burst int

	// Now and Timer exist so tests can drive the limiter without sleeping.
	// Both are optional; leaving them nil uses time.Now and time.NewTimer.
	Now   func() time.Time
	Timer func(d time.Duration) (<-chan time.Time, func())
}

// Limiter hands out permission to call the Bot API, one bucket per bot.
//
// It is safe for concurrent use. Its mutex protects the map of buckets only;
// each bucket has its own lock, so two bots never contend with one another and
// a bot parked on a long flood-wait holds nothing that another bot needs.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	rate  float64
	burst float64

	now   func() time.Time
	timer func(d time.Duration) (<-chan time.Time, func())
}

// New builds a Limiter from cfg.
func New(cfg Config) *Limiter {
	rate := float64(cfg.Rate)
	if rate <= 0 {
		rate = DefaultRate
	}
	burst := float64(cfg.Burst)
	if burst <= 0 {
		burst = DefaultBurst
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	tmr := cfg.Timer
	if tmr == nil {
		tmr = realTimer
	}

	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
		now:     now,
		timer:   tmr,
	}
}

// realTimer is the production timer. It returns a stop function so a Wait that
// loses the race to a cancelled context does not leave a timer alive until it
// fires; with a flood-wait of tens of seconds that is not a theoretical leak.
func realTimer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

// bucket is one bot's token bucket plus its flood-wait pause.
type bucket struct {
	mu sync.Mutex

	rate  float64
	burst float64

	tokens float64
	last   time.Time

	// pausedUntil is set by PauseFor. While it is in the future the bucket
	// hands out nothing, whatever the token count says.
	pausedUntil time.Time
}

// Wait blocks until botKey may send, or until ctx is done.
//
// It returns ctx.Err() on cancellation and nil when permission is granted. A
// granted permission is consumed: every Wait that returns nil corresponds to
// exactly one Bot API call the caller is now expected to make.
//
// Callers waiting on the same bot are not queued in arrival order. Ordering
// within one bot belongs to that bot's send queue (MASTER_PROMPT section 15,
// where a P0 user command must not sit behind a P3 broadcast); a limiter that
// enforced its own FIFO would quietly fight that priority queue.
func (l *Limiter) Wait(ctx context.Context, botKey string) error {
	if botKey == "" {
		return ErrNoBotKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b := l.bucketFor(botKey)
	for {
		wait, ok := b.take(l.now())
		if ok {
			return nil
		}

		ch, stop := l.timer(wait)
		select {
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-ch:
			stop()
		}
	}
}

// PauseFor stalls one bot for d, and only that bot.
//
// This is what the sender calls when the client returns a
// *client.FloodWaitError: pass that error's RetryAfter. Pausing is additive
// only in the sense that the furthest deadline wins; a shorter pause never
// shortens a longer one that is already running.
//
// A non-positive d does nothing, so a caller need not guard against a missing
// retry_after (the client already substitutes its DefaultFloodWait).
func (l *Limiter) PauseFor(botKey string, d time.Duration) {
	if botKey == "" || d <= 0 {
		return
	}

	b := l.bucketFor(botKey)
	now := l.now()
	until := now.Add(d)

	b.mu.Lock()
	defer b.mu.Unlock()
	if until.After(b.pausedUntil) {
		b.pausedUntil = until
	}

	// The bucket restarts empty from the end of the pause. Telegram answered
	// 429, so resuming with a full bucket and firing a burst is the one
	// reaction guaranteed to earn a longer ban.
	b.tokens = 0
	b.last = b.pausedUntil
}

// PausedUntil reports when a bot may send again, and false when it is not
// paused. It is for health endpoints and logs; Wait does not need it.
func (l *Limiter) PausedUntil(botKey string) (time.Time, bool) {
	b := l.bucketFor(botKey)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pausedUntil.After(l.now()) {
		return b.pausedUntil, true
	}
	return time.Time{}, false
}

// SetRate applies one bot's configured allowance, as read from the bot
// registry (telegram_bots.rate_limit). A rate of zero or less restores the
// limiter's default rather than stopping the bot: a misconfigured row must not
// silently take a bot off the air.
func (l *Limiter) SetRate(botKey string, rate int) {
	if botKey == "" {
		return
	}

	effective := float64(rate)
	if effective <= 0 {
		effective = l.rate
	}

	b := l.bucketFor(botKey)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.rate = effective
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}

// bucketFor returns the bot's bucket, creating it on first use. A bot that has
// never sent starts with a full bucket, so the first message of a quiet bot
// goes out immediately.
func (l *Limiter) bucketFor(botKey string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()

	if b, ok := l.buckets[botKey]; ok {
		return b
	}
	b := &bucket{
		rate:   l.rate,
		burst:  l.burst,
		tokens: l.burst,
		last:   l.now(),
	}
	l.buckets[botKey] = b
	return b
}

// take consumes a token if one is available.
//
// It reports either (0, true) — go ahead — or (d, false), where d is how long
// the caller should sleep before asking again. It never blocks, so the bucket
// lock is never held across a sleep and PauseFor can always get in.
func (b *bucket) take(now time.Time) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if now.Before(b.pausedUntil) {
		return b.pausedUntil.Sub(now), false
	}

	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}

	// Time until the bucket holds one whole token. Rounded up to a whole
	// nanosecond, so a rounding error can never produce a zero-length sleep
	// and spin this into a busy loop.
	missing := 1 - b.tokens
	return time.Duration(missing/b.rate*float64(time.Second)) + time.Nanosecond, false
}
