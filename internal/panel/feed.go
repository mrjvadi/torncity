package panel

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Feed keeps panel:ops current: every Interval it looks for audit rows,
// watch flags and held payments newer than the last it saw and publishes
// each; every KPIInterval it publishes the dashboard's figures, and a health
// event when the system's health changed. It reads through the same
// read-only Reader as the console, so it adds no path to the game's data.
type Feed struct {
	Reader      Reader
	Publisher   Publisher
	Interval    time.Duration
	KPIInterval time.Duration
	Log         *slog.Logger
	Now         func() time.Time

	lastAudit  int64
	lastFlag   time.Time
	lastHold   time.Time
	lastHealth string
}

// feedBatch bounds one look's rows of each kind.
const feedBatch = 100

// Run publishes until ctx ends.
func (f *Feed) Run(ctx context.Context) {
	if f.Now == nil {
		f.Now = time.Now
	}
	if f.Log == nil {
		f.Log = slog.Default()
	}
	if err := f.start(ctx); err != nil {
		f.Log.Warn("panel: the live feed could not find where to start", "error", err.Error())
	}
	tick := time.NewTicker(f.Interval)
	kpi := time.NewTicker(f.KPIInterval)
	defer tick.Stop()
	defer kpi.Stop()
	f.publishKPIs(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := f.Step(ctx); err != nil && ctx.Err() == nil {
				f.Log.Warn("panel: the live feed missed a step", "error", err.Error())
			}
		case <-kpi.C:
			f.publishKPIs(ctx)
		}
	}
}

// start sets the cursors to now: the feed publishes what happens from here.
func (f *Feed) start(ctx context.Context) error {
	t, err := f.Reader.Read(ctx, `SELECT COALESCE((SELECT max(id) FROM audit_logs), 0)::bigint,
		COALESCE((SELECT max(updated_at) FROM watch_flags), now()), COALESCE((SELECT max(created_at) FROM payment_holds), now())`)
	if err != nil {
		return err
	}
	if len(t.Rows) == 1 && len(t.Rows[0]) == 3 {
		f.lastAudit, _ = t.Rows[0][0].(int64)
		f.lastFlag, _ = t.Rows[0][1].(time.Time)
		f.lastHold, _ = t.Rows[0][2].(time.Time)
	}
	return nil
}

func (f *Feed) send(ctx context.Context, typ string, data any) {
	ev := Event{Type: typ, At: f.Now().UTC(), Data: data}
	if err := f.Publisher.Publish(ctx, OpsChannel, ev); err != nil {
		f.Log.Warn("panel: publishing to the live feed failed", "type", typ, "error", err.Error())
	}
}

// Step publishes what is new since the last step.
func (f *Feed) Step(ctx context.Context) error {
	audits, err := f.Reader.Read(ctx, `SELECT id, created_at, actor, action, target_type, target_id::text AS target
		FROM audit_logs WHERE id > $1 ORDER BY id LIMIT $2`, f.lastAudit, feedBatch)
	if err != nil {
		return fmt.Errorf("audit rows: %w", err)
	}
	for _, r := range records(audits) {
		if id, ok := r["id"].(int64); ok && id > f.lastAudit {
			f.lastAudit = id
		}
		f.send(ctx, "audit", r)
	}
	flags, err := f.Reader.Read(ctx, `SELECT f.no, f.rule, p.public_code AS player, f.score, f.hits, f.status, f.updated_at
		FROM watch_flags f JOIN players p ON p.id = f.player_id WHERE f.updated_at > $1 ORDER BY f.updated_at LIMIT $2`,
		f.lastFlag, feedBatch)
	if err != nil {
		return fmt.Errorf("flags: %w", err)
	}
	for _, r := range records(flags) {
		if at, ok := r["updated_at"].(time.Time); ok && at.After(f.lastFlag) {
			f.lastFlag = at
		}
		f.send(ctx, "flag", r)
	}
	holds, err := f.Reader.Read(ctx, `SELECT h.no, a.public_code AS payer, b.public_code AS payee, h.amount, h.method, h.status,
		h.created_at FROM payment_holds h JOIN players a ON a.id = h.payer_id JOIN players b ON b.id = h.payee_id
		WHERE h.created_at > $1 ORDER BY h.created_at LIMIT $2`, f.lastHold, feedBatch)
	if err != nil {
		return fmt.Errorf("holds: %w", err)
	}
	for _, r := range records(holds) {
		if at, ok := r["created_at"].(time.Time); ok && at.After(f.lastHold) {
			f.lastHold = at
		}
		f.send(ctx, "hold", r)
	}
	return nil
}

// healthOf names the system's state from the dashboard's figures: "ok",
// or what is wrong.
func healthOf(k map[string]any, now time.Time) string {
	n := func(key string) int64 { v, _ := k[key].(int64); return v }
	switch {
	case n("actions_stuck") > 0 || n("outbox_failed") > 0:
		return "degraded"
	case n("actions_overdue") > 0:
		return "lagging"
	}
	if oldest, ok := k["outbox_oldest"].(time.Time); ok && now.Sub(oldest) > time.Minute {
		return "lagging"
	}
	return "ok"
}

func (f *Feed) publishKPIs(ctx context.Context) {
	t, err := f.Reader.Read(ctx, kpiSQL)
	state := "down"
	if err == nil && len(t.Rows) == 1 {
		k := records(t)[0]
		state = healthOf(k, f.Now())
		k["health"] = state
		f.send(ctx, "kpis", k)
	}
	if state != f.lastHealth {
		if f.lastHealth != "" {
			f.send(ctx, "health", map[string]any{"state": state, "was": f.lastHealth})
		}
		f.lastHealth = state
	}
}
