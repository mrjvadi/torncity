package nats

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// The subject-to-stream mapping is derived rather than supplied, so a wrong
// mapping would not fail loudly: the consumer would attach to a stream that
// never carries the subject and would simply sit idle, which looks the same
// as a quiet system.
func TestStreamForSubject(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		want    string
		wantErr bool
	}{
		{"command", subjects.Command("player", "profile.get"), CommandStreamName, false},
		{"command wildcard", subjects.CommandStream, CommandStreamName, false},
		{"event", subjects.Event("player", "created"), EventStreamName, false},
		{"event wildcard", subjects.EventStream, EventStreamName, false},
		{"response subjects are not streamed", subjects.Response("req-1"), "", true},
		{"unknown root", "other.command.player.v1", "", true},
		{"empty", "", "", true},
		{"prefix without the trailing dot is not a command subject", "game.commandx.player.v1", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := streamForSubject(tc.subject)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("streamForSubject(%q) = %q, want an error", tc.subject, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("streamForSubject(%q) returned unexpected error: %v", tc.subject, err)
			}
			if got != tc.want {
				t.Errorf("streamForSubject(%q) = %q, want %q", tc.subject, got, tc.want)
			}
		})
	}
}

// The subjects this package builds its streams from must still match the
// grammar the subjects package enforces, or EnsureStreams would create a
// stream nothing publishes to.
func TestStreamSubjectsMatchTheGrammar(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		stream  string
	}{
		{"command", subjects.Command("player", "profile.get"), CommandStreamName},
		{"event", subjects.Event("player", "created"), EventStreamName},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !subjects.Grammar.MatchString(tc.subject) {
				t.Fatalf("%q does not match the subject grammar", tc.subject)
			}
			got, err := streamForSubject(tc.subject)
			if err != nil {
				t.Fatalf("streamForSubject(%q): %v", tc.subject, err)
			}
			if got != tc.stream {
				t.Errorf("streamForSubject(%q) = %q, want %q", tc.subject, got, tc.stream)
			}
		})
	}
}

// Stream names go into the JetStream API, which rejects a name containing a
// dot, a space or a wildcard. Getting this wrong fails only at boot against a
// real broker, so it is asserted here instead.
func TestStreamNamesAreValid(t *testing.T) {
	for _, name := range []string{CommandStreamName, EventStreamName} {
		if name == "" {
			t.Error("stream name is empty")
		}
		for _, bad := range []string{".", " ", "*", ">", "/", "\\"} {
			if contains(name, bad) {
				t.Errorf("stream name %q contains %q, which JetStream rejects", name, bad)
			}
		}
	}
	if CommandStreamName == EventStreamName {
		t.Error("the command and event streams share a name")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The delivery budget is a documented operational decision. A change to it
// alters how long a broken dependency stalls a work queue before its messages
// reach the dead-letter path, so it should break a test rather than only a
// comment.
func TestDeliveryBudget(t *testing.T) {
	if MaxDeliver != 5 {
		t.Errorf("MaxDeliver = %d, want 5", MaxDeliver)
	}

	// JetStream uses one interval per redelivery. More entries than
	// redeliveries is dead configuration and hides the real schedule.
	if len(deliveryBackOff) != MaxDeliver-1 {
		t.Errorf("deliveryBackOff has %d entries, want %d (one per redelivery)", len(deliveryBackOff), MaxDeliver-1)
	}

	// Graduated, not flat: a tight loop against a dependency that is down
	// would exhaust the budget before it could recover.
	for i := 1; i < len(deliveryBackOff); i++ {
		if deliveryBackOff[i] <= deliveryBackOff[i-1] {
			t.Errorf("backoff is not increasing at index %d: %s then %s",
				i, deliveryBackOff[i-1], deliveryBackOff[i])
		}
	}

	// A redelivery cannot be requested sooner than the server is willing to
	// wait for the ack it is replacing.
	if nakDelay <= 0 {
		t.Errorf("nakDelay = %s, want a positive delay so a NAK is not a hot loop", nakDelay)
	}
	if ackWait <= 0 {
		t.Errorf("ackWait = %s, want a positive wait", ackWait)
	}
}

// Total retry window, reported rather than bounded tightly: what matters is
// that it is long enough for a failover and short enough that a poisoned
// message does not hold the head of a work queue for hours.
func TestRetryWindowIsBounded(t *testing.T) {
	var total time.Duration
	for _, d := range deliveryBackOff {
		total += d
	}

	if total < 30*time.Second {
		t.Errorf("total retry window %s is too short to survive a failover", total)
	}
	if total > 10*time.Minute {
		t.Errorf("total retry window %s would stall a work queue for too long", total)
	}
	t.Logf("a message is retried over roughly %s before reaching the dead-letter path", total)
}

// Commands are perishable and events are history; collapsing the two
// retentions would either replay stale commands or discard history.
func TestStreamRetentionWindows(t *testing.T) {
	if commandMaxAge >= eventMaxAge {
		t.Errorf("commandMaxAge (%s) should be shorter than eventMaxAge (%s)", commandMaxAge, eventMaxAge)
	}
	if duplicateWindow <= 0 {
		t.Errorf("duplicateWindow = %s, which disables server-side deduplication", duplicateWindow)
	}
	if duplicateWindow >= commandMaxAge {
		t.Errorf("duplicateWindow (%s) should be far shorter than commandMaxAge (%s)", duplicateWindow, commandMaxAge)
	}
}
