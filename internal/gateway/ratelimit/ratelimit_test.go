package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock makes the limiter's waiting instantaneous while still accounting
// for it. Its timer does not wait: it advances virtual time by the requested
// duration and fires at once, so a test can assert on how long the limiter
// intended to wait without spending that long.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) timer(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	fired := c.now
	c.mu.Unlock()

	ch := make(chan time.Time, 1)
	ch <- fired
	return ch, func() {}
}

// elapsed reports how much virtual time has passed since start.
func (c *fakeClock) elapsed(start time.Time) time.Duration {
	return c.Now().Sub(start)
}

func newFakeLimiter(cfg Config) (*Limiter, *fakeClock) {
	clock := newFakeClock()
	cfg.Now = clock.Now
	cfg.Timer = clock.timer
	return New(cfg), clock
}

// TestPauseIsolatesOneBot is the test this package exists for. ADR 0001 and
// MASTER_PROMPT section 16 both promise that a flood-wait on one bot leaves
// the rest of the fleet running; if this test ever fails, the fleet has
// stopped buying the only thing it was adopted for.
func TestPauseIsolatesOneBot(t *testing.T) {
	limiter, clock := newFakeLimiter(Config{Rate: 25})

	// The example from MASTER_PROMPT section 16: bot07 got retry_after = 8.
	limiter.PauseFor("bot07", 8*time.Second)

	start := clock.Now()
	if err := limiter.Wait(context.Background(), "bot03"); err != nil {
		t.Fatalf("Wait(bot03) returned an error: %v", err)
	}
	if delayed := clock.elapsed(start); delayed != 0 {
		t.Errorf("bot03 was delayed by %s because bot07 is paused; the fleet is not isolated", delayed)
	}

	start = clock.Now()
	if err := limiter.Wait(context.Background(), "bot07"); err != nil {
		t.Fatalf("Wait(bot07) returned an error: %v", err)
	}
	if waited := clock.elapsed(start); waited < 8*time.Second {
		t.Errorf("bot07 waited %s, want at least 8s (its own flood-wait)", waited)
	}
}

// TestPauseIsolatesOneBotOnTheRealClock repeats the same claim against real
// time, because the fake clock cannot catch a limiter that accidentally holds
// a shared lock across a sleep: that failure is about goroutines, not
// durations.
func TestPauseIsolatesOneBotOnTheRealClock(t *testing.T) {
	limiter := New(Config{Rate: 1000})
	limiter.PauseFor("botA", 300*time.Millisecond)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		_ = limiter.Wait(context.Background(), "botB")
		done <- time.Since(start)
	}()

	select {
	case took := <-done:
		if took > 100*time.Millisecond {
			t.Errorf("botB waited %s while botA was paused", took)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("botB is still blocked while botA is paused; one bot's flood-wait is stalling the fleet")
	}

	start := time.Now()
	if err := limiter.Wait(context.Background(), "botA"); err != nil {
		t.Fatalf("Wait(botA) returned an error: %v", err)
	}
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Errorf("botA waited only %s; its pause was not honoured", took)
	}
}

// TestWaitRespectsContextCancellation covers shutdown (section 85): a gateway
// draining its queues must not sit on a flood-wait for half a minute.
func TestWaitRespectsContextCancellation(t *testing.T) {
	limiter := New(Config{Rate: 1000})
	limiter.PauseFor("botA", 30*time.Second)

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := limiter.Wait(ctx, "botA")
		if err != context.DeadlineExceeded {
			t.Fatalf("Wait error = %v, want %v", err, context.DeadlineExceeded)
		}
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Errorf("Wait took %s to notice the deadline", took)
		}
	})

	t.Run("cancel while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		errs := make(chan error, 1)
		go func() { errs <- limiter.Wait(ctx, "botA") }()

		cancel()
		select {
		case err := <-errs:
			if err != context.Canceled {
				t.Errorf("Wait error = %v, want %v", err, context.Canceled)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Wait did not return after its context was cancelled")
		}
	})

	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := limiter.Wait(ctx, "botB"); err != context.Canceled {
			t.Errorf("Wait error = %v, want %v", err, context.Canceled)
		}
	})
}

// TestWaitPacesOneBot checks the gate itself: the first call is free, the rest
// are spaced by the configured rate.
func TestWaitPacesOneBot(t *testing.T) {
	const rate = 25
	limiter, clock := newFakeLimiter(Config{Rate: rate})

	start := clock.Now()
	for i := 0; i < 5; i++ {
		if err := limiter.Wait(context.Background(), "bot01"); err != nil {
			t.Fatalf("Wait %d returned an error: %v", i, err)
		}
	}

	// Four gaps between five messages, each at least 1/rate of a second.
	want := 4 * time.Second / rate
	if got := clock.elapsed(start); got < want {
		t.Errorf("five messages took %s of virtual time, want at least %s at %d/s", got, want, rate)
	}
}

