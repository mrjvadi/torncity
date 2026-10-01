package notification

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

const villageID = "11111111-0000-4000-8000-0000000000aa"

// memVersions is SettlementVersions in memory, with the Redis store's rule:
// one number per event, decided once.
type memVersions struct {
	mu   sync.Mutex
	next map[string]int64
	seen map[string]int64
	fail error
}

func newMemVersions() *memVersions {
	return &memVersions{next: map[string]int64{}, seen: map[string]int64{}}
}

func (m *memVersions) Assign(_ context.Context, settlement, event string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return 0, m.fail
	}
	if v, ok := m.seen[settlement+"|"+event]; ok {
		return v, nil
	}
	m.next[settlement]++
	m.seen[settlement+"|"+event] = m.next[settlement]
	return m.next[settlement], nil
}

// memPublisher records every publication.
type memPublisher struct {
	mu   sync.Mutex
	pubs []memPub
	err  error
}

type memPub struct {
	channel string
	data    map[string]any
	key     string
}

func (m *memPublisher) Publish(_ context.Context, channel string, data any, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	raw, _ := json.Marshal(data)
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	m.pubs = append(m.pubs, memPub{channel: channel, data: d, key: key})
	return nil
}

// memNews is NewsQueue in memory with the Redis queue's rules.
type memNews struct {
	mu    sync.Mutex
	items map[string][]QueuedNews
	seen  map[string]bool
	last  map[string]time.Time
}

func newMemNews() *memNews {
	return &memNews{items: map[string][]QueuedNews{}, seen: map[string]bool{}, last: map[string]time.Time{}}
}

func (m *memNews) Push(_ context.Context, id string, it QueuedNews) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen[id+"|"+it.EventID] {
		return nil
	}
	m.seen[id+"|"+it.EventID] = true
	m.items[id] = append(m.items[id], it)
	return nil
}

func (m *memNews) ClaimDue(_ context.Context, now time.Time, window, gap time.Duration, max int) ([]NewsBatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id := range m.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []NewsBatch
	for _, id := range ids {
		its := m.items[id]
		if len(its) == 0 || now.Sub(its[0].At) < window {
			continue
		}
		if last, ok := m.last[id]; ok && now.Sub(last) < gap {
			continue
		}
		m.last[id] = now
		out = append(out, NewsBatch{SettlementID: id, Items: its})
		delete(m.items, id)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}

func (m *memNews) Requeue(_ context.Context, b NewsBatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[b.SettlementID] = append(b.Items, m.items[b.SettlementID]...)
	delete(m.last, b.SettlementID)
	return nil
}

func villageRoute(t *testing.T, domain, event, name string) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == domain && r.Event == event && r.Name == name {
			return r
		}
	}
	t.Fatalf("no %s route for %s.%s", name, domain, event)
	return Route{}
}

func villageEvent2(t *testing.T, id string, at time.Time, payload map[string]any) *envelope.Envelope {
	t.Helper()
	env := arrivalEvent(t, id, at, payload)
	env.Metadata.EventID = id
	return env
}

func villageRig(t *testing.T) (*rig, *memPublisher, *memVersions, *memNews) {
	t.Helper()
	r := newRig(t, &application.Player{ID: playerID, TelegramUserID: 77, DisplayName: "Ada", Language: "en"})
	pub, ver, news := &memPublisher{}, newMemVersions(), newMemNews()
	r.w.cfg.Realtime = pub
	r.w.cfg.RealtimeLanguages = []string{"fa", "en"}
	r.w.cfg.SettlementVersions = ver
	r.w.cfg.News = news
	r.w.cfg.NewsMergeWindow = 20 * time.Second
	r.w.cfg.NewsMinGap = time.Minute
	r.w.cfg.Deps.Players = fakePlayers{player: &application.Player{ID: playerID, DisplayName: "Ada"}}
	r.w.cfg.Groups = fakeCityGroups{groups: []application.CityGroup{
		{CityID: villageID, CityCode: "korendal", CityName: "کورندال", ChatID: cityGroup, BotID: botA, Language: "fa"},
	}}
	return r, pub, ver, news
}

