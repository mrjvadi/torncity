package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// contentSource is what the reload loop reads: the active version's number,
// and the whole active version.
type contentSource interface {
	Active(ctx context.Context) (postgres.ActiveVersion, error)
	LoadActive(ctx context.Context) (*content.Pack, error)
}

// watchContent keeps the registry on the active content version, so a load
// (`admin content load`, or an activation of an older version) reaches this
// process without a restart — ADR 0004's "change the world without a
// deployment".
//
// It polls the one-row question "which version is active?" every interval
// and reads the whole version only when the answer changed. The database is
// the source of truth and NATS only an accelerator in the ADR, so polling the
// database is the path that works whether or not the announcement arrived.
// A version that fails to load or to build is logged and skipped: the process
// keeps serving the version it has, which is far better than serving none.
// The swap is atomic (content.Registry); a request already running keeps the
// snapshot it started with.
func watchContent(ctx context.Context, src contentSource, registry *content.Registry, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failed := 0 // the version that last failed, so it is reported once
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		failed = reloadOnce(ctx, src, registry, failed, logger)
	}
}

// reloadOnce swaps in the active version if it is not the one the registry
// holds, and returns the version that failed to load (or the one passed in).
func reloadOnce(ctx context.Context, src contentSource, registry *content.Registry, failed int, logger *slog.Logger) int {
	active, err := src.Active(ctx)
	if errors.Is(err, postgres.ErrNoActiveVersion) || (err == nil && active.Version == registry.Version()) {
		return failed
	}
	if err != nil {
		logger.Warn("cannot check the active content version", slog.String("error", err.Error()))
		return failed
	}
	if active.Version == failed {
		return failed
	}
	pack, err := src.LoadActive(ctx)
	if err == nil {
		var snap *content.Snapshot
		if snap, err = content.BuildSnapshot(pack.Version, pack); err == nil {
			previous := registry.Swap(snap)
			logger.Info("content reloaded",
				slog.Int("content_version", snap.Version()),
				slog.Int("previous_version", previous.Version()))
			return 0
		}
	}
	logger.Error("the active content version cannot be served; keeping the current one",
		slog.Int("content_version", active.Version),
		slog.Int("serving_version", registry.Version()),
		slog.String("error", err.Error()))
	return active.Version
}
