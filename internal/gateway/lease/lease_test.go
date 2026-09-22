package lease

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeLease is the Redis lock without Redis. It records every call in order,
// so a test can assert on what the keeper did rather than on what it says it
// does.
type fakeLease struct {
	mu    sync.Mutex
	calls []record

	acquired   bool
	acquireErr error

	renewed  bool
	renewErr error

	releaseErr error

	// releaseCtxErr is the ctx.Err() observed inside Release. It proves the
	// release did not run on the context that was just cancelled.
	releaseCtxErr error

	renewSignal   chan struct{}
	releaseSignal chan struct{}
}

type record struct {
	method  string
	botKey  string
	ownerID string
	ttl     time.Duration
}

func newFakeLease() *fakeLease {
	return &fakeLease{
		acquired:      true,
		renewed:       true,
		renewSignal:   make(chan struct{}, 64),
		releaseSignal: make(chan struct{}, 1),
	}
}

func (f *fakeLease) Acquire(ctx context.Context, botKey, ownerID string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, record{"acquire", botKey, ownerID, ttl})
	acquired, err := f.acquired, f.acquireErr
	f.mu.Unlock()
	return acquired, err
}

func (f *fakeLease) Renew(ctx context.Context, botKey, ownerID string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, record{"renew", botKey, ownerID, ttl})
	renewed, err := f.renewed, f.renewErr
	f.mu.Unlock()

	select {
	case f.renewSignal <- struct{}{}:
	default:
	}
	return renewed, err
}

func (f *fakeLease) Release(ctx context.Context, botKey, ownerID string) error {
	f.mu.Lock()
	f.calls = append(f.calls, record{method: "release", botKey: botKey, ownerID: ownerID})
	f.releaseCtxErr = ctx.Err()
	err := f.releaseErr
	f.mu.Unlock()

	select {
	case f.releaseSignal <- struct{}{}:
	default:
	}
	return err
}

func (f *fakeLease) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.method)
	}
	return out
}

func (f *fakeLease) count(method string) int {
	n := 0
	for _, m := range f.methods() {
		if m == method {
			n++
		}
	}
	return n
}

func testKeeper(t *testing.T, l Lease) *Keeper {
	t.Helper()

	// A 60ms TTL renewed every 5ms: the same shape as production, three
	// orders of magnitude faster.
	keeper, err := NewKeeper(KeeperConfig{
		Lease:          l,
		BotKey:         "bot01",
		OwnerID:        "gateway-02",
		TTL:            60 * time.Millisecond,
		RenewEvery:     5 * time.Millisecond,
		ReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewKeeper: %v", err)
	}
	return keeper
}

// TestKeeperRenewsAndReleases is the whole contract of section 57: hold the
// bot by renewing, and hand it back when told to stop.
func TestKeeperRenewsAndReleases(t *testing.T) {
	fake := newFakeLease()
	keeper := testKeeper(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- keeper.Run(ctx) }()

	// Wait for three renewals rather than sleeping for a fixed time: the
	// assertion is "it renews", not "it renews within n milliseconds".
	for i := 0; i < 3; i++ {
		select {
		case <-fake.renewSignal:
		case <-time.After(time.Second):
			t.Fatalf("only %d renewals after a second; the keeper is not renewing", i)
		}
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v on a clean shutdown, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}

	if fake.count("acquire") != 1 {
		t.Errorf("acquire was called %d times, want once", fake.count("acquire"))
	}
	if got := fake.count("renew"); got < 3 {
		t.Errorf("renew was called %d times, want at least 3", got)
	}
	if got := fake.count("release"); got != 1 {
		t.Errorf("release was called %d times, want exactly once; a bot that is not released "+
			"stays dark until its ttl expires", got)
	}
	if methods := fake.methods(); methods[0] != "acquire" || methods[len(methods)-1] != "release" {
		t.Errorf("call order was %v, want acquire first and release last", methods)
	}

	// Every call must carry this instance's identity and the configured ttl,
	// or the lock cannot tell holders apart.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, c := range fake.calls {
		if c.botKey != "bot01" || c.ownerID != "gateway-02" {
			t.Errorf("%s was called for %q/%q, want bot01/gateway-02", c.method, c.botKey, c.ownerID)
		}
		if c.method != "release" && c.ttl != 60*time.Millisecond {
			t.Errorf("%s used ttl %s, want the configured 60ms", c.method, c.ttl)
		}
	}
}

// TestReleaseRunsOnALiveContext guards the bug that makes a release silently
// do nothing: performing it with the context that was just cancelled.
func TestReleaseRunsOnALiveContext(t *testing.T) {
	fake := newFakeLease()
	keeper := testKeeper(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- keeper.Run(ctx) }()

	select {
	case <-fake.renewSignal:
	case <-time.After(time.Second):
		t.Fatal("the keeper never renewed")
	}
	cancel()

	select {
	case <-fake.releaseSignal:
	case <-time.After(time.Second):
		t.Fatal("the lease was never released")
	}
	<-done

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.releaseCtxErr != nil {
		t.Errorf("Release ran on a context that was already done (%v); "+
			"a real implementation would refuse the call and the bot would stay locked",
			fake.releaseCtxErr)
	}
}

// TestKeeperDoesNotStartWhenTheBotIsTaken: in a cluster this is the normal
// answer for most bots, and it must not look like a failure.
func TestKeeperDoesNotStartWhenTheBotIsTaken(t *testing.T) {
	fake := newFakeLease()
	fake.acquired = false
	keeper := testKeeper(t, fake)

	err := keeper.Run(context.Background())
	if !errors.Is(err, ErrNotAcquired) {
		t.Fatalf("Run error = %v, want %v", err, ErrNotAcquired)
	}
	if fake.count("release") != 0 {
		t.Error("the keeper released a lease it never held, which would free another instance's bot")
	}
	if fake.count("renew") != 0 {
		t.Error("the keeper renewed a lease it never held")
	}
}

