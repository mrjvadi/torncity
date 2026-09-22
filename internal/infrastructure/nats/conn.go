// Package nats implements the messaging backbone over NATS JetStream.
//
// Core NATS is fire-and-forget: a subscriber that is down when a message is
// sent never learns it existed. That is unusable for commands that spend a
// player's money, so everything here goes through JetStream, which persists a
// message until a consumer acknowledges it.
//
// JetStream's guarantee is at-least-once, not exactly-once. This package
// leans into that rather than pretending otherwise: the publisher stamps
// Nats-Msg-Id so the server can collapse an obvious duplicate, and the
// consumer acknowledges explicitly so a handler that failed gets its message
// back instead of losing it. The remaining duplicates are settled on the
// consumer side by the inbox table in internal/infrastructure/postgres.
package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// connectTimeout bounds the initial dial. Without it a boot against an
// unreachable broker hangs rather than failing, and a supervisor cannot tell
// "starting slowly" from "never coming up".
const connectTimeout = 10 * time.Second

// drainTimeout bounds a graceful shutdown. Draining lets in-flight messages
// finish rather than dropping them, but it cannot be allowed to hold a
// shutdown open indefinitely.
const drainTimeout = 30 * time.Second

// Conn owns the connection to NATS and the JetStream context built from it.
type Conn struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// New dials url and prepares JetStream.
//
// Reconnection is left switched on and unbounded on purpose: a broker restart
// is an ordinary event, and a gateway that gave up after a few attempts would
// need an operator to restart it for something that heals on its own. The
// handlers are wired so a reconnect is visible rather than silent.
func New(ctx context.Context, url string) (*Conn, error) {
	opts := []nats.Option{
		nats.Name("torncity"),
		nats.Timeout(connectTimeout),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.ReconnectJitter(500*time.Millisecond, time.Second),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		// The URL may carry credentials, so only the failure is reported.
		return nil, fmt.Errorf("nats: connect failed: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		// A constructor that fails must not leak the connection it opened.
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream context: %w", err)
	}

	// RetryOnFailedConnect means Connect can return before the link is up, so
	// the context is what actually proves the broker is reachable. Asking
	// JetStream for its account information is the cheapest round trip that
	// exercises both the connection and JetStream being enabled at all.
	if _, err := js.AccountInfo(ctx); err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream unavailable: %w", err)
	}

	return &Conn{nc: nc, js: js}, nil
}

// JetStream exposes the JetStream context for adapters in this package.
func (c *Conn) JetStream() jetstream.JetStream { return c.js }

// Raw exposes the underlying connection.
func (c *Conn) Raw() *nats.Conn { return c.nc }

// Close drains and then closes the connection.
//
// Drain rather than Close: it lets already-received messages be handled and
// already-published ones be flushed. Closing outright would drop both, turning
// every deploy into a small burst of redelivered commands.
func (c *Conn) Close() error {
	if c == nil || c.nc == nil {
		return nil
	}

	if err := c.nc.Drain(); err != nil {
		c.nc.Close()
		return fmt.Errorf("nats: drain: %w", err)
	}

	// Drain is asynchronous; wait for it, but not forever.
	deadline := time.Now().Add(drainTimeout)
	for c.nc.IsDraining() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	c.nc.Close()
	return nil
}
