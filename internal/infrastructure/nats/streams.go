package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// Stream names. They are uppercase by NATS convention and must not contain a
// dot, which is why they are not simply the subject wildcards.
const (
	CommandStreamName = "GAME_COMMANDS"
	EventStreamName   = "GAME_EVENTS"
)

// duplicateWindow is how long the server remembers a Nats-Msg-Id.
//
// It bounds server-side deduplication: a republication of the same request id
// inside this window is dropped by the broker. Two minutes covers the realistic
// case — an outbox row re-claimed after a publisher crashed between publishing
// and marking it published — without holding an id index for events that will
// never be repeated. It is a first line of defence only; the inbox table is
// what actually guarantees effectively-once processing.
const duplicateWindow = 2 * time.Minute

// commandMaxAge caps how long an unconsumed command is kept.
//
// Commands are perishable in a way events are not: replaying a player's
// three-day-old "travel to this city" after an outage is worse than dropping
// it, because the player has long since given up and moved on. Events are
// history and are retained far longer.
const (
	commandMaxAge = 24 * time.Hour
	eventMaxAge   = 30 * 24 * time.Hour
)

// EnsureStreams creates the command and event streams if they are absent.
//
// It is called on every boot and is idempotent, which is the point: there is
// no separate provisioning step that someone can forget to run against a new
// environment, and a fresh broker becomes a working one by starting the
// service. CreateOrUpdateStream is used rather than CreateStream so that a
// retention or age change in this file takes effect on the next deploy instead
// of being silently ignored against an already-existing stream.
func EnsureStreams(ctx context.Context, c *Conn) error {
	configs := []jetstream.StreamConfig{
		{
			Name:     CommandStreamName,
			Subjects: []string{subjects.CommandStream},
			// WorkQueue: a command has exactly one owner, and the message is
			// removed once that owner acknowledges it. Limits retention would
			// leave every consumed command sitting in the stream until it
			// aged out, and would let a second consumer group execute the
			// same command again.
			Retention:  jetstream.WorkQueuePolicy,
			Storage:    jetstream.FileStorage,
			MaxAge:     commandMaxAge,
			Duplicates: duplicateWindow,
			Discard:    jetstream.DiscardOld,
		},
		{
			Name:     EventStreamName,
			Subjects: []string{subjects.EventStream},
			// Limits, not WorkQueue: an event has many independent readers
			// (projections, notifications, analytics) and must stay in the
			// stream after the first of them acknowledges it.
			Retention:  jetstream.LimitsPolicy,
			Storage:    jetstream.FileStorage,
			MaxAge:     eventMaxAge,
			Duplicates: duplicateWindow,
			Discard:    jetstream.DiscardOld,
		},
	}

	for _, cfg := range configs {
		if _, err := c.js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("nats: ensuring stream %s: %w", cfg.Name, err)
		}
	}

	return nil
}
