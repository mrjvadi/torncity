package redis

import (
	"strings"
	"testing"
	"time"
)

// validateLease is the guard that keeps an unsafe lease from ever being
// written, so it is tested directly: every rejection here is a production
// failure mode (indistinguishable holders, or a key with no expiry that would
// strand a bot forever if its owner died).
func TestValidateLease(t *testing.T) {
	tests := []struct {
		name    string
		botKey  string
		holder  string
		ttl     time.Duration
		wantErr bool
	}{
		{"valid", "bot01", "gateway-a", 30 * time.Second, false},
		{"empty bot key", "", "gateway-a", 30 * time.Second, true},
		{"empty holder", "bot01", "", 30 * time.Second, true},
		{"zero ttl", "bot01", "gateway-a", 0, true},
		{"negative ttl", "bot01", "gateway-a", -time.Second, true},
		{"sub-second ttl is allowed", "bot01", "gateway-a", 250 * time.Millisecond, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLease(tc.botKey, tc.holder, tc.ttl)
			if tc.wantErr && err == nil {
				t.Fatalf("validateLease(%q, %q, %s) = nil, want an error", tc.botKey, tc.holder, tc.ttl)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateLease(%q, %q, %s) returned unexpected error: %v", tc.botKey, tc.holder, tc.ttl, err)
			}
		})
	}
}

// The scripts are the whole safety argument for the lock and the lease, so
// their text is asserted: a refactor that replaced the guarded delete with a
// bare DEL would still compile and still pass every other test in this
// package, and would only fail in production under a TTL overrun.
func TestCompareAndDeleteScriptIsGuarded(t *testing.T) {
	src := compareAndDeleteSrc

	if !strings.Contains(src, `redis.call("GET", KEYS[1]) == ARGV[1]`) {
		t.Errorf("release script does not compare the ownership token before deleting:\n%s", src)
	}
	if !strings.Contains(src, `redis.call("DEL", KEYS[1])`) {
		t.Errorf("release script does not delete the key:\n%s", src)
	}
	if strings.Count(src, "redis.call") != 2 {
		t.Errorf("release script should do exactly one GET and one DEL:\n%s", src)
	}
}

func TestCompareAndExpireScriptIsGuarded(t *testing.T) {
	src := compareAndExpireSrc

	if !strings.Contains(src, `redis.call("GET", KEYS[1]) == ARGV[1]`) {
		t.Errorf("renew script does not compare the ownership token before extending:\n%s", src)
	}
	if !strings.Contains(src, `redis.call("PEXPIRE", KEYS[1], ARGV[2])`) {
		t.Errorf("renew script does not extend the key:\n%s", src)
	}
}

// A token that repeated across holders would defeat the compare-and-delete
// guard entirely, so both its length and its uniqueness are pinned.
func TestNewToken(t *testing.T) {
	const samples = 256

	seen := make(map[string]bool, samples)
	for i := 0; i < samples; i++ {
		tok, err := newToken()
		if err != nil {
			t.Fatalf("newToken returned unexpected error: %v", err)
		}
		if len(tok) != tokenBytes*2 {
			t.Fatalf("newToken() length = %d, want %d hex characters", len(tok), tokenBytes*2)
		}
		if seen[tok] {
			t.Fatalf("newToken produced a duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

// DedupTTL is a documented operational decision (a redelivery after a long
// outage must still be suppressed), so a silent change to it should break a
// test rather than only a comment.
func TestDedupTTLDefault(t *testing.T) {
	if DedupTTL != 24*time.Hour {
		t.Errorf("DedupTTL = %s, want 24h", DedupTTL)
	}
}

// NewDeduplicator falls back rather than writing keys that never expire,
// which would slowly consume the whole instance.
func TestNewDeduplicatorRejectsNonPositiveTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{"zero falls back", 0, DedupTTL},
		{"negative falls back", -time.Hour, DedupTTL},
		{"positive is kept", time.Hour, time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDeduplicator(nil, tc.ttl)
			if d.ttl != tc.want {
				t.Errorf("ttl = %s, want %s", d.ttl, tc.want)
			}
		})
	}
}
