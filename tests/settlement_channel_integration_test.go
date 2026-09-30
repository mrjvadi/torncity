//go:build integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/infrastructure/centrifugo"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	infraredis "github.com/mrjvadi/torncity/internal/infrastructure/redis"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/workers/notification"
)

// The settlement channel and the village news (docs/adr/0030, R2), against
// real PostgreSQL, Redis and — when INTEGRATION_CENTRIFUGO_API_URL/KEY are
// set — a real Centrifugo: the events a village really writes to the outbox
// are run through the notifier's worker, twice, the way a redelivery would.

// recordingPublisher is a Realtime that keeps what it was given, used when no
// Centrifugo is configured.
type recordingPublisher struct {
	mu   sync.Mutex
	pubs []map[string]any
	keys []string
}

func (r *recordingPublisher) Publish(_ context.Context, channel string, data any, key string) error {
	raw, _ := json.Marshal(data)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["_channel"] = channel
	r.mu.Lock()
	r.pubs = append(r.pubs, m)
	r.keys = append(r.keys, key)
	r.mu.Unlock()
	return nil
}

// captureSender records the notices the worker asks the gateway to send.
type captureSender struct {
	mu      sync.Mutex
	notices []notification.Notice
	metas   []envelope.Metadata
}

func (c *captureSender) Send(_ context.Context, _ string, env *envelope.Envelope) (notification.Receipt, error) {
	var n notification.Notice
	if err := env.Decode(&n); err != nil {
		return notification.Receipt{}, err
	}
	c.mu.Lock()
	c.notices = append(c.notices, n)
	c.metas = append(c.metas, env.Metadata)
	c.mu.Unlock()
	return notification.Receipt{Outcome: notification.OutcomeDelivered}, nil
}