// A village event becomes a small typed publication with a version, and a
// redelivery is the same publication: same version, same idempotency key.
func TestSettlementPublicationIsVersionedAndIdempotent(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	route := villageRoute(t, "settlement", "build_started", "realtime")
	ev := villageEvent2(t, "ev-1", r.now, map[string]any{
		"settlement_id": villageID, "building_id": "b1", "type_code": "watch_hut", "lot_x": 2, "lot_y": 3, "rotated": false,
		"finish_at": "2026-09-23T14:00:00Z",
	})
	for i := 0; i < 2; i++ {
		if err := r.w.Handle(context.Background(), route, ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(pub.pubs) != 2 || pub.pubs[0].key != pub.pubs[1].key || pub.pubs[0].data["seq"] != pub.pubs[1].data["seq"] {
		t.Fatalf("a redelivery must repeat the same publication: %+v", pub.pubs)
	}
	got := pub.pubs[0]
	if got.channel != "settlement:"+villageID || got.data["type"] != "build_started" || got.data["settlement_id"] != villageID ||
		got.data["seq"] != 1.0 || got.data["building_id"] != "b1" || got.data["type_code"] != "watch_hut" || got.data["lot_x"] != 2.0 {
		t.Errorf("publication = %+v", got)
	}

	// The next event of the same settlement is the next version.
	done := villageRoute(t, "settlement", "built", "realtime")
	if err := r.w.Handle(context.Background(), done, villageEvent2(t, "ev-2", r.now, map[string]any{
		"settlement_id": villageID, "building_id": "b1", "type_code": "watch_hut"})); err != nil {
		t.Fatal(err)
	}
	if last := pub.pubs[len(pub.pubs)-1]; last.data["type"] != "build_finished" || last.data["seq"] != 2.0 {
		t.Errorf("second publication = %+v", last)
	}
}

// Every village event has its settlement publication kind.
func TestEachVillageEventMakesItsOwnPublication(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	cases := []struct {
		event   string
		payload map[string]any
		want    string
	}{
		{"building_demolished", map[string]any{"building_id": "b", "type_code": "watch_hut"}, "build_salvaged"},
		{"research_started", map[string]any{"research_id": "r", "code": "canal_irrigation", "finish_at": "2026-09-24T00:00:00Z"}, "research_started"},
		{"knowledge_researched", map[string]any{"code": "canal_irrigation"}, "research_finished"},
		{"knowledge_bought", map[string]any{"code": "carpentry"}, "knowledge_bought"},
		{"literacy_advanced", map[string]any{"literacy_share_bps": 2400}, "literacy_changed"},
	}
	for i, c := range cases {
		c.payload["settlement_id"] = villageID
		route := villageRoute(t, "settlement", c.event, "realtime")
		if err := r.w.Handle(context.Background(), route, villageEvent2(t, "e"+strconv.Itoa(i), r.now, c.payload)); err != nil {
			t.Fatal(err)
		}
		if got := pub.pubs[len(pub.pubs)-1].data["type"]; got != c.want {
			t.Errorf("%s made %v, want %s", c.event, got, c.want)
		}
	}
	if pub.pubs[3].data["code"] != "carpentry" || pub.pubs[4].data["literacy_share_bps"] != 2400.0 {
		t.Errorf("fields lost: %+v", pub.pubs)
	}
}

// A journey is an arrival in one settlement and a departure from another;
// moving house is the same for residence.
func TestMembersJoinAndLeave(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	const from = "22222222-0000-4000-8000-0000000000bb"
	trip := villageRoute(t, "travel", "completed", "realtime")
	if err := r.w.Handle(context.Background(), trip, villageEvent2(t, "t1", r.now, map[string]any{
		"player_id": playerID, "from_city_id": from, "to_city_id": villageID})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 2 {
		t.Fatalf("a journey made %d publications, want 2", len(pub.pubs))
	}
	byChannel := map[string]map[string]any{}
	for _, p := range pub.pubs {
		byChannel[p.channel] = p.data
	}
	if j := byChannel["settlement:"+villageID]; j["type"] != "member_joined" || j["player_id"] != playerID || j["player_name"] != "Ada" || j["via"] != "travel" {
		t.Errorf("arrival = %+v", j)
	}
	if l := byChannel["settlement:"+from]; l["type"] != "member_left" {
		t.Errorf("departure = %+v", l)
	}

	before := len(pub.pubs)
	move := villageRoute(t, "residence", "changed", "realtime")
	if err := r.w.Handle(context.Background(), move, villageEvent2(t, "m1", r.now, map[string]any{
		"player_id": playerID, "from_city_id": villageID, "to_city_id": from})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != before+2 || pub.pubs[before].data["via"] != "residence" {
		t.Errorf("moving house: %+v", pub.pubs[before:])
	}

	// Arriving from nowhere known joins; staying put publishes nothing.
	before = len(pub.pubs)
	_ = r.w.Handle(context.Background(), trip, villageEvent2(t, "t2", r.now, map[string]any{"player_id": playerID, "to_city_id": villageID}))
	_ = r.w.Handle(context.Background(), trip, villageEvent2(t, "t3", r.now, map[string]any{"player_id": playerID, "from_city_id": villageID, "to_city_id": villageID}))
	if len(pub.pubs) != before+1 {
		t.Errorf("publications = %d, want one", len(pub.pubs)-before)
	}
}

// A head office changing hands is published; another office is not.
func TestHeadChanged(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	route := villageRoute(t, "governance", "appointed", "realtime")
	payload := func(office string) map[string]any {
		return map[string]any{"player_id": playerID, "player_name": "Ada", "office": office, "place_kind": "village", "city_ids": []string{villageID}}
	}
	if err := r.w.Handle(context.Background(), route, villageEvent2(t, "g1", r.now, payload("village_head"))); err != nil {
		t.Fatal(err)
	}
	if err := r.w.Handle(context.Background(), route, villageEvent2(t, "g2", r.now, payload("village_judge"))); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 1 || pub.pubs[0].data["type"] != "head_changed" || pub.pubs[0].data["office"] != "village_head" ||
		pub.pubs[0].data["player_id"] != playerID || pub.pubs[0].data["vacated"] != false {
		t.Fatalf("publications = %+v", pub.pubs)
	}
	gone := villageRoute(t, "governance", "dismissed", "realtime")
	if err := r.w.Handle(context.Background(), gone, villageEvent2(t, "g3", r.now, payload("village_head"))); err != nil {
		t.Fatal(err)
	}
	if last := pub.pubs[len(pub.pubs)-1]; last.data["vacated"] != true {
		t.Errorf("a dismissal must say vacated: %+v", last)
	}
}

// A publish that fails is retried (the event is not acknowledged), and the
// retry carries the same version, so the hole a lost publish would leave is
// filled with the right number.
func TestFailedSettlementPublishIsRetriedWithTheSameVersion(t *testing.T) {
	r, pub, ver, _ := villageRig(t)
	route := villageRoute(t, "settlement", "knowledge_bought", "realtime")
	ev := villageEvent2(t, "kb-1", r.now, map[string]any{"settlement_id": villageID, "code": "carpentry"})
	pub.err = errors.New("centrifugo is down")
	if err := r.w.Handle(context.Background(), route, ev); err == nil {
		t.Fatal("a failed publish was acknowledged")
	}
	pub.err = nil
	if err := r.w.Handle(context.Background(), route, ev); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 1 || pub.pubs[0].data["seq"] != 1.0 || ver.next[villageID] != 1 {
		t.Errorf("publications = %+v, counter = %d", pub.pubs, ver.next[villageID])
	}
	ver.fail = errors.New("redis is down")
	if err := r.w.Handle(context.Background(), route, villageEvent2(t, "kb-2", r.now, map[string]any{"settlement_id": villageID, "code": "x"})); err == nil {
		t.Error("an event that could not be stamped was acknowledged")
	}
}

// Without the version store nothing is published: a channel with no order
// is worse than none.
func TestNoVersionsNoSettlementPublication(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	r.w.cfg.SettlementVersions = nil
	route := villageRoute(t, "settlement", "built", "realtime")
	if err := r.w.Handle(context.Background(), route, villageEvent2(t, "x", r.now, map[string]any{"settlement_id": villageID, "building_id": "b"})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 0 {
		t.Errorf("published %+v", pub.pubs)
	}
}

func newsEvent(t *testing.T, r *rig, id, event string, payload map[string]any) {
	t.Helper()
	payload["settlement_id"] = villageID
	route := villageRoute(t, "settlement", event, "news")
	if err := r.w.Handle(context.Background(), route, villageEvent2(t, id, r.now, payload)); err != nil {
		t.Fatal(err)
	}
}

// A burst of village events is one post, not one per event, and only once
// the oldest has waited out the merge window.
func TestVillageNewsBurstIsMergedIntoOnePost(t *testing.T) {
	r, _, _, _ := villageRig(t)
	newsEvent(t, r, "n1", "built", map[string]any{"building_id": "b1", "type_code": "watch_hut", "name": "نگهبانی محله"})
	r.now = r.now.Add(5 * time.Second)
	newsEvent(t, r, "n2", "knowledge_researched", map[string]any{"code": "canal_irrigation", "name": "آبیاری نهری"})
	newsEvent(t, r, "n2", "knowledge_researched", map[string]any{"code": "canal_irrigation", "name": "آبیاری نهری"}) // redelivery

	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 0 {
		t.Fatalf("posted before the merge window: %d", len(r.sender.sent))
	}
	r.now = r.now.Add(30 * time.Second)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 {
		t.Fatalf("posted %d times, want once", len(r.sender.sent))
	}
	got := r.sender.sent[0]
	text := got.notice.Response.Text
	if !got.notice.Announcement || !got.notice.Keyboard || got.meta.TelegramChatID != cityGroup || got.meta.BotID != botA || got.meta.Language != "fa" {
		t.Errorf("the post went %+v %+v", got.meta, got.notice)
	}
	if !strings.Contains(text, "کورندال") || !strings.Contains(text, "نگهبانی محله") || !strings.Contains(text, "آبیاری نهری") ||
		strings.Count(text, "آبیاری نهری") != 1 {
		t.Errorf("the merged post is %q", text)
	}
	if kb := got.notice.Response.Keyboard; kb == nil || len(kb.Rows) == 0 || kb.Rows[0][0].CallbackData != "settlement:overview" {
		t.Errorf("a mix opens the village overview: %+v", got.notice.Response.Keyboard)
	}

	// Nothing left to post, and a late redelivery of an old event does not
	// bring it back.
	newsEvent(t, r, "n1", "built", map[string]any{"building_id": "b1", "type_code": "watch_hut", "name": "نگهبانی محله"})
	r.now = r.now.Add(5 * time.Minute)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 {
		t.Errorf("a redelivered event was posted again: %d posts", len(r.sender.sent))
	}
}

// A lone event is a sentence with the button of the screen it is about, and
// the min gap holds the next post back.
func TestVillageNewsLoneEventAndMinGap(t *testing.T) {
	r, _, _, _ := villageRig(t)
	newsEvent(t, r, "a", "built", map[string]any{"building_id": "b", "type_code": "watch_hut", "name": "نگهبانی محله"})
	r.now = r.now.Add(25 * time.Second)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 {
		t.Fatal("no post")
	}
	post := r.sender.sent[0].notice.Response
	if strings.Contains(post.Text, "\n") || !strings.Contains(post.Text, "نگهبانی محله") ||
		post.Keyboard.Rows[0][0].CallbackData != "settlement:build.progress" {
		t.Errorf("post = %q %+v", post.Text, post.Keyboard)
	}

	newsEvent(t, r, "b", "knowledge_bought", map[string]any{"code": "carpentry", "name": "نجاری"})
	r.now = r.now.Add(25 * time.Second) // window passed, gap (60s) not
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 {
		t.Fatalf("the min gap was ignored: %d posts", len(r.sender.sent))
	}
	r.now = r.now.Add(40 * time.Second)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 2 || r.sender.sent[1].notice.Response.Keyboard.Rows[0][0].CallbackData != "settlement:knowledge" {
		t.Fatalf("after the gap: %d posts", len(r.sender.sent))
	}
}

// A post the gateway cannot deliver is put back and tried again.
func TestVillageNewsFailedSendIsRetried(t *testing.T) {
	r, _, _, _ := villageRig(t)
	newsEvent(t, r, "a", "built", map[string]any{"building_id": "b", "type_code": "watch_hut", "name": "نگهبانی محله"})
	r.now = r.now.Add(25 * time.Second)
	r.sender.err = errors.New("no receipt")
	r.w.FlushVillageNews(context.Background())
	r.sender.err = nil
	r.w.FlushVillageNews(context.Background())
	if r.sender.delivered != 1 {
		t.Errorf("delivered %d, want exactly one after the retry", r.sender.delivered)
	}
}

// Teaching steps come every few minutes; only a milestone is news.
func TestLiteracyIsNewsOnlyAtAMilestone(t *testing.T) {
	r, _, _, _ := villageRig(t)
	r.w.cfg.Deps.LiteracyStepBPS = 1000
	step := func(id string, from, to int) {
		newsEvent(t, r, id, "literacy_advanced", map[string]any{"previous_bps": from, "literacy_share_bps": to})
	}
	step("l1", 1200, 1350) // inside the same ten points
	step("l2", 1900, 2100) // crossed 20%
	r.now = r.now.Add(25 * time.Second)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 || !strings.Contains(r.sender.sent[0].notice.Response.Text, "21") {
		t.Fatalf("posts = %+v", r.sender.sent)
	}
}
