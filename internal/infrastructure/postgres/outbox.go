package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// StatusPending and its siblings are the values outbox_status_check accepts.
// They are named rather than written inline so a typo becomes a compile error
// instead of a constraint violation discovered at runtime.
const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusFailed    = "failed"
)

// emptyJSON is what an absent payload becomes. The column is jsonb NOT NULL,
// so a nil payload has to be written as something; an empty object says
// "this event carries no fields" and stays valid JSON, whereas an empty string
// would simply be rejected by the server.
const emptyJSON = "{}"

// OutboxRepository appends events for the outbox worker to publish.
type OutboxRepository struct {
	q querier
}

var _ application.OutboxRepository = (*OutboxRepository)(nil)

// NewOutboxRepository returns a repository over the pool.
func NewOutboxRepository(p *Pool) *OutboxRepository { return &OutboxRepository{q: p.Raw()} }

const insertOutbox = `
INSERT INTO outbox (event_id, subject, metadata, payload, status, attempts, created_at)
VALUES ($1::uuid, $2, $3::jsonb, $4::jsonb, $5, 0, $6)`

// Append writes one event, in the caller's transaction, with status pending.
//
// The status is always pending and never something the caller chooses: the
// row's whole purpose is to be picked up by the publisher after this
// transaction commits, and a row inserted as published would be an event
// nobody ever sends.
func (r *OutboxRepository) Append(ctx context.Context, rec application.OutboxRecord) error {
	eventID, err := ensureID(rec.EventID)
	if err != nil {
		return err
	}

	if rec.Subject == "" {
		// A row with no destination can never be published and would sit
		// pending forever, so it is refused at the point it is written rather
		// than becoming a mystery for whoever reads the backlog later.
		return fmt.Errorf("postgres: outbox append: subject is required")
	}

	metadata, err := json.Marshal(rec.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: outbox append: marshalling metadata: %w", err)
	}

	payload, err := normalizeJSON(rec.Payload)
	if err != nil {
		return fmt.Errorf("postgres: outbox append for subject %s: %w", rec.Subject, err)
	}

	if _, err := r.q.Exec(ctx, insertOutbox,
		eventID,
		rec.Subject,
		string(metadata),
		payload,
		StatusPending,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("postgres: outbox append for subject %s: %w", rec.Subject, err)
	}

	return nil
}

// normalizeJSON prepares a payload for a jsonb column.
//
// It is validated here rather than left to the server because the error is
// far more useful at the call site: PostgreSQL would reject the whole
// transaction with a syntax complaint about a byte offset, taking the state
// change down with it, while this names the outbox row as the culprit.
//
// A string is returned rather than []byte because the driver encodes []byte as
// bytea, which is not what a jsonb column wants.
func normalizeJSON(raw []byte) (string, error) {
	if len(raw) == 0 {
		return emptyJSON, nil
	}
	if !json.Valid(raw) {
		// The payload itself is not echoed: it can contain player-supplied
		// text, and an error string is one of the easiest things to end up in
		// a log or a chat window.
		return "", fmt.Errorf("payload is not valid json (%d bytes)", len(raw))
	}
	return string(raw), nil
}

// PendingEvent is one row the publisher has claimed.
type PendingEvent struct {
	ID       int64
	EventID  string
	Subject  string
	Metadata envelope.Metadata
	Payload  []byte
	Attempts int
}

// OutboxStore is the publisher side of the outbox, used by the worker that
// drains it. It is not an application port: the game core writes rows and
// never reads them back, so this stays out of ports.go.
type OutboxStore struct {
	pool *pgxpool.Pool
}

// NewOutboxStore returns a store over the pool. It takes the pool rather than
// a querier because claiming rows must run on its own connection, not inside
// a caller's transaction.
func NewOutboxStore(p *Pool) *OutboxStore { return &OutboxStore{pool: p.Raw()} }

// claimPending selects and claims a batch in one statement.
//
// FOR UPDATE SKIP LOCKED is the entire reason several publisher instances can
// run at once. FOR UPDATE alone would make the second worker block on the
// first worker's rows and then process exactly those rows once it was
// released — the same event published twice. SKIP LOCKED instead makes the
// second worker step over locked rows and take the next unclaimed ones, so the
// batches are disjoint by construction and no coordination between workers is
// needed at all.
//
// The SELECT is wrapped in an UPDATE rather than run on its own because a row
// lock lives only as long as the transaction that took it. A bare SELECT ...
// FOR UPDATE SKIP LOCKED sent as a standalone statement commits immediately
// and releases every lock it just took, which makes SKIP LOCKED decorative.
// Folding it into a CTE of an UPDATE puts the select and the claim in one
// implicit transaction, so the locks are genuinely held for the claim.
//
// The claim is the attempts bump, not a status change: outbox_status_check
// admits only pending / published / failed, so there is no "claimed" state to
// move to. That is deliberate rather than a workaround — a row stays pending
// until it is actually published, so a worker that dies between claiming and
// publishing leaves its rows recoverable by the next poll instead of stranded
// in a state nobody clears. The cost is that such a row may be published
// twice, which is why the publisher stamps Nats-Msg-Id with the request id and
// why consumers keep an inbox: at-least-once on the wire, effectively-once on
// processing.
//
// ORDER BY id preserves insertion order, which is the reason docs/database.md
// makes outbox.id a bigserial in the first place.
const claimPending = `
WITH claimed AS (
    SELECT id
    FROM outbox
    WHERE status = 'pending'
    ORDER BY id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox o
SET attempts = o.attempts + 1
FROM claimed c
WHERE o.id = c.id
RETURNING o.id, o.event_id, o.subject, o.metadata, o.payload, o.attempts`

// FetchPending claims up to limit pending events, oldest first.
func (s *OutboxStore) FetchPending(ctx context.Context, limit int) ([]PendingEvent, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("postgres: outbox fetch: limit must be positive, got %d", limit)
	}

	rows, err := s.pool.Query(ctx, claimPending, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: fetching pending outbox rows: %w", err)
	}
	defer rows.Close()

	var out []PendingEvent
	for rows.Next() {
		var (
			ev       PendingEvent
			metadata []byte
		)
		if err := rows.Scan(&ev.ID, &ev.EventID, &ev.Subject, &metadata, &ev.Payload, &ev.Attempts); err != nil {
			return nil, fmt.Errorf("postgres: scanning pending outbox row: %w", err)
		}
		if err := json.Unmarshal(metadata, &ev.Metadata); err != nil {
			return nil, fmt.Errorf("postgres: decoding metadata of outbox event %s: %w", ev.EventID, err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading pending outbox rows: %w", err)
	}

	return out, nil
}

// markPublished closes out a batch.
//
// The status = 'pending' predicate makes the update idempotent: replaying a
// batch cannot move a row out of failed, and cannot rewrite the published_at
// of a row that was already closed, so the timestamp keeps meaning "when this
// event first reached the broker".
const markPublished = `
UPDATE outbox
SET status = $1, published_at = $2
WHERE event_id = ANY($3::uuid[]) AND status = 'pending'`

// MarkPublished records that these events reached the broker.
func (s *OutboxStore) MarkPublished(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		// Not an error: an empty poll publishes nothing and has nothing to
		// close out, and making the caller special-case that would only
		// invite it to be forgotten.
		return nil
	}

	if _, err := s.pool.Exec(ctx, markPublished, StatusPublished, time.Now().UTC(), eventIDs); err != nil {
		return fmt.Errorf("postgres: marking %d outbox event(s) published: %w", len(eventIDs), err)
	}

	return nil
}
