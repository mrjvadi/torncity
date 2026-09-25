package panel

import (
	"sync"
	"time"
)

// limiter is a token bucket per key: perMinute tokens a minute, up to
// perMinute at once. It forgets a key once its bucket has refilled, so its
// memory is bounded by the keys active within the last minute.
type limiter struct {
	mu        sync.Mutex
	perMinute float64
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(perMinute int) *limiter {
	return &limiter{perMinute: float64(perMinute), buckets: map[string]*bucket{}}
}

// allow takes one token for key at now, or reports how long until one is
// there.
func (l *limiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > time.Minute {
		for k, b := range l.buckets {
			if l.refill(b, now) >= l.perMinute {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.perMinute, at: now}
		l.buckets[key] = b
	}
	b.tokens, b.at = l.refill(b, now), now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.perMinute * float64(time.Minute))
	return false, wait
}

func (l *limiter) refill(b *bucket, now time.Time) float64 {
	t := b.tokens + now.Sub(b.at).Minutes()*l.perMinute
	if t > l.perMinute {
		t = l.perMinute
	}
	return t
}

// strikes counts failed sign-ins per name that has no account, locking it
// the way a real account is locked (lockout_after failures, then
// lockout_base doubling up to lockout_max), so a name's existence cannot be
// told from how its failures are answered.
type strikes struct {
	mu        sync.Mutex
	after     int
	base, max time.Duration
	names     map[string]*strike
}

type strike struct {
	count int
	until time.Time
	last  time.Time
}

func newStrikes(after int, base, max time.Duration) *strikes {
	return &strikes{after: after, base: base, max: max, names: map[string]*strike{}}
}

// lockedUntil is when name's lock ends, zero when it is not locked.
func (s *strikes) lockedUntil(name string, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.names[name]; ok && st.until.After(now) {
		return st.until
	}
	return time.Time{}
}

// fail records one failure and returns the lock it causes, if any.
func (s *strikes) fail(name string, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, st := range s.names {
		if now.Sub(st.last) > s.max+time.Hour {
			delete(s.names, k)
		}
	}
	st, ok := s.names[name]
	if !ok {
		st = &strike{}
		s.names[name] = st
	}
	st.count++
	st.last = now
	if st.count >= s.after {
		st.until = now.Add(lockFor(st.count, s.after, s.base, s.max))
	}
	return st.until
}

// lockFor is the lock after the n-th failure in a row: base at the after-th,
// doubling with each further one, capped at max. The database computes the
// same for a real account (postgres.PanelAccounts.LoginFailure).
func lockFor(n, after int, base, max time.Duration) time.Duration {
	d := base
	for i := after; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}
