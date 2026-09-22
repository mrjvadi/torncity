package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// The guard clauses run before the JetStream context is touched, so they can
// be exercised with a zero Publisher. That is the point of putting them first:
// an envelope no consumer would accept must not reach the stream, where it
// would only consume the delivery budget on its way to the dead-letter path.
func TestPublishRejectsBadEnvelopesBeforeSending(t *testing.T) {
	valid := envelope.Metadata{
		RequestID:     "req-1",
		TraceID:       "trace-1",
		Command:       "profile.get",
		SchemaVersion: envelope.SchemaVersion,
		ReceivedAt:    time.Unix(0, 0).UTC(),
	}

	withoutRequestID := valid
	withoutRequestID.RequestID = ""

	withoutTraceID := valid
	withoutTraceID.TraceID = ""

	withoutCommand := valid
	withoutCommand.Command = ""

	wrongVersion := valid
	wrongVersion.SchemaVersion = envelope.SchemaVersion + 1

	tests := []struct {
		name string
		env  *envelope.Envelope
	}{
		{"nil envelope", nil},
		{"missing request id", &envelope.Envelope{Metadata: withoutRequestID}},
		{"missing trace id", &envelope.Envelope{Metadata: withoutTraceID}},
		{"missing command", &envelope.Envelope{Metadata: withoutCommand}},
		{"unsupported schema version", &envelope.Envelope{Metadata: wrongVersion}},
	}

	// A nil JetStream context: reaching it would panic, which is exactly the
	// signal wanted if a guard were removed.
	p := &Publisher{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Publish(context.Background(), subjects.Event("player", "created"), tc.env)
			if err == nil {
				t.Fatal("Publish accepted an envelope it should have refused")
			}
		})
	}
}

// The deduplication header's name is part of the wire contract with the
// server. If the driver ever renamed it, server-side deduplication would stop
// working silently: publishing would still succeed and duplicates would simply
// stop being collapsed.
func TestDeduplicationHeaderName(t *testing.T) {
	if jetstream.MsgIDHeader != "Nats-Msg-Id" {
		t.Errorf("deduplication header = %q, want %q", jetstream.MsgIDHeader, "Nats-Msg-Id")
	}
}
