package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

var epoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const (
	actionID = "8d3f1c2a-0000-4000-8000-000000000001"
	playerID = "8d3f1c2a-0000-4000-8000-0000000000aa"
	travelID = "8d3f1c2a-0000-4000-8000-0000000000bb"
)

func travelAction(id string, finish time.Time) application.GameAction {
	return application.GameAction{
		ID:            id,
		ActionType:    ActionTypeTravel,
		ActorType:     "player",
		ActorID:       playerID,
		ReferenceType: "travel",
		ReferenceID:   travelID,
		Payload:       []byte(`{"travel_id":"` + travelID + `","player_id":"` + playerID + `"}`),
		StartedAt:     finish.Add(-time.Hour),
		FinishAt:      finish,
	}
}

type harness struct {
	s      *Scheduler
	store  *store
	broker *broker
	clock  *clock
	logs   *syncBuffer
}

func newHarness(t *testing.T, withReaper bool, actions ...application.GameAction) *harness {
	t.Helper()

	h := &harness{
		store:  newStore(actions...),
		broker: &broker{},
		clock:  &clock{now: epoch},
		logs:   &syncBuffer{},
	}
	opts := Options{
		Actions:         h.store,
		Publisher:       h.broker,
		Logger:          testLogger(h.logs),
		TickInterval:    time.Millisecond,
		BatchSize:       10,
		ShutdownTimeout: 5 * time.Second,
		NoisyAttempts:   3,
		InstanceID:      "scheduler-test",
		DefaultLanguage: "fa",
		ClaimTimeout:    2 * time.Minute,
	}
	if withReaper {
		opts.Reaper = h.store
	}

	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.now = h.clock.Now
	h.s = s
	return h
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	valid := func() Options {
		return Options{
			Actions:         newStore(),
			Publisher:       &broker{},
			Logger:          testLogger(&syncBuffer{}),
			TickInterval:    time.Second,
			BatchSize:       1,
			ShutdownTimeout: time.Second,
			DefaultLanguage: "fa",
			ClaimTimeout:    time.Minute,
		}
	}
	tests := []struct {
		name   string
		break_ func(*Options)
		want   error
	}{
		{"no repository", func(o *Options) { o.Actions = nil }, ErrNoActions},
		{"no publisher", func(o *Options) { o.Publisher = nil }, ErrNoPublisher},
		{"no logger", func(o *Options) { o.Logger = nil }, ErrNoLogger},
		{"no tick", func(o *Options) { o.TickInterval = 0 }, ErrNoTickInterval},
		{"no batch", func(o *Options) { o.BatchSize = 0 }, ErrNoBatchSize},
		{"no batch budget", func(o *Options) { o.ShutdownTimeout = 0 }, ErrNoBatchBudget},
		{"no language", func(o *Options) { o.DefaultLanguage = "" }, ErrNoLanguage},
		{"lease inside the batch budget", func(o *Options) { o.ClaimTimeout = o.ShutdownTimeout }, ErrNoClaimTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := valid()
			tc.break_(&o)
			if _, err := New(o); !errors.Is(err, tc.want) {
				t.Fatalf("New() = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := New(valid()); err != nil {
		t.Fatalf("New(valid) = %v", err)
	}
}

// TestTickClaimsPublishesAndCompletes is the happy path: a due travel becomes
// one command on the arrival subject and the row is closed.
func TestTickClaimsPublishesAndCompletes(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch.Add(-time.Second)))

	n, err := h.s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Fatalf("dispatched %d, want 1", n)
	}

	msgs := h.broker.messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if want := subjects.Command("travel", "arrive"); msgs[0].subject != want {
		t.Errorf("subject %q, want %q", msgs[0].subject, want)
	}

	completed, failed := h.store.snapshot()
	if len(completed) != 1 || completed[0] != actionID {
		t.Errorf("completed %v, want [%s]", completed, actionID)
	}
	if len(failed) != 0 {
		t.Errorf("failed %v, want none", failed)
	}

	var cmd Command
	if err := msgs[0].env.Decode(&cmd); err != nil {
		t.Fatalf("payload does not decode: %v", err)
	}
	if cmd.ActionID != actionID || cmd.ActorID != playerID || cmd.ReferenceID != travelID {
		t.Errorf("command identity %+v does not carry the row", cmd)
	}
	if !cmd.ScheduledFor.Equal(epoch.Add(-time.Second)) {
		t.Errorf("scheduled_for %s, want the row's finish_at", cmd.ScheduledFor)
	}
	if !strings.Contains(string(cmd.Payload), travelID) {
		t.Errorf("the row's jsonb was not forwarded: %s", cmd.Payload)
	}

	// Not yet due: a second tick finds nothing and publishes nothing.
	if n, _ := h.s.Tick(context.Background()); n != 0 {
		t.Errorf("second tick dispatched %d, want 0", n)
	}
}

