package notification

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// Alongside every notice, a player's personal channel also carries a full
// snapshot of the numbers their HUD shows (RealtimeVitals): cash, bank,
// energy, health, xp, level and unread count. It is read fresh, once per
// publish, from the same repositories player.profile.get and bank.show
// already read through, rather than threaded out of every route's own view
// — that is what makes this ONE choke point (Worker.Handle) instead of a
// change to every domain file in this package.
//
// Energy and health regenerate between writes (docs/adr/0025 game time); this
// reads their last-written value, exactly as every existing notice's view
// already does, and the client's slower HTTP poll is what catches a regen
// tick that happened between events.

// StatsReader reads a player's condition (player_stats).
type StatsReader interface {
	Get(ctx context.Context, playerID string) (*application.Stats, error)
}

// BalanceReader reads one of a player's ledger accounts.
type BalanceReader interface {
	AccountFor(ctx context.Context, kind application.AccountKind, ownerID string) (application.Account, error)
}

// VitalsReader composes a player's vitals snapshot for the realtime
// publication. Nil in Config.Vitals publishes none: every deployment and
// test that predates this feature keeps working unchanged.
type VitalsReader interface {
	Get(ctx context.Context, playerID string) (RealtimeVitals, error)
}

// DefaultVitals reads Stats and the ledger directly. Inbox is optional: nil
// leaves Unread at zero, which still gives a client cash, bank, energy,
// health, xp and level.
type DefaultVitals struct {
	Stats  StatsReader
	Ledger BalanceReader
	Inbox  PlayerInbox
}

// Get reads the sources and assembles one snapshot. It fails only if Stats
// or the cash/bank accounts cannot be read — those are the numbers a vitals
// push exists for. A failed inbox summary is not fatal: Unread simply stays
// zero here, and the existing "inbox" publication (realtime.go) is still the
// bell badge's own source of truth.
func (v DefaultVitals) Get(ctx context.Context, playerID string) (RealtimeVitals, error) {
	var out RealtimeVitals

	stats, err := v.Stats.Get(ctx, playerID)
	if err != nil {
		return out, err
	}
	out.Level, out.XP = stats.Level, stats.XP
	out.Health, out.MaxHealth = stats.Health, stats.MaxHealth
	out.Energy, out.MaxEnergy = stats.Energy, stats.MaxEnergy

	cash, err := v.Ledger.AccountFor(ctx, application.AccountPlayerCash, playerID)
	if err != nil {
		return out, err
	}
	out.Cash = cash.Balance.Minor()

	bank, err := v.Ledger.AccountFor(ctx, application.AccountPlayerBank, playerID)
	if err != nil {
		return out, err
	}
	out.Bank = bank.Balance.Minor()

	if v.Inbox != nil {
		if summary, err := v.Inbox.Summary(ctx, playerID, 0); err == nil {
			out.Unread = summary.Unread
		}
	}
	return out, nil
}

// vitalsDebounce bounds how often one player's vitals are read and
// published. Worker.Handle runs once per event, and a busy player (a crime
// spree, a travelling crew told together) would otherwise cost a stats read
// and two ledger reads per event for a number a client is about to replace
// again a moment later.
//
// The process never shuts this down explicitly; an opportunistic sweep on
// the write path keeps the map from growing without bound across a long
// uptime with many distinct players.
//
// A suppressed event is not dropped: the first one inside the window books
// one trailing publish for when the window closes, so the snapshot a client
// ends up with is always the one after the burst's last event, never a
// stale one from its start.
type vitalsDebounce struct {
	mu      sync.Mutex
	min     time.Duration
	last    map[string]time.Time
	pending map[string]bool
	nextRun time.Time
	// after runs fn once d has passed; time.AfterFunc outside tests.
	after func(d time.Duration, fn func())
}

func newVitalsDebounce(min time.Duration) *vitalsDebounce {
	return &vitalsDebounce{
		min: min, last: map[string]time.Time{}, pending: map[string]bool{},
		after: func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
	}
}

const (
	vitalsSweepSize     = 20000
	vitalsSweepAge      = time.Hour
	vitalsSweepInterval = 5 * time.Minute
)

// allow reports whether playerID's vitals may be read and published now, and
// if so records that they were. When they may not, trail is how long until
// the window closes if this call booked the one trailing publish, or zero
// if one is already booked.
func (d *vitalsDebounce) allow(playerID string, now time.Time) (ok bool, trail time.Duration) {
	if d == nil || d.min <= 0 {
		return true, 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, seen := d.last[playerID]; seen && now.Sub(t) < d.min {
		if d.pending[playerID] {
			return false, 0
		}
		d.pending[playerID] = true
		return false, d.min - now.Sub(t)
	}
	d.last[playerID] = now
	delete(d.pending, playerID)
	if len(d.last) > vitalsSweepSize && now.After(d.nextRun) {
		for id, t := range d.last {
			if now.Sub(t) > vitalsSweepAge {
				delete(d.last, id)
			}
		}
		d.nextRun = now.Add(vitalsSweepInterval)
	}
	return true, 0
}

// publishVitals reads and publishes playerID's vitals snapshot, debounced.
// Best effort throughout, like publishNotice: a failure here never affects
// the notice already delivered, or the Telegram delivery about to be tried.
// It is called from Worker.Handle, well after the command that changed
// these numbers has committed and returned — never from inside a
// transaction (see the package's realtime.go doc and the incident this
// guards against).
func (w *Worker) publishVitals(ctx context.Context, now time.Time, playerID string, log *slog.Logger) {
	if w.cfg.Realtime == nil || w.cfg.Vitals == nil {
		return
	}
	ok, trail := w.vitals.allow(playerID, now)
	if !ok {
		if trail > 0 {
			// The event's own context ends with Handle; the trailing
			// publish gets its own short one.
			w.vitals.after(trail, func() {
				tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				w.publishVitals(tctx, w.cfg.Now(), playerID, log)
			})
		}
		return
	}
	v, err := w.cfg.Vitals.Get(ctx, playerID)
	if err != nil {
		log.Warn("cannot read vitals for the realtime publish", slog.String("error", err.Error()))
		return
	}
	v.Type = "vitals"
	// Not scoped to one event's message id, unlike a notice: several fresh
	// reads of the same unchanged numbers are harmless to send again, and a
	// key that never repeats would defeat Centrifugo's idempotency window
	// for nothing gained.
	key := "vitals:" + playerID + ":" + strconv.FormatInt(now.UnixMilli(), 10)
	if err := w.cfg.Realtime.Publish(ctx, playerChannel(playerID), v, key); err != nil {
		log.Warn("cannot publish vitals to the realtime server", slog.String("error", err.Error()))
	}
}
