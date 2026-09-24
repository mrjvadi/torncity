package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Constraint names from migrations/0006_ledger.up.sql this file maps to
// sentinels. Named here so a rename in the migration breaks a test in this
// package instead of turning a mapped sentinel back into a raw driver error.
const (
	accountsBalanceNonNegativeCheck = "accounts_balance_non_negative_check"
	accountsKindOwnerCurrencyKey    = "accounts_kind_owner_currency_key"
	rewardGrantsPlayerFkey          = "reward_grants_player_id_fkey"
	sqlstateForeignKeyViolation     = "23503"
)

// ownerTables names the table each ownable account kind's owner_id points at.
// A kind missing here has no owner table yet (companies and factions arrive
// in later phases), so an account of that kind cannot be opened yet.
var ownerTables = map[application.AccountKind]string{
	application.AccountPlayerCash:   "players",
	application.AccountPlayerBank:   "players",
	application.AccountCityTreasury: "cities",
	application.AccountPlayerEscrow: "players",
}

// LedgerRepository implements application.LedgerRepository.
//
// Money columns are read and written as int64 minor units and converted with
// money.FromMinor / Amount.Minor only; nothing in this file touches a float.
type LedgerRepository struct {
	q transactor
}

var _ application.LedgerRepository = (*LedgerRepository)(nil)

// NewLedgerRepository returns a ledger over the pool, for callers outside a
// unit of work (the admin tool). Inside one, use Tx.Ledger.
func NewLedgerRepository(p *Pool) *LedgerRepository {
	return &LedgerRepository{q: p.Raw()}
}

// AccountFor returns, opening on first use, the account of kind for ownerID.
//
// Opening is an INSERT ... ON CONFLICT DO NOTHING followed by a read, so two
// callers racing to open the same account both come back with the one row;
// the loser's insert waits for the winner's transaction and then does nothing.
// The insert is conditional on the owner existing, so an account can never be
// opened for a player that is not there.
func (r *LedgerRepository) AccountFor(ctx context.Context, kind application.AccountKind, ownerID string) (application.Account, error) {
	if !kind.Valid() {
		return application.Account{}, application.ErrUnknownAccountKind.WithDetail("kind", string(kind))
	}

	if kind.IsSystem() {
		if ownerID != "" {
			return application.Account{}, application.ErrUnknownAccountKind.WithDetail("problem", "a system account has no owner")
		}
		return r.scanAccount(ctx,
			`SELECT id::text, kind, COALESCE(owner_id::text, ''), currency, balance
			   FROM accounts WHERE kind = $1 AND owner_id IS NULL AND currency = $2`,
			string(kind), application.DefaultCurrency)
	}

	table, ok := ownerTables[kind]
	if !ok {
		return application.Account{}, application.ErrUnknownAccountKind.WithDetail("kind", string(kind))
	}
	if ownerID == "" {
		return application.Account{}, notFoundOwner(kind)
	}

	id, err := newUUID()
	if err != nil {
		return application.Account{}, err
	}
	// table comes from ownerTables, a fixed map in this file, never from input.
	_, err = r.q.Exec(ctx, fmt.Sprintf(
		`INSERT INTO accounts (id, kind, owner_id, currency, balance, created_at)
		 SELECT $1, $2, $3, $4, 0, $5
		  WHERE EXISTS (SELECT 1 FROM %s WHERE id = $3)
		 ON CONFLICT ON CONSTRAINT %s DO NOTHING`, table, accountsKindOwnerCurrencyKey),
		id, string(kind), ownerID, application.DefaultCurrency, time.Now().UTC())
	if err != nil {
		if isInvalidUUIDText(err) {
			return application.Account{}, notFoundOwner(kind)
		}
		return application.Account{}, fmt.Errorf("postgres: opening %s account: %w", kind, err)
	}

	acct, err := r.scanAccount(ctx,
		`SELECT id::text, kind, COALESCE(owner_id::text, ''), currency, balance
		   FROM accounts WHERE kind = $1 AND owner_id = $2 AND currency = $3`,
		string(kind), ownerID, application.DefaultCurrency)
	if errors.Is(err, application.ErrAccountNotFound) {
		// The conditional insert wrote nothing and there was no row to
		// find: the owner does not exist.
		return application.Account{}, notFoundOwner(kind)
	}
	return acct, err
}

