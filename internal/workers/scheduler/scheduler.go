// Package scheduler turns the durable schedule into published commands.
//
// # What it does
//
// game_actions is the source of truth for every timed operation in the game
// (19_SCHEDULER_WORKERS.md). A row carries a finish_at and a status, and
// migrations/0002_phase1.up.sql indexes exactly the rows that are still
// waiting:
//
//	CREATE INDEX game_actions_due_idx ON game_actions (finish_at)
//	    WHERE status = 'scheduled';
//
// Each tick asks GameActionRepository.Due for the next batch of rows whose
// instant has passed. The partial predicate is what makes that query walk the
// front of an index and stop, which is the project's binding constraint from
// 01_WORLD.md: no tick may scan the whole database. The batch size is the
// second half of it — a tick claims a bounded number of rows and leaves the
// rest for the next one, so a backlog costs more ticks rather than one
// unbounded pass.
//
// # Why it publishes instead of executing
//
// This package owns WHEN something happens and never WHAT happens. A due
// travel becomes a published command on game.command.travel.arrive.v1; the
// game service picks it up and lands the player.
//
// Landing the player here instead would be shorter by one hop and wrong in
// two ways. It would put a game rule — where a journey ends, what arriving
// costs, what it announces — outside internal/domain, in a process whose job
// is a clock. And it would be the second copy of that rule: the same arrival
// already has to exist in the game service, because a player can arrive
// through other paths and because the rules live with the domain. Two copies
// of a rule drift, and the one in the scheduler is the one nobody remembers to
// update.
//
// Publishing also buys the properties the rest of the system already has for
// free: the command is persisted by JetStream, retried on a failed handler,
// deduplicated by Nats-Msg-Id, and settled once by the consumer's inbox. A
// direct call has none of that.
//
// # What it does on failure
//
// A publish failure is transient by default — the broker restarted, the
// network blipped — so the action is left exactly as Due claimed it and
// nothing is written. It is never marked failed: Fail is permanent, and a
// permanently failed travel is a player stranded between two cities because
// NATS was unreachable for a second. Only a fault in the row itself, an
// action_type this build cannot route, is a Fail.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	gwcontext "github.com/mrjvadi/torncity/internal/gateway/context"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// Construction failures. They are reported rather than defaulted: a scheduler
// missing its repository would tick forever against nothing and look healthy.
var (
	ErrNoActions      = errors.New("scheduler: a game action repository is required")
	ErrNoPublisher    = errors.New("scheduler: a publisher is required")
	ErrNoLogger       = errors.New("scheduler: a logger is required")
	ErrNoTickInterval = errors.New("scheduler: tick_interval must be greater than zero")
	ErrNoBatchSize    = errors.New("scheduler: batch_size must be greater than zero")
	ErrNoBatchBudget  = errors.New("scheduler: shutdown_timeout must be greater than zero")
	ErrNoLanguage     = errors.New("scheduler: a default language is required")
)

// actorTypePlayer is the game_actions.actor_type that means actor_id is a
// player id. The other two values the schema admits, company and system, have
// no player behind them and leave Metadata.PlayerID empty.
const actorTypePlayer = "player"

// updateTypeScheduled is what a scheduler-published command carries in
// Metadata.UpdateType.
//
// A Telegram update would carry message, edited_message or callback_query
// there. This value is deliberately one no gateway can ever produce, so that
// "which of these commands came from a clock rather than from a person" is
// answerable from a single field, in a log line or in the inbox table.
const updateTypeScheduled = "scheduled_action"

// Options is everything a Scheduler needs. Every field is required.
type Options struct {
	// Actions is the durable schedule.
	Actions application.GameActionRepository

	// Publisher is the broker. EventPublisher rather than Publisher because
	// the deduplication id must be set explicitly; see dispatch.
	Publisher application.EventPublisher

	Logger *slog.Logger

	// TickInterval is how often the due index is asked for work. It is the
	// floor on how late a scheduled action can be.
	TickInterval time.Duration

	// BatchSize caps how many actions one tick claims.
	BatchSize int

	// ShutdownTimeout bounds a batch once its rows have been claimed.
	//
	// It is not only a shutdown value. A claimed row has left the due index
	// and nothing else will pick it up, so the batch that claimed it must be
	// allowed to finish even while the process is stopping — and must not be
	// allowed to hold the process open forever. The same budget answers both,
	// which is why there is one number and not two.
	ShutdownTimeout time.Duration

	// NoisyAttempts is the retry count at which a row that will not publish
	// stops being an ordinary retry and starts being an operator's problem.
	// Nothing about the row changes; only the log level does.
	NoisyAttempts int

	// InstanceID identifies this process in the metadata it publishes, so a
	// command can be traced back to the scheduler that emitted it.
	InstanceID string

	// DefaultLanguage is the language stamped on the metadata. A scheduled
	// action has no Telegram user to read a locale from, and a handler that
	// renders player-facing text needs something rather than an empty string.
	DefaultLanguage string
}

