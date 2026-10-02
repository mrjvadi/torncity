package statesync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// memStore is the Store contract in memory: the model the Postgres store is
// checked against in the integration harness. State is what a test sets.
type memStore struct {
	mu      sync.Mutex
	state   map[string][]Entity // player -> current entities
	held    map[string]map[Key]Held
	data    map[string]map[Key]json.RawMessage
	log     map[string][]Record
	keys    map[string]map[string]bool
	minPTS  map[string]int64
	failNow bool
}

func newMem() *memStore {
	return &memStore{state: map[string][]Entity{}, held: map[string]map[Key]Held{}, data: map[string]map[Key]json.RawMessage{},
		log: map[string][]Record{}, keys: map[string]map[string]bool{}, minPTS: map[string]int64{}}
}

func (m *memStore) max(p string) int64 {
	l := m.log[p]
	n := m.minPTS[p] - 1
	if n < 0 {
		n = 0
	}
	if len(l) > 0 && l[len(l)-1].PTS > n {
		n = l[len(l)-1].PTS
	}
	return n
}

func (m *memStore) Project(_ context.Context, req ProjectRequest) (Projection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNow {
		return Projection{}, errors.New("down")
	}
	if m.held[req.PlayerID] == nil {
		m.held[req.PlayerID], m.data[req.PlayerID], m.keys[req.PlayerID] = map[Key]Held{}, map[Key]json.RawMessage{}, map[string]bool{}
	}
	var out Projection
	next := m.max(req.PlayerID)
	for _, c := range Plan(m.state[req.PlayerID], m.held[req.PlayerID], req.Kinds) {
		key := CauseKey(req.Source, c.Kind, c.ID)
		if m.keys[req.PlayerID][key] {
			continue
		}
		m.keys[req.PlayerID][key] = true
		next++
		r := Record{PTS: next, Type: TypeOf(c.Kind, c.Op), Entity: c.Kind, ID: c.ID, V: c.V, Op: c.Op, Data: c.Data, At: req.At, Cause: req.Cause}
		m.log[req.PlayerID] = append(m.log[req.PlayerID], r)
		m.held[req.PlayerID][Key{c.Kind, c.ID}] = Held{V: c.V, Hash: c.Hash, Deleted: c.Op == OpDel}
		m.data[req.PlayerID][Key{c.Kind, c.ID}] = c.Data
		out.Records = append(out.Records, r)
	}
	out.MaxPTS = next
	return out, nil
}

