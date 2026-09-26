package panel

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/switches"
)

// knownSwitches are always listed, at their built-in default, even before
// their first change — so System > Switches can turn one on for the very
// first time rather than only ever changing a row that already exists. A
// future switch not in this list still shows up once an operator (or
// another feature) sets it at least once; this list only guarantees the two
// this feature ships are never missing.
var knownSwitches = []struct{ key, def string }{
	{switches.KeyTelegramPlay, switches.PlayOn},
	{switches.KeyTelegramNotices, switches.NoticesOn},
}

// System > Switches (migrations/0041_runtime_switches): the operator's
// runtime switches, read and changed through internal/operator.Ops exactly
// as cmd/admin's `switch` command does, so a change is audited and shows up
// in `admin switch list` and here alike.

// switchValue reads one switch's current value straight from the database,
// falling back to def when it has never been set. Used by Overview, which
// needs the value only, not the whole health line.
func (p *PG) switchValue(ctx context.Context, key, def string) string {
	v, found, err := postgres.NewSwitchOps(p.Pool).Get(ctx, key)
	if err != nil || !found {
		return def
	}
	return v
}

// Switches reads every switch's row — telegram_play and telegram_notices
// always, at their default when neither has ever been set, plus any other
// switch a row exists for — and, when the panel has a Redis cache to check
// (SwitchCache is set — see cmd/panel/main.go), the value the gateway is
// actually acting on right now and how long ago it was cached.
func (p *PG) Switches(ctx context.Context) (SwitchesView, error) {
	rows, err := operator.Ops{Pool: p.Pool}.Switches(ctx)
	if err != nil {
		return SwitchesView{}, err
	}
	byKey := make(map[string]postgres.SwitchState, len(rows))
	for _, s := range rows {
		byKey[s.Key] = s
	}

	out := SwitchesView{Switches: make([]SwitchStatus, 0, len(rows)+len(knownSwitches))}
	if p.Config != nil {
		out.MiniAppURL = p.Config.Client.MiniAppURL
		out.MiniAppURLMissing = out.MiniAppURL == ""
	}

	seen := make(map[string]bool, len(knownSwitches))
	for _, k := range knownSwitches {
		seen[k.key] = true
		if s, ok := byKey[k.key]; ok {
			out.Switches = append(out.Switches, p.switchStatus(ctx, s))
			continue
		}
		out.Switches = append(out.Switches, p.switchStatus(ctx, postgres.SwitchState{Key: k.key, Value: k.def}))
	}
	for _, s := range rows {
		if !seen[s.Key] {
			out.Switches = append(out.Switches, p.switchStatus(ctx, s))
		}
	}
	return out, nil
}

// switchStatus adds the cache health line to one switch's row.
func (p *PG) switchStatus(ctx context.Context, s postgres.SwitchState) SwitchStatus {
	st := SwitchStatus{Key: s.Key, Value: s.Value, Reason: s.Reason, Effective: s.Value}
	if !s.ChangedAt.IsZero() {
		st.ChangedBy = s.ChangedBy
		st.ChangedAt = s.ChangedAt.UTC().Format(time.RFC3339)
	}
	if p.SwitchCache != nil {
		if v, age, err := p.SwitchCache.Get(ctx, s.Key, s.Value); err == nil {
			st.Effective = v
			if age > 0 {
				secs := age.Seconds()
				st.CacheAgeSeconds = &secs
			}
		}
	}
	return st
}

// SetSwitch flips one switch, audited.
func (p *PG) SetSwitch(ctx context.Context, key, value string, a operator.Actor) (postgres.SwitchState, error) {
	return operator.Ops{Pool: p.Pool}.SetSwitch(ctx, key, value, a)
}

// SwitchHistory lists past switch changes, newest first.
func (p *PG) SwitchHistory(ctx context.Context, limit int) ([]postgres.SwitchHistoryEntry, error) {
	return operator.Ops{Pool: p.Pool}.SwitchHistory(ctx, limit)
}