// Scheduler claims due actions and publishes them as commands.
type Scheduler struct {
	actions   application.GameActionRepository
	out       application.EventPublisher
	logger    *slog.Logger
	opts      Options

	// now, newRequestID and newTraceID are fields rather than direct calls so
	// a test can make a tick deterministic without a clock or a random source.
	now          func() time.Time
	newRequestID func() (string, error)
	newTraceID   func() (string, error)
}

// New validates opts and returns a Scheduler.
func New(opts Options) (*Scheduler, error) {
	switch {
	case opts.Actions == nil:
		return nil, ErrNoActions
	case opts.Publisher == nil:
		return nil, ErrNoPublisher
	case opts.Logger == nil:
		return nil, ErrNoLogger
	case opts.TickInterval <= 0:
		return nil, ErrNoTickInterval
	case opts.BatchSize <= 0:
		return nil, ErrNoBatchSize
	case opts.ShutdownTimeout <= 0:
		return nil, ErrNoBatchBudget
	case opts.DefaultLanguage == "":
		return nil, ErrNoLanguage
	}

	return &Scheduler{
		actions:      opts.Actions,
		out:          opts.Publisher,
		logger:       opts.Logger,
		opts:         opts,
		now:          time.Now,
		newRequestID: gwcontext.NewRequestID,
		newTraceID:   gwcontext.NewTraceID,
	}, nil
}

// Run ticks until ctx is cancelled.
//
// Cancellation stops the claiming, not the work already claimed: the loop
// leaves as soon as it is not inside a batch, and a batch that is already
// running finishes on a context the signal does not reach. The alternative —
// handing the batch the cancelled context — would abandon rows that are
// already out of the due index, where the next tick of the next process
// cannot see them either.
func (s *Scheduler) Run(ctx context.Context) error {
	s.logger.Info("scheduler started",
		slog.Duration("tick_interval", s.opts.TickInterval),
		slog.Int("batch_size", s.opts.BatchSize),
		slog.Duration("shutdown_timeout", s.opts.ShutdownTimeout),
		slog.Int("noisy_attempts", s.opts.NoisyAttempts))

	ticker := time.NewTicker(s.opts.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("shutdown signal received, no further actions will be claimed")
			return nil
		case <-ticker.C:
		}

		// select picks at random between two ready cases, so a tick that
		// fired at the same moment as the signal could claim a batch nobody
		// asked for. Checked again, so "stop claiming" means it.
		if ctx.Err() != nil {
			s.logger.Info("shutdown signal received, no further actions will be claimed")
			return nil
		}

		batchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
		dispatched, err := s.Tick(batchCtx)
		cancel()

		if err != nil {
			// The rows are still claimed and nothing was written, so waiting
			// for the next tick loses nothing.
			s.logger.Error("scheduler tick failed", slog.String("error", err.Error()))
			continue
		}
		if dispatched > 0 {
			s.logger.Debug("scheduler tick dispatched actions", slog.Int("actions", dispatched))
		}
	}
}

// Tick claims one batch and dispatches it. It returns how many actions reached
// the broker.
//
// Exported so a caller can run a single pass — a test, or a one-shot drain —
// without starting the loop.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	now := s.now()

	due, err := s.actions.Due(ctx, now, s.opts.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("scheduler: claiming due actions: %w", err)
	}
	if len(due) == 0 {
		return 0, nil
	}

	dispatched := 0
	for _, action := range due {
		if s.dispatch(ctx, action, now) {
			dispatched++
		}
	}
	return dispatched, nil
}

