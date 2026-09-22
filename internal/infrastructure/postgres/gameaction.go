package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// Action statuses, as game_actions_status_check accepts them. They are named
// rather than written inline wherever a Go value is passed, so a typo becomes
// a compile error instead of a constraint violation discovered at runtime.
const (
	ActionScheduled = "scheduled"
	ActionRunning   = "running"
	ActionCompleted = "completed"
	ActionFailed    = "failed"
	ActionCancelled = "cancelled"
)

// failureReasonKey is the payload key Fail records its reason under; see Fail.
const failureReasonKey = "last_error"

// maxFailureReason caps how much of a failure reason is stored. A reason is
// usually a wrapped driver or API error and can carry a whole response body;
// game_actions is a hot table and an unbounded string in its jsonb column
// would push rows out of line into TOAST storage for no benefit, since nothing
// reads past the first line of a reason anyway.
const maxFailureReason = 500

// GameActionRepository is the durable schedule.
//
// This table is the source of truth for every timed operation in the game. A
// Redis structure may accelerate lookups, but a service that restarts
// mid-travel still lands the player, and it can only do that because the work
// outlived the process here.
type GameActionRepository struct {
	q querier
}

var _ application.GameActionRepository = (*GameActionRepository)(nil)

// NewGameActionRepository returns a repository over the pool.
func NewGameActionRepository(p *Pool) *GameActionRepository {
	return &GameActionRepository{q: p.Raw()}
}

const insertGameAction = `
INSERT INTO game_actions (id, action_type, actor_type, actor_id, reference_type, reference_id, payload, status, retry_count, started_at, finish_at)
VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6::uuid, $7::jsonb, $8, 0, $9, $10)`

// Schedule writes one action, always with status scheduled and retry_count 0.
//
// The status is not the caller's to choose, for the same reason
// OutboxRepository.Append fixes pending: the row's whole purpose is to be
// claimed by Due once finish_at passes, and a row inserted as completed or
// running is work nobody will ever pick up. retry_count is fixed for the same
// reason — a row that starts life with attempts already spent would be
// abandoned early by whatever retry budget the worker applies.
//
// A caller that needs to reference the action later must supply a.ID, because
// GameAction is passed by value and there is no way to hand a generated
// identifier back. travels.game_action_id is exactly such a reference, so the
// travel handler mints the id first and passes the same value to both writes.
func (r *GameActionRepository) Schedule(ctx context.Context, a application.GameAction) error {
	id, err := ensureID(a.ID)
	if err != nil {
		return err
	}

	if a.ActionType == "" {
		// action_type is NOT NULL and deliberately has no CHECK, so the
		// server would happily store an empty type that no worker can route.
		return fmt.Errorf("postgres: schedule action: action type is required")
	}
	if a.FinishAt.IsZero() {
		// finish_at is what makes the row due. A zero value is the year 1,
		// which would make the action due immediately and forever-first in
		// every claim batch.
		return fmt.Errorf("postgres: schedule action %s: finish time is required", a.ActionType)
	}

	payload, err := normalizeJSON(a.Payload)
	if err != nil {
		return fmt.Errorf("postgres: schedule action %s: %w", a.ActionType, err)
	}

	startedAt := a.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	// actor_id and reference_id are NULL-able uuids: a system action belongs
	// to nobody, and an action about nothing in particular points at nothing.
	// An empty string would be rejected by the uuid cast rather than stored as
	// "absent", so it is turned into a NULL here.
	actorID := nullableUUID(a.ActorID)
	referenceID := nullableUUID(a.ReferenceID)
	referenceType := nullableText(a.ReferenceType)

	actorType := a.ActorType
	if actorType == "" {
		// game_actions_actor_type_check admits player / company / system.
		// An action arriving without an owner is a world-level one.
		actorType = "system"
	}

	if _, err := r.q.Exec(ctx, insertGameAction,
		id,
		a.ActionType,
		actorType,
		actorID,
		referenceType,
		referenceID,
		payload,
		ActionScheduled,
		startedAt,
		a.FinishAt,
	); err != nil {
		return fmt.Errorf("postgres: scheduling %s action: %w", a.ActionType, err)
	}

	return nil
}

// claimDueSQL selects and claims a batch of due actions in one statement.
//
// The shape is the one OutboxStore.claimPending established, and for the same
// reason: a row lock lives only as long as the transaction that took it, so a
// standalone SELECT ... FOR UPDATE SKIP LOCKED sent on its own commits
// immediately, releases every lock it just took, and claims nothing — two
// schedulers would then each receive the same batch and land the same player
// twice. Folding the select into a CTE of the UPDATE puts the lock and the
// claim in one implicit transaction, so SKIP LOCKED does what it says: a
// second scheduler steps over the locked rows and takes the next unclaimed
// ones, and the batches are disjoint by construction.
//
// Unlike the outbox, the claim here IS a status change. game_actions_status_check
// admits 'running', so a claimed row leaves the set of candidates immediately
// and cannot be handed out again on the next poll a second later. The cost of
// that is the mirror image of the outbox's: a worker that dies between claiming
// and completing leaves its row in running rather than recoverable, so a reaper
// that returns long-running rows to scheduled (or fails them) is required
// operationally. That is the right trade for actions, because the work behind
// one is not idempotent the way publishing a deduplicated message is — paying a
// salary twice is worse than paying it late.
//
// WHERE status = 'scheduled' AND finish_at <= $1 ORDER BY finish_at is written
// to match game_actions_due_idx (finish_at) WHERE status = 'scheduled' exactly.
// The predicate makes the index partial, so the scan walks the front of the
// pending backlog and stops at the limit, with no sort and without visiting a
// single completed row however many millions of them have piled up behind it.
const claimDueSQL = `
WITH claimed AS (
    SELECT id
    FROM game_actions
    WHERE status = 'scheduled' AND finish_at <= $1
    ORDER BY finish_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE game_actions a
SET status = 'running'
FROM claimed c
WHERE a.id = c.id
RETURNING a.id, a.action_type, a.actor_type, a.actor_id, a.reference_type, a.reference_id,
          a.payload, a.status, a.retry_count, a.started_at, a.finish_at, a.completed_at`