func notFoundOwner(kind application.AccountKind) error {
	if kind == application.AccountPlayerCash || kind == application.AccountPlayerBank || kind == application.AccountPlayerEscrow {
		return application.ErrPlayerNotFound
	}
	return application.ErrAccountOwnerNotFound.WithDetail("kind", string(kind))
}

func (r *LedgerRepository) scanAccount(ctx context.Context, sql string, args ...any) (application.Account, error) {
	var (
		a       application.Account
		kind    string
		balance int64
	)
	err := r.q.QueryRow(ctx, sql, args...).Scan(&a.ID, &kind, &a.OwnerID, &a.Currency, &balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Account{}, application.ErrAccountNotFound
	}
	if err != nil {
		return application.Account{}, fmt.Errorf("postgres: reading account: %w", err)
	}
	a.Kind = application.AccountKind(kind)
	a.Balance = money.FromMinor(balance)
	return a, nil
}

// Balance returns the cached balance of one account.
func (r *LedgerRepository) Balance(ctx context.Context, accountID string) (money.Amount, error) {
	var balance int64
	err := r.q.QueryRow(ctx, `SELECT balance FROM accounts WHERE id = $1`, accountID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUIDText(err) {
		return money.Amount{}, application.ErrAccountNotFound
	}
	if err != nil {
		return money.Amount{}, fmt.Errorf("postgres: reading balance: %w", err)
	}
	return money.FromMinor(balance), nil
}

// lockAccountsSQL locks every account a transaction touches, in id order.
//
// The order is the point. Two transactions moving money between the same two
// accounts in opposite directions would otherwise each lock one row and wait
// for the other's: a deadlock. Locking in one global order makes that
// impossible. The same lock is what serialises concurrent posts to one
// account, so the cached balance is always read-modify-written by one writer.
const lockAccountsSQL = `
SELECT id::text, currency FROM accounts
 WHERE id = ANY($1::uuid[])
 ORDER BY id
   FOR UPDATE`

// postSQL writes every leg and moves the cached balances in ONE statement.
//
// The entries and the balance change are the same data: the UPDATE sums
// exactly the rows the INSERT returned. There is no second statement that
// could be skipped, reordered or run with different numbers, which is what
// keeps accounts.balance a cache of the entries rather than a second opinion.
const postSQL = `
WITH legs AS (
    INSERT INTO ledger_entries
           (id, transaction_id, account_id, amount, currency, reason,
            reference_type, reference_id, created_at)
    SELECT l.id, $1, l.account_id, l.amount, $5, $6, $7, $8, $9
      FROM unnest($2::uuid[], $3::uuid[], $4::bigint[]) AS l(id, account_id, amount)
    RETURNING account_id, amount
)
UPDATE accounts a
   SET balance = a.balance + d.delta
  FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM legs GROUP BY account_id) d
 WHERE a.id = d.account_id`

// Post writes t. See application.LedgerRepository.
//
// It runs inside inTx: within a unit of work that is a savepoint, so a refused
// post (insufficient funds, a missing account) leaves the caller's
// transaction usable; over the pool it is a transaction of its own.
func (r *LedgerRepository) Post(ctx context.Context, t application.LedgerTransaction) (string, error) {
	// Refused before anything is locked or written.
	if err := t.Validate(); err != nil {
		return "", err
	}

	txID, err := ensureID(t.ID)
	if err != nil {
		return "", err
	}
	at := t.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	entryIDs := make([]string, len(t.Entries))
	accountIDs := make([]string, len(t.Entries))
	amounts := make([]int64, len(t.Entries))
	distinct := map[string]struct{}{}
	for i, e := range t.Entries {
		if entryIDs[i], err = newUUID(); err != nil {
			return "", err
		}
		accountIDs[i] = e.AccountID
		amounts[i] = e.Amount.Minor()
		distinct[e.AccountID] = struct{}{}
	}
	toLock := make([]string, 0, len(distinct))
	for id := range distinct {
		toLock = append(toLock, id)
	}
	sort.Strings(toLock)

	var refType, refID *string
	if t.ReferenceType != "" {
		refType, refID = &t.ReferenceType, &t.ReferenceID
	}

	err = inTx(ctx, r.q, func(ctx context.Context, tx pgx.Tx) error {
		currency, err := lockAccounts(ctx, tx, toLock)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, postSQL,
			txID, entryIDs, accountIDs, amounts, currency, string(t.Reason), refType, refID, at)
		if err != nil {
			return err
		}
		if int(tag.RowsAffected()) != len(toLock) {
			return fmt.Errorf("postgres: ledger post moved %d balances, want %d", tag.RowsAffected(), len(toLock))
		}
		return nil
	})
	if err != nil {
		switch {
		case violates(err, sqlstateCheckViolation, accountsBalanceNonNegativeCheck):
			return "", application.ErrInsufficientFunds
		case isInvalidUUIDText(err):
			return "", application.ErrAccountNotFound
		case isAppError(err):
			return "", err
		}
		return "", fmt.Errorf("postgres: posting ledger transaction: %w", err)
	}
	return txID, nil
}

