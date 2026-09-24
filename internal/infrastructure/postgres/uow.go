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

	// defaultLanguage is handed to every player repository this unit of work
	// builds; see NewPlayerRepository.
	defaultLanguage string
}

var _ application.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork returns a unit of work over p.
//
// defaultLanguage is player.default_language from the configuration. It is
// carried here because the repositories are built per transaction, so there
// is no other place to hand it to them.
func NewUnitOfWork(p *Pool, defaultLanguage string) *UnitOfWork {
	return &UnitOfWork{pool: p.Raw(), defaultLanguage: defaultLanguage}
}

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

	if err := fn(ctx, &tx{q: pgtx, defaultLanguage: u.defaultLanguage}); err != nil {
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
//
// q is a transactor, not merely a querier, because TravelRepository and
// FriendshipRepository each run one multi-statement operation through inTx.
// Handed this transaction, inTx calls pgx.Tx.Begin, which opens a SAVEPOINT on
// the same connection rather than a second transaction on another one. Two
// properties follow, and both are what a unit of work needs:
//
//   - nothing commits early. Releasing a savepoint makes its statements part
//     of the outer transaction, not durable; they become durable when Do
//     commits, and vanish if any later step of the handler fails.
//   - a failure inside the inner operation rolls back to the savepoint only,
//     so its mapped sentinel (ErrNoActiveTravel, ErrNotFriends) reaches the
//     handler with the outer transaction still usable, instead of the
//     connection being left in the aborted state where every later statement
//     is refused.
//
// The alternative — teaching those repositories to skip inTx when already
// inside a transaction — was rejected: it would need a second code path per
// operation, and a repository built over the pool must still open its own
// transaction, which inTx already does correctly for both cases.
type tx struct {
	q               transactor
	defaultLanguage string
}

var _ application.Tx = (*tx)(nil)

// Players returns the player repository bound to this transaction.
func (t *tx) Players() application.PlayerRepository {
	return &PlayerRepository{q: t.q, defaultLanguage: t.defaultLanguage}
}

// Outbox returns the outbox repository bound to this transaction.
func (t *tx) Outbox() application.OutboxRepository { return &OutboxRepository{q: t.q} }

// Idempotency returns the idempotency repository bound to this transaction.
func (t *tx) Idempotency() application.IdempotencyRepository {
	return &IdempotencyRepository{q: t.q}
}

// Stats returns the stats repository bound to this transaction.
func (t *tx) Stats() application.StatsRepository { return &StatsRepository{q: t.q} }

// Skills returns the skill repository bound to this transaction.
func (t *tx) Skills() application.SkillRepository { return &SkillRepository{q: t.q} }

// Travels returns the travel repository bound to this transaction. Its
// Complete runs inside a savepoint of this transaction; see the note on tx.
func (t *tx) Travels() application.TravelRepository { return &TravelRepository{q: t.q} }

// GameActions returns the schedule bound to this transaction, so an action is
// scheduled if and only if the journey that points at it is.
func (t *tx) GameActions() application.GameActionRepository {
	return &GameActionRepository{q: t.q}
}

// Friendships returns the friendship repository bound to this transaction.
// Its Accept runs inside a savepoint of this transaction; see the note on tx.
func (t *tx) Friendships() application.FriendshipRepository {
	return &FriendshipRepository{q: t.q}
}

// Ledger returns the ledger bound to this transaction, so money moves if and
// only if the change that moved it commits. Post runs inside a savepoint of
// this transaction; see the note on tx.
func (t *tx) Ledger() application.LedgerRepository { return &LedgerRepository{q: t.q} }

// Governance returns the governance repository bound to this transaction, so
// a lever changes if and only if its public record is written.
func (t *tx) Governance() application.GovernanceRepository {
	return &GovernanceRepository{q: t.q}
}

// Bank returns the bank's presence locks bound to this transaction, so they
// are held until the money they guard has moved.
func (t *tx) Bank() application.BankRepository { return &BankRepository{q: t.q} }

// Employment returns the job repository bound to this transaction, so a shift
// commits with the wage and the energy it cost.
func (t *tx) Employment() application.EmploymentRepository { return &EmploymentRepository{q: t.q} }

// Education returns the study repository bound to this transaction, so an
// enrolment commits with its fee and its scheduled completion.
func (t *tx) Education() application.EducationRepository { return &EducationRepository{q: t.q} }

// Crime returns the crime repository bound to this transaction, so an
// attempt commits with its nerve, its money and its sentence.
func (t *tx) Crime() application.CrimeRepository { return &CrimeRepository{q: t.q} }

// Places returns the place repository bound to this transaction, so a walk
// commits with the energy it cost and its scheduled end.
func (t *tx) Places() application.PlaceRepository { return &PlaceRepository{q: t.q} }

// Items returns the goods repository bound to this transaction, so goods
// move with the money that paid for them.
func (t *tx) Items() application.ItemRepository { return &ItemRepository{q: t.q} }

// Shops returns the shelves and sales of the city shops.
func (t *tx) Shops() application.ShopRepository { return &ShopRepository{q: t.q} }

// Market returns the player market's orders and trades.
func (t *tx) Market() application.MarketRepository { return &MarketRepository{q: t.q} }

// Auctions returns the auction house.
func (t *tx) Auctions() application.AuctionRepository { return &AuctionRepository{q: t.q} }
