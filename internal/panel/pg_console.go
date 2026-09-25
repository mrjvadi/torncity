package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

var _ Console = (*PG)(nil)

// defaultReadTimeout is a read's limit when no configuration is given.
const defaultReadTimeout = 8 * time.Second

// Read runs one of the console's read-only statements.
func (p *PG) Read(ctx context.Context, sql string, args ...any) (postgres.PanelTable, error) {
	timeout := defaultReadTimeout
	if p.Config != nil && p.Config.Panel.ReadTimeout > 0 {
		timeout = p.Config.Panel.ReadTimeout
	}
	return p.admin().PanelRead(ctx, timeout, sql, args...)
}

// Moderate mutes or bans a player.
func (p *PG) Moderate(ctx context.Context, player, kind string, dur time.Duration, a operator.Actor) (postgres.Moderated, error) {
	return p.Ops.Moderate(ctx, player, kind, dur, a)
}

// LiftModeration lifts a player's mute or ban.
func (p *PG) LiftModeration(ctx context.Context, player, kind string, a operator.Actor) (postgres.Moderated, error) {
	return p.Ops.LiftModeration(ctx, player, kind, a)
}

// Release ends a player's sentence now.
func (p *PG) Release(ctx context.Context, player string, a operator.Actor) (postgres.Ended, error) {
	return p.Ops.Release(ctx, player, a)
}

// Discharge ends a player's hospital stay now.
func (p *PG) Discharge(ctx context.Context, player string, a operator.Actor) (postgres.Ended, error) {
	return p.Ops.Discharge(ctx, player, a)
}

// Requeue hands a stuck action back to the scheduler.
func (p *PG) Requeue(ctx context.Context, id string, a operator.Actor) (postgres.Requeued, error) {
	return p.Ops.Requeue(ctx, id, a)
}

// Dissolve closes a company on the operator's authority.
func (p *PG) Dissolve(ctx context.Context, company string, a operator.Actor) (handlers.OperatorClosing, error) {
	return p.Ops.Dissolve(ctx, company, a)
}

// snapCache keeps the content in force for a minute, so a dossier or a
// chart does not reload it on every request.
type snapCache struct {
	mu   sync.Mutex
	snap *content.Snapshot
	pack *content.Pack
	at   time.Time
}

func (p *PG) cached(ctx context.Context) (*content.Snapshot, *content.Pack, error) {
	c := &p.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && time.Since(c.at) < time.Minute {
		return c.snap, c.pack, nil
	}
	pack, err := postgres.NewContentStore(p.Pool).LoadActive(ctx)
	if err != nil {
		return nil, nil, err
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return nil, nil, err
	}
	c.snap, c.pack, c.at = snap, pack, time.Now()
	return snap, pack, nil
}

// Age is a character's age and stage of life on the game's clock.
func (p *PG) Age(ctx context.Context, born, now time.Time) (int, string, bool) {
	if p.Config == nil || born.IsZero() {
		return 0, "", false
	}
	snap, _, err := p.cached(ctx)
	if err != nil {
		return 0, "", false
	}
	def, ok := snap.Life()
	if !ok {
		return 0, "", false
	}
	aging := def.Aging()
	age := aging.Age(born, now, gametime.Scale(p.Config.Game.TimeScale))
	return age, aging.StageOf(age), true
}

// EconomySeries is the money over the last days.
type EconomySeries struct {
	Days    []string     `json:"days"`
	Supply  []int64      `json:"supply"`
	Minted  []int64      `json:"minted"`
	Burned  []int64      `json:"burned"`
	Faucets []SeriesLine `json:"faucets"`
	Drains  []SeriesLine `json:"drains"`
	// PriceIndex is what goods traded for against their reference prices,
	// bps (10000 = at reference); 0 on a day nothing priced traded.
	PriceIndex []int64 `json:"price_index_bps"`
	Total      int64   `json:"total"`
}