func (m *memStore) BySource(_ context.Context, p, source string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.log[p] {
		if m.keys[p][CauseKey(source, r.Entity, r.ID)] && r.PTS > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) ByCause(_ context.Context, p, cause string, _ int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.log[p] {
		if r.Cause == cause {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memStore) Snapshot(_ context.Context, p string, kinds KindSet) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{PTS: m.max(p), Epoch: "1", Entities: map[string]map[string]SnapshotEntity{}}
	for k, h := range m.held[p] {
		if h.Deleted || !kinds.Has(k.Kind) {
			continue
		}
		if s.Entities[k.Kind] == nil {
			s.Entities[k.Kind] = map[string]SnapshotEntity{}
		}
		s.Entities[k.Kind][k.ID] = SnapshotEntity{V: h.V, D: m.data[p][k]}
	}
	return s, nil
}

func (m *memStore) Since(_ context.Context, p string, since int64, limit int) (Log, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := Log{MinPTS: max(m.minPTS[p], 1), MaxPTS: m.max(p), Epoch: "1"}
	for _, r := range m.log[p] {
		if r.PTS > since && len(l.Records) < limit {
			l.Records = append(l.Records, r)
		}
	}
	return l, nil
}

func (m *memStore) Trim(context.Context, TrimPolicy, string, time.Time) (TrimResult, error) {
	return TrimResult{}, nil
}
func (m *memStore) Audience(context.Context, string, int) ([]string, error) { return nil, nil }

type pubCall struct {
	channel string
	pub     Publication
}

type memPub struct {
	mu    sync.Mutex
	calls []pubCall
}

func (p *memPub) Publish(_ context.Context, channel string, data any, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, pubCall{channel, data.(Publication)})
	return nil
}

const pid = "11111111-1111-4111-8111-111111111111"

func newSvc() (*Service, *memStore, *memPub) {
	st, pub := newMem(), &memPub{}
	return &Service{Store: st, Pub: pub, Cfg: Config{Enabled: true, Epoch: "1", ResetThreshold: 5, PullLimit: 3,
		PushMaxRecords: 2, CommandWait: time.Second}, Metrics: NewMetrics()}, st, pub
}

func TestUpdatesPagesAndResets(t *testing.T) {
	svc, st, _ := newSvc()
	ctx := context.Background()
	for i := range 4 {
		st.state[pid] = []Entity{ent(KindWallet, "SUP", `{"cash":`+string(rune('0'+i))+`}`)}
		if _, err := svc.Project(ctx, pid, RefreshSource(string(rune('a'+i))), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := svc.Updates(ctx, pid, 0, "", 0)
	if d.Reset || len(d.Updates) != 3 || !d.More || d.PTS != 4 {
		t.Fatalf("first page = %+v", d)
	}
	d, _ = svc.Updates(ctx, pid, 3, "1", 0)
	if d.Reset || len(d.Updates) != 1 || d.More {
		t.Fatalf("second page = %+v", d)
	}
	if d, _ = svc.Updates(ctx, pid, 4, "", 0); d.Reset || len(d.Updates) != 0 {
		t.Fatalf("an up-to-date pull = %+v", d)
	}
	if d, _ = svc.Updates(ctx, pid, 9, "", 0); !d.Reset || d.Reason != ResetAhead {
		t.Fatalf("a cursor beyond the log = %+v", d)
	}
	if d, _ = svc.Updates(ctx, pid, 0, "7", 0); !d.Reset || d.Reason != ResetEpoch {
		t.Fatalf("another epoch = %+v", d)
	}
	st.minPTS[pid] = 3
	st.log[pid] = st.log[pid][2:]
	if d, _ = svc.Updates(ctx, pid, 1, "", 0); !d.Reset || d.Reason != ResetTooLong {
		t.Fatalf("a trimmed cursor = %+v", d)
	}
	if d, _ = svc.Updates(ctx, pid, 2, "", 0); d.Reset || len(d.Updates) != 2 {
		t.Fatalf("the floor itself = %+v", d)
	}
}

func TestPublicationTooLongIsAPoke(t *testing.T) {
	svc, st, pub := newSvc()
	st.state[pid] = []Entity{ent(KindWallet, "SUP", `{}`), ent(KindWallet, "NIL", `{}`), ent(KindSkill, "x", `{}`)}
	if _, err := svc.Project(context.Background(), pid, "s", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(pub.calls) != 1 || pub.calls[0].pub.Type != PublicationTooLong || pub.calls[0].pub.From != 1 || pub.calls[0].pub.To != 3 ||
		len(pub.calls[0].pub.Updates) != 0 || pub.calls[0].channel != "player:"+pid {
		t.Fatalf("publications = %+v", pub.calls)
	}
}

func TestRedeliveredEventAppendsNothingAndResends(t *testing.T) {
	svc, st, pub := newSvc()
	st.state[pid] = []Entity{ent(KindWallet, "SUP", `{"cash":1}`)}
	env := &envelope.Envelope{Metadata: envelope.Metadata{RequestID: "r1", EventID: "e1", PlayerID: pid}, Payload: json.RawMessage(`{}`)}
	if err := svc.HandleEvent(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleEvent(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if n := len(st.log[pid]); n != 1 {
		t.Fatalf("a redelivery appended: %d records", n)
	}
	if len(pub.calls) != 2 || pub.calls[1].pub.From != 1 || pub.calls[1].pub.To != 1 {
		t.Fatalf("the redelivery did not re-send the event's records: %+v", pub.calls)
	}
	if st.log[pid][0].Cause != "r1" {
		t.Fatalf("cause = %q", st.log[pid][0].Cause)
	}
}

func TestForCommandFindsTheProjectorsRecords(t *testing.T) {
	svc, st, _ := newSvc()
	st.state[pid] = []Entity{ent(KindWallet, "SUP", `{"cash":1}`)}
	env := &envelope.Envelope{Metadata: envelope.Metadata{RequestID: "r9", EventID: "e9", PlayerID: pid}}
	_ = svc.HandleEvent(context.Background(), env)
	u := svc.ForCommand(context.Background(), pid, "r9")
	if u == nil || len(u.Records) != 1 || u.Records[0].Cause != "r9" {
		t.Fatalf("ForCommand = %+v", u)
	}
	st.failNow = true
	if u := svc.ForCommand(context.Background(), pid, "r10"); u != nil {
		t.Fatalf("a failed projection still answered %+v", u)
	}
}

func TestHandleResponseSkipsClientCommands(t *testing.T) {
	svc, st, _ := newSvc()
	st.state[pid] = []Entity{ent(KindWallet, "SUP", `{"cash":1}`)}
	svc.HandleResponse(context.Background(), envelope.Metadata{RequestID: "r", PlayerID: pid, ChatType: envelope.ChatTypeClient})
	if len(st.log[pid]) != 0 {
		t.Fatal("a client command was projected twice")
	}
	svc.HandleResponse(context.Background(), envelope.Metadata{RequestID: "r", PlayerID: pid, ChatType: "private"})
	if len(st.log[pid]) != 1 || !strings.HasPrefix(CauseKey(RequestSource("r"), "", ""), "req:r") {
		t.Fatal("a Telegram command was not projected")
	}
}

func TestOldEventsAreAcknowledgedWithoutWork(t *testing.T) {
	svc, st, _ := newSvc()
	svc.Cfg.MaxEventAge = time.Minute
	st.state[pid] = []Entity{ent(KindWallet, "SUP", `{}`)}
	env := &envelope.Envelope{Metadata: envelope.Metadata{RequestID: "r", PlayerID: pid, ReceivedAt: time.Now().Add(-time.Hour)}}
	if err := svc.HandleEvent(context.Background(), env); err != nil || len(st.log[pid]) != 0 {
		t.Fatalf("an hour-old event was projected (err %v)", err)
	}
}