// lockAccounts locks ids and returns their common currency, refusing a
// missing account or a transaction that spans two currencies.
func lockAccounts(ctx context.Context, tx pgx.Tx, ids []string) (string, error) {
	rows, err := tx.Query(ctx, lockAccountsSQL, ids)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	currency, found := "", 0
	for rows.Next() {
		var id, cur string
		if err := rows.Scan(&id, &cur); err != nil {
			return "", err
		}
		if found > 0 && cur != currency {
			return "", application.ErrMixedCurrencies
		}
		currency = cur
		found++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if found != len(ids) {
		return "", application.ErrAccountNotFound
	}
	return currency, nil
}

// isAppError reports whether err is already a classified sentinel, which
// must reach the caller unwrapped.
func isAppError(err error) bool {
	for _, s := range []error{
		application.ErrAccountNotFound, application.ErrMixedCurrencies,
	} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// recordGrantSQL appends one grant. The ON CONFLICT names the partial unique
// index reward_grants_one_starting_grant_idx through its column list and
// predicate: a second starting grant for one player writes nothing and
// returns no row. Any other source never matches that index.
const recordGrantSQL = `
INSERT INTO reward_grants
       (id, player_id, source, source_reference_id, item_id, quantity,
        amount, ledger_transaction_id, granted_by, created_at)
VALUES ($1, $2, $3, $4, NULL, NULL, $5, $6, $7, $8)
ON CONFLICT (player_id, source) WHERE source = 'starting_grant' DO NOTHING
RETURNING id::text`

// RecordGrant appends a cash grant. See application.LedgerRepository.
//
// Only cash grants exist today; an item grant needs the items table.
func (r *LedgerRepository) RecordGrant(ctx context.Context, g application.RewardGrant) (application.RewardGrant, bool, error) {
	if g.Amount.IsZero() || g.Amount.IsNegative() {
		return application.RewardGrant{}, false,
			application.ErrInvalidLedgerTransaction.WithDetail("problem", "a cash grant must be above zero")
	}
	var err error
	if g.ID, err = ensureID(g.ID); err != nil {
		return application.RewardGrant{}, false, err
	}
	if g.LedgerTransactionID, err = ensureID(g.LedgerTransactionID); err != nil {
		return application.RewardGrant{}, false, err
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	g.CreatedAt = g.CreatedAt.UTC()

	var sourceRef *string
	if g.SourceReferenceID != "" {
		sourceRef = &g.SourceReferenceID
	}

	var id string
	err = r.q.QueryRow(ctx, recordGrantSQL,
		g.ID, g.PlayerID, string(g.Source), sourceRef,
		g.Amount.Minor(), g.LedgerTransactionID, g.GrantedBy, g.CreatedAt).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return application.RewardGrant{}, false, nil
	case violates(err, sqlstateForeignKeyViolation, rewardGrantsPlayerFkey), isInvalidUUIDText(err):
		return application.RewardGrant{}, false, application.ErrPlayerNotFound
	case err != nil:
		return application.RewardGrant{}, false, fmt.Errorf("postgres: recording reward grant: %w", err)
	}
	return g, true, nil
}