// centrifugoHistory reads a channel's history through the server API.
func centrifugoHistory(t *testing.T, url, key, channel string) []map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"channel": channel, "limit": 100})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(url, "/")+"/history", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reading channel history: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Result struct {
			Publications []struct {
				Data map[string]any `json:"data"`
			} `json:"publications"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Error != nil {
		t.Fatalf("history of %s: %s", channel, raw)
	}
	var pubs []map[string]any
	for _, p := range out.Result.Publications {
		pubs = append(pubs, p.Data)
	}
	return pubs
}

// verifySettlementChannel takes every event a village wrote to the outbox and
// proves the settlement channel and the village news are right for them.
func verifySettlementChannel(t *testing.T, pool *postgres.Pool, cityID string) {
	t.Helper()
	ctx := testCtx(t)
	rdb := requireRedis(t)
	catalog, err := i18n.Load("../configs/locales")
	if err != nil {
		t.Fatal(err)
	}

	var realtime notification.Realtime = &recordingPublisher{}
	cfURL, cfKey := os.Getenv("INTEGRATION_CENTRIFUGO_API_URL"), os.Getenv("INTEGRATION_CENTRIFUGO_API_KEY")
	if cfURL != "" {
		realtime = centrifugo.NewPublisher(cfURL, cfKey, 5*time.Second)
	}
	sender := &captureSender{}
	clock := time.Now().UTC()
	players := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	worker, err := notification.New(notification.Config{
		Msgs: i18n.NewStore(catalog), Players: players, Links: players, Inbox: postgres.NewInboxStore(pool), Sender: sender,
		Deps:       notification.Deps{Cities: postgres.NewCityRepository(pool), LiteracyStepBPS: 1},
		SendBudget: 10 * time.Second, ReceiptMargin: 2 * time.Second,
		Groups: postgres.NewCityGroupRepository(pool), Now: func() time.Time { return clock },

		Realtime: realtime, RealtimeLanguages: []string{"fa", "en"},
		SettlementVersions: infraredis.NewSettlementVersions(rdb, time.Hour),
		News:               infraredis.NewNewsQueue(rdb, time.Hour),
		NewsMergeWindow:    20 * time.Second, NewsMinGap: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The village's real outbox rows, in the order they were written.
	rows, err := pool.Raw().Query(ctx,
		`SELECT event_id::text, subject, metadata, payload FROM outbox WHERE payload->>'settlement_id' = $1 ORDER BY id`, cityID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		subject string
		env     *envelope.Envelope
	}
	var events []row
	for rows.Next() {
		var eventID, subject string
		var meta, payload []byte
		if err := rows.Scan(&eventID, &subject, &meta, &payload); err != nil {
			t.Fatal(err)
		}
		var m envelope.Metadata
		if err := json.Unmarshal(meta, &m); err != nil {
			t.Fatal(err)
		}
		m.EventID = eventID
		m.ReceivedAt = clock
		events = append(events, row{subject: subject, env: &envelope.Envelope{Metadata: m, Payload: payload}})
	}
	rows.Close()
	if len(events) < 6 {
		t.Fatalf("the village wrote only %d events to the outbox", len(events))
	}
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[strings.TrimSuffix(strings.TrimPrefix(e.subject, "game.event.settlement."), ".v1")] = true
	}
	for _, want := range []string{"build_started", "built", "research_started", "knowledge_researched", "knowledge_bought", "literacy_advanced"} {
		if !kinds[want] {
			t.Errorf("the village never wrote settlement.%s", want)
		}
	}

	// Twice, as a redelivery would.
	handled := 0
	for round := 0; round < 2; round++ {
		for _, e := range events {
			for _, route := range notification.Routes() {
				if route.Subject() != e.subject || (route.Settlement == nil && route.News == nil) {
					continue
				}
				if err := worker.Handle(ctx, route, e.env); err != nil {
					t.Fatalf("%s (%s): %v", route.Durable(), e.subject, err)
				}
				if round == 0 && route.Settlement != nil {
					handled++
				}
			}
		}
	}

	channel := centrifugo.SettlementChannel(cityID)
	var pubs []map[string]any
	if cfURL != "" {
		pubs = centrifugoHistory(t, cfURL, cfKey, channel)
	} else {
		rec := realtime.(*recordingPublisher)
		seen := map[string]bool{}
		for i, p := range rec.pubs {
			if !seen[rec.keys[i]] {
				seen[rec.keys[i]] = true
				pubs = append(pubs, p)
			}
		}
	}
	// Centrifugo answers history newest first; put it in version order.
	sort.SliceStable(pubs, func(i, j int) bool { return pubs[i]["seq"].(float64) < pubs[j]["seq"].(float64) })
	if len(pubs) != handled {
		t.Fatalf("the channel holds %d publications for %d events (a redelivery must not publish twice)", len(pubs), handled)
	}
	prev := 0.0
	types := map[string]bool{}
	for _, p := range pubs {
		v := p["seq"].(float64)
		if v != prev+1 && prev != 0 {
			t.Errorf("versions are not consecutive: %v after %v", v, prev)
		}
		prev = v
		if p["settlement_id"] != cityID || p["type"] == nil || p["at"] == nil {
			t.Errorf("malformed publication %v", p)
		}
		types[p["type"].(string)] = true
	}
	for _, want := range []string{"build_started", "build_finished", "research_started", "research_finished", "knowledge_bought", "literacy_changed"} {
		if !types[want] {
			t.Errorf("no %q publication on the settlement channel; got %v", want, types)
		}
	}

	// The last building event names the version the layout has now, for each
	// kind of viewer: computed by the handler in the transaction that changed
	// the buildings, with the code the layout endpoint uses.
	var lastLayout map[string]any
	for _, p := range pubs {
		if lv, ok := p["layout_version"].(map[string]any); ok {
			lastLayout = lv
		}
	}
	if lastLayout == nil {
		t.Fatal("no building publication carried a layout version")
	}
	snap := loadTestContent(t)
	var cityRow application.FoundedSettlement
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, name, tier FROM cities WHERE id = $1::uuid`, cityID).Scan(&cityRow.CityID, &cityRow.Name, &cityRow.Tier); err != nil {
		t.Fatal(err)
	}
	stored, err := postgres.NewSettlementBuildingReader(pool).List(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	now := application.LayoutVersionsOf(cityID, cityRow.Tier, cityRow.Name, wsettle.GridLotsForTier(cityRow.Tier, 5), stored,
		func(code string, rotated bool) (int, int) {
			if d, ok := snap.SettlementBuildingDef(code); ok {
				def := d.Def()
				if rotated {
					def = def.Rotate()
				}
				return def.FootprintW, def.FootprintH
			}
			return 1, 1
		})
	if lastLayout["head"] != now.Head || lastLayout["member"] != now.Member || lastLayout["public"] != now.Public {
		t.Errorf("the last event says the layout is %v, the buildings now make it %+v", lastLayout, now)
	}

	// The news: nothing before the window, one merged post after it, and a
	// redelivery of the whole history brings nothing back.
	worker.FlushVillageNews(ctx)
	if len(sender.notices) != 0 {
		t.Fatalf("posted before the merge window: %d", len(sender.notices))
	}
	clock = clock.Add(time.Minute)
	worker.FlushVillageNews(ctx)
	if len(sender.notices) != 1 {
		t.Fatalf("posted %d times, want one merged post", len(sender.notices))
	}
	post := sender.notices[0]
	if !post.Announcement || !post.Keyboard || post.Response.Keyboard == nil {
		t.Errorf("the post is not a group announcement with a button: %+v", post)
	}
	if !strings.Contains(post.Response.Text, "\n") {
		t.Errorf("a burst must read as one list:\n%s", post.Response.Text)
	}
	t.Logf("village news post:\n%s", post.Response.Text)
	for _, e := range events {
		for _, route := range notification.Routes() {
			if route.Subject() == e.subject && route.News != nil {
				_ = worker.Handle(ctx, route, e.env)
			}
		}
	}
	clock = clock.Add(5 * time.Minute)
	worker.FlushVillageNews(ctx)
	if len(sender.notices) != 1 {
		t.Errorf("a redelivered history was posted again: %d posts", len(sender.notices))
	}
}

