// Command scheduler turns the durable schedule into published commands.
//
// game_actions is the source of truth for every timed operation in the game.
// This process asks it, once per tick, for the rows whose instant has passed,
// and publishes each one as a command for the game service. The first such
// operation is travel: a journey that comes due is published as travel.arrive,
// and the game service lands the player (ROADMAP.md, Phase 1).
//
// It owns WHEN, never WHAT. Nothing here knows what arriving means; see
// internal/workers/scheduler for why the rule stays in the game service.
//
// # Shutdown
//
// A signal stops the claiming, not the work already claimed. A claimed row has
// left the due index, so the batch that claimed it is allowed to finish on a
// context of its own, bounded by scheduler.shutdown_timeout.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mrjvadi/torncity/internal/config"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/workers/scheduler"
)

// defaultConfigPath is where this process looks for its configuration when
// TORN_CONFIG does not say otherwise.
//
// This one path comes from the environment rather than from the configuration
// file, for the same reason DATABASE_URL does: something has to say where the
// configuration lives before any configuration has been read. Everything else
// — tick interval, batch size, shutdown budget, claim lease — is in the file
// this points at, and none of it is in this binary.
const defaultConfigPath = config.DefaultPath

func main() {
	env, err := loadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: %v\n", err)
		os.Exit(2)
	}

	// A process that cannot read its configuration must not guess: how often
	// to tick, how much to claim and how long a claim is held would otherwise
	// silently become whatever this binary was built with.
	cfg, err := config.Load(env.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(env.logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, env, cfg, logger); err != nil {
		logger.Error("scheduler stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("scheduler stopped cleanly")
}

// env is the bootstrap: the handful of facts that must be known before the
// configuration file can be read. Addresses and credentials stay here (ADR
// 0002); every operational value lives in that file.
type env struct {
	databaseURL string
	natsURL     string
	logLevel    string
	configPath  string
	instanceID  string
}

func loadEnv() (env, error) {
	e := env{
		databaseURL: os.Getenv("DATABASE_URL"),
		natsURL:     os.Getenv("NATS_URL"),
		logLevel:    os.Getenv("LOG_LEVEL"),
		configPath:  os.Getenv("TORN_CONFIG"),
		instanceID:  os.Getenv("SCHEDULER_INSTANCE_ID"),
	}
	if e.configPath == "" {
		e.configPath = defaultConfigPath
	}
	if e.instanceID == "" {
		// The hostname is what a container orchestrator already makes unique
		// per replica, so it traces a command back to the scheduler that
		// emitted it without anyone having to set a variable.
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "scheduler"
		}
		e.instanceID = host
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

func run(ctx context.Context, e env, cfg *config.Config, logger *slog.Logger) error {
	logger = logger.With(slog.String("service", "scheduler"), slog.String("instance_id", e.instanceID))

	// This line is the proof that the file on disk is what the process runs.
	logger.Info("scheduler starting",
		slog.String("config", e.configPath),
		slog.Duration("tick_interval", cfg.Scheduler.TickInterval),
		slog.Int("batch_size", cfg.Scheduler.BatchSize),
		slog.Duration("shutdown_timeout", cfg.Scheduler.ShutdownTimeout),
		slog.Duration("claim_timeout", cfg.Scheduler.ClaimTimeout),
		slog.Int("noisy_attempts", cfg.Scheduler.NoisyAttempts))

	pool, err := postgres.Open(ctx, e.databaseURL, postgres.Options{MaxConns: cfg.Postgres.MaxConns,
		IdleInTransactionTimeout: cfg.Postgres.IdleInTransactionTimeout})
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := infranats.New(ctx, e.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// The scheduler publishes into the command stream, so it must exist
	// before the first tick: a publish to a subject no stream captures is
	// refused, and every due action would sit claimed until the lease ran out.
	if err := infranats.EnsureStreams(ctx, conn, infranats.StreamOptions{
		CommandMaxAge:   cfg.NATS.CommandMaxAge,
		EventMaxAge:     cfg.NATS.EventMaxAge,
		DuplicateWindow: cfg.NATS.DuplicateWindow,
	}); err != nil {
		return err
	}

	actions := postgres.NewGameActionRepository(pool)

	// The reaper is switched on by the repository, not by this file: any
	// repository that can answer "claimed longer ago than the lease" (see
	// scheduler.ClaimReaper) gets it. The postgres adapter can, since
	// migration 0004 added claimed_at; reaper_test.go pins that, so losing
	// ReclaimStale fails a test instead of silently stranding claimed work.
	var reaper scheduler.ClaimReaper
	if r, ok := any(actions).(scheduler.ClaimReaper); ok {
		reaper = r
	}

	s, err := scheduler.New(scheduler.Options{
		Actions:         actions,
		Publisher:       infranats.NewPublisher(conn),
		Logger:          logger,
		TickInterval:    cfg.Scheduler.TickInterval,
		BatchSize:       cfg.Scheduler.BatchSize,
		ShutdownTimeout: cfg.Scheduler.ShutdownTimeout,
		NoisyAttempts:   cfg.Scheduler.NoisyAttempts,
		InstanceID:      e.instanceID,
		DefaultLanguage: cfg.Player.DefaultLanguage,
		ClaimTimeout:    cfg.Scheduler.ClaimTimeout,
		Reaper:          reaper,
	})
	if err != nil {
		return err
	}

	// Run returns once the signal has arrived AND the batch in flight, if
	// any, has finished; the deferred closes run only after that, so a batch
	// is never cut off by its own connections going away underneath it.
	return s.Run(ctx)
}
