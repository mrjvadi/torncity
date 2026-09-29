package notification

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// --- DefaultVitals -----------------------------------------------------

type fakeStatsReader struct {
	stats *application.Stats
	err   error
}

func (f fakeStatsReader) Get(ctx context.Context, playerID string) (*application.Stats, error) {
	return f.stats, f.err
}

type fakeBalanceReader struct {
	byKind map[application.AccountKind]application.Account
	err    error
}

func (f fakeBalanceReader) AccountFor(ctx context.Context, kind application.AccountKind, ownerID string) (application.Account, error) {
	if f.err != nil {
		return application.Account{}, f.err
	}
	return f.byKind[kind], nil
}

// fakePlayerInbox implements PlayerInbox with only Summary meaningful; the
// rest of this feature never calls the others.
type fakePlayerInbox struct {
	unread int
	err    error
}

func (f fakePlayerInbox) Record(context.Context, application.InboxRecord, bool, time.Time) error {
	return nil
}
func (f fakePlayerInbox) Summary(ctx context.Context, playerID string, recent int) (application.InboxSummary, error) {
	if f.err != nil {
		return application.InboxSummary{}, f.err
	}
	return application.InboxSummary{Unread: f.unread}, nil
}
func (f fakePlayerInbox) Badge(context.Context, string) (*application.InboxBadge, error) {
	return nil, nil
}
func (f fakePlayerInbox) SaveBadge(context.Context, application.InboxBadge, time.Time) error {
	return nil
}
func (f fakePlayerInbox) DueReminders(context.Context, time.Time, time.Time, int) ([]application.InboxReminder, error) {
	return nil, nil
}
func (f fakePlayerInbox) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

