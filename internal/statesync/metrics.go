package statesync

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics are the state sync's counters (docs/adr/0034 "Metrics"), kept per
// process and written to the log by Report at an interval: the services
// have no metrics endpoint, and a counter per replica summed by whoever
// reads the logs is enough to see lag, resets and redeliveries.
type Metrics struct {
	mu       sync.Mutex
	appended map[string]int64 // updates_appended_total{kind}
	resets   map[string]int64 // reset_total{reason}
	pulls    map[string]int64 // updates_pull_total{result}

	projections   atomic.Int64 // projections run
	projectErrors atomic.Int64 // projections that failed
	redeliveries  atomic.Int64 // event projections that appended nothing and re-sent what the event had appended
	published     atomic.Int64 // publications sent
	publishErrors atomic.Int64
	tooLong       atomic.Int64 // publications replaced by a too_long poke
	commandHits   atomic.Int64 // command answers that carried their updates
	commandMisses atomic.Int64 // command answers without (the wait ran out)
	fanout        atomic.Int64 // settlement summaries projected for an audience

	lagCount atomic.Int64 // update_lag_seconds: commit -> publish
	lagSumUS atomic.Int64
	lagMaxUS atomic.Int64

	clientLagCount atomic.Int64 // client_pts_lag: allocated - since
	clientLagSum   atomic.Int64
	clientLagMax   atomic.Int64
}

// NewMetrics returns empty counters.
func NewMetrics() *Metrics {
	return &Metrics{appended: map[string]int64{}, resets: map[string]int64{}, pulls: map[string]int64{}}
}

func (m *Metrics) add(table map[string]int64, key string, n int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	table[key] += n
	m.mu.Unlock()
}

// Appended counts records appended, by kind.
func (m *Metrics) Appended(records []Record) {
	if m == nil {
		return
	}
	m.mu.Lock()
	for _, r := range records {
		m.appended[r.Entity]++
	}
	m.mu.Unlock()
}

// Pull counts one GET /updates answer: "empty", "ok" or "reset".
func (m *Metrics) Pull(result string) {
	if m != nil {
		m.add(m.pulls, result, 1)
	}
}

// Reset counts one reset by reason.
func (m *Metrics) Reset(reason string) {
	if m != nil {
		m.add(m.resets, reason, 1)
	}
}

// Lag records how long a batch waited between its commit and its publish.
func (m *Metrics) Lag(d time.Duration) {
	if m == nil {
		return
	}
	us := d.Microseconds()
	m.lagCount.Add(1)
	m.lagSumUS.Add(us)
	for {
		cur := m.lagMaxUS.Load()
		if us <= cur || m.lagMaxUS.CompareAndSwap(cur, us) {
			break
		}
	}
}

// ClientLag records how far behind a pulling client was.
func (m *Metrics) ClientLag(n int64) {
	if m == nil || n < 0 {
		return
	}
	m.clientLagCount.Add(1)
	m.clientLagSum.Add(n)
	for {
		cur := m.clientLagMax.Load()
		if n <= cur || m.clientLagMax.CompareAndSwap(cur, n) {
			break
		}
	}
}

func (m *Metrics) inc(c *atomic.Int64) {
	if m != nil {
		c.Add(1)
	}
}

// Snapshot is every counter by name, for the log line and for tests.
func (m *Metrics) Snapshot() map[string]any {
	if m == nil {
		return nil
	}
	copyTable := func(t map[string]int64) map[string]int64 {
		out := make(map[string]int64, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = t[k]
		}
		return out
	}
	m.mu.Lock()
	appended, resets, pulls := copyTable(m.appended), copyTable(m.resets), copyTable(m.pulls)
	m.mu.Unlock()
	out := map[string]any{
		"updates_appended_total":  appended,
		"reset_total":             resets,
		"updates_pull_total":      pulls,
		"projections_total":       m.projections.Load(),
		"projection_errors_total": m.projectErrors.Load(),
		"projector_redeliveries":  m.redeliveries.Load(),
		"publications_total":      m.published.Load(),
		"publish_errors_total":    m.publishErrors.Load(),
		"too_long_pokes_total":    m.tooLong.Load(),
		"command_updates_hit":     m.commandHits.Load(),
		"command_updates_miss":    m.commandMisses.Load(),
		"settlement_fanout_total": m.fanout.Load(),
		"update_lag_max_ms":       float64(m.lagMaxUS.Load()) / 1000,
		"client_pts_lag_max":      m.clientLagMax.Load(),
	}
	if n := m.lagCount.Load(); n > 0 {
		out["update_lag_avg_ms"] = float64(m.lagSumUS.Load()) / float64(n) / 1000
	}
	if n := m.clientLagCount.Load(); n > 0 {
		out["client_pts_lag_avg"] = float64(m.clientLagSum.Load()) / float64(n)
	}
	return out
}