// Versions are decided once per event, whoever asks and however often, and a
// crowd of replicas stamping at once never hands out a number twice.
func TestSettlementVersionsAreIdempotentAndUnique(t *testing.T) {
	rdb := requireRedis(t)
	ctx := testCtx(t)
	v := infraredis.NewSettlementVersions(rdb, time.Minute)
	village := newUUID(t)

	first, err := v.Assign(ctx, village, "e1")
	if err != nil || first <= 0 {
		t.Fatalf("Assign: %d %v", first, err)
	}
	if again, _ := v.Assign(ctx, village, "e1"); again != first {
		t.Errorf("the same event got %d then %d", first, again)
	}
	if cur, _ := v.Current(ctx, village); cur != first {
		t.Errorf("Current = %d, want %d", cur, first)
	}

	const n = 40
	var mu sync.Mutex
	got := map[int64]int{}
	var wg sync.WaitGroup
	var errs atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Two replicas racing for the same event id, several events.
			for _, id := range []string{"c" + string(rune('a'+i%20)), "c" + string(rune('a'+i%20))} {
				ver, err := v.Assign(ctx, village, id)
				if err != nil {
					errs.Add(1)
					return
				}
				mu.Lock()
				got[ver]++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if errs.Load() != 0 {
		t.Fatal("stamping failed")
	}
	if len(got) != 20 {
		t.Errorf("20 distinct events produced %d distinct versions", len(got))
	}
	lo, hi := int64(1<<62), int64(0)
	for ver := range got {
		lo, hi = min(lo, ver), max(hi, ver)
	}
	if hi-lo != 19 || lo != first+1 {
		t.Errorf("versions run %d..%d after %d, want consecutive", lo, hi, first)
	}

	// A flushed counter moves forward: it re-seeds from the clock, which is
	// ahead of a counter that has advanced less than once a millisecond.
	time.Sleep(100 * time.Millisecond)
	if err := rdb.Raw().Del(ctx, "settlement:version:{settlement:"+village+"}").Err(); err != nil {
		t.Fatal(err)
	}
	after, _ := v.Assign(ctx, village, "after-flush")
	if after <= hi {
		t.Errorf("after a flush the version went back: %d after %d", after, hi)
	}
}

// The village news queue against Redis: a burst is claimed whole by exactly
// one of many concurrent replicas, the min gap holds the next batch back, an
// event is queued once even after it was posted, and a failed post is put
// back.
func TestNewsQueueClaimsEachBatchOnce(t *testing.T) {
	rdb := requireRedis(t)
	ctx := testCtx(t)
	q := infraredis.NewNewsQueue(rdb, time.Minute)
	village := newUUID(t)
	t0 := time.Now().UTC()

	push := func(id string, at time.Time) {
		if err := q.Push(ctx, village, application.QueuedNews{EventID: id, Kind: "built", Code: "watch_hut", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	push("e1", t0)
	push("e1", t0) // a redelivery
	push("e2", t0.Add(3*time.Second))

	if got, _ := q.ClaimDue(ctx, t0.Add(5*time.Second), 20*time.Second, time.Minute, 10); claimedFor(got, village) != nil {
		t.Fatal("claimed before the merge window")
	}
	due := t0.Add(30 * time.Second)
	var mu sync.Mutex
	var winners []application.NewsBatch
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := q.ClaimDue(ctx, due, 20*time.Second, time.Minute, 1000)
			if err != nil {
				t.Error(err)
				return
			}
			if b := claimedFor(got, village); b != nil {
				mu.Lock()
				winners = append(winners, *b)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(winners) != 1 || len(winners[0].Items) != 2 {
		t.Fatalf("%d replicas claimed the batch (%+v), want exactly one holding both items", len(winners), winners)
	}

	// The gap: a new item is due by the window but not by the gap.
	push("e3", due)
	if got, _ := q.ClaimDue(ctx, due.Add(25*time.Second), 20*time.Second, time.Minute, 10); claimedFor(got, village) != nil {
		t.Fatal("claimed inside the min gap")
	}
	// A posted event's redelivery is not queued again.
	push("e1", due)
	if err := q.Requeue(ctx, winners[0]); err != nil {
		t.Fatal(err)
	}
	got, _ := q.ClaimDue(ctx, due.Add(25*time.Second), 20*time.Second, time.Minute, 10)
	b := claimedFor(got, village)
	if b == nil || len(b.Items) != 3 {
		t.Fatalf("after a requeue the batch is %+v, want the two put back and e3, released from the gap", b)
	}
}

func claimedFor(batches []application.NewsBatch, village string) *application.NewsBatch {
	for i := range batches {
		if batches[i].SettlementID == village {
			return &batches[i]
		}
	}
	return nil
}
