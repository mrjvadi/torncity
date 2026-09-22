package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Publisher sends envelopes to JetStream.
type Publisher struct {
	js jetstream.JetStream
}

var _ application.Publisher = (*Publisher)(nil)

// NewPublisher returns a publisher over c.
func NewPublisher(c *Conn) *Publisher { return &Publisher{js: c.JetStream()} }

// Publish marshals env and sends it to subject, waiting for the server's ack.
//
// The Nats-Msg-Id header carries the envelope's RequestID. That is what lets
// the server drop a duplicate within the stream's duplicate window, which is
// not a theoretical concern: the outbox publisher can re-claim a row it
// already published if it died before recording the fact, and without the
// header the same event would be delivered twice from the stream itself.
// RequestID is the right value because it identifies one logical request
// across every hop, so the same event republished from the same outbox row
// carries the same id, while two genuinely different events never do.
//
// The synchronous form is used, not PublishAsync: the caller is normally the
// outbox worker, which must not mark a row published until the server has
// actually accepted it. An async publish would let the worker record a
// success the broker never granted, and the event would be lost.
func (p *Publisher) Publish(ctx context.Context, subject string, env *envelope.Envelope) error {
	return p.PublishWithID(ctx, subject, env, "")
}

// PublishWithID publishes with an explicit broker deduplication id.
//
// The plain Publish falls back to the request id, which identifies the command
// that produced the message. That is the right identity for a command, but the
// wrong one for an event: a single command may append several outbox rows, and
// they would then share one deduplication id. JetStream would silently discard
// every event after the first inside its duplicate window, and the loss would
// be invisible — no error, no dead letter, just a missing event.
//
// The outbox worker therefore passes outbox.event_id, which is unique per row.
// Pass an empty dedupID to keep the request-id behaviour.
func (p *Publisher) PublishWithID(ctx context.Context, subject string, env *envelope.Envelope, dedupID string) error {
	if env == nil {
		return fmt.Errorf("nats: publish to %s: nil envelope", subject)
	}
	if err := env.Metadata.Validate(); err != nil {
		// Publishing an envelope that no consumer may accept would fill the
		// stream with messages destined for the dead-letter path. It is
		// cheaper and clearer to refuse it here.
		return fmt.Errorf("nats: publish to %s: %w", subject, err)
	}

	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("nats: publish to %s: marshalling envelope: %w", subject, err)
	}

	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}
	if dedupID == "" {
		dedupID = env.Metadata.RequestID
	}
	msg.Header.Set(jetstream.MsgIDHeader, dedupID)

	if _, err := p.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("nats: publishing to %s: %w", subject, err)
	}

	return nil
}
