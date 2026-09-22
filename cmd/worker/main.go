// Command worker drains the transactional outbox onto NATS.
//
// A domain event is written to the outbox table inside the same transaction as
// the state change it announces (MASTER_PROMPT section 21). This process is
// the other half of that arrangement: it polls the pending rows, publishes
// them, and records that they reached the broker.
//
// # Claim semantics
//
// OutboxStore.FetchPending claims a batch with FOR UPDATE SKIP LOCKED and
// bumps `attempts`. The claim is NOT a status change, because
// outbox_status_check admits only pending / published / failed and there is no
// "claimed" state to move to. A claimed row therefore stays `pending` until it
// is actually published, which is what makes a worker that dies mid-batch
// harmless: its rows are simply picked up by the next poll instead of being
// stranded in a state nobody clears.
//
// This worker honours that. It never writes a status of its own. A row that
// fails to publish is left exactly as it is — still pending, with a higher
// attempt count — and is logged. Moving it to `failed` would take it out of
// the publisher's sight permanently for what is usually a transient broker
// error, and inventing any other status would be rejected by the constraint.
//
// The price is that a row may be published twice: claimed, published, and then
// the worker dies before MarkPublished. That is deliberate and accounted for.
// The publisher stamps Nats-Msg-Id with the request id, JetStream's duplicate
// window collapses the repeat, and consumers keep an inbox. At-least-once on
// the wire, effectively-once on processing.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

const (
	// defaultPollInterval is how often an idle outbox is checked. It is the
	// floor on end-to-end event latency, so it is short; the partial index on
	// pending rows makes an empty poll almost free.
	defaultPollInterval = 250 * time.Millisecond

	// defaultBatchSize bounds one claim. Large enough that a burst drains in
	// a few polls, small enough that a worker crash re-publishes little.
	defaultBatchSize = 100

	// shutdownTimeout bounds the final drain.
	shutdownTimeout = 15 * time.Second

	// noisyAttempts is where a row stops being a retry and starts being a
	// problem worth an operator's attention. The row is still not touched.
	noisyAttempts = 5
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(cfg.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("worker stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("worker stopped cleanly")
}

type config struct {
	databaseURL  string
	natsURL      string
	logLevel     string
	pollInterval time.Duration
	batchSize    int
}

func loadConfig() (config, error) {
	cfg := config{
		databaseURL:  os.Getenv("DATABASE_URL"),
		natsURL:      os.Getenv("NATS_URL"),
		logLevel:     os.Getenv("LOG_LEVEL"),
		pollInterval: defaultPollInterval,
		batchSize:    defaultBatchSize,
	}

	var missing []string
	if cfg.databaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.natsURL == "" {
		missing = append(missing, "NATS_URL")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if raw := os.Getenv("OUTBOX_POLL_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return config{}, fmt.Errorf("OUTBOX_POLL_INTERVAL must be a positive duration, got %q", raw)
		}
		cfg.pollInterval = d
	}
	if raw := os.Getenv("OUTBOX_BATCH_SIZE"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return config{}, fmt.Errorf("OUTBOX_BATCH_SIZE must be a positive integer, got %q", raw)
		}
		cfg.batchSize = n
	}

	return cfg, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// metaAttrs is the trace context every hop logs.
func metaAttrs(meta envelope.Metadata) []any {
	return []any{
		slog.String("trace_id", meta.TraceID),
		slog.String("request_id", meta.RequestID),
		slog.String("bot_id", meta.BotID),
	}
}

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "worker"))
	logger.Info("outbox publisher starting",
		slog.Duration("poll_interval", cfg.pollInterval),
		slog.Int("batch_size", cfg.batchSize))

	pool, err := postgres.New(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := infranats.New(ctx, cfg.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := infranats.EnsureStreams(ctx, conn); err != nil {
		return err
	}

	p := &publisher{
		logger: logger,
		store:  postgres.NewOutboxStore(pool),
		out:    infranats.NewPublisher(conn),
		batch:  cfg.batchSize,
	}

	ticker := time.NewTicker(cfg.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received, draining the current backlog")

			// One last pass on a context of its own: the loop's context is
			// already cancelled, and rows claimed a moment ago would
			// otherwise wait for the next process to notice them.
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
			published, err := p.publishBatch(drainCtx)
			cancel()
			if err != nil {
				logger.Warn("final drain did not complete", slog.String("error", err.Error()))
			} else if published > 0 {
				logger.Info("final drain published events", slog.Int("events", published))
			}
			return nil

		case <-ticker.C:
			if _, err := p.publishBatch(ctx); err != nil {
				if ctx.Err() != nil {
					continue
				}
				// A failing poll is logged and retried on the next tick. The
				// rows are still pending, so nothing is lost by waiting.
				logger.Error("outbox poll failed", slog.String("error", err.Error()))
			}
		}
	}
}

// Publisher is the port this worker needs from the broker. It is the same
// interface the application layer declares, which keeps the worker testable
// without a broker.
type Publisher = application.EventPublisher

// publisher is one poll-publish-mark cycle.
type publisher struct {
	logger *slog.Logger
	store  *postgres.OutboxStore
	out    Publisher
	batch  int
}

// publishBatch claims one batch, publishes what it can, and marks those rows
// published. It returns how many events reached the broker.
func (p *publisher) publishBatch(ctx context.Context) (int, error) {
	pending, err := p.store.FetchPending(ctx, p.batch)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}

	// Only the rows that actually reached the broker are marked. A row whose
	// publish failed is left pending, exactly as claimPending left it, and
	// the next poll claims it again.
	published := make([]string, 0, len(pending))
	var firstErr error

	for _, ev := range pending {
		// The deduplication id is the outbox event id, not the request id:
		// one command can append several events, and sharing one id would
		// have the broker discard every event after the first.
		err := p.out.PublishWithID(ctx, ev.Subject, &envelope.Envelope{
			Metadata: ev.Metadata,
			Payload:  ev.Payload,
		}, ev.EventID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			level := slog.LevelWarn
			if ev.Attempts >= noisyAttempts {
				// Still not touched: the status stays pending on purpose.
				// This only raises the volume so a row that cannot be
				// published is noticed rather than retried in silence.
				level = slog.LevelError
			}
			p.logger.Log(ctx, level, "cannot publish an outbox event",
				append(metaAttrs(ev.Metadata),
					slog.String("event_id", ev.EventID),
					slog.String("subject", ev.Subject),
					slog.Int("attempts", ev.Attempts),
					slog.String("error", err.Error()),
				)...)
			continue
		}

		published = append(published, ev.EventID)
		p.logger.Debug("outbox event published",
			append(metaAttrs(ev.Metadata),
				slog.String("event_id", ev.EventID),
				slog.String("subject", ev.Subject),
			)...)
	}

	if len(published) > 0 {
		if err := p.store.MarkPublished(ctx, published); err != nil {
			// The events are on the broker but the rows still say pending,
			// so the next poll republishes them. That is safe — Nats-Msg-Id
			// and the consumer inbox absorb the repeat — and it is the right
			// side to fail on: the alternative is a row marked published for
			// an event nobody received.
			return len(published), fmt.Errorf("worker: events published but not marked: %w", err)
		}
	}

	if firstErr != nil {
		return len(published), fmt.Errorf("worker: %d of %d events failed to publish: %w",
			len(pending)-len(published), len(pending), firstErr)
	}

	return len(published), nil
}