func TestTickLeavesFutureActionsAlone(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch.Add(time.Minute)))

	if n, err := h.s.Tick(context.Background()); err != nil || n != 0 {
		t.Fatalf("Tick = %d, %v; want 0, nil", n, err)
	}
	if got := h.store.status(actionID); got != "scheduled" {
		t.Errorf("status %q, want scheduled", got)
	}
}

// TestEnvelopeValidates checks the metadata a clock-driven command carries:
// it must pass the same validation every consumer applies, and every field a
// Telegram update would have supplied must say "no Telegram here".
func TestEnvelopeValidates(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch))

	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	msgs := h.broker.messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d, want 1", len(msgs))
	}

	// Round-trip through json, as the consumer receives it.
	raw, err := json.Marshal(msgs[0].env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env envelope.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m := env.Metadata
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata does not validate: %v", err)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"command", m.Command, "travel.arrive"},
		{"action", m.Action, "arrive"},
		{"update_type", m.UpdateType, updateTypeScheduled},
		{"player_id", m.PlayerID, playerID},
		{"idempotency_key", m.IdempotencyKey, actionID},
		{"language", m.Language, "fa"},
		{"gateway_instance_id", m.GatewayInstanceID, "scheduler-test"},
		{"bot_id", m.BotID, ""},
		{"telegram_user_id", m.TelegramUserID, int64(0)},
		{"telegram_chat_id", m.TelegramChatID, int64(0)},
		{"schema_version", m.SchemaVersion, envelope.SchemaVersion},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if m.RequestID == "" || m.TraceID == "" {
		t.Error("request and trace ids must be minted")
	}
	if m.CallbackQueryID != nil || m.ReplyToMessageID != nil || m.TelegramThreadID != nil {
		t.Error("a scheduled command has nothing to thread under, reply to or answer")
	}
}

// TestSystemActionHasNoPlayer: only a player actor fills PlayerID.
func TestSystemActionHasNoPlayer(t *testing.T) {
	a := travelAction(actionID, epoch)
	a.ActorType = "system"
	h := newHarness(t, false, a)

	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := h.broker.messages()[0].env.Metadata.PlayerID; got != "" {
		t.Errorf("player_id %q on a system action", got)
	}
}

// TestDedupIDIsTheActionID: the broker must see the same id for every
// dispatch of one row, and a different request id each time.
func TestDedupIDIsTheActionID(t *testing.T) {
	h := newHarness(t, true, travelAction(actionID, epoch))

	// First dispatch reaches the broker but the process "dies" before
	// Complete: simulate by publishing, then reopening the row.
	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	h.store.mu.Lock()
	h.store.rows[actionID].action.Status = "scheduled"
	h.store.mu.Unlock()
	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}

	msgs := h.broker.messages()
	if len(msgs) != 2 {
		t.Fatalf("published %d, want 2", len(msgs))
	}
	for i, m := range msgs {
		if m.dedupID != actionID {
			t.Errorf("dispatch %d: dedup id %q, want the action id %q", i, m.dedupID, actionID)
		}
	}
	if msgs[0].env.Metadata.RequestID == msgs[1].env.Metadata.RequestID {
		t.Error("two dispatches share a request id; a trace could not tell them apart")
	}
}