// TestSetRateAppliesPerBot proves telegram_bots.rate_limit actually reaches
// the gate, and that one bot's rate is not another's.
func TestSetRateAppliesPerBot(t *testing.T) {
	limiter, clock := newFakeLimiter(Config{Rate: 25})
	limiter.SetRate("slow", 1)

	// Drain the initial token of each bucket so the next call must wait.
	for _, key := range []string{"slow", "fast"} {
		if err := limiter.Wait(context.Background(), key); err != nil {
			t.Fatalf("priming Wait(%s): %v", key, err)
		}
	}

	start := clock.Now()
	if err := limiter.Wait(context.Background(), "fast"); err != nil {
		t.Fatalf("Wait(fast): %v", err)
	}
	fast := clock.elapsed(start)

	start = clock.Now()
	if err := limiter.Wait(context.Background(), "slow"); err != nil {
		t.Fatalf("Wait(slow): %v", err)
	}
	slow := clock.elapsed(start)

	// Not a full second: the fast bot's own 40ms of waiting refilled part of
	// the slow bucket, which is exactly how a token bucket should behave.
	if slow < 900*time.Millisecond {
		t.Errorf("the 1/s bot waited %s, want close to a second", slow)
	}
	if fast >= slow {
		t.Errorf("the default-rate bot waited %s and the 1/s bot %s; SetRate is not per bot", fast, slow)
	}
}

// TestConfigDefaults records what an empty Config means, since every caller
// that does not care about rates gets exactly this.
func TestConfigDefaults(t *testing.T) {
	limiter := New(Config{})
	if limiter.rate != DefaultRate {
		t.Errorf("default rate = %v, want %d", limiter.rate, DefaultRate)
	}
	if limiter.burst != DefaultBurst {
		t.Errorf("default burst = %v, want %d", limiter.burst, DefaultBurst)
	}
	if DefaultRate >= 30 {
		t.Errorf("DefaultRate = %d, which is not below the ~30/s figure ADR 0001 records", DefaultRate)
	}
}

func TestWaitRejectsAnEmptyBotKey(t *testing.T) {
	limiter := New(Config{})
	if err := limiter.Wait(context.Background(), ""); err != ErrNoBotKey {
		t.Errorf("Wait(\"\") error = %v, want %v", err, ErrNoBotKey)
	}
}

// TestPausedUntil covers the reporting helper, including the case that matters
// most: a pause that has expired must not look active.
func TestPausedUntil(t *testing.T) {
	limiter, clock := newFakeLimiter(Config{Rate: 25})

	if _, paused := limiter.PausedUntil("bot01"); paused {
		t.Error("a fresh bot reports as paused")
	}

	limiter.PauseFor("bot01", 5*time.Second)
	until, paused := limiter.PausedUntil("bot01")
	if !paused {
		t.Fatal("a paused bot does not report as paused")
	}
	if want := clock.Now().Add(5 * time.Second); !until.Equal(want) {
		t.Errorf("PausedUntil = %s, want %s", until, want)
	}

	// A shorter pause must not cut a longer one short.
	limiter.PauseFor("bot01", time.Second)
	if shortened, _ := limiter.PausedUntil("bot01"); !shortened.Equal(until) {
		t.Errorf("PausedUntil = %s after a shorter pause, want the longer deadline %s", shortened, until)
	}

	if err := limiter.Wait(context.Background(), "bot01"); err != nil {
		t.Fatalf("Wait returned an error: %v", err)
	}
	if _, paused := limiter.PausedUntil("bot01"); paused {
		t.Error("the bot still reports as paused after its pause elapsed")
	}
}

// TestPauseForIgnoresNonPositiveDurations means a caller can forward a
// retry_after without guarding it.
func TestPauseForIgnoresNonPositiveDurations(t *testing.T) {
	limiter, clock := newFakeLimiter(Config{Rate: 25})
	limiter.PauseFor("bot01", 0)
	limiter.PauseFor("bot01", -time.Second)
	limiter.PauseFor("", time.Hour)

	start := clock.Now()
	if err := limiter.Wait(context.Background(), "bot01"); err != nil {
		t.Fatalf("Wait returned an error: %v", err)
	}
	if delayed := clock.elapsed(start); delayed != 0 {
		t.Errorf("a non-positive pause delayed the bot by %s", delayed)
	}
}

// TestConcurrentWaitsAreRaceFree exists for `go test -race`: the limiter is
// shared by every sender goroutine in the gateway.
func TestConcurrentWaitsAreRaceFree(t *testing.T) {
	limiter := New(Config{Rate: 10000})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "bot0" + string(rune('0'+i%3))
			for j := 0; j < 5; j++ {
				if err := limiter.Wait(context.Background(), key); err != nil {
					t.Errorf("Wait returned an error: %v", err)
					return
				}
			}
			limiter.PauseFor(key, time.Millisecond)
			limiter.SetRate(key, 500)
		}(i)
	}
	wg.Wait()
}