// TestKeeperStopsWhenTheLeaseIsLost covers the partition case: this instance
// was slow, the TTL expired, somebody else took the bot.
func TestKeeperStopsWhenTheLeaseIsLost(t *testing.T) {
	fake := newFakeLease()
	fake.renewed = false
	keeper := testKeeper(t, fake)

	err := keeper.Run(context.Background())
	if !errors.Is(err, ErrLost) {
		t.Fatalf("Run error = %v, want %v", err, ErrLost)
	}
	if fake.count("release") != 0 {
		t.Error("the keeper released a lease it had already lost, deleting the new holder's claim")
	}
}

func TestKeeperReportsUnderlyingFailures(t *testing.T) {
	t.Run("acquire fails", func(t *testing.T) {
		redisDown := errors.New("redis: connection refused")
		fake := newFakeLease()
		fake.acquireErr = redisDown

		err := testKeeper(t, fake).Run(context.Background())
		if !errors.Is(err, redisDown) {
			t.Errorf("Run error = %v, want the underlying failure", err)
		}
	})

	t.Run("renew fails", func(t *testing.T) {
		redisDown := errors.New("redis: connection refused")
		fake := newFakeLease()
		fake.renewErr = redisDown

		err := testKeeper(t, fake).Run(context.Background())
		if !errors.Is(err, redisDown) {
			t.Errorf("Run error = %v, want the underlying failure", err)
		}
	})
}

// TestReleaseFailureIsNotFatal: the gateway is shutting down and can do
// nothing about it. The TTL is the backstop.
func TestReleaseFailureIsNotFatal(t *testing.T) {
	fake := newFakeLease()
	fake.releaseErr = errors.New("redis: connection refused")
	keeper := testKeeper(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- keeper.Run(ctx) }()

	select {
	case <-fake.renewSignal:
	case <-time.After(time.Second):
		t.Fatal("the keeper never renewed")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil: a failed release is not the caller's problem", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestNewKeeperValidation(t *testing.T) {
	valid := KeeperConfig{Lease: newFakeLease(), BotKey: "bot01", OwnerID: "gateway-01"}

	tests := []struct {
		name    string
		mutate  func(*KeeperConfig)
		wantErr error
	}{
		{name: "no lease", mutate: func(c *KeeperConfig) { c.Lease = nil }, wantErr: ErrNoLease},
		{name: "no bot key", mutate: func(c *KeeperConfig) { c.BotKey = "  " }, wantErr: ErrNoBotKey},
		{name: "no owner id", mutate: func(c *KeeperConfig) { c.OwnerID = "" }, wantErr: ErrNoOwnerID},
		{name: "negative ttl", mutate: func(c *KeeperConfig) { c.TTL = -time.Second }, wantErr: ErrBadTTL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if _, err := NewKeeper(cfg); err != tt.wantErr {
				t.Errorf("NewKeeper error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	t.Run("renew interval at least as long as the ttl", func(t *testing.T) {
		cfg := valid
		cfg.TTL = 10 * time.Second
		cfg.RenewEvery = 10 * time.Second
		if _, err := NewKeeper(cfg); err == nil {
			t.Error("NewKeeper accepted a renew interval equal to the ttl; the bot would flap between instances")
		}
	})

	t.Run("defaults", func(t *testing.T) {
		keeper, err := NewKeeper(valid)
		if err != nil {
			t.Fatalf("NewKeeper: %v", err)
		}
		if keeper.ttl != DefaultTTL {
			t.Errorf("ttl = %s, want %s", keeper.ttl, DefaultTTL)
		}
		if keeper.renewEvery != DefaultTTL/DefaultRenewDivisor {
			t.Errorf("renewEvery = %s, want %s", keeper.renewEvery, DefaultTTL/DefaultRenewDivisor)
		}
		if keeper.renewEvery >= keeper.ttl {
			t.Error("the default renew interval is not shorter than the default ttl")
		}
		if keeper.releaseTimeout != DefaultReleaseTimeout {
			t.Errorf("releaseTimeout = %s, want %s", keeper.releaseTimeout, DefaultReleaseTimeout)
		}
		if keeper.BotKey() != "bot01" {
			t.Errorf("BotKey = %q, want bot01", keeper.BotKey())
		}
	})
}

// TestKeeperAcceptsACustomTicker keeps the injection point honest: without it
// a test of a production-sized TTL would have to sleep for ten seconds.
func TestKeeperAcceptsACustomTicker(t *testing.T) {
	fake := newFakeLease()
	ticks := make(chan time.Time, 1)
	stopped := false

	keeper, err := NewKeeper(KeeperConfig{
		Lease:   fake,
		BotKey:  "bot01",
		OwnerID: "gateway-01",
		Ticker: func(time.Duration) (<-chan time.Time, func()) {
			return ticks, func() { stopped = true }
		},
	})
	if err != nil {
		t.Fatalf("NewKeeper: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- keeper.Run(ctx) }()

	ticks <- time.Now()
	select {
	case <-fake.renewSignal:
	case <-time.After(time.Second):
		t.Fatal("a tick did not produce a renewal")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return")
	}
	if !stopped {
		t.Error("the ticker was not stopped; a keeper per bot would leak one ticker per bot")
	}
}
