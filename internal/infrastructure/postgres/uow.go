package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/application"
)

// UnitOfWork runs a function inside one database transaction.
//
// It exists so a command handler cannot accidentally split a state change from
// the outbox row that announces it: both go through the same Tx, so both
// commit or neither does.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

var _ application.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork returns a unit of work over p.
func NewUnitOfWork(p *Pool) *UnitOfWork { return &UnitOfWork{pool: p.Raw()} }

// Do begins a transaction, runs fn against it and commits when fn succeeds.
//
// A rollback failure is joined onto the original error rather than dropped.
// Losing it would be the worst possible trade: fn's error explains what the
// application refused to do, while the rollback error is the one that says the
// connection is broken or the transaction is in an unknown state, and only the
// second one tells an operator the database itself needs looking at.
//
// pgx.ErrTxClosed is excluded from that join because it is not a failure: it
// means the transaction was already finished — typically because fn committed
// or rolled back itself — and reporting it would turn a clean path into a
// spurious error.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx application.Tx) error) error {
	pgtx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin transaction: %w", err)
	}

	// A panic inside fn must not leave the transaction open holding locks;
	// the rollback runs and the panic continues on its way.
	committed := false
	defer func() {
		if !committed {
			_ = pgtx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err := fn(ctx, &tx{q: pgtx}); err != nil {
		// context.WithoutCancel: when fn failed because ctx was cancelled, a
		// rollback on that same context would fail too and the transaction
		// would be left for the server to clean up on connection close.
		if rbErr := pgtx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			committed = true // the deferred rollback has nothing left to do
			return errors.Join(err, fmt.Errorf("postgres: rollback failed: %w", rbErr))
		}
		committed = true
		return err
	}

	if err := pgtx.Commit(ctx); err != nil {
		committed = true
		return fmt.Errorf("postgres: commit: %w", err)
	}
	committed = true

	return nil
}

// tx exposes the repositories bound to one transaction.
//
// The repositories are built per call rather than cached: each is a struct
// holding only a querier, so constructing one is free, and a cached instance
// would have to be reset between transactions.
type tx struct {
	q querier
}

var _ application.Tx = (*tx)(nil)

// Players returns the player repository bound to this transaction.
func (t *tx) Players() application.PlayerRepository { return &PlayerRepository{q: t.q} }

// Outbox returns the outbox repository bound to this transaction.
func (t *tx) Outbox() application.OutboxRepository { return &OutboxRepository{q: t.q} }

// Idempotency returns the idempotency repository bound to this transaction.
func (t *tx) Idempotency() application.IdempotencyRepository {
	return &IdempotencyRepository{q: t.q}
}
