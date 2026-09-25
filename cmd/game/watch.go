package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/watch"
)

// watchThresholds is the watch's tuning from the configuration
// (docs/adr/0023, internal/domain/watch).
func watchThresholds(c config.AntiCheat) watch.Thresholds {
	return watch.Thresholds{Window: c.Window, OneWayCount: c.OneWayCount, OneWayMinTotal: c.OneWayMinTotal,
		OneWayRatioBPS: c.OneWayRatioBPS, OffMarketBPS: c.OffMarketBPS, OffMarketMinValue: c.OffMarketMinValue,
		SinglePartnerMinCount: c.SinglePartnerMinCount, SinglePartnerShareBPS: c.SinglePartnerShareBPS,
		CommandsPerMinute: c.CommandsPerMinute, HoldAbove: c.HoldAbove, WashTradeCount: c.WashTradeCount}
}

// commandRate counts the commands each player sends in a sliding minute and
// flags one who sends more than a person plays (watch.CommandRate). The
// count is this process's: with several game instances each counts its own
// share, so the limit is per instance — a bound all the same. A flag is
// raised at most once per player per minute, best effort and outside any
// command's transaction; nothing is ever refused for it.
type commandRate struct {
	th       watch.Thresholds
	recorder application.WatchRecorder
	logger   *slog.Logger

	mu      sync.Mutex
	seen    map[string][]time.Time
	flagged map[string]time.Time
}

func newCommandRate(th watch.Thresholds, recorder application.WatchRecorder, logger *slog.Logger) *commandRate {
	return &commandRate{th: th, recorder: recorder, logger: logger, seen: map[string][]time.Time{},
		flagged: map[string]time.Time{}}
}

// note records a command of a player at now, and flags the rate when it
// runs over.
func (r *commandRate) note(ctx context.Context, playerID string, now time.Time) {
	if r == nil || playerID == "" {
		return
	}
	finding, fire, crowded := r.count(playerID, now)
	if fire {
		if _, err := r.recorder.Raise(ctx, application.WatchFlag{Rule: string(finding.Rule), PlayerID: playerID,
			Score: finding.Score, Evidence: finding.Evidence, UpdatedAt: now}); err != nil {
			r.logger.Warn("cannot record a command-rate flag", slog.String("error", err.Error()))
		}
	}
	// Forget the quiet: a map of every player ever seen would only grow.
	if crowded {
		r.sweep(now)
	}
}

// count adds one command and says whether it runs over the rate, for the
// first time this minute.
func (r *commandRate) count(playerID string, now time.Time) (watch.Finding, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	from := now.Add(-time.Minute)
	times := r.seen[playerID]
	kept := times[:0]
	for _, t := range times {
		if t.After(from) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	r.seen[playerID] = kept
	crowded := len(kept) == 1 && len(r.seen) > 10_000
	f, over := r.th.Rate(len(kept))
	if !over || now.Sub(r.flagged[playerID]) < time.Minute {
		return f, false, crowded
	}
	r.flagged[playerID] = now
	return f, true, crowded
}

// sweep drops players with nothing in the last minute.
func (r *commandRate) sweep(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	from := now.Add(-time.Minute)
	for id, times := range r.seen {
		if len(times) == 0 || !times[len(times)-1].After(from) {
			delete(r.seen, id)
			delete(r.flagged, id)
		}
	}
}
