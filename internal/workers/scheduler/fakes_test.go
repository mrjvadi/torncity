package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// store is an in-memory schedule that behaves like the postgres adapter in the
// ways the scheduler depends on: Due claims by moving a row to running, only
// scheduled rows are claimable, and Complete/Fail close only an open row. It
// also implements ClaimReaper, recording the claim time the real table does
// not have yet, so the reaper's contract can be exercised end to end.
type store struct {
	mu        sync.Mutex
	rows      map[string]*storedAction
	dueErr    error
	dueCalls  int
	completed []string
	failed    map[string]string

	// ctxErrs records the context state each write saw, so a test can tell
	// whether a batch ran on a cancelled context.
	ctxErrs []error
}

type storedAction struct {
	action    application.GameAction
	claimedAt time.Time
}

func newStore(actions ...application.GameAction) *store {
	s := &store{rows: map[string]*storedAction{}, failed: map[string]string{}}
	for _, a := range actions {
		if a.Status == "" {
			a.Status = "scheduled"
		}
		s.rows[a.ID] = &storedAction{action: a}
	}
	return s
}

func (s *store) Schedule(_ context.Context, a application.GameAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Status = "scheduled"
	s.rows[a.ID] = &storedAction{action: a}
	return nil
}

func (s *store) Due(ctx context.Context, now time.Time, limit int) ([]application.GameAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dueCalls++
	if s.dueErr != nil {
		return nil, s.dueErr
	}

	var due []*storedAction
	for _, r := range s.rows {
		if r.action.Status == "scheduled" && !r.action.FinishAt.After(now) {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].action.FinishAt.Before(due[j].action.FinishAt) })
	if len(due) > limit {
		due = due[:limit]
	}

	out := make([]application.GameAction, 0, len(due))
	for _, r := range due {
		r.action.Status = "running"
		r.claimedAt = now
		out = append(out, r.action)
	}
	return out, nil
}

func (s *store) Complete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	r, ok := s.rows[id]
	if !ok || (r.action.Status != "scheduled" && r.action.Status != "running") {
		return errors.New("store: no open action")
	}
	r.action.Status = "completed"
	s.completed = append(s.completed, id)
	return nil
}

func (s *store) Fail(ctx context.Context, id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	r, ok := s.rows[id]
	if !ok || (r.action.Status != "scheduled" && r.action.Status != "running") {
		return errors.New("store: no open action")
	}
	r.action.Status = "failed"
	r.action.RetryCount++
	s.failed[id] = reason
	return nil
}

func (s *store) ReclaimStale(_ context.Context, claimedBefore time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.rows {
		if n >= limit {
			break
		}
		if r.action.Status == "running" && r.claimedAt.Before(claimedBefore) {
			r.action.Status = "scheduled"
			r.action.RetryCount++
			n++
		}
	}
	return n, nil
}

func (s *store) status(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[id].action.Status
}

func (s *store) snapshot() (completed []string, failed map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	failed = make(map[string]string, len(s.failed))
	for k, v := range s.failed {
		failed[k] = v
	}
	return append([]string(nil), s.completed...), failed
}

// published is one message that reached the fake broker.
type published struct {
	subject string
	env     *envelope.Envelope
	dedupID string
	ctxErr  error
}

// broker is a fake EventPublisher. It validates the envelope the way the real
// publisher does, so an invalid envelope fails here exactly as it would there.
type broker struct {
	mu   sync.Mutex
	sent []published
	err  error

	// When started is non-nil, each publish signals it and then waits on
	// release, so a test can hold a batch in flight.
	started chan struct{}
	release chan struct{}
}

func (b *broker) Publish(ctx context.Context, subject string, env *envelope.Envelope) error {
	return b.PublishWithID(ctx, subject, env, "")
}

func (b *broker) PublishWithID(ctx context.Context, subject string, env *envelope.Envelope, dedupID string) error {
	if b.started != nil {
		b.started <- struct{}{}
		<-b.release
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if err := env.Metadata.Validate(); err != nil {
		return err
	}
	if dedupID == "" {
		dedupID = env.Metadata.RequestID
	}
	b.sent = append(b.sent, published{subject: subject, env: env, dedupID: dedupID, ctxErr: ctx.Err()})
	return nil
}

func (b *broker) setErr(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *broker) messages() []published {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]published(nil), b.sent...)
}

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// syncBuffer is a log sink safe to write from the Run goroutine and read from
// the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLogger(out *syncBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