// Due claims up to limit actions whose finish_at has passed, oldest first.
//
// started_at is deliberately not touched by the claim. It records when the
// action began — a travel's departure, a production run's start — and is what
// finish_at was computed from; rewriting it at claim time would erase the
// journey's own timeline.
func (r *GameActionRepository) Due(ctx context.Context, now time.Time, limit int) ([]application.GameAction, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("postgres: due actions: limit must be positive, got %d", limit)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	rows, err := r.q.Query(ctx, claimDueSQL, now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: claiming due actions: %w", err)
	}
	defer rows.Close()

	var out []application.GameAction
	for rows.Next() {
		var (
			a             application.GameAction
			actorID       *string
			referenceType *string
			referenceID   *string
		)
		if err := rows.Scan(
			&a.ID, &a.ActionType, &a.ActorType, &actorID, &referenceType, &referenceID,
			&a.Payload, &a.Status, &a.RetryCount, &a.StartedAt, &a.FinishAt, &a.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scanning due action row: %w", err)
		}

		// The port models these as plain strings, so an absent value becomes
		// the empty string rather than travelling as a nil pointer nobody
		// checks.
		if actorID != nil {
			a.ActorID = *actorID
		}
		if referenceType != nil {
			a.ReferenceType = *referenceType
		}
		if referenceID != nil {
			a.ReferenceID = *referenceID
		}

		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading due action rows: %w", err)
	}

	return out, nil
}

// completeAction closes an action out.
//
// The status predicate is what makes this safe to replay: a completed row
// keeps its first completed_at, so the timestamp keeps meaning "when this
// action actually finished", and a cancelled or failed row cannot be
// resurrected by a late worker that finished its side of the work anyway.
const completeAction = `
UPDATE game_actions
SET status = $2, completed_at = $3
WHERE id = $1::uuid AND status IN ('scheduled', 'running')`

// Complete marks the action completed, or returns ErrGameActionNotFound when
// no open action carries that id — either it is unknown or it was already
// closed out.
func (r *GameActionRepository) Complete(ctx context.Context, id string) error {
	tag, err := r.q.Exec(ctx, completeAction, id, ActionCompleted, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("postgres: completing action %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGameActionNotFound
	}

	return nil
}

// failAction records a failure, bumps the retry counter and keeps the reason.
//
// The reason has nowhere of its own to live: game_actions in
// migrations/0002_phase1.up.sql has no error or reason column, and the port
// requires one to be accepted. Dropping it would silence exactly the
// information an operator needs at three in the morning, so it is merged into
// the payload under a reserved key instead.
//
// The CASE is not defensive padding. payload is jsonb, and `||` on two jsonb
// values means "concatenate" for arrays and "merge" only for objects, so an
// action whose payload is an array or a scalar would have its reason appended
// as an element — or, for a scalar, the statement would fail outright and the
// failure would be lost on top of the original one. Only an object payload is
// merged; anything else is wrapped so nothing is destroyed.
const failAction = `
UPDATE game_actions
SET status      = $2,
    retry_count = retry_count + 1,
    completed_at = $3,
    payload     = CASE
        WHEN jsonb_typeof(payload) = 'object'
            THEN payload || jsonb_build_object($4::text, $5::text)
        ELSE jsonb_build_object($4::text, $5::text, 'original_payload', payload)
    END
WHERE id = $1::uuid AND status IN ('scheduled', 'running')`

// Fail marks the action failed and records why.
//
// completed_at is set even though the action did not succeed: the column
// answers "when did this stop being open", which a failure answers just as
// well as a success, and leaving it NULL would make a failed row look like
// work still in flight to anything that sweeps for stalled actions.
func (r *GameActionRepository) Fail(ctx context.Context, id string, reason string) error {
	tag, err := r.q.Exec(ctx, failAction,
		id,
		ActionFailed,
		time.Now().UTC(),
		failureReasonKey,
		truncateReason(reason, maxFailureReason),
	)
	if err != nil {
		return fmt.Errorf("postgres: failing action %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGameActionNotFound
	}

	return nil
}

// truncateReason shortens s to at most max runes.
//
// It counts runes rather than bytes because the reason may contain non-ASCII
// text, and cutting a byte slice mid-character would store an invalid UTF-8
// sequence that the jsonb encoder rejects — turning a failure report into a
// second, unrelated failure.
func truncateReason(s string, max int) string {
	if max <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// nullableUUID turns an empty identifier into a NULL parameter. The columns it
// is used for are NULL-able uuids, and an empty string is not valid uuid text:
// passing it through would fail the cast instead of storing "absent".
func nullableUUID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// nullableText turns an empty string into a NULL parameter, so an absent value
// is stored as absent rather than as an empty string that every reader then
// has to special-case.
func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