// dispatch publishes one action and records the outcome. It reports whether
// the action reached the broker.
func (s *Scheduler) dispatch(ctx context.Context, action application.GameAction, now time.Time) bool {
	route, ok := RouteFor(action.ActionType)
	if !ok {
		// Not a transient failure and not something a retry can fix: this
		// build has no subject for that action_type, and every future tick
		// would reach the same conclusion. Left scheduled it would be claimed
		// forever; failed it is out of the way and visible.
		s.logger.Error("no route for this action type, failing the action",
			slog.String("action_id", action.ID),
			slog.String("action_type", action.ActionType))

		reason := fmt.Sprintf("no command route for action_type %q", action.ActionType)
		if err := s.actions.Fail(ctx, action.ID, reason); err != nil {
			s.logger.Error("cannot fail an unroutable action",
				slog.String("action_id", action.ID),
				slog.String("action_type", action.ActionType),
				slog.String("error", err.Error()))
		}
		return false
	}

	subject := subjects.Command(route.Domain, route.Action)

	env, err := s.envelopeFor(action, route, now)
	if err != nil {
		// Reached only when the random source fails or the row's payload is
		// not valid json. Nothing is written: the row keeps its claim and the
		// failure is loud rather than silently permanent.
		s.logger.Error("cannot build the command envelope",
			slog.String("action_id", action.ID),
			slog.String("action_type", action.ActionType),
			slog.String("subject", subject),
			slog.String("error", err.Error()))
		return false
	}

	// The deduplication id is the action id, not the request id. The request
	// id is minted per dispatch, so a row published twice — claimed,
	// published, and then the process died before Complete — would arrive
	// under two ids and be executed twice. The action id is the one value that
	// is the same on both attempts and different for every other action, which
	// is exactly what the broker's duplicate window needs. It is the same
	// reasoning that makes the outbox worker pass the outbox event id rather
	// than the request id.
	if err := s.out.PublishWithID(ctx, subject, env, action.ID); err != nil {
		level := slog.LevelWarn
		if s.opts.NoisyAttempts > 0 && action.RetryCount >= s.opts.NoisyAttempts {
			level = slog.LevelError
		}
		// Deliberately not failed. A broker that is down for a minute must
		// cost a minute of lateness, not a permanently dropped journey.
		s.logger.Log(ctx, level, "cannot publish a due action",
			append(metaAttrs(env.Metadata),
				slog.String("action_id", action.ID),
				slog.String("action_type", action.ActionType),
				slog.String("subject", subject),
				slog.Int("retry_count", action.RetryCount),
				slog.String("error", err.Error()),
			)...)
		return false
	}

	if err := s.actions.Complete(ctx, action.ID); err != nil {
		// The command is on the broker but the row does not say so. That is
		// the right side to fail on: the alternative is a row marked complete
		// for a command nobody received. A republication is absorbed by the
		// deduplication id above and by the idempotency key on the metadata.
		s.logger.Error("action published but not marked complete",
			append(metaAttrs(env.Metadata),
				slog.String("action_id", action.ID),
				slog.String("subject", subject),
				slog.String("error", err.Error()),
			)...)
		return true
	}

	s.logger.Info("due action dispatched",
		append(metaAttrs(env.Metadata),
			slog.String("action_id", action.ID),
			slog.String("action_type", action.ActionType),
			slog.String("subject", subject),
		)...)
	return true
}

// Command is the payload a dispatched action carries.
//
// It is the row, not an interpretation of the row. The scheduler does not know
// what a travel payload contains and must not: it forwards the jsonb the
// handler wrote when it scheduled the action, alongside the identity of the
// action itself so the handler can find what the work is about and recognise a
// replay.
type Command struct {
	ActionID      string          `json:"action_id"`
	ActionType    string          `json:"action_type"`
	ActorType     string          `json:"actor_type"`
	ActorID       string          `json:"actor_id,omitempty"`
	ReferenceType string          `json:"reference_type,omitempty"`
	ReferenceID   string          `json:"reference_id,omitempty"`
	ScheduledFor  time.Time       `json:"scheduled_for"`
	Payload       json.RawMessage `json:"payload"`
}

