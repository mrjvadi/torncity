package dedup

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// fakeDeduplicator is the Redis-backed port, without Redis. It records what it
// was asked so a test can prove the (bot_id, update_id) key is really what
// reaches the store.
type fakeDeduplicator struct {
	seen  map[string]bool
	calls []string
	err   error
}

func newFakeDeduplicator() *fakeDeduplicator {
	return &fakeDeduplicator{seen: make(map[string]bool)}
}

func (f *fakeDeduplicator) key(botID string, updateID int64) string {
	return botID + "/" + strconv.FormatInt(updateID, 10)
}

func (f *fakeDeduplicator) Seen(ctx context.Context, botID string, updateID int64) (bool, error) {
	key := f.key(botID, updateID)
	f.calls = append(f.calls, key)
	if f.err != nil {
		return false, f.err
	}
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

// TestAllowDropsARepeat is the ordinary case: long polling re-delivered an
// update the gateway already handled.
func TestAllowDropsARepeat(t *testing.T) {
	store := newFakeDeduplicator()
	filter, err := New(store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	allowed, err := filter.Allow(context.Background(), "bot07", 4711)
	if err != nil || !allowed {
		t.Fatalf("first Allow = (%v, %v), want (true, nil)", allowed, err)
	}

	allowed, err = filter.Allow(context.Background(), "bot07", 4711)
	if err != nil {
		t.Fatalf("second Allow returned an error: %v", err)
	}
	if allowed {
		t.Error("the same update was allowed twice")
	}
}

// TestUpdateIDsAreScopedPerBot: two bots number their updates independently,
// so the same id from two bots is two different updates.
func TestUpdateIDsAreScopedPerBot(t *testing.T) {
	store := newFakeDeduplicator()
	filter, _ := New(store)

	if allowed, _ := filter.Allow(context.Background(), "bot01", 100); !allowed {
		t.Fatal("bot01's update was rejected on first sight")
	}
	allowed, err := filter.Allow(context.Background(), "bot02", 100)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Error("bot02's update 100 was dropped because bot01 had an update 100")
	}

	want := []string{"bot01/100", "bot02/100"}
	if len(store.calls) != len(want) {
		t.Fatalf("the store saw %v, want %v", store.calls, want)
	}
	for i := range want {
		if store.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, store.calls[i], want[i])
		}
	}
}

// TestAllowFailsOpen pins the decision documented in the package comment. If
// someone ever changes it, this test is where the argument lives.
func TestAllowFailsOpen(t *testing.T) {
	redisDown := errors.New("redis: connection refused")
	store := newFakeDeduplicator()
	store.err = redisDown
	filter, _ := New(store)

	allowed, err := filter.Allow(context.Background(), "bot07", 4711)
	if !errors.Is(err, redisDown) {
		t.Errorf("Allow error = %v, want the store's error so the caller can log it", err)
	}
	if !allowed {
		t.Error("Allow dropped an update because the deduplicator was unavailable; " +
			"player commands must not be eaten during a Redis outage")
	}
}

func TestAllowRejectsAnEmptyBotID(t *testing.T) {
	filter, _ := New(newFakeDeduplicator())

	allowed, err := filter.Allow(context.Background(), "", 1)
	if err != ErrNoBotID {
		t.Errorf("Allow error = %v, want %v", err, ErrNoBotID)
	}
	if allowed {
		t.Error("an update with no bot id was allowed through")
	}
}

func TestNewRequiresAStore(t *testing.T) {
	if _, err := New(nil); err != ErrNoStore {
		t.Errorf("New(nil) = %v, want %v", err, ErrNoStore)
	}
}