// TestUnknownActionTypeFailsLoudly: a row this build cannot route is failed,
// logged at error, and never published.
func TestUnknownActionTypeFailsLoudly(t *testing.T) {
	a := travelAction(actionID, epoch)
	a.ActionType = "salary"
	h := newHarness(t, false, a)

	n, err := h.s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("dispatched %d, want 0", n)
	}
	if msgs := h.broker.messages(); len(msgs) != 0 {
		t.Fatalf("published %d messages for an unroutable action", len(msgs))
	}

	completed, failed := h.store.snapshot()
	if len(completed) != 0 {
		t.Errorf("completed %v, want none", completed)
	}
	reason, ok := failed[actionID]
	if !ok {
		t.Fatal("the unroutable action was not failed")
	}
	if !strings.Contains(reason, "salary") {
		t.Errorf("reason %q does not name the action type", reason)
	}
	if logs := h.logs.String(); !strings.Contains(logs, `"level":"ERROR"`) || !strings.Contains(logs, "no route for this action type") {
		t.Errorf("the failure was not logged at error:\n%s", logs)
	}
}

func TestInvalidPayloadFails(t *testing.T) {
	a := travelAction(actionID, epoch)
	a.Payload = []byte(`{not json`)
	h := newHarness(t, false, a)

	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(h.broker.messages()) != 0 {
		t.Fatal("an invalid payload was published")
	}
	if _, failed := h.store.snapshot(); failed[actionID] == "" {
		t.Error("an invalid payload was not failed")
	}
}

// TestPublishErrorIsNotAFailure: a broker error leaves the row neither
// completed nor failed.
func TestPublishErrorIsNotAFailure(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch))
	h.broker.setErr(errors.New("nats: connection closed"))

	n, err := h.s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick returned %v; a publish error is per action, not per tick", err)
	}
	if n != 0 {
		t.Errorf("dispatched %d, want 0", n)
	}
	completed, failed := h.store.snapshot()
	if len(completed) != 0 || len(failed) != 0 {
		t.Fatalf("completed %v failed %v; a transient broker error must write nothing", completed, failed)
	}
	if got := h.store.status(actionID); got != "running" {
		t.Errorf("status %q, want it still claimed (running)", got)
	}
	if logs := h.logs.String(); !strings.Contains(logs, "cannot publish a due action") {
		t.Errorf("the publish failure was not logged:\n%s", logs)
	}
}

func TestPublishErrorEscalatesAtNoisyAttempts(t *testing.T) {
	a := travelAction(actionID, epoch)
	a.RetryCount = 3 // NoisyAttempts in the harness
	h := newHarness(t, false, a)
	h.broker.setErr(errors.New("nats: timeout"))

	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if logs := h.logs.String(); !strings.Contains(logs, `"level":"ERROR","msg":"cannot publish a due action"`) {
		t.Errorf("a row at noisy_attempts was not logged at error:\n%s", logs)
	}
}

func TestDueErrorIsReported(t *testing.T) {
	h := newHarness(t, false)
	h.store.dueErr = errors.New("postgres: connection refused")

	if _, err := h.s.Tick(context.Background()); err == nil {
		t.Fatal("Tick swallowed a claim failure")
	}
}

// TestReaperReturnsStaleClaims: a row stranded in running by a failed
// publish is handed back after the lease and then dispatched.
func TestReaperReturnsStaleClaims(t *testing.T) {
	h := newHarness(t, true, travelAction(actionID, epoch))
	h.broker.setErr(errors.New("nats: connection closed"))

	if _, err := h.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := h.store.status(actionID); got != "running" {
		t.Fatalf("status %q, want running", got)
	}

	// Inside the lease: nothing is reclaimed.
	h.clock.Advance(time.Minute)
	if n, err := h.s.Reap(context.Background()); err != nil || n != 0 {
		t.Fatalf("Reap inside the lease = %d, %v; want 0, nil", n, err)
	}
	if got := h.store.status(actionID); got != "running" {
		t.Fatalf("status %q, want running: a live claim was reclaimed", got)
	}

	// Past the lease: reclaimed, then dispatched once the broker is back.
	h.clock.Advance(2 * time.Minute)
	h.broker.setErr(nil)
	if n, err := h.s.Reap(context.Background()); err != nil || n != 1 {
		t.Fatalf("Reap past the lease = %d, %v; want 1, nil", n, err)
	}
	if got := h.store.status(actionID); got != "scheduled" {
		t.Fatalf("status %q, want scheduled", got)
	}
	if n, err := h.s.Tick(context.Background()); err != nil || n != 1 {
		t.Fatalf("Tick after reap = %d, %v; want 1, nil", n, err)
	}
	if got := h.store.status(actionID); got != "completed" {
		t.Errorf("status %q, want completed", got)
	}
	if logs := h.logs.String(); !strings.Contains(logs, "stale claims returned to the schedule") {
		t.Errorf("the reclaim was not logged:\n%s", logs)
	}
}