func TestDefaultVitalsComposesTheSnapshot(t *testing.T) {
	dv := DefaultVitals{
		Stats: fakeStatsReader{stats: &application.Stats{
			Level: 3, XP: 420, Health: 90, MaxHealth: 100, Energy: 80, MaxEnergy: 100,
		}},
		Ledger: fakeBalanceReader{byKind: map[application.AccountKind]application.Account{
			application.AccountPlayerCash: {Balance: money.FromMinor(125000)},
			application.AccountPlayerBank: {Balance: money.FromMinor(480000)},
		}},
		Inbox: fakePlayerInbox{unread: 2},
	}
	v, err := dv.Get(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	want := RealtimeVitals{
		Level: 3, XP: 420, Health: 90, MaxHealth: 100, Energy: 80, MaxEnergy: 100,
		Cash: 125000, Bank: 480000, Unread: 2,
	}
	if v != want {
		t.Errorf("vitals = %+v, want %+v", v, want)
	}
}

// A failed inbox summary is not fatal: the rest of the snapshot still comes
// back, just with Unread at zero.
func TestDefaultVitalsSurvivesAFailedInboxSummary(t *testing.T) {
	dv := DefaultVitals{
		Stats: fakeStatsReader{stats: &application.Stats{Level: 1, MaxHealth: 100, MaxEnergy: 100}},
		Ledger: fakeBalanceReader{byKind: map[application.AccountKind]application.Account{
			application.AccountPlayerCash: {Balance: money.FromMinor(0)},
			application.AccountPlayerBank: {Balance: money.FromMinor(0)},
		}},
		Inbox: fakePlayerInbox{err: context.DeadlineExceeded},
	}
	v, err := dv.Get(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Unread != 0 {
		t.Errorf("unread = %d, want 0", v.Unread)
	}
}

// A failed stats read fails the whole snapshot: those are the numbers a
// vitals push exists for.
func TestDefaultVitalsFailsOnAStatsError(t *testing.T) {
	dv := DefaultVitals{
		Stats:  fakeStatsReader{err: context.DeadlineExceeded},
		Ledger: fakeBalanceReader{},
	}
	if _, err := dv.Get(context.Background(), "p1"); err == nil {
		t.Fatal("want an error")
	}
}

// --- Worker.publishVitals / Handle --------------------------------------

type fakeVitalsReader struct {
	calls int
	v     RealtimeVitals
	err   error
}

func (f *fakeVitalsReader) Get(ctx context.Context, playerID string) (RealtimeVitals, error) {
	f.calls++
	return f.v, f.err
}

func withVitals(r *rig, vr VitalsReader) {
	r.w.cfg.Vitals = vr
}

// A notice's event also gets the player a "vitals" publication, alongside
// the existing "notice" one, when Config.Vitals is set.
func TestNoticeAlsoPublishesVitalsWhenConfigured(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	rs := newRealtimeServer(t)
	withRealtime(r, rs)
	fv := &fakeVitalsReader{v: RealtimeVitals{Cash: 100, Bank: 200, Energy: 50, MaxEnergy: 100, Health: 90, MaxHealth: 100, XP: 10, Level: 2, Unread: 1}}
	withVitals(r, fv)

	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-vitals", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if fv.calls != 1 {
		t.Fatalf("vitals reads = %d, want 1", fv.calls)
	}
	pubs := rs.published()
	if len(pubs) != 2 {
		t.Fatalf("published %d, want 2 (notice + vitals)", len(pubs))
	}
	var sawVitals bool
	for _, p := range pubs {
		data, _ := p["data"].(map[string]any)
		if data["type"] == "vitals" {
			sawVitals = true
			if p["channel"] != "player:"+playerID {
				t.Errorf("vitals channel = %v", p["channel"])
			}
			if data["cash"] != float64(100) || data["bank"] != float64(200) || data["unread"] != float64(1) {
				t.Errorf("vitals data = %v", data)
			}
		}
	}
	if !sawVitals {
		t.Error("no vitals publication seen")
	}
}

// A burst of events for the same player within VitalsMinInterval reads and
// publishes vitals only once; after the window passes, the next one goes
// through again.
func TestVitalsAreDebouncedPerPlayer(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	rs := newRealtimeServer(t)
	withRealtime(r, rs)
	fv := &fakeVitalsReader{}
	withVitals(r, fv)
	r.w.cfg.VitalsMinInterval = 5 * time.Second
	r.w.vitals = newVitalsDebounce(5 * time.Second)
	var trailing []func()
	r.w.vitals.after = func(_ time.Duration, fn func()) { trailing = append(trailing, fn) }
	route := travelRoute(t)

	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-1", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-2", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if fv.calls != 1 {
		t.Fatalf("vitals reads = %d, want 1 (debounced)", fv.calls)
	}
	if len(trailing) != 1 {
		t.Fatalf("trailing publishes booked = %d, want 1", len(trailing))
	}
	// the window closes: the booked publish sends the burst's final numbers
	r.now = r.now.Add(5 * time.Second)
	trailing[0]()
	if fv.calls != 2 {
		t.Fatalf("vitals reads = %d, want 2 (trailing publish)", fv.calls)
	}

	r.now = r.now.Add(6 * time.Second)
	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-3", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if fv.calls != 3 {
		t.Fatalf("vitals reads = %d, want 3 (window passed)", fv.calls)
	}
}

// A vitals read failure never fails the event: the notice is still
// delivered and recorded exactly as if Vitals were not configured.
func TestVitalsFailureNeverBreaksTheNotice(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	rs := newRealtimeServer(t)
	withRealtime(r, rs)
	withVitals(r, &fakeVitalsReader{err: context.DeadlineExceeded})
	route := travelRoute(t)

	if err := r.w.Handle(context.Background(), route, arrivalEvent(t, "req-fail", r.now, nil)); err != nil {
		t.Fatalf("a vitals failure failed the notice: %v", err)
	}
	if r.sender.delivered != 1 {
		t.Fatal("the notice must still be delivered")
	}
	pubs := rs.published()
	if len(pubs) != 1 {
		t.Fatalf("published %d, want 1 (notice only, no vitals)", len(pubs))
	}
}

// With no Realtime configured, Vitals is never read, exactly as a notice is
// never published.
func TestVitalsAreNotReadWithoutRealtime(t *testing.T) {
	r := newRig(t, englishPlayer(), link(botA, 1001))
	fv := &fakeVitalsReader{}
	withVitals(r, fv)

	if err := r.w.Handle(context.Background(), travelRoute(t), arrivalEvent(t, "req-norealtime", r.now, nil)); err != nil {
		t.Fatal(err)
	}
	if fv.calls != 0 {
		t.Fatalf("vitals reads = %d, want 0", fv.calls)
	}
}
