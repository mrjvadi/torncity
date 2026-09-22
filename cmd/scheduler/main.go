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
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	logger := newLogger(os.Getenv("LOG_LEVEL")).With(slog.String("service", "scheduler"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