// EconomySeries reads the money supply, the faucets and drains by reason,
// and the price index, per day on the game's clock.
func (p *PG) EconomySeries(ctx context.Context, days int, now time.Time) (EconomySeries, error) {
	var out EconomySeries
	since := now.In(tehran).AddDate(0, 0, -(days - 1))
	since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, tehran)
	out.Days = dayList(since, now)
	flows := func(account, sign string) (Series, error) {
		t, err := p.Read(ctx, `SELECT `+day("created_at")+`, reason, (`+sign+`SUM(amount))::bigint FROM ledger_entries
			WHERE account_id = $2::uuid AND created_at >= $1 GROUP BY 1, 2`, since.UTC(), account)
		if err != nil {
			return Series{}, err
		}
		return buildSeries("", TMoney, out.Days, t.Rows), nil
	}
	faucets, err := flows("00000000-0000-4000-8000-000000000001", "-")
	if err != nil {
		return out, err
	}
	drains, err := flows("00000000-0000-4000-8000-000000000002", "")
	if err != nil {
		return out, err
	}
	out.Faucets, out.Drains = faucets.Lines, drains.Lines
	n := len(out.Days)
	out.Minted, out.Burned, out.Supply, out.PriceIndex = make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)
	for _, l := range out.Faucets {
		for i, v := range l.Values {
			out.Minted[i] += v
		}
	}
	for _, l := range out.Drains {
		for i, v := range l.Values {
			out.Burned[i] += v
		}
	}
	t, err := p.Read(ctx, `SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind NOT IN ('system_source', 'system_sink')`)
	if err != nil {
		return out, err
	}
	if len(t.Rows) > 0 {
		out.Total, _ = t.Rows[0][0].(int64)
	}
	running := out.Total
	for i := n - 1; i >= 0; i-- {
		out.Supply[i] = running
		running -= out.Minted[i] - out.Burned[i]
	}
	snap, _, err := p.cached(ctx)
	if err != nil {
		return out, nil
	}
	t, err = p.Read(ctx, `SELECT d, item, SUM(qty)::bigint, SUM(value)::bigint FROM (
		SELECT `+day("created_at")+` AS d, item_code AS item, quantity AS qty, notional AS value FROM market_trades WHERE created_at >= $1
		UNION ALL SELECT `+day("created_at")+`, item_code, quantity, total FROM company_sales WHERE created_at >= $1) x
		GROUP BY d, item`, since.UTC())
	if err != nil {
		return out, err
	}
	idx := map[string]int{}
	for i, d := range out.Days {
		idx[d] = i
	}
	paid, ref := make([]int64, n), make([]int64, n)
	for _, r := range t.Rows {
		d, _ := r[0].(time.Time)
		item, _ := r[1].(string)
		qty, _ := r[2].(int64)
		val, _ := r[3].(int64)
		i, ok := idx[d.Format("2006-01-02")]
		def, known := snap.ItemDef(item)
		if !ok || !known || def.BasePrice <= 0 {
			continue
		}
		paid[i] += val
		ref[i] += qty * def.BasePrice
	}
	for i := range paid {
		if ref[i] > 0 {
			out.PriceIndex[i] = paid[i] * 10000 / ref[i]
		}
	}
	return out, nil
}

// ContentDiff is the files on the server against the content in force.
type ContentDiff struct {
	Version    int           `json:"version"`
	LocalError string        `json:"local_error"`
	Sections   []SectionDiff `json:"sections"`
}

