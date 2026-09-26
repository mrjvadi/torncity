package panel

import (
	"context"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

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

// Switches reads every switch's row and, when the panel has a Redis cache to
// check (SwitchCache is set — see cmd/panel/main.go), the value the gateway
// is actually acting on right now and how long ago it was cached.
func (p *PG) Switches(ctx context.Context) (SwitchesView, error) {
	rows, err := operator.Ops{Pool: p.Pool}.Switches(ctx)
	if err != nil {
		return SwitchesView{}, err
	}
	out := SwitchesView{Switches: make([]SwitchStatus, 0, len(rows))}
	if p.Config != nil {
		out.MiniAppURL = p.Config.Client.MiniAppURL
		out.MiniAppURLMissing = out.MiniAppURL == ""
	}
	for _, s := range rows {
		st := SwitchStatus{SwitchState: s, Effective: s.Value}
		if p.SwitchCache != nil {
			if v, age, err := p.SwitchCache.Get(ctx, s.Key, s.Value); err == nil {
				st.Effective = v
				if age > 0 {
					secs := age.Seconds()
					st.CacheAgeSeconds = &secs
				}
			}
		}
		out.Switches = append(out.Switches, st)
	}
	return out, nil
}

// SetSwitch flips one switch, audited.
func (p *PG) SetSwitch(ctx context.Context, key, value string, a operator.Actor) (postgres.SwitchState, error) {
	return operator.Ops{Pool: p.Pool}.SetSwitch(ctx, key, value, a)
}

// SwitchHistory lists past switch changes, newest first.
func (p *PG) SwitchHistory(ctx context.Context, limit int) ([]postgres.SwitchHistoryEntry, error) {
	return operator.Ops{Pool: p.Pool}.SwitchHistory(ctx, limit)
}