func TestReapUsesTheLeaseAndTheBatchSize(t *testing.T) {
	var got struct {
		cutoff time.Time
		limit  int
	}
	h := newHarness(t, false)
	h.s.reaper = reaperFunc(func(_ context.Context, before time.Time, limit int) (int, error) {
		got.cutoff, got.limit = before, limit
		return 0, nil
	})

	if _, err := h.s.Reap(context.Background()); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if want := epoch.Add(-2 * time.Minute); !got.cutoff.Equal(want) {
		t.Errorf("cutoff %s, want now - claim_timeout = %s", got.cutoff, want)
	}
	if got.limit != 10 {
		t.Errorf("limit %d, want the batch size 10", got.limit)
	}
}

func TestReapWithoutReaperIsANoop(t *testing.T) {
	h := newHarness(t, false)
	if n, err := h.s.Reap(context.Background()); n != 0 || err != nil {
		t.Fatalf("Reap = %d, %v; want 0, nil", n, err)
	}
}

type reaperFunc func(context.Context, time.Time, int) (int, error)

func (f reaperFunc) ReclaimStale(ctx context.Context, before time.Time, limit int) (int, error) {
	return f(ctx, before, limit)
}

// TestRunReapsBeforeClaiming: the loop runs the reaper on each tick, so a
// stranded row is dispatched without anyone calling Reap by hand.
func TestRunReapsBeforeClaiming(t *testing.T) {
	a := travelAction(actionID, epoch)
	a.Status = "running"
	h := newHarness(t, true, a) // claimedAt is the zero time: long stale

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for h.store.status(actionID) != "completed" {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("the stranded action was never dispatched; status %q", h.store.status(actionID))
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestRunDrainsTheInFlightBatch: a signal that arrives mid-batch stops the
// claiming but not the batch, which finishes on a context the signal did not
// cancel.
func TestRunDrainsTheInFlightBatch(t *testing.T) {
	h := newHarness(t, false, travelAction(actionID, epoch))
	h.broker.started = make(chan struct{})
	h.broker.release = make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx) }()

	select {
	case <-h.broker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the batch never reached the broker")
	}

	// The signal arrives while the publish is in flight.
	cancel()
	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) with a batch still in flight", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(h.broker.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the batch finished")
	}

	msgs := h.broker.messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d, want 1", len(msgs))
	}
	if msgs[0].ctxErr != nil {
		t.Errorf("the in-flight publish saw a cancelled context: %v", msgs[0].ctxErr)
	}
	completed, _ := h.store.snapshot()
	if len(completed) != 1 {
		t.Fatalf("completed %v; the claimed row was abandoned on shutdown", completed)
	}
	for _, err := range h.store.ctxErrs {
		if err != nil {
			t.Errorf("a write in the drained batch saw a cancelled context: %v", err)
		}
	}

	// And nothing further is claimed after the signal.
	h.store.mu.Lock()
	calls := h.store.dueCalls
	h.store.mu.Unlock()
	if calls != 1 {
		t.Errorf("Due called %d times; claiming must stop at the signal", calls)
	}
}

func TestRunStopsWhenIdle(t *testing.T) {
	h := newHarness(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.s.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestBatchSizeBoundsAClaim(t *testing.T) {
	var actions []application.GameAction
	for i := 0; i < 25; i++ {
		id := "8d3f1c2a-0000-4000-8000-1000000000" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		actions = append(actions, travelAction(id, epoch.Add(-time.Duration(i)*time.Second)))
	}
	h := newHarness(t, false, actions...)

	for _, want := range []int{10, 10, 5, 0} {
		n, err := h.s.Tick(context.Background())
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if n != want {
			t.Fatalf("dispatched %d, want %d", n, want)
		}
	}
}
