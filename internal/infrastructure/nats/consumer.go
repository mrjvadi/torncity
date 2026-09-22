package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// The four values below are the DEFAULTS a Consumer falls back to when a
// ConsumerOptions field is left at zero. The values a deployment actually runs
// are declared in configs/config.yml under `nats:` and injected through
// ConsumerOptions; these exist so a caller that supplies nothing still gets
// the redelivery policy this package shipped with, and so config.Defaults()
// has something to mirror.

// MaxDeliver is how many times JetStream will offer one message before giving
// up on it.
//
// Five is chosen against the shape of the failures a handler actually sees.
// The common ones — a database failover, a brief broker partition, a lock held
// by a slower request — clear within the backoff schedule below, so five
// attempts spread over roughly a minute and a half recover them. The failures
// that do not clear are deterministic: a payload the handler cannot satisfy,
// or a bug. Retrying those forever does not fix them, it just keeps one
// message at the head of a work queue starving everything behind it.
//
// After the fifth attempt the message goes to the dead-letter path: JetStream
// stops redelivering it and publishes a MAX_DELIVERIES advisory on
// $JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.<stream>.<consumer>. That
// advisory is the dead letter — it names the stream, the consumer and the
// sequence number, so the original message can be read back out of the stream
// and replayed by hand once the cause is fixed. A command that reaches it has
// not been executed and the player has not been charged.
const MaxDeliver = 5

// deliveryBackOff is the wait before each redelivery.
//
// It is graduated rather than flat because the first retry and the fourth are
// answering different questions. The first asks "was that a blip", and wants
// to be quick. The fourth asks "has the dependency come back yet", and a tight
// loop there only adds load to something already struggling. The schedule has
// MaxDeliver-1 entries, one per redelivery; JetStream reuses the last interval
// if it ever needs more.
var deliveryBackOff = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
}

// ackWait is how long the server waits for an ack before assuming the handler
// died. It must exceed the slowest legitimate handler, or a still-running
// handler's message is redelivered and the work is done twice.
const ackWait = 30 * time.Second

// nakDelay is the wait requested when a handler returns an error.
//
// NakWithDelay rather than a bare Nak: a plain Nak asks for immediate
// redelivery, which against a dependency that is down becomes a hot loop
// burning the delivery budget in milliseconds and reaching MaxDeliver before
// the dependency has had any chance to recover.
const nakDelay = 5 * time.Second

// Handler processes one decoded envelope.
//
// Returning an error means "not done, try again". It must therefore be
// returned only for failures that a later attempt could plausibly survive;
// a handler that cannot ever succeed on a message should absorb it and return
// nil after recording the fact, rather than sending it round the retry loop
// four more times on its way to the dead-letter path.
type Handler func(ctx context.Context, env *envelope.Envelope) error

// ConsumerOptions is the redelivery policy every durable consumer this type
// creates is given.
//
// It is four values rather than the whole configuration tree: this package
// needs the ack wait, the delivery budget and the two delays, and handing it
// everything would make every future field of the config a dependency of the
// broker adapter.
type ConsumerOptions struct {
	// AckWait is how long the server waits for an acknowledgement before
	// assuming the handler died. Zero means ackWait.
	AckWait time.Duration

	// MaxDeliver is how many times one message is offered. Zero or less means
	// MaxDeliver.
	MaxDeliver int

	// NakDelay is the delay requested when a handler returns an error. Zero
	// means nakDelay.
	NakDelay time.Duration

	// Backoff is the wait before each redelivery. Empty means
	// deliveryBackOff.
	Backoff []time.Duration
}

// withDefaults fills every unset field. A zero AckWait or NakDelay would be
// read by JetStream as "no wait at all", which turns a struggling dependency
// into a hot redelivery loop, so none of them is allowed through as zero.
func (o ConsumerOptions) withDefaults() ConsumerOptions {
	if o.AckWait <= 0 {
		o.AckWait = ackWait
	}
	if o.MaxDeliver <= 0 {
		o.MaxDeliver = MaxDeliver
	}
	if o.NakDelay <= 0 {
		o.NakDelay = nakDelay
	}
	if len(o.Backoff) == 0 {
		o.Backoff = deliveryBackOff
	}
	return o
}

