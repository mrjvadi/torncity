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
	"strings"
	"syscall"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// defaultConfigPath is where this process looks for its configuration when
// TORN_CONFIG does not say otherwise.
//
// This one path comes from the environment rather than from the configuration
// file, for the same reason DATABASE_URL and TORN_LOCALES_DIR do: something
// has to say where the configuration lives before any configuration has been
// read. Everything else — poll interval, batch size, shutdown budget, the
// attempt count that turns a retry into an alarm — is in the file this points
// at, and none of it is in this binary.
const defaultConfigPath = config.DefaultPath

func main() {
	env, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(2)
	}

	// A process that cannot read its configuration must not guess: every
	// value below — how often to poll, how much to claim, how long to drain —
	// would otherwise silently become whatever this binary was built with.
	cfg, err := config.Load(env.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(env.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, env, cfg, logger); err != nil {
		logger.Error("worker stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("worker stopped cleanly")
}

// env is the bootstrap: the handful of facts that must be known before the
// configuration file can be read. Addresses and credentials stay here (ADR
// 0002); every operational value lives in that file.
type env struct {
	databaseURL string
	natsURL     string
	logLevel    string
	configPath  string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL: os.Getenv("DATABASE_URL"),
		natsURL:     os.Getenv("NATS_URL"),
		logLevel:    os.Getenv("LOG_LEVEL"),
		configPath:  os.Getenv("TORN_CONFIG"),
	}
	if e.configPath == "" {
		e.configPath = defaultConfigPath
	}

	var missing []string
	if e.databaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if e.natsURL == "" {
		missing = append(missing, "NATS_URL")
	}
	if len(missing) > 0 {
		return env{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return e, nil
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

func run(ctx context.Context, e env, cfg *config.Config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "worker"))

	// This line is the proof that the file on disk is what the process runs.
	// It names the file it came from, so "what is this environment actually
	// configured with" is answerable from the logs alone.
	logger.Info("outbox publisher starting",
		slog.String("config", e.configPath),
		slog.Duration("poll_interval", cfg.Worker.PollInterval),
		slog.Int("batch_size", cfg.Worker.BatchSize),
		slog.Duration("shutdown_timeout", cfg.Worker.ShutdownTimeout),
		slog.Int("noisy_attempts", cfg.Worker.NoisyAttempts))

	pool, err := postgres.New(ctx, e.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := infranats.New(ctx, e.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := infranats.EnsureStreams(ctx, conn, infranats.StreamOptions{
		CommandMaxAge:   cfg.NATS.CommandMaxAge,
		EventMaxAge:     cfg.NATS.EventMaxAge,
		DuplicateWindow: cfg.NATS.DuplicateWindow,
	}); err != nil {
		return err
	}

	p := &publisher{
		logger:        logger,
		store:         postgres.NewOutboxStore(pool),
		out:           infranats.NewPublisher(conn),
		batch:         cfg.Worker.BatchSize,
		noisyAttempts: cfg.Worker.NoisyAttempts,
	}

	ticker := time.NewTicker(cfg.Worker.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutdown signal received, draining the current backlog")

			// One last pass on a context of its own: the loop's context is
			// already cancelled, and rows claimed a moment ago would
			// otherwise wait for the next process to notice them.
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Worker.ShutdownTimeout)
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

	// noisyAttempts is the attempt count at which a row stops being a normal
	// retry and starts being a problem worth an operator's attention. The row
	// is still not touched; only the log level changes.
	noisyAttempts int
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
			if ev.Attempts >= p.noisyAttempts {
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