// SectionDiff is one section of the content compared entry by entry.
type SectionDiff struct {
	Name    string   `json:"name"`
	Local   int      `json:"local"`
	Active  int      `json:"active"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
}

// sections lists a pack's sections by their snake_case names, each as its
// entries keyed by code (or position when an entry has none).
func sections(p *content.Pack) map[string]map[string]string {
	out := map[string]map[string]string{}
	if p == nil {
		return out
	}
	v := reflect.ValueOf(*p)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Name == "Schema" || f.Name == "Version" {
			continue
		}
		entries := map[string]string{}
		fv := v.Field(i)
		if fv.Kind() == reflect.Slice {
			for j := 0; j < fv.Len(); j++ {
				e := fv.Index(j)
				key := fmt.Sprintf("#%d", j+1)
				ev := reflect.Indirect(e)
				if ev.Kind() == reflect.Struct {
					for _, name := range []string{"Code", "Key", "ID", "Name"} {
						if c := ev.FieldByName(name); c.IsValid() && c.Kind() == reflect.String && c.String() != "" {
							key = c.String()
							break
						}
					}
				} else if ev.Kind() == reflect.String {
					key = ev.String()
				}
				b, _ := json.Marshal(e.Interface())
				entries[key] = string(b)
			}
		} else {
			b, _ := json.Marshal(fv.Interface())
			entries[""] = string(b)
		}
		out[snake(f.Name)] = entries
	}
	return out
}

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// diffSections compares two packs' sections.
func diffSections(local, active *content.Pack) []SectionDiff {
	l, a := sections(local), sections(active)
	names := map[string]bool{}
	for n := range l {
		names[n] = true
	}
	for n := range a {
		names[n] = true
	}
	out := []SectionDiff{}
	for n := range names {
		d := SectionDiff{Name: n, Local: len(l[n]), Active: len(a[n]), Added: []string{}, Removed: []string{}, Changed: []string{}}
		for k, v := range l[n] {
			old, ok := a[n][k]
			switch {
			case !ok:
				d.Added = append(d.Added, k)
			case old != v:
				d.Changed = append(d.Changed, k)
			}
		}
		for k := range a[n] {
			if _, ok := l[n][k]; !ok {
				d.Removed = append(d.Removed, k)
			}
		}
		sort.Strings(d.Added)
		sort.Strings(d.Removed)
		sort.Strings(d.Changed)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ContentDiff compares the files on the server with the content in force.
func (p *PG) ContentDiff(ctx context.Context) (ContentDiff, error) {
	var out ContentDiff
	active, err := postgres.NewContentStore(p.Pool).LoadActive(ctx)
	if err != nil && !errors.Is(err, postgres.ErrNoActiveVersion) {
		return out, err
	}
	if active != nil {
		out.Version = active.Version
	}
	local, lerr := operator.LoadAndValidate(p.ContentDir)
	if lerr != nil {
		out.LocalError = lerr.Error()
		local = nil
	}
	out.Sections = diffSections(local, active)
	return out, nil
}

// ContentSection is one section of the content in force, as authored.
func (p *PG) ContentSection(ctx context.Context, name string) (any, error) {
	_, pack, err := p.cached(ctx)
	if err != nil {
		return nil, err
	}
	v := reflect.ValueOf(*pack)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() && snake(t.Field(i).Name) == name {
			return v.Field(i).Interface(), nil
		}
	}
	return nil, postgres.ErrNotFound
}

// NATSStatus is the broker's streams as its monitoring endpoint reports.
type NATSStatus struct {
	URL     string       `json:"url"`
	Error   string       `json:"error,omitempty"`
	Streams []NATSStream `json:"streams"`
}

// NATSStream is one stream and its consumers.
type NATSStream struct {
	Name      string         `json:"name"`
	Messages  int64          `json:"messages"`
	Bytes     int64          `json:"bytes"`
	Consumers []NATSConsumer `json:"consumers"`
}

// NATSConsumer is one consumer's backlog.
type NATSConsumer struct {
	Name        string `json:"name"`
	Pending     int64  `json:"pending"`
	AckPending  int64  `json:"ack_pending"`
	Redelivered int64  `json:"redelivered"`
}

// jsz is the part of NATS's /jsz answer the panel reads.
type jsz struct {
	Accounts []struct {
		Streams []struct {
			Name  string `json:"name"`
			State struct {
				Messages int64 `json:"messages"`
				Bytes    int64 `json:"bytes"`
			} `json:"state"`
			Consumers []struct {
				Name        string `json:"name"`
				Pending     int64  `json:"num_pending"`
				AckPending  int64  `json:"num_ack_pending"`
				Redelivered int64  `json:"num_redelivered"`
			} `json:"consumer_detail"`
		} `json:"stream_detail"`
	} `json:"account_details"`
}

// parseJSZ reads a /jsz?streams=true&consumers=true answer.
func parseJSZ(raw []byte) ([]NATSStream, error) {
	var j jsz
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	out := []NATSStream{}
	for _, a := range j.Accounts {
		for _, s := range a.Streams {
			st := NATSStream{Name: s.Name, Messages: s.State.Messages, Bytes: s.State.Bytes, Consumers: []NATSConsumer{}}
			for _, c := range s.Consumers {
				st.Consumers = append(st.Consumers, NATSConsumer{Name: c.Name, Pending: c.Pending, AckPending: c.AckPending,
					Redelivered: c.Redelivered})
			}
			out = append(out, st)
		}
	}
	return out, nil
}

// NATS reads the broker's streams, when its monitoring endpoint is
// reachable from the panel.
func (p *PG) NATS(ctx context.Context) (NATSStatus, error) {
	if p.Config == nil || p.Config.Panel.NATSMonitorURL == "" {
		return NATSStatus{Streams: []NATSStream{}}, errors.New("no NATS monitoring address is configured")
	}
	base := strings.TrimRight(p.Config.Panel.NATSMonitorURL, "/")
	out := NATSStatus{URL: base, Streams: []NATSStream{}}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/jsz?streams=true&consumers=true", nil)
	if err != nil {
		return out, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, errors.New("the NATS monitoring endpoint is not reachable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || res.StatusCode != http.StatusOK {
		return out, fmt.Errorf("the NATS monitoring endpoint answered %d", res.StatusCode)
	}
	out.Streams, err = parseJSZ(raw)
	return out, err
}