// Consumer subscribes to a subject with a durable, explicitly acknowledged
// JetStream consumer.
type Consumer struct {
	js   jetstream.JetStream
	opts ConsumerOptions
}

// NewConsumer returns a consumer over c, running the redelivery policy in
// opts. A zero ConsumerOptions selects this package's defaults.
func NewConsumer(c *Conn, opts ConsumerOptions) *Consumer {
	return &Consumer{js: c.JetStream(), opts: opts.withDefaults()}
}

// streamForSubject maps a subject to the stream that carries it.
//
// The mapping is derived rather than passed in so a caller cannot subscribe to
// a command subject against the event stream, which would silently deliver
// nothing and look exactly like an idle system.
func streamForSubject(subject string) (string, error) {
	switch {
	case strings.HasPrefix(subject, "game.command."):
		return CommandStreamName, nil
	case strings.HasPrefix(subject, "game.event."):
		return EventStreamName, nil
	}
	return "", fmt.Errorf("nats: no stream carries subject %q", subject)
}

// Subscribe starts a durable consumer on subject and delivers to handler.
//
// The consumer is durable and explicitly acknowledged, which together are what
// make a restart safe: the server remembers how far this consumer got, and a
// message is removed only once the handler has said it finished. A push
// consumer with automatic acks would acknowledge on delivery, so every message
// in flight when a process died would be lost.
//
// A pull consumer is used (jetstream.Consume drives it) because flow control
// then belongs to this process: it asks for work when it is ready for work. A
// push consumer lets the server outrun a slow handler and pile messages into
// the client's buffer, where a crash loses all of them at once.
//
// Subscribe returns once the subscription is running. It stops when ctx is
// cancelled.
func (c *Consumer) Subscribe(ctx context.Context, subject, durable string, handler Handler) error {
	if durable == "" {
		// Without a durable name the consumer is ephemeral and its position
		// is forgotten on restart, which for a command stream means replaying
		// or skipping an unknown amount of work.
		return fmt.Errorf("nats: subscribe to %s: a durable name is required", subject)
	}
	if handler == nil {
		return fmt.Errorf("nats: subscribe to %s: nil handler", subject)
	}

	streamName, err := streamForSubject(subject)
	if err != nil {
		return err
	}

	stream, err := c.js.Stream(ctx, streamName)
	if err != nil {
		return fmt.Errorf("nats: opening stream %s: %w", streamName, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.opts.AckWait,
		MaxDeliver:    c.opts.MaxDeliver,
		BackOff:       c.opts.Backoff,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("nats: creating consumer %s on %s: %w", durable, streamName, err)
	}

	consumeCtx, err := cons.Consume(func(msg jetstream.Msg) {
		c.deliver(ctx, msg, handler)
	})
	if err != nil {
		return fmt.Errorf("nats: consuming %s as %s: %w", subject, durable, err)
	}

	// The subscription outlives this call, so something has to own its
	// lifetime; ctx does.
	go func() {
		<-ctx.Done()
		consumeCtx.Stop()
	}()

	return nil
}

// deliver decodes one message and routes the handler's outcome to an ack.
//
// The three outcomes are deliberately different:
//
//   - decode failure is terminal. A message that is not a valid envelope will
//     not become one on the fourth attempt, so it is Termed immediately rather
//     than spending four redeliveries and an ack-wait each to reach the same
//     conclusion. Term still records the message in the stream for inspection.
//   - handler error is a NAK with a delay. Never an ACK: acking a failed
//     handler discards the message, and the command it carried is lost with
//     no trace anywhere except a log line.
//   - success is an ACK, which is what finally removes the message.
func (c *Consumer) deliver(ctx context.Context, msg jetstream.Msg, handler Handler) {
	var env envelope.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		_ = msg.TermWithReason("envelope is not decodable")
		return
	}

	if err := env.Metadata.Validate(); err != nil {
		// Same reasoning as a decode failure: metadata that fails validation
		// today fails it identically on every redelivery.
		_ = msg.TermWithReason("envelope metadata is invalid")
		return
	}

	if err := handler(ctx, &env); err != nil {
		_ = msg.NakWithDelay(c.opts.NakDelay)
		return
	}

	_ = msg.Ack()
}
