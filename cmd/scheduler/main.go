// Command scheduler will run time-based game actions.
//
// It is a stub. Phase 0 is a walking skeleton and has no scheduled work: the
// first thing that needs to finish at a future instant is travel, which
// arrives with Phase 1 together with the game_actions table it is driven from
// (ROADMAP.md, Phase 1).
//
// The process exists now so the deployment topology is complete and so the
// service can be added to compose, built and shipped before it has a job.
// It deliberately does nothing else: ROADMAP.md principle 2 says a placeholder
// that pretends to work is worse than an honest gap, and a scheduler that
// silently scanned nothing would look healthy while doing no work.
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
)

// defaultConfigPath is where this process looks for its configuration when
// TORN_CONFIG does not say otherwise.
//
// This one path comes from the environment rather than from the configuration
// file, for the same reason DATABASE_URL and TORN_LOCALES_DIR do: something
// has to say where the configuration lives before any configuration has been
// read.
const defaultConfigPath = config.DefaultPath

func main() {
	configPath := os.Getenv("TORN_CONFIG")
	if configPath == "" {
		configPath = defaultConfigPath
	}

	// The stub loads its configuration like every other process, and fails
	// the same way if it cannot. There is no scheduled work yet to apply it
	// to, but a service that starts without reading the file would be the one
	// place a configuration error could hide until Phase 1 gave it a job.
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scheduler: %v\n", err)
		os.Exit(2)
	}

	logger := newLogger(os.Getenv("LOG_LEVEL")).With(slog.String("service", "scheduler"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Proof that the file parsed: one value from it, named, so a bad config
	// is visible here rather than at the first scheduled action that ever
	// runs.
	logger.Info("configuration loaded",
		slog.String("config", configPath),
		slog.String("default_language", cfg.Player.DefaultLanguage))

	logger.Info("scheduler: not implemented in phase 0")

	<-ctx.Done()

	logger.Info("shutdown signal received, exiting")
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
