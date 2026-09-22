package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file holds what the phase 1 repositories share: the mapping from a
// server-side constraint violation to a sentinel the application understands,
// and the transaction helper the two multi-statement operations need.

// SQLSTATE codes this package reacts to. They are the five-character class
// codes PostgreSQL guarantees; the human-readable message beside them is not
// guaranteed at all — it is translated when the server runs under a non-English
// lc_messages and it has been reworded between major versions. Matching on the
// code is therefore the only stable option, and matching on the message text
// would be a bug waiting for a server upgrade or a locale change.
const (
	sqlstateUniqueViolation          = "23505"
	sqlstateCheckViolation           = "23514"
	sqlstateInvalidTextRepresentation = "22P02"
)

// Constraint and index names, quoted from migrations/0002_phase1.up.sql.
//
// They are named here so that a rename in the migration breaks a test in this
// package rather than silently turning a mapped sentinel back into a raw
// driver error that reaches a player as "something went wrong".
const (
	travelsOneActivePerPlayerIdx = "travels_one_active_per_player_idx"
	friendshipsNoSelfCheck       = "friendships_no_self_check"
)

// violates reports whether err is a PostgreSQL error with exactly this
// SQLSTATE and this constraint name.
//
// Both halves are required. The SQLSTATE alone is far too broad: a single
// INSERT can violate several unique indexes, so treating every 23505 from the
// travel insert as "already travelling" would also relabel a duplicate primary
// key — a retried insert reusing an id — as an ordinary player pressing a
// button twice, and the real fault would never be seen. The constraint name
// alone is not enough either, because PostgreSQL reports the same name for
// different failure classes on the same object.
//
// For a violation of a unique INDEX that is not backed by a table constraint,
// as travels_one_active_per_player_idx is, the server still reports the index
// name in ConstraintName, which is what makes this precise enough to use.
func violates(err error, sqlstate, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == sqlstate && pgErr.ConstraintName == constraint
}

// isInvalidUUIDText reports whether err is the server rejecting a parameter
// that was not valid uuid text.
//
// This is used only where the parameter is an identifier that came from
// outside — a city code typed into a callback payload, say. Such a value names
// nothing, so the lookup is a miss rather than a fault, and mapping it here
// also keeps the server's "invalid input syntax for type uuid: ..." text, which
// echoes the caller's string, out of the error that gets logged.
func isInvalidUUIDText(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == sqlstateInvalidTextRepresentation
}

// Sentinels this package has to declare itself.
//
// internal/application/errors_phase1.go is the home of every sentinel the
// application branches on, and these four conditions have no sentinel there:
// stats missing, a scheduled action missing, a self-edge in friendships (which
// the schema rejects with friendships_no_self_check) and a request into an
// edge the caller has blocked. Declaring them here rather than inventing a
// fmt.Errorf keeps them classified: each carries a Code, so
// errors.CodeOf(err) and errors.Is(err, errors.ErrNotFound) still work for a
// caller in the application layer, which cannot import this package.
//
// They must be treated as read-only. Each is a pointer to a shared value, so
// calling WithCause or WithDetail on one would mutate the sentinel itself for
// every other caller in the process; repositories return them unwrapped, the
// same way PlayerRepository returns ErrPlayerNotFound.
var (
	// ErrStatsNotFound reports that a player has no player_stats row yet.
	// StatsRepository.EnsureDefaults is what creates it.
	ErrStatsNotFound = apperrors.NotFound("player stats not found")

	// ErrGameActionNotFound reports that no scheduled action with that id is
	// still open. It covers both an unknown id and one that was already
	// completed, failed or cancelled.
	ErrGameActionNotFound = apperrors.NotFound("scheduled action not found")

	// ErrSelfFriendship reports an edge from a player to themselves, which
	// friendships_no_self_check rejects. It is InvalidInput and not Conflict:
	// no state makes it valid, it is always a caller bug.
	ErrSelfFriendship = apperrors.InvalidInput("a player cannot befriend themselves")

	// ErrFriendshipBlocked reports that the caller's own edge to that player
	// is blocked, so a friend request would contradict it.
	ErrFriendshipBlocked = apperrors.Conflict("that player is blocked")
)

// transactor is a querier that can also open a transaction.
//
// Two phase 1 operations are multi-statement and must be atomic
// (TravelRepository.Complete and FriendshipRepository.Accept), so those
// repositories need more than the querier subset. Both *pgxpool.Pool and
// pgx.Tx satisfy this, which means such a repository still works unchanged
// inside a caller's unit of work: pgx.Tx.Begin opens a savepoint, so the inner
// "transaction" rolls back into the outer one instead of committing behind its
// back.
type transactor interface {
	querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Compile-time proof that both implementations still fit the subset.
var (
	_ transactor = (*pgxpool.Pool)(nil)
	_ transactor = (pgx.Tx)(nil)
)

// inTx runs fn inside one transaction on t, committing when fn succeeds.
//
// It follows UnitOfWork.Do exactly: a rollback failure is joined onto fn's
// error rather than dropped, because fn's error says what the application
// refused to do while the rollback error is the one that says the connection
// or the transaction is in an unknown state. pgx.ErrTxClosed is excluded from
// that join because it means the transaction was already finished, which is
// not a failure.
//
// It is a package-level helper rather than a method on UnitOfWork because
// these repositories must also be able to open their own transaction when they
// were built over the pool, outside any unit of work.
func inTx(ctx context.Context, t transactor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	pgtx, err := t.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}

	// A panic inside fn must not leave the transaction open holding locks.
	finished := false
	defer func() {
		if !finished {
			_ = pgtx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err := fn(ctx, pgtx); err != nil {
		// context.WithoutCancel: when fn failed because ctx was cancelled, a
		// rollback on that same context would fail too and the transaction
		// would be left for the server to clean up on connection close.
		if rbErr := pgtx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			finished = true
			return errors.Join(err, fmt.Errorf("postgres: rollback failed: %w", rbErr))
		}
		finished = true
		return err
	}

	if err := pgtx.Commit(ctx); err != nil {
		finished = true
		return fmt.Errorf("postgres: commit: %w", err)
	}
	finished = true

	return nil
}