// envelopeFor builds the message for one action.
func (s *Scheduler) envelopeFor(action application.GameAction, route Route, now time.Time) (*envelope.Envelope, error) {
	meta, err := s.metadataFor(action, route, now)
	if err != nil {
		return nil, err
	}

	payload := action.Payload
	if len(payload) == 0 {
		// jsonb defaults to '{}' in the schema, but a repository that returns
		// a nil slice for it would otherwise produce `"payload":null` and a
		// handler decoding into a struct would get a zero value it cannot
		// tell from an empty object.
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("scheduler: action %s: payload is not valid json", action.ID)
	}

	return envelope.New(meta, Command{
		ActionID:      action.ID,
		ActionType:    action.ActionType,
		ActorType:     action.ActorType,
		ActorID:       action.ActorID,
		ReferenceType: action.ReferenceType,
		ReferenceID:   action.ReferenceID,
		ScheduledFor:  action.FinishAt,
		Payload:       payload,
	})
}

// metadataFor builds the request context a scheduled command travels with.
//
// # The fields a Telegram update would have supplied
//
// envelope.Metadata was designed around an update arriving at the gateway.
// There is no update here and no person: the origin is a clock. Rather than
// invent plausible values, each such field is set to the one that says "this
// did not come from Telegram", and here is what each means:
//
//   - TelegramUserID, TelegramChatID, TelegramMessageID are zero. No chat is
//     waiting for this command and none may be assumed. A handler that needs
//     to reach the player resolves the bot and chat from the player's bot link
//     (application.BotLink), which is where that fact actually lives and stays
//     correct when the player last spoke to a different bot.
//   - TelegramThreadID, ReplyToMessageID and CallbackQueryID are nil for the
//     same reason: there is nothing to thread under, reply to or answer.
//   - BotID is empty. Ten bots exist for outbound capacity, and no one of them
//     originated this; claiming one would put a false attribution in the
//     inbox and in every log line downstream.
//   - ChatType is empty: there is no chat, so neither "private" nor "group" is
//     true.
//   - UpdateType is updateTypeScheduled, a value no gateway can produce, so
//     clock-driven traffic is distinguishable at a glance.
//   - Language is the configured default. A scheduled action has no Telegram
//     locale behind it, and an empty language would render player-facing text
//     in whatever a lookup falls back to rather than in the game's own
//     default.
//   - GatewayInstanceID carries the scheduler's instance id. The field names a
//     gateway because the gateway was the only publisher when it was written;
//     what it is for is "which process emitted this", and leaving it empty
//     would make a misbehaving scheduler replica untraceable.
//
// RequestID and TraceID are minted per dispatch, because each dispatch is a
// new request into the game core: reusing the action id for them would make
// two dispatches of one row indistinguishable in a trace, which is precisely
// the thing an operator is looking at the trace to tell apart.
//
// IdempotencyKey is the action id, and that is the value that makes a repeat
// harmless: a republished action is the same intent, and the handler keyed on
// player + request + idempotency key settles it once.
func (s *Scheduler) metadataFor(action application.GameAction, route Route, now time.Time) (envelope.Metadata, error) {
	requestID, err := s.newRequestID()
	if err != nil {
		return envelope.Metadata{}, fmt.Errorf("scheduler: %w", err)
	}
	traceID, err := s.newTraceID()
	if err != nil {
		return envelope.Metadata{}, fmt.Errorf("scheduler: %w", err)
	}

	meta := envelope.Metadata{
		RequestID:         requestID,
		TraceID:           traceID,
		BotID:             "",
		GatewayInstanceID: s.opts.InstanceID,
		ChatType:          "",
		UpdateType:        updateTypeScheduled,
		Command:           route.Command(),
		Action:            route.Action,
		Language:          s.opts.DefaultLanguage,
		IdempotencyKey:    action.ID,
		ReceivedAt:        now,
		SchemaVersion:     envelope.SchemaVersion,
	}

	// A company or system action has no player behind it, and PlayerID is
	// omitempty precisely so it can say that.
	if action.ActorType == actorTypePlayer {
		meta.PlayerID = action.ActorID
	}

	return meta, nil
}

// metaAttrs is the trace context every hop logs, in the same shape the other
// processes log it.
func metaAttrs(meta envelope.Metadata) []any {
	return []any{
		slog.String("trace_id", meta.TraceID),
		slog.String("request_id", meta.RequestID),
	}
}
