package redis

import "testing"

// Key construction is deliberately factored into pure functions so the
// namespace contract can be pinned without a server. A changed key prefix is
// a silent production incident — old locks and old dedup markers stop being
// seen — so the exact strings are asserted here, not just their shape.

func TestDedupKey(t *testing.T) {
	tests := []struct {
		name     string
		botID    string
		updateID int64
		want     string
	}{
		{"typical", "bot01", 812345, "gateway:dedup:bot01:812345"},
		{"uuid bot id", "a1b2-c3d4", 1, "gateway:dedup:a1b2-c3d4:1"},
		{"zero update", "bot01", 0, "gateway:dedup:bot01:0"},
		{"negative update", "bot01", -7, "gateway:dedup:bot01:-7"},
		{"large update", "bot01", 9223372036854775807, "gateway:dedup:bot01:9223372036854775807"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dedupKey(tc.botID, tc.updateID); got != tc.want {
				t.Errorf("dedupKey(%q, %d) = %q, want %q", tc.botID, tc.updateID, got, tc.want)
			}
		})
	}
}

// TestDedupKeyDistinguishesBots is the reason the bot id is in the key at all:
// Telegram numbers updates per bot, so the same update_id from two bots is two
// different events and must not collapse into one marker.
func TestDedupKeyDistinguishesBots(t *testing.T) {
	a := dedupKey("bot01", 500)
	b := dedupKey("bot02", 500)
	if a == b {
		t.Fatalf("two bots share the dedup key %q for the same update id", a)
	}
}

// TestDedupKeyIsUnambiguous guards against a separator collision: with a
// naive concatenation, ("bot01", 12) and ("bot0", 112) would produce the same
// key and one bot's update would silently suppress another's.
func TestDedupKeyIsUnambiguous(t *testing.T) {
	if dedupKey("bot01", 12) == dedupKey("bot0", 112) {
		t.Fatal("dedup keys collide across a bot-id/update-id boundary")
	}
}

func TestPlayerLockKey(t *testing.T) {
	tests := []struct {
		name     string
		playerID string
		want     string
	}{
		{"uuid", "0f8fad5b-d9cb-469f-a165-70867728950e", "lock:player:0f8fad5b-d9cb-469f-a165-70867728950e"},
		{"short", "p1", "lock:player:p1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := playerLockKey(tc.playerID); got != tc.want {
				t.Errorf("playerLockKey(%q) = %q, want %q", tc.playerID, got, tc.want)
			}
		})
	}
}

func TestBotLeaseKey(t *testing.T) {
	tests := []struct {
		name   string
		botKey string
		want   string
	}{
		{"typical", "bot01", "gateway:bot-lease:bot01"},
		{"double digit", "bot42", "gateway:bot-lease:bot42"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := botLeaseKey(tc.botKey); got != tc.want {
				t.Errorf("botLeaseKey(%q) = %q, want %q", tc.botKey, got, tc.want)
			}
		})
	}
}

// TestNamespacesDoNotOverlap keeps the three concerns separable: an operator
// flushing one namespace with a SCAN pattern must not take out another.
func TestNamespacesDoNotOverlap(t *testing.T) {
	keys := []string{
		dedupKey("bot01", 1),
		playerLockKey("bot01"),
		botLeaseKey("bot01"),
	}

	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("namespace collision on %q", k)
		}
		seen[k] = true
	}
}
